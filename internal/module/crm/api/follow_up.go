package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// PageFollowUps 跟进分页（客户/类型筛选 + 数据范围经父客户传导）。
func (h *Handler) PageFollowUps(c *gin.Context) {
	var req dto.FollowUpPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListFollowUps(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetFollowUp 跟进详情（客户数据范围外 404）。
func (h *Handler) GetFollowUp(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	followUp, err := h.svc.GetFollowUp(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, followUp)
}

// CreateFollowUp 在客户下新建跟进（同事务刷新客户 last_follow_time），
// 返回带所属客户名称的完整跟进。
func (h *Handler) CreateFollowUp(c *gin.Context) {
	var req dto.FollowUpCreateReq
	if !bindJSON(c, &req) {
		return
	}
	followUp, err := h.svc.CreateFollowUp(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, followUp)
}

// UpdateFollowUp 修改跟进（创建人本人或 crm:follow-up:update，service 层校验）。
func (h *Handler) UpdateFollowUp(c *gin.Context) {
	var req dto.FollowUpUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateFollowUp(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteFollowUp 删除跟进（软删除；创建人本人或 crm:follow-up:delete，service 层校验）。
func (h *Handler) DeleteFollowUp(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteFollowUp(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
