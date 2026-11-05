package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// Handler 是 notify 模块的 HTTP 入口。
// 通知是个人数据：四个接口都只作用于当前登录用户本人（service 层以 authn
// 主体收口，不接受 userId 入参），租户条件由租户插件按上下文追加。
// 列表挂 notify:message:query（spec §5）；未读数/标记已读仅登录即可——
// unread-count 供顶栏铃铛全员轮询，标记已读是个人动作。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
}

// New 创建 notify Handler。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requirePermission: requirePermission}
}

// RegisterProtected 注册需要登录的 notify 接口，rg 上应已挂载 Auth 中间件。
// 统一前缀 /notify/message（docs/oa-mvp-spec.md §5）。
// read-all 是静态路由，先于 /:id/read 注册（仓库惯例：静态段先注册）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	msg := rg.Group("/notify/message")
	msg.GET("/page", h.requirePermission("notify:message:query"), h.Page)
	msg.GET("/unread-count", h.UnreadCount)
	msg.PUT("/read-all", h.MarkAllRead)
	msg.PUT("/:id/read", h.MarkRead)
}

// Page 当前用户通知分页（id 倒序；type/read 可选过滤）。
func (h *Handler) Page(c *gin.Context) {
	var req dto.MessagePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageMy(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// UnreadCount 当前用户未读数（顶栏铃铛 60 秒轮询，spec §4.13）。
func (h *Handler) UnreadCount(c *gin.Context) {
	n, err := h.svc.CountUnread(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dto.UnreadCountResp{Count: n})
}

// MarkRead 单条标记已读（幂等：已读重复标记仍 200；非本人/不存在 404）。
func (h *Handler) MarkRead(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.MarkRead(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// MarkAllRead 全部标记已读（幂等）。
func (h *Handler) MarkAllRead(c *gin.Context) {
	if err := h.svc.MarkAllRead(c.Request.Context()); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
