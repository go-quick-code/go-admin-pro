package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 支付应用（租户级） ----------

// PageApps 分页查询应用。
func (h *Handler) PageApps(c *gin.Context) {
	var req dto.AppPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListApps(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetApp 应用详情（含绑定码清单）。
func (h *Handler) GetApp(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetApp(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// CreateApp 创建应用。
func (h *Handler) CreateApp(c *gin.Context) {
	var req dto.AppSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateApp(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateApp 修改应用（id 在 body）。
func (h *Handler) UpdateApp(c *gin.Context) {
	var req dto.AppUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateApp(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteApp 删除应用（绑定/引用守卫在 service 层，spec §4.10）。
func (h *Handler) DeleteApp(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteApp(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// GetAppChannels 返回应用已绑定的渠道码（GET /pay/app/:id/channels）。
func (h *Handler) GetAppChannels(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	codes, err := h.svc.ListAppChannels(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, codes)
}

// ReplaceAppChannels 全量替换应用绑定（PUT /pay/app/:id/channels，码数组）。
func (h *Handler) ReplaceAppChannels(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req struct {
		ChannelCodes []string `json:"channelCodes"`
	}
	if !bindJSON(c, &req) {
		return
	}
	codes, err := h.svc.ReplaceAppChannels(c.Request.Context(), id, req.ChannelCodes)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, codes)
}

// ---------- 渠道（平台级） ----------

// PageChannels 平台侧分页查询渠道（config 已脱敏）。
func (h *Handler) PageChannels(c *gin.Context) {
	var req dto.ChannelPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListChannels(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// UpdateChannel 修改渠道（启停 + 凭证占位符合并 + 备注）。
func (h *Handler) UpdateChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.ChannelUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateChannel(c.Request.Context(), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ListChannelOptions 租户侧只读渠道清单（无凭证，绑定 tab 数据源）。
func (h *Handler) ListChannelOptions(c *gin.Context) {
	options, err := h.svc.ListChannelOptions(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, options)
}
