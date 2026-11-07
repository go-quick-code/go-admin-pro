package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 供应商（issue #89，docs/erp-mvp-spec.md §3.1/§5）----------
// owner = 创建人（spec §4.10），列表/导出按数据范围过滤。

// PageSuppliers 供应商分页（筛选）。
func (h *Handler) PageSuppliers(c *gin.Context) {
	var req dto.SupplierPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListSuppliers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetSupplier 供应商详情（不存在 404）。
func (h *Handler) GetSupplier(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetSupplier(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateSupplier 新建供应商，返回完整实体。
func (h *Handler) CreateSupplier(c *gin.Context) {
	var req dto.SupplierCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateSupplier(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateSupplier 修改供应商（PUT 契约 id 在 body，与 #88 产品/单位/分类一致）。
func (h *Handler) UpdateSupplier(c *gin.Context) {
	var req dto.SupplierUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSupplier(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteSupplier 删除供应商（软删除；引用约束骨架见 party_refs.go）。
func (h *Handler) DeleteSupplier(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteSupplier(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportSuppliers 导出供应商（复用列表筛选与数据范围口径，见 party_excel.go）。
func (h *Handler) ExportSuppliers(c *gin.Context) {
	var req dto.SupplierPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportSuppliers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-suppliers", "供应商列表", data)
}

// RegisterSupplierRoutes 注册供应商路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（erp:supplier:*）；静态路由先于 /:id 注册。
func (h *Handler) RegisterSupplierRoutes(rg *gin.RouterGroup) {
	supplier := rg.Group("/erp/supplier")
	supplier.GET("/page", h.requirePermission("erp:supplier:query"), h.PageSuppliers)
	supplier.GET("/export-excel", h.requirePermission("erp:supplier:export"), h.ExportSuppliers)
	supplier.GET("/:id", h.requirePermission("erp:supplier:query"), h.GetSupplier)
	supplier.POST("", h.requirePermission("erp:supplier:create"), h.CreateSupplier)
	supplier.PUT("", h.requirePermission("erp:supplier:update"), h.UpdateSupplier)
	supplier.DELETE("/:id", h.requirePermission("erp:supplier:delete"), h.DeleteSupplier)
}
