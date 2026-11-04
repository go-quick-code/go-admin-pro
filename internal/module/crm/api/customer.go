package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
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
