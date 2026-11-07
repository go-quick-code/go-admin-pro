// Package api 是 message 模块的 HTTP 入口。
package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// sendMaxBodyBytes 是发送端点（短信/邮件共用 SendReq）的请求体字节硬上限
// （#85 安全审计 NV3）：params 值上限合计 ≤100KB + JSON 信封余量；超限 400。
// 之前 params 无界且 smtp 投递时全量 base64 复制，超界请求会放大内存占用。
const sendMaxBodyBytes = 128 << 10

// PermissionMiddleware 由 system 模块提供权限中间件。
type PermissionMiddleware func(code string) gin.HandlerFunc

// Handler 是 message 模块的 HTTP 入口。
type Handler struct {
	svc  *service.Service
	perm PermissionMiddleware
}

// New 创建 message Handler。perm 复用 system 的 RequirePermission。
func New(svc *service.Service, perm PermissionMiddleware) *Handler {
	return &Handler{svc: svc, perm: perm}
}

func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

// bindJSONLimit 在 bindJSON 之上用 MaxBytesReader 给请求体加字节上限
// （#85 安全审计 NV3，参照 infra 模块上传接口的既有惯例）：超限一律 400
// 并提示上限。
func bindJSONLimit(c *gin.Context, v interface{}, maxBytes int64, what string) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	if err := c.ShouldBindJSON(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			response.BadRequest(c, fmt.Sprintf("%s请求体过大，上限 %dKB", what, maxBytes>>10))
			return false
		}
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}

// ---------- 渠道 ----------

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

func (h *Handler) CreateChannel(c *gin.Context) {
	var req dto.ChannelCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateChannel(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	var req dto.ChannelUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateChannel(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateChannelStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.StatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateChannelStatus(c.Request.Context(), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteChannel(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// TestChannel 渠道测试发送（issue #73）：用指定渠道投递一条真实消息，
// 验证渠道配置；仅平台租户可用（service 层校验）。
func (h *Handler) TestChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.ChannelTestReq
	if !bindJSON(c, &req) {
		return
	}
	result, err := h.svc.TestChannel(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// ---------- 模板 ----------

func (h *Handler) PageTemplates(c *gin.Context) {
	var req dto.TemplatePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListTemplates(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) CreateTemplate(c *gin.Context) {
	var req dto.TemplateCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateTemplate(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

func (h *Handler) UpdateTemplate(c *gin.Context) {
	var req dto.TemplateUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateTemplate(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteTemplate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteTemplate(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 发送 ----------

func (h *Handler) SendSMS(c *gin.Context) {
	var req dto.SendReq
	// 请求体收口（#85 安全审计 NV3）：params 与邮件共用 SendReq，同口径限界。
	if !bindJSONLimit(c, &req, sendMaxBodyBytes, "短信发送") {
		return
	}
	result, err := h.svc.SendSMS(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

func (h *Handler) SendMail(c *gin.Context) {
	var req dto.SendReq
	// 请求体收口（#85 安全审计 NV3）：params 无界会经 smtp 全量 base64 复制
	// 放大内存占用，字节硬上限 + dto 值上限双层收口。
	if !bindJSONLimit(c, &req, sendMaxBodyBytes, "邮件发送") {
		return
	}
	result, err := h.svc.SendMail(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// ---------- 发送日志 ----------

func (h *Handler) PageLogs(c *gin.Context) {
	var req dto.LogPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListLogs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) ResendLog(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.ResendLog(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteLog(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteLog(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteLogs 批量删除发送日志（spec §5.2：DELETE /message/log 按 ids；
// #85 审查补齐）。任一 ID 跨租户/不存在 → 404 且不删任何行。
func (h *Handler) DeleteLogs(c *gin.Context) {
	var req dto.LogBatchDeleteReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.DeleteLogs(c.Request.Context(), req.IDs); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) ClearLogs(c *gin.Context) {
	if err := h.svc.ClearLogs(c.Request.Context()); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
