package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// notify_http.go 是商户回调的出站机制层（出站 HTTP 客户端 + SSRF 地址守卫），
// 照 internal/module/system/service/social_provider_http.go 先例把机制收在
// 独立文件——机制与业务分层可导航（spec #153 / 实施 #154 纯搬移，零行为改动）。
// 商户回调业务域（重试策略/响应分类/报文契约/重试 job/demo 接收/管理端）在
// notify.go。

// notifyHTTP 是回调 job 的出站 HTTP 客户端（照 socialHTTP 先例：10s 单请求
// 超时、1MB 响应体上限；错误剥离 url.Error 外壳——其 Error() 含完整 URL）。
type notifyHTTP struct {
	client *http.Client
}

// notifyHTTPTimeout 是单次回调 POST 的超时（spec §4.5 照 social 先例）。
const notifyHTTPTimeout = 10 * time.Second

// notifyHTTPBodyLimit 是回调响应体的读取上限（防御性，正常应答远小于此）。
const notifyHTTPBodyLimit = 1 << 20

func newNotifyHTTP() *notifyHTTP {
	return &notifyHTTP{client: &http.Client{
		Timeout: notifyHTTPTimeout,
		// 禁跟随重定向（PR #140 安全审查 P2）：重定向不得绕过 POST 前的
		// 地址安全校验（notifyURLGuardReason）。ErrUseLastResponse 让 3xx
		// 应答原样返回 → 非 2xx → 任务 22（spec §4.5 标准语义），不访问
		// Location 指向的任何地址。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// postJSON 发起 JSON POST（附带 header）并返回状态码与响应体：
// 与 socialHTTP 不同，非 2xx 不视为 transport 错误——状态码与响应体原样返回，
// 由 classifyNotifyResult 按 spec §4.5 语义分类（21/22 均继续重试）。
func (h *notifyHTTP) postJSON(ctx context.Context, endpoint string, body []byte, header map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("构造回调请求: %w", err)
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		// 剥离 url.Error 外壳：其 Error() 含完整 URL（不向日志泄内部拓扑）。
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		msg := "请求商户回调地址失败"
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			msg = fmt.Sprintf("请求商户回调地址超时（%s）", notifyHTTPTimeout)
		}
		return 0, nil, fmt.Errorf("%s: %w", msg, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, notifyHTTPBodyLimit))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取回调响应: %w", err)
	}
	return resp.StatusCode, data, nil
}

// SetNotifySSRFPrivateAllow 注入出站回调地址的受保护网段放行清单
// （configs pay.notify-ssrf-private-allow，PR #140 安全审查 P2）：条目为
// 主机名 / IP 字面量（精确匹配，大小写不敏感）或 CIDR（netip.Prefix）。
// 非法条目丢弃并记日志（宁可少放行不可误放行）。生产建议留空——默认只放行
// dev 配置里的本机环回（run-demo 链路与集成测试的 httptest 商户端点）。
func (s *Service) SetNotifySSRFPrivateAllow(entries []string) {
	s.ssrfAllowHosts = make(map[string]struct{}, len(entries))
	s.ssrfAllowPrefixes = nil
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(e); err == nil {
			s.ssrfAllowPrefixes = append(s.ssrfAllowPrefixes, prefix.Masked())
			continue
		}
		if ip, err := netip.ParseAddr(e); err == nil {
			s.ssrfAllowHosts[ip.String()] = struct{}{}
			continue
		}
		s.ssrfAllowHosts[strings.ToLower(e)] = struct{}{}
	}
}

// notifyURLGuardReason 出站回调地址安全校验（PR #140 安全审查 P2）：返回空串
// 表示放行，非空为拒绝原因（进 pay_notify_log 摘要，供运维排查放行配置）。
//
// 规则：scheme 白名单 http/https；主机名经 net.LookupIP 解析后任一 IP 落
// 环回/私网（RFC1918 + ULA）/链路本地（169.254.0.0/16 含云元数据
// 169.254.169.254）/未指定网段，且不在放行清单（host 精确匹配或 CIDR）内 →
// 拒绝；解析失败同样拒绝（fail-closed）。
//
// 残余风险（记录取舍）：本守卫是「解析后校验」，实际 POST 时标准库会重新
// 解析主机名，存在 DNS rebinding 的 TOCTOU 窗口（校验时解析公网、连接时
// 解析私网）。v1 接受该残余风险——解析检查 + 禁重定向已收敛主要攻击面
// （元数据/私网直连与 3xx 跳板），接入真实渠道时再评估固定解析结果拨号
// （pinned-dial）方案。
func (s *Service) notifyURLGuardReason(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "回调地址无法解析（fail-closed 拒发）"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Sprintf("回调地址 scheme %q 不在白名单（仅 http/https）", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "回调地址缺少主机名"
	}
	if _, ok := s.ssrfAllowHosts[strings.ToLower(host)]; ok {
		return ""
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ips, err = net.LookupIP(host)
		if err != nil {
			return fmt.Sprintf("回调地址主机 %q 解析失败（fail-closed 拒发）", host)
		}
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !notifyProtectedIP(addr) {
			continue
		}
		if s.notifyAllowIP(addr) {
			continue
		}
		return fmt.Sprintf("回调地址主机 %q 解析到受保护网段 %s（私网/环回/链路本地默认拦截，如需放行配置 pay.notify-ssrf-private-allow）", host, addr)
	}
	return ""
}

// notifyProtectedIP 报告地址是否落在默认拦截的受保护网段：环回 / 私网
// （IsPrivate：RFC1918 + fc00::/7 ULA）/ 链路本地单播（169.254.0.0/16 含
// 云厂商元数据 169.254.169.254、fe80::/10）/ 未指定地址。
func notifyProtectedIP(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified()
}

// notifyAllowIP 报告地址是否被放行清单覆盖（IP 字面量条目精确匹配或 CIDR
// 包含；host 条目对解析结果 IP 的匹配在 SetNotifySSRFPrivateAllow 归一化后
// 同样落到字符串比对）。
func (s *Service) notifyAllowIP(addr netip.Addr) bool {
	if _, ok := s.ssrfAllowHosts[addr.String()]; ok {
		return true
	}
	for _, prefix := range s.ssrfAllowPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
