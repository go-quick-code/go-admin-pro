package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 商品 SPU/SKU（issue #177） ----------

// PageProductSPUs 分页查询 SPU（GET /mall/product/spu/page，分类/品牌/状态/
// 关键词过滤）。
func (h *Handler) PageProductSPUs(c *gin.Context) {
	var req dto.SPUPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageProductSPUs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetProductSPU SPU 详情（含 SKU 列表，主 SKU 恒首位）。
func (h *Handler) GetProductSPU(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetProductSPU(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateProductSPU 创建 SPU（SPU+SKU 同事务；name 租户内唯一 409，分类
// 非叶子 400，规格引用属性值不存在/不归属 400）。
func (h *Handler) CreateProductSPU(c *gin.Context) {
	var req dto.SPUSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateProductSPU(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateProductSPU 修改 SPU（id 在 body；字段全量覆盖 + SKU 集全量替换）。
func (h *Handler) UpdateProductSPU(c *gin.Context) {
	var req dto.SPUUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProductSPU(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateProductSPUStatus 上下架（PUT /mall/product/spu/:id/status）：
// 0 下架 / 1 上架。
func (h *Handler) UpdateProductSPUStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.SPUStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProductSPUStatus(c.Request.Context(), id, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteProductSPU 删除 SPU（被订单引用 409——T6 接线前恒放行；级联软删
// 其 SKU）。
func (h *Handler) DeleteProductSPU(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteProductSPU(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
