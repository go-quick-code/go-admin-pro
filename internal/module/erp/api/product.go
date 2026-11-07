package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 产品（issue #88，docs/erp-mvp-spec.md §3.1/§5）----------
// owner = 创建人（spec §4.10），列表/导出按数据范围过滤。

// PageProducts 产品分页（筛选）。
func (h *Handler) PageProducts(c *gin.Context) {
	var req dto.ProductPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListProducts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetProduct 产品详情（不存在 404）。
func (h *Handler) GetProduct(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetProduct(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateProduct 新建产品，返回完整实体。
func (h *Handler) CreateProduct(c *gin.Context) {
	var req dto.ProductCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateProduct(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateProduct 修改产品。
func (h *Handler) UpdateProduct(c *gin.Context) {
	var req dto.ProductUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProduct(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteProduct 删除产品（软删除）。
func (h *Handler) DeleteProduct(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteProduct(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportProducts 导出产品（复用列表筛选与数据范围口径）。
func (h *Handler) ExportProducts(c *gin.Context) {
	var req dto.ProductPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportProducts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-products", "产品列表", data)
}

// RegisterProductRoutes 注册产品路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（erp:product:*）；静态路由先于 /:id 注册。
func (h *Handler) RegisterProductRoutes(rg *gin.RouterGroup) {
	product := rg.Group("/erp/product")
	product.GET("/page", h.requirePermission("erp:product:query"), h.PageProducts)
	product.GET("/export-excel", h.requirePermission("erp:product:export"), h.ExportProducts)
	product.GET("/:id", h.requirePermission("erp:product:query"), h.GetProduct)
	product.POST("", h.requirePermission("erp:product:create"), h.CreateProduct)
	product.PUT("", h.requirePermission("erp:product:update"), h.UpdateProduct)
	product.DELETE("/:id", h.requirePermission("erp:product:delete"), h.DeleteProduct)
}
