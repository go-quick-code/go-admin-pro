package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// wsReadLimit 是客户端消息上限（issue #79：4KB）。服务端只发不收，数据消息
// 一律丢弃；上限只为在畸形客户端撑爆内存前快速断连（spec §4.5.3）。
const wsReadLimit = 4096

// PushHandler 是 notify 模块的实时推送 HTTP 入口（issue #78 SSE + issue #79
// WebSocket，spec §4.5.3 / §5.4 / ADR 0013）：
//   - POST /notify/push/ticket（登录即可）：Bearer 换一次性票据；
//   - GET  /notify/push/sse?ticket=（公开，凭票据）：SSE 事件流；
//   - GET  /notify/push/ws?ticket=（公开，凭票据）：WebSocket 事件流。
//
// 浏览器 EventSource/WebSocket 不能带 Authorization 头，因此先用 Bearer 换票据
// 再建流，避免 JWT 进入 URL/日志（ADR 0013）。票据一次性、TTL 30 秒、绑定
// tenant/user；校验失败一律 401，不泄露内部原因（spec §4.11.3）。推送是
// best-effort：断开即清理订阅，丢事件由前端 60 秒轮询兜底。
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

// Ticket 签发一次性推送票据（登录即可，spec §5.4）。transports 按前端实际
// 优先级列出（issue #79：WS 优先，SSE 次之）。
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
		Transports: []string{"ws", "sse"},
	})
}

// authTicket 是 SSE/WS 共用的票据认证（issue #79 抽取自 #78 的 SSE 内联逻辑，
// 语义不变）：带 Bearer 时做第二身份比对，Consume 失败一律写 401 并返回 false。
func (h *PushHandler) authTicket(c *gin.Context) (*push.TicketIdentity, bool) {
	// 第二身份（可选）：带 Bearer 时必须与票据绑定一致。
	var wantTenant, wantUser int64
	if token := bearerToken(c); token != "" {
		if h.resolvePrincipal == nil {
			response.Fail(c, xerr.Unauthorized("推送票据无效"))
			return nil, false
		}
		tenantID, userID, ok := h.resolvePrincipal(token)
		if !ok {
			response.Fail(c, xerr.Unauthorized("登录状态已失效，请重新登录"))
			return nil, false
		}
		wantTenant, wantUser = tenantID, userID
	}
	id, err := h.tickets.Consume(c.Query("ticket"), wantTenant, wantUser)
	if err != nil {
		response.Fail(c, xerr.Unauthorized("推送票据无效"))
		return nil, false
	}
	return id, true
}

// SSE 建立 text/event-stream 事件流：
//   - 票据一次性/过期/与 Bearer 身份不一致 → 401（不泄露具体原因）；
//   - 订阅 (tenant, user) 后循环推送事件，每 heartbeat 写一条注释心跳
//     （默认 25s，spec §4.5.3）防止中间层断连；
//   - 客户端断开（请求 ctx 取消）或退订后返回，defer 清理订阅（断开清理）。
func (h *PushHandler) SSE(c *gin.Context) {
	id, ok := h.authTicket(c)
	if !ok {
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

// WS 建立 WebSocket 事件流（issue #79，spec §4.5.3 / ADR 0013）：
//   - 票据认证与 SSE 共用（authTicket）：一次性/过期/跨租户 → 401；
//   - 握手后订阅 (tenant, user)，事件写循环与 SSE 共用同一 hub 与事件模型；
//   - 服务端只发不收：读循环丢弃一切客户端数据消息（spec §4.5.3），
//     客户端 ping 由 coder/websocket 在读循环中自动 pong；
//   - 心跳：每 heartbeat 服务端主动 ping（兼做死连接检测）；
//   - 断开/超限/应用关闭即退出，defer 清理订阅（连接数不泄漏）。
//
// TODO(multi-instance): hub 与票据都在进程内——多实例部署时其他实例发布的
// 事件到不了本实例的连接，需要外部 pub/sub（Redis/NATS）扇出后本层无需改动，
// 见 ADR 0013（本轮不实现，spec §4.5.7）。
//
// AcceptOptions 缺省不校验 Origin：鉴权靠一次性票据而非 Cookie，
// 跨站风险与 SSE 一致（浏览器需同源策略+CORS 允许）。
func (h *PushHandler) WS(c *gin.Context) {
	// 升级预检先于票据消费：普通 HTTP 请求（无 Upgrade 头）在认证之前即被
	// 拒绝，一次性票据不被失败路径浪费（Accept 自身会写 426，但那在消费之后）。
	if c.Request.Method != http.MethodGet || !strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
		response.Fail(c, xerr.New(http.StatusBadRequest, http.StatusBadRequest, "仅支持 WebSocket 连接"))
		return
	}

	id, ok := h.authTicket(c)
	if !ok {
		return
	}

	ws, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		// 握手被拒（非 WS 升级请求等）：Accept 已写 HTTP 错误响应。
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// 4KB 客户端消息上限（issue #79）：超限读错即断连（库自动发 1009 关闭帧），
	// 读循环退出后经 wcancel 走统一清理。
	ws.SetReadLimit(wsReadLimit)

	sub := h.hub.Subscribe(id.TenantID, id.UserID)
	defer h.hub.Unsubscribe(sub)

	// wctx 贯穿读写循环：客户端断开（请求 ctx 取消）、应用关闭或任一侧
	// 出错时取消，另一侧随之终止。库约定：任何方法出错即关闭连接。
	wctx, wcancel := context.WithCancel(c.Request.Context())
	defer wcancel()

	// 读循环（独立 goroutine）：服务端只发不收——数据消息一律丢弃；
	// 客户端 ping 由库在读循环中自动 pong（spec §4.5.3「客户端 ping 除外」）。
	go func() {
		defer wcancel()
		for {
			if _, _, err := ws.Read(wctx); err != nil {
				return
			}
		}
	}()

	// 心跳：每 heartbeat 服务端主动 ping（默认 25s，spec §4.5.3，与 SSE 心跳
	// 同一间隔配置）。Ping 阻塞等 pong——对端失联（不读不回）即错误退出，
	// 连接随之关闭清理，心跳兼做死连接检测；在线对端由库自动回 pong。
	heartbeat := time.NewTicker(h.heartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case ev := <-sub.Events():
			data, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if err := ws.Write(wctx, websocket.MessageText, data); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := ws.Ping(wctx); err != nil {
				return
			}
		case <-sub.Done(): // hub 退订（如应用关闭）
			return
		case <-wctx.Done(): // 客户端断开或读循环出错
			return
		}
	}
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
