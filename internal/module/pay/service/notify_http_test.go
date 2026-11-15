package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// PR #140 安全审查 P2：notify_url 零校验直达 job 出站 POST 构成认证 SSRF
// （登录用户自建订单携带私网/元数据地址 → job 每分钟 POST HMAC 签名报文并
// 回读响应摘要）。守卫语义：scheme 白名单 http/https；主机解析后任一 IP 落
// 环回/私网/链路本地/未指定网段即拒发，除非在 pay.notify-ssrf-private-allow
// 放行清单（host 精确匹配或 CIDR）内；拒发 fail-closed（不消耗重试次数，
// 推进一个间隔，记日志）。

// newGuardService 构造仅注入放行清单的最小 Service（守卫纯逻辑测试用）。
func newGuardService(allow []string) *Service {
	s := &Service{}
	s.SetNotifySSRFPrivateAllow(allow)
	return s
}

// TestNotifyURLGuardSchemeRefused scheme 白名单：仅 http/https 放行，其余
// （file/ftp/javascript/空）一律拒绝。
func TestNotifyURLGuardSchemeRefused(t *testing.T) {
	s := newGuardService([]string{"127.0.0.1"})
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"javascript:alert(1)",
		"",
	} {
		if reason := s.notifyURLGuardReason(raw); reason == "" {
			t.Fatalf("scheme 非法/空地址 %q 应被拒绝", raw)
		}
	}
}

// TestNotifyURLGuardPrivateIPRefused 受保护网段（无放行清单）：环回、RFC1918
// 私网、链路本地（含云元数据 169.254.169.254）、未指定地址、IPv4 映射 IPv6
// 一律拒绝。
func TestNotifyURLGuardPrivateIPRefused(t *testing.T) {
	s := newGuardService(nil)
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.1.2.3:8080/notify",
		"http://172.16.0.9/notify",
		"http://192.168.1.1/notify",
		"http://127.0.0.1:8080/notify",
		"http://[::1]/notify",
		"http://[::ffff:127.0.0.1]/notify",
		"http://0.0.0.0/notify",
	} {
		if reason := s.notifyURLGuardReason(raw); reason == "" {
			t.Fatalf("受保护地址 %q 应被拒绝", raw)
		}
	}
}

// TestNotifyURLGuardHostnameResolvesRefused 主机名走 DNS 解析后按 IP 判定：
// localhost 解析到环回 → 拒绝（解析路径与 IP 字面量同口径）。
func TestNotifyURLGuardHostnameResolvesRefused(t *testing.T) {
	s := newGuardService(nil)
	if reason := s.notifyURLGuardReason("http://localhost/notify"); reason == "" {
		t.Fatal("localhost 解析到环回应被拒绝")
	}
}

// TestNotifyURLGuardPublicIPAllowed 公网 IP 字面量放行（守卫只拦受保护网段）。
func TestNotifyURLGuardPublicIPAllowed(t *testing.T) {
	s := newGuardService(nil)
	if reason := s.notifyURLGuardReason("http://93.184.216.34/notify"); reason != "" {
		t.Fatalf("公网地址应放行，实际拒绝：%s", reason)
	}
	if reason := s.notifyURLGuardReason("https://93.184.216.34/notify"); reason != "" {
		t.Fatalf("https 公网地址应放行，实际拒绝：%s", reason)
	}
}

// TestNotifyURLGuardAllowanceRestores 放行清单语义（configs
// pay.notify-ssrf-private-allow）：host 精确匹配与 CIDR 网段均可放行，
// 清单外的受保护网段仍拒绝；host 匹配大小写不敏感。
func TestNotifyURLGuardAllowanceRestores(t *testing.T) {
	// dev 默认清单：本机环回（run-demo 链路与集成测试的 httptest 商户）。
	s := newGuardService([]string{"127.0.0.1", "::1"})
	if reason := s.notifyURLGuardReason("http://127.0.0.1:9/callback"); reason != "" {
		t.Fatalf("dev 默认放行 127.0.0.1 应生效，实际拒绝：%s", reason)
	}
	if reason := s.notifyURLGuardReason("http://[::1]/callback"); reason != "" {
		t.Fatalf("dev 默认放行 ::1 应生效，实际拒绝：%s", reason)
	}
	if reason := s.notifyURLGuardReason("http://10.1.2.3/notify"); reason == "" {
		t.Fatal("清单外私网仍应拒绝")
	}

	// CIDR 放行：10/8 内放行，其余私网仍拒绝。
	s = newGuardService([]string{"10.0.0.0/8"})
	if reason := s.notifyURLGuardReason("http://10.9.9.9/notify"); reason != "" {
		t.Fatalf("CIDR 10.0.0.0/8 放行应生效，实际拒绝：%s", reason)
	}
	if reason := s.notifyURLGuardReason("http://192.168.0.1/notify"); reason == "" {
		t.Fatal("CIDR 外私网仍应拒绝")
	}

	// host 条目匹配（含主机名形式，大小写不敏感）。
	s = newGuardService([]string{"169.254.169.254", "Internal.Host.Example"})
	if reason := s.notifyURLGuardReason("http://169.254.169.254/latest/"); reason != "" {
		t.Fatalf("host 放行应生效，实际拒绝：%s", reason)
	}
	if reason := s.notifyURLGuardReason("http://internal.host.example/notify"); reason != "" {
		t.Fatalf("host 放行应大小写不敏感，实际拒绝：%s", reason)
	}
}

// TestNotifyHTTPNoRedirectFollow 出站客户端禁跟随重定向（3xx 应答原样返回 →
// 非 2xx → 任务 22 标准语义）：重定向不得绕过 POST 前的地址安全校验。
func TestNotifyHTTPNoRedirectFollow(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/hop")
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	h := newNotifyHTTP()
	status, _, err := h.postJSON(context.Background(), redirector.URL, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("postJSON 不应因 3xx 报 transport 错误: %v", err)
	}
	if status != http.StatusFound {
		t.Fatalf("3xx 应原样返回不跟随，实际 status=%d", status)
	}
	if n := atomic.LoadInt32(&targetHits); n != 0 {
		t.Fatalf("重定向目标不应被访问，实际 %d 次", n)
	}
}
