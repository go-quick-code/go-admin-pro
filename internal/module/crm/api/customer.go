package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// PageCustomers 客户分页（名称/负责人/状态/部门筛选 + 数据范围过滤）。
func (h *Handler) PageCustomers(c *gin.Context) {
	var req dto.CustomerPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListCustomers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetCustomer 客户详情（数据范围外 404）。
func (h *Handler) GetCustomer(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	customer, err := h.svc.GetCustomer(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, customer)
}

// CreateCustomer 新建客户，返回带编号的完整客户。
func (h *Handler) CreateCustomer(c *gin.Context) {
	var req dto.CustomerCreateReq
	if !bindJSON(c, &req) {
		return
	}
	customer, err := h.svc.CreateCustomer(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, customer)
}

// UpdateCustomer 修改客户。
func (h *Handler) UpdateCustomer(c *gin.Context) {
	var req dto.CustomerUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateCustomer(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteCustomer 删除客户（软删除）。
func (h *Handler) DeleteCustomer(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteCustomer(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 公海（issue #13，docs/crm-mvp-spec.md §4.2）----------

// PagePoolCustomers 公海分页（owner IS NULL，不受数据范围过滤）。
func (h *Handler) PagePoolCustomers(c *gin.Context) {
	var req dto.CustomerPoolPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListPoolCustomers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// ReleaseCustomer 释放客户到公海（操作权限在 service：负责人本人或权限码）。
func (h *Handler) ReleaseCustomer(c *gin.Context) {
	var req dto.CustomerReleaseReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.ReleaseCustomer(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ClaimCustomer 从公海认领客户到自己名下，返回认领后的客户。
func (h *Handler) ClaimCustomer(c *gin.Context) {
	var req dto.CustomerClaimReq
	if !bindJSON(c, &req) {
		return
	}
	customer, err := h.svc.ClaimCustomer(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, customer)
}

// TransferCustomer 转移客户给同租户其他用户（操作权限在 service）。
func (h *Handler) TransferCustomer(c *gin.Context) {
	var req dto.CustomerTransferReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.TransferCustomer(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// PageCustomerOwnerHistory 按客户分页查负责人历史（新→旧）。
func (h *Handler) PageCustomerOwnerHistory(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.OwnerHistoryPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListCustomerOwnerHistory(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
