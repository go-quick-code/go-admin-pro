package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 产品单位（issue #88，docs/erp-mvp-spec.md §3.1/§5）----------
// 单位是仅名称字典（无换算率），分页 CRUD + 数据范围过滤。

// PageProductUnits 单位分页（筛选）。
func (h *Handler) PageProductUnits(c *gin.Context) {
	var req dto.ProductUnitPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListProductUnits(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetProductUnit 单位详情（不存在 404）。
func (h *Handler) GetProductUnit(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetProductUnit(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateProductUnit 新建单位，返回完整实体。
func (h *Handler) CreateProductUnit(c *gin.Context) {
	var req dto.ProductUnitCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateProductUnit(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateProductUnit 修改单位。
func (h *Handler) UpdateProductUnit(c *gin.Context) {
	var req dto.ProductUnitUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProductUnit(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteProductUnit 删除单位（软删除；被产品引用 409）。
func (h *Handler) DeleteProductUnit(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteProductUnit(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportProductUnits 导出单位（复用列表筛选与数据范围口径）。
func (h *Handler) ExportProductUnits(c *gin.Context) {
	var req dto.ProductUnitPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportProductUnits(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-product-units", "产品单位列表", data)
}

// RegisterProductUnitRoutes 注册产品单位路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（erp:product-unit:*）；静态路由先于 /:id 注册。
func (h *Handler) RegisterProductUnitRoutes(rg *gin.RouterGroup) {
	unit := rg.Group("/erp/product-unit")
	unit.GET("/page", h.requirePermission("erp:product-unit:query"), h.PageProductUnits)
	unit.GET("/export-excel", h.requirePermission("erp:product-unit:export"), h.ExportProductUnits)
	unit.GET("/:id", h.requirePermission("erp:product-unit:query"), h.GetProductUnit)
	unit.POST("", h.requirePermission("erp:product-unit:create"), h.CreateProductUnit)
	unit.PUT("", h.requirePermission("erp:product-unit:update"), h.UpdateProductUnit)
	unit.DELETE("/:id", h.requirePermission("erp:product-unit:delete"), h.DeleteProductUnit)
}
