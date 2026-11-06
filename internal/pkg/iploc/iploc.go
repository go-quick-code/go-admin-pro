// Package iploc 提供 IP 归属地解析（issue #82，spec §4.8.4/§4.8.5）。
//
// 设计要点：
//   - Resolver 是唯一消费接缝：system 登录日志分页只依赖该接口，
//     测试注入假实现即可覆盖展示逻辑；
//   - 未配置 xdb 或加载失败一律退回空实现，定位是展示增强，
//     不允许阻断启动或分页（spec §4.8.4「只告警一次，不影响启动」）；
//   - 不内置任何 xdb 数据文件：数据由部署侧提供（ip-location.xdb-path）。
package iploc

import (
	"log/slog"
	"strings"
)

// Resolver 把 IP 解析为归属地展示文本；解析不出（未配置/失败/无该 IP）
// 返回空串，永远不返回错误。
type Resolver interface {
	Resolve(ip string) string
}

// ResolverFunc 让普通函数满足 Resolver，便于测试与小型适配。
type ResolverFunc func(ip string) string

// Resolve 实现 Resolver。
func (f ResolverFunc) Resolve(ip string) string { return f(ip) }

// noop 是空实现：未配置 xdb 或加载失败时的兜底。
type noop struct{}

// NewNoop 返回空实现。
func NewNoop() Resolver { return noop{} }

// Resolve 实现 Resolver：恒返回空串。
func (noop) Resolve(string) string { return "" }

// New 按配置构造 Resolver（应用装配入口）：
//   - xdbPath 为空 → 空实现，无告警（未启用是正常配置，不是故障）；
//   - 文件不存在/加载失败 → 告警一次并退回空实现，不影响启动。
func New(log *slog.Logger, xdbPath string) Resolver {
	xdbPath = strings.TrimSpace(xdbPath)
	if xdbPath == "" {
		return NewNoop()
	}
	r, err := NewIP2Region(xdbPath)
	if err != nil {
		log.Warn("ip2region xdb 加载失败，登录日志归属地将为空；请检查 ip-location.xdb-path 配置",
			"path", xdbPath, "err", err)
		return NewNoop()
	}
	return r
}

// FormatRegion 把 ip2region 的区域串「国家|区域|省份|城市|ISP」格式化为
// 展示文本：过滤空段与占位「0」，非空段以空格连接；全空返回空串。
func FormatRegion(region string) string {
	parts := strings.Split(region, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "0" {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}
