package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 商品分类（租户级两级树，issue #176） ----------

// TreeProductCategories 分类树（GET /mall/product/category/tree）。
func (h *Handler) TreeProductCategories(c *gin.Context) {
	tree, err := h.svc.TreeProductCategories(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tree)
}

// GetProductCategory 分类详情。
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

// CreateProductCategory 创建分类（两级树父级校验；同级同名 409）。
func (h *Handler) CreateProductCategory(c *gin.Context) {
	var req dto.CategorySaveReq
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

// UpdateProductCategory 修改分类（id 在 body）。
func (h *Handler) UpdateProductCategory(c *gin.Context) {
	var req dto.CategoryUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProductCategory(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteProductCategory 删除分类（有子级或被 SPU 引用 409）。
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

// ---------- 商品品牌（租户级，issue #176） ----------

// PageBrands 分页查询品牌。
func (h *Handler) PageBrands(c *gin.Context) {
	var req dto.BrandPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListBrands(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetBrand 品牌详情。
func (h *Handler) GetBrand(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetBrand(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateBrand 创建品牌（name 租户内唯一，撞键 409）。
func (h *Handler) CreateBrand(c *gin.Context) {
	var req dto.BrandSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateBrand(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateBrand 修改品牌（id 在 body）。
func (h *Handler) UpdateBrand(c *gin.Context) {
	var req dto.BrandUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateBrand(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteBrand 删除品牌。
func (h *Handler) DeleteBrand(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteBrand(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 商品属性名（租户级，issue #176） ----------

// PageProperties 分页查询属性名。
func (h *Handler) PageProperties(c *gin.Context) {
	var req dto.PropertyPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListProperties(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetProductProperty 属性名详情（不含值——值清单走 ListPropertyValues）。
func (h *Handler) GetProductProperty(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetProductProperty(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateProperty 创建属性名（name 租户内唯一，撞键 409）。
func (h *Handler) CreateProperty(c *gin.Context) {
	var req dto.PropertySaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateProperty(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateProperty 修改属性名（id 在 body）。
func (h *Handler) UpdateProperty(c *gin.Context) {
	var req dto.PropertyUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProperty(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteProperty 删除属性名（同事务级联软删其属性值）。
func (h *Handler) DeleteProperty(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteProperty(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 商品属性值（属性名子资源，issue #176） ----------

// ListPropertyValues 属性名下的全部属性值（GET /mall/product/property/:id/value）。
func (h *Handler) ListPropertyValues(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	list, err := h.svc.ListPropertyValues(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// CreatePropertyValue 创建属性值（POST /mall/product/property/value，
// propertyId 在 body；同属性同名值 409）。
func (h *Handler) CreatePropertyValue(c *gin.Context) {
	var req dto.PropertyValueSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreatePropertyValue(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdatePropertyValue 修改属性值（PUT /mall/product/property/value，id 在 body）。
func (h *Handler) UpdatePropertyValue(c *gin.Context) {
	var req dto.PropertyValueUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePropertyValue(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeletePropertyValue 删除属性值（DELETE /mall/product/property/value/:id）。
func (h *Handler) DeletePropertyValue(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeletePropertyValue(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
