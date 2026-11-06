package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// PushHandler 是 notify 模块的实时推送 HTTP 入口（issue #78，spec §4.5.3 /
// §5.4 / ADR 0013）：
//   - POST /notify/push/ticket（登录即可）：Bearer 换一次性票据；
//   - GET  /notify/push/sse?ticket=（公开，凭票据）：SSE 事件流；
//   - GET  /notify/push/ws?ticket=（公开，凭票据）：WebSocket，#79 实现，
//     本期 501 占位让前端立即降级 SSE。
//
// 浏览器 EventSource 不能带 Authorization 头，因此先用 Bearer 换票据再建流，
// 避免 JWT 进入 URL/日志（ADR 0013）。票据一次性、TTL 30 秒、绑定 tenant/user；
// 校验失败一律 401，不泄露内部原因（spec §4.11.3）。推送是 best-effort：
// 断开即清理订阅，丢事件由前端 60 秒轮询兜底。
type PushHandler struct {
	hub       *push.Hub
	tickets   *push.TicketManager
	heartbeat time.Duration
	// resolvePrincipal 解析 Bearer token 为 (tenantID, userID, ok)。
	// SSE 请求带 Authorization 时做第二身份比对（票据与 Bearer 必须同人，
	// 防脚本转发票据跨租户订阅）；浏览器不带该头，仅凭票据认证。
	// internal/app 用 system Service.ParseToken 装配。
	resolvePrincipal func(token string) (tenantID, userID int64, ok bool)
}

// NewPushHandler 创建推送 HTTP 入口。heartbeat <= 0 时取 25 秒（spec §4.5.3）。
func NewPushHandler(hub *push.Hub, tickets *push.TicketManager, heartbeat time.Duration,
	resolvePrincipal func(token string) (tenantID, userID int64, ok bool),
) *PushHandler {
	if heartbeat <= 0 {
		heartbeat = 25 * time.Second
	}
	return &PushHandler{hub: hub, tickets: tickets, heartbeat: heartbeat, resolvePrincipal: resolvePrincipal}
}

// RegisterStream 注册换票端点：需要登录但不挂操作日志（每次建流都会换票，
// 属读语义，写操作日志会被连接心跳灌满）。rg 上应已挂载 Auth 中间件。
func (h *PushHandler) RegisterStream(rg *gin.RouterGroup) {
	rg.POST("/notify/push/ticket", h.Ticket)
}

// RegisterPublic 注册凭票据的流端点（公开路由，票据即凭证）。
func (h *PushHandler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("/notify/push/sse", h.SSE)
	rg.GET("/notify/push/ws", h.WS)
}

// Ticket 签发一次性推送票据（登录即可，spec §5.4）。
func (h *PushHandler) Ticket(c *gin.Context) {
	p, ok := authn.From(c.Request.Context())
	if !ok || p.UserID <= 0 {
		response.Fail(c, xerr.Unauthorized("未登录"))
		return
	}
	token, expiresIn := h.tickets.Issue(p.TenantID, p.UserID)
	response.OK(c, dto.PushTicketResp{
		Ticket:     token,
		ExpiresIn:  int(expiresIn.Seconds()),
		Transports: []string{"sse"},
	})
}

// SSE 建立 text/event-stream 事件流：
//   - 票据一次性/过期/与 Bearer 身份不一致 → 401（不泄露具体原因）；
//   - 订阅 (tenant, user) 后循环推送事件，每 heartbeat 写一条注释心跳
//     （默认 25s，spec §4.5.3）防止中间层断连；
//   - 客户端断开（请求 ctx 取消）或退订后返回，defer 清理订阅（断开清理）。
func (h *PushHandler) SSE(c *gin.Context) {
	// 第二身份（可选）：带 Bearer 时必须与票据绑定一致。
	var wantTenant, wantUser int64
	if token := bearerToken(c); token != "" {
		if h.resolvePrincipal == nil {
			response.Fail(c, xerr.Unauthorized("推送票据无效"))
			return
		}
		tenantID, userID, ok := h.resolvePrincipal(token)
		if !ok {
			response.Fail(c, xerr.Unauthorized("登录状态已失效，请重新登录"))
			return
		}
		wantTenant, wantUser = tenantID, userID
	}
	id, err := h.tickets.Consume(c.Query("ticket"), wantTenant, wantUser)
	if err != nil {
		response.Fail(c, xerr.Unauthorized("推送票据无效"))
		return
	}

	sub := h.hub.Subscribe(id.TenantID, id.UserID)
	defer h.hub.Unsubscribe(sub)

	w := c.Writer
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	// 反向代理（nginx）默认缓冲响应体，会吞掉心跳；显式关闭。
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// 首条注释行：客户端收到字节即确认流已建立。
	if _, err := w.WriteString(": connected\n\n"); err != nil {
		return
	}
	w.Flush()

	heartbeat := time.NewTicker(h.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case ev := <-sub.Events():
			data, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if _, err := w.WriteString("data: " + string(data) + "\n\n"); err != nil {
				return
			}
			w.Flush()
		case <-heartbeat.C:
			if _, err := w.WriteString(": ping\n\n"); err != nil {
				return
			}
			w.Flush()
		case <-sub.Done(): // hub 退订（如应用关闭）
			return
		case <-c.Request.Context().Done(): // 客户端断开
			return
		}
	}
}

// WS 是 WebSocket 传输的占位（issue #79 实现握手/ping/读循环）：返回 501，
// 让前端立即降级 SSE（spec §4.5.6）。不消费票据。
func (h *PushHandler) WS(c *gin.Context) {
	response.Fail(c, xerr.New(http.StatusNotImplemented, http.StatusNotImplemented, "WebSocket 推送暂未实现"))
}

// bearerToken 解析 Authorization 头中的 Bearer token（与 system 中间件同构，
// 保持模块自包含）。
func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(header)
}
