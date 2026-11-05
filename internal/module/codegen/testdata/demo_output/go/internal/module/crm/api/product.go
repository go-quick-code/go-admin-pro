package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------

// PageProducts 产品分页（筛选 + 数据范围过滤）。
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

// GetProduct 产品详情（数据范围外 404）。
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

// RegisterProductRoutes 注册产品路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（crm:product:*）。
func (h *Handler) RegisterProductRoutes(rg *gin.RouterGroup) {
	product := rg.Group("/crm/product")
	product.GET("/page", h.requirePermission("crm:product:query"), h.PageProducts)
	product.GET("/:id", h.requirePermission("crm:product:query"), h.GetProduct)
	product.POST("", h.requirePermission("crm:product:create"), h.CreateProduct)
	product.PUT("", h.requirePermission("crm:product:update"), h.UpdateProduct)
	product.DELETE("/:id", h.requirePermission("crm:product:delete"), h.DeleteProduct)

	// TODO(#20): Excel 导入导出接线（internal/pkg/excel）——路由先行留位，实现随 #20：
	//	product.POST("/import", h.requirePermission("crm:product:import"), h.ImportProduct)
	//	product.GET("/export", h.requirePermission("crm:product:export"), h.ExportProduct)
	//	product.GET("/import-template", h.requirePermission("crm:product:import"), h.ImportProductTemplate)
}
