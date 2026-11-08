package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 销售退货单（issue #92，docs/erp-mvp-spec.md §3.4/§4.1/§4.5/§5）----------
// 必挂已审核销售出库单（outId 必填，仓库取自出库单服务端派生）；单号（no）服务
// 端发放；审核回库存并回写订单进度（return_count），反审核 _CANCEL 反向流水。

// PageSaleReturns 销售退货单分页。
func (h *Handler) PageSaleReturns(c *gin.Context) {
	var req dto.SaleReturnPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListSaleReturns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetSaleReturn 销售退货单详情（带头 + 明细）。
func (h *Handler) GetSaleReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetSaleReturn(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateSaleReturn 新建销售退货单（未审核）。
func (h *Handler) CreateSaleReturn(c *gin.Context) {
	var req dto.SaleReturnCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateSaleReturn(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateSaleReturn 修改销售退货单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdateSaleReturn(c *gin.Context) {
	var req dto.SaleReturnUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSaleReturn(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateSaleReturnStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateSaleReturnStatus(c *gin.Context) {
	var req dto.SaleDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSaleReturnStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteSaleReturn 删除销售退货单（仅未审核）。
func (h *Handler) DeleteSaleReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteSaleReturn(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportSaleReturns 导出销售退货单。
func (h *Handler) ExportSaleReturns(c *gin.Context) {
	var req dto.SaleReturnPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportSaleReturns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-sale-returns", "销售退货单", data)
}

// RegisterSaleReturnRoutes 注册销售退货单路由（静态路由先于 /:id）。
func (h *Handler) RegisterSaleReturnRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/sale-return")
	doc.GET("/page", h.requirePermission("erp:sale-return:query"), h.PageSaleReturns)
	doc.GET("/export-excel", h.requirePermission("erp:sale-return:export"), h.ExportSaleReturns)
	doc.PUT("/update-status", h.requirePermission("erp:sale-return:approve"), h.UpdateSaleReturnStatus)
	doc.GET("/:id", h.requirePermission("erp:sale-return:query"), h.GetSaleReturn)
	doc.POST("", h.requirePermission("erp:sale-return:create"), h.CreateSaleReturn)
	doc.PUT("", h.requirePermission("erp:sale-return:update"), h.UpdateSaleReturn)
	doc.DELETE("/:id", h.requirePermission("erp:sale-return:delete"), h.DeleteSaleReturn)
}
