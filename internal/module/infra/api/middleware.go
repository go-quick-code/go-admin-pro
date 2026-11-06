package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/google/uuid"
)

// ---------- API 访问日志中间件（issue #80，spec §4.6，ADR 0014）----------
//
// 挂载在 Auth 之后、OperateLog 之前（router.go 的 protected 组）：只记录
// 受保护接口——公开接口（登录/验证码/社交回调/推送流）不在该组，天然不记录；
// 推送流（换票 + SSE）挂在仅 Auth 的 streams 组，同样不在采集范围。
//
// 记录字段（spec §4.6.2）：路由模板 c.FullPath()（取不到用原始 path）、
// 脱敏 query、状态码、耗时、IP、UA、request_id；不记录请求体（§4.6.6）。
// request_id（§4.6.3）：优先读 X-Request-Id 头（限 64 字符），否则生成
// UUID，并回写响应头。采集完成后只做一次非阻塞入队（spec §4.6.5），写库
// 由 service.APILogWriter 异步完成，任何失败都不影响请求。

const (
	// apilogQueryMax 是 query 的整体截断长度（spec §4.6.4）。
	apilogQueryMax = 1024
	// apilogUserAgentMax 是 UA 的截断长度（spec §4.6.4）。
	apilogUserAgentMax = 500
	// apilogRequestIDMax 是 request_id 的长度上限（spec §4.6.3）。
	apilogRequestIDMax = 64
)

// sensitiveQueryParams 是必须脱敏的 query 参数名（spec §4.6.4），
// 比较时忽略大小写。
var sensitiveQueryParams = map[string]struct{}{
	"token":         {},
	"access_token":  {},
	"password":      {},
	"secret":        {},
	"refresh_token": {},
}

// APILogMiddleware 返回访问日志采集中间件。sink 是异步写入口
// （生产装配传 *service.APILogWriter）。
func APILogMiddleware(sink service.APILogEnqueuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		rid := resolveRequestID(c.Request)
		// 回写响应头要在处理器写响应体之前完成。
		c.Header("X-Request-Id", rid)
		c.Next()

		entry := model.APILog{
			// created_at 在采集时刻定格：异步落库有延迟，不能以落库时刻为准。
			CreatedAt:      time.Now(),
			RequestMethod:  c.Request.Method,
			RequestPath:    requestPath(c),
			RequestQuery:   maskQuery(c.Request.URL.RawQuery),
			ResponseStatus: c.Writer.Status(),
			DurationMs:     time.Since(start).Milliseconds(),
			ClientIP:       c.ClientIP(),
			UserAgent:      truncate(c.GetHeader("User-Agent"), apilogUserAgentMax),
			RequestID:      rid,
		}
		// 用户/租户来自 Auth 写入的登录主体；Auth 之后必然存在，
		// 兜底 tenant.ID 仅为防御（测试直挂中间件的场景）。
		if p, ok := authn.From(c.Request.Context()); ok {
			entry.TenantID, entry.UserID, entry.Username = p.TenantID, p.UserID, p.Username
		} else {
			entry.TenantID = tenant.ID(c.Request.Context())
		}
		sink.Enqueue(entry)
	}
}

// requestPath 返回路由模板；未匹配路由（FullPath 为空）时退回原始 path。
func requestPath(c *gin.Context) string {
	if p := c.FullPath(); p != "" {
		return p
	}
	return c.Request.URL.Path
}

// resolveRequestID 解析请求 ID：优先 X-Request-Id 头（限 64 字符），
// 否则生成 UUID v4。
func resolveRequestID(req *http.Request) string {
	if rid := strings.TrimSpace(req.Header.Get("X-Request-Id")); rid != "" {
		return truncate(rid, apilogRequestIDMax)
	}
	return uuid.NewString()
}

// maskQuery 脱敏 query：敏感参数值替换为 ***，整体截断 1024。
// 可解析时按 url.Values 重编码（参数名字典序）；解析失败（如包含裸分号）
// 时退回逐段掩码，保留原始串，避免审计线索整条丢失。
func maskQuery(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return maskQueryFallback(raw)
	}
	maskValues(vals)
	// Encode 会把掩码 * 百分号编码为 %2A，还原回 * 保证脱敏结果肉眼可读。
	return truncate(strings.ReplaceAll(vals.Encode(), "%2A", "*"), apilogQueryMax)
}

// maskQueryFallback 对不可解析的 query 逐段脱敏：只替换敏感参数的值，
// 其余字符原样保留。
func maskQueryFallback(raw string) string {
	pairs := strings.Split(raw, "&")
	for i, p := range pairs {
		if k, _, ok := strings.Cut(p, "="); ok {
			if _, sensitive := sensitiveQueryParams[strings.ToLower(strings.TrimSpace(k))]; sensitive {
				pairs[i] = k + "=***"
			}
		}
	}
	return truncate(strings.Join(pairs, "&"), apilogQueryMax)
}

// maskValues 原地替换敏感参数的全部值。
func maskValues(vals url.Values) {
	for k := range vals {
		if _, sensitive := sensitiveQueryParams[strings.ToLower(k)]; sensitive {
			for i := range vals[k] {
				vals[k][i] = "***"
			}
		}
	}
}

// truncate 按字节截断（query/UA/request_id 的长度上限均为字节口径）。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
