package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// PageReceivablePlans 回款计划分页（合同/派生状态/逾期筛选 + join 合同数据范围）。
func (h *Handler) PageReceivablePlans(c *gin.Context) {
	var req dto.ReceivablePlanPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListReceivablePlans(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// CreateReceivablePlan 新建回款计划（金额不变量 SUM(计划) ≤ 合同金额）。
func (h *Handler) CreateReceivablePlan(c *gin.Context) {
	var req dto.ReceivablePlanCreateReq
	if !bindJSON(c, &req) {
		return
	}
	plan, err := h.svc.CreateReceivablePlan(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, plan)
}

// UpdateReceivablePlan 修改回款计划（合同不可改；金额校验排除自身）。
func (h *Handler) UpdateReceivablePlan(c *gin.Context) {
	var req dto.ReceivablePlanUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateReceivablePlan(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteReceivablePlan 删除回款计划（存在回款记录时 400）。
func (h *Handler) DeleteReceivablePlan(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteReceivablePlan(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// PageReceivableRecords 回款记录分页（合同/计划筛选 + join 合同数据范围）。
func (h *Handler) PageReceivableRecords(c *gin.Context) {
	var req dto.ReceivableRecordPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListReceivableRecords(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// CreateReceivableRecord 登记回款记录（HK 编号 + 记录同事务；金额不变量
// SUM(记录) ≤ 计划金额）。
func (h *Handler) CreateReceivableRecord(c *gin.Context) {
	var req dto.ReceivableRecordCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateReceivableRecord(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// DeleteReceivableRecord 删除回款记录（数据范围外 404）。
func (h *Handler) DeleteReceivableRecord(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteReceivableRecord(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
