package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// PageOpportunities 商机分页（名称/阶段/客户筛选 + owner-scoped 数据范围）。
func (h *Handler) PageOpportunities(c *gin.Context) {
	var req dto.OpportunityPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListOpportunities(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetOpportunity 商机详情（含行项目与合计；数据范围外 404）。
func (h *Handler) GetOpportunity(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	opportunity, err := h.svc.GetOpportunity(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, opportunity)
}

// CreateOpportunity 新建商机（SJ 编号 + 行项目同事务）。
func (h *Handler) CreateOpportunity(c *gin.Context) {
	var req dto.OpportunityCreateReq
	if !bindJSON(c, &req) {
		return
	}
	opportunity, err := h.svc.CreateOpportunity(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, opportunity)
}

// UpdateOpportunity 修改商机（编号/客户/阶段不可改；行项目整体替换）。
func (h *Handler) UpdateOpportunity(c *gin.Context) {
	var req dto.OpportunityUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateOpportunity(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// AdvanceOpportunityStage 阶段推进（唯一阶段入口；终态 400）。
func (h *Handler) AdvanceOpportunityStage(c *gin.Context) {
	var req dto.OpportunityStageReq
	if !bindJSON(c, &req) {
		return
	}
	opportunity, err := h.svc.AdvanceOpportunityStage(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, opportunity)
}

// DeleteOpportunity 删除商机（软删除；行项目随之不可见，产品恢复可删）。
func (h *Handler) DeleteOpportunity(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteOpportunity(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
