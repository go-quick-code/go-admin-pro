package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 产品分类（issue #88，docs/erp-mvp-spec.md §3.1/§5）----------
// 分类是租户级主数据，owner = 创建人（spec §4.10），列表/导出按数据范围过滤。

// ListProductCategories 分类全量列表（GET /erp/product-category/list）：
// 返回扁平列表，树组装在前端（spec §5）。
func (h *Handler) ListProductCategories(c *gin.Context) {
	list, err := h.svc.ListProductCategories(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// GetProductCategory 分类详情（不存在 404）。
func (h *Handler) GetProductCategory(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetProductCategory(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateProductCategory 新建分类，返回完整实体。
func (h *Handler) CreateProductCategory(c *gin.Context) {
	var req dto.ProductCategoryCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateProductCategory(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateProductCategory 修改分类。
func (h *Handler) UpdateProductCategory(c *gin.Context) {
	var req dto.ProductCategoryUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProductCategory(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteProductCategory 删除分类（软删除；有子分类 409，被产品引用 409）。
func (h *Handler) DeleteProductCategory(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteProductCategory(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportProductCategories 导出分类（全量列表口径，含数据范围谓词）。
func (h *Handler) ExportProductCategories(c *gin.Context) {
	data, err := h.svc.ExportProductCategories(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-product-categories", "产品分类列表", data)
}

// RegisterProductCategoryRoutes 注册产品分类路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（erp:product-category:*）；静态路由先于 /:id 注册。
func (h *Handler) RegisterProductCategoryRoutes(rg *gin.RouterGroup) {
	category := rg.Group("/erp/product-category")
	category.GET("/list", h.requirePermission("erp:product-category:query"), h.ListProductCategories)
	category.GET("/export-excel", h.requirePermission("erp:product-category:export"), h.ExportProductCategories)
	category.GET("/:id", h.requirePermission("erp:product-category:query"), h.GetProductCategory)
	category.POST("", h.requirePermission("erp:product-category:create"), h.CreateProductCategory)
	category.PUT("", h.requirePermission("erp:product-category:update"), h.UpdateProductCategory)
	category.DELETE("/:id", h.requirePermission("erp:product-category:delete"), h.DeleteProductCategory)
}
