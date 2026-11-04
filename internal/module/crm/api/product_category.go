package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 产品分类（issue #11，手写：codegen 不生成树逻辑）----------
// 分类是租户级共享资源：普通 CRUD + 权限码，无数据范围谓词。

// TreeProductCategories 产品分类树（GET /crm/product-category/tree）。
func (h *Handler) TreeProductCategories(c *gin.Context) {
	tree, err := h.svc.TreeProductCategories(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tree)
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

// DeleteProductCategory 删除分类（软删除；有子分类/有产品由 service 拒绝）。
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

// RegisterProductCategoryRoutes 注册产品分类路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（crm:product-category:*）。
func (h *Handler) RegisterProductCategoryRoutes(rg *gin.RouterGroup) {
	category := rg.Group("/crm/product-category")
	category.GET("/tree", h.requirePermission("crm:product-category:query"), h.TreeProductCategories)
	category.POST("", h.requirePermission("crm:product-category:create"), h.CreateProductCategory)
	category.PUT("", h.requirePermission("crm:product-category:update"), h.UpdateProductCategory)
	category.DELETE("/:id", h.requirePermission("crm:product-category:delete"), h.DeleteProductCategory)
}
