package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- ERP 客户（issue #89，docs/erp-mvp-spec.md §3.1/§5）----------
// owner = 创建人（spec §4.10），列表/导出按数据范围过滤；与 CRM 客户完全
// 独立（ADR 0015），路径 /erp/customer 与 /crm/customer 零交叉。

// PageCustomers ERP 客户分页（筛选）。
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

// GetCustomer ERP 客户详情（不存在 404）。
func (h *Handler) GetCustomer(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetCustomer(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateCustomer 新建 ERP 客户，返回完整实体。
func (h *Handler) CreateCustomer(c *gin.Context) {
	var req dto.CustomerCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateCustomer(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateCustomer 修改 ERP 客户（PUT 契约 id 在 body，与 #88 产品/单位/分类一致）。
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

// DeleteCustomer 删除 ERP 客户（软删除；引用约束骨架见 party_refs.go）。
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

// ExportCustomers 导出 ERP 客户（复用列表筛选与数据范围口径，见 party_excel.go）。
func (h *Handler) ExportCustomers(c *gin.Context) {
	var req dto.CustomerPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportCustomers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-customers", "客户列表", data)
}

// RegisterCustomerRoutes 注册 ERP 客户路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（erp:customer:*）；静态路由先于 /:id 注册。
func (h *Handler) RegisterCustomerRoutes(rg *gin.RouterGroup) {
	customer := rg.Group("/erp/customer")
	customer.GET("/page", h.requirePermission("erp:customer:query"), h.PageCustomers)
	customer.GET("/export-excel", h.requirePermission("erp:customer:export"), h.ExportCustomers)
	customer.GET("/:id", h.requirePermission("erp:customer:query"), h.GetCustomer)
	customer.POST("", h.requirePermission("erp:customer:create"), h.CreateCustomer)
	customer.PUT("", h.requirePermission("erp:customer:update"), h.UpdateCustomer)
	customer.DELETE("/:id", h.requirePermission("erp:customer:delete"), h.DeleteCustomer)
}
