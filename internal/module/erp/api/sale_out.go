package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 销售出库单（issue #92，docs/erp-mvp-spec.md §3.4/§4.1/§4.5/§5）----------
// 必挂已审核销售订单（orderId 必填）；单号（no）服务端发放；审核减库存并回写
// 订单进度（out_count），反审核 _CANCEL 反向流水回滚。

// PageSaleOuts 销售出库单分页。
func (h *Handler) PageSaleOuts(c *gin.Context) {
	var req dto.SaleOutPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListSaleOuts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetSaleOut 销售出库单详情（带头 + 明细）。
func (h *Handler) GetSaleOut(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetSaleOut(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateSaleOut 新建销售出库单（未审核）。
func (h *Handler) CreateSaleOut(c *gin.Context) {
	var req dto.SaleOutCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateSaleOut(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateSaleOut 修改销售出库单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdateSaleOut(c *gin.Context) {
	var req dto.SaleOutUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSaleOut(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateSaleOutStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateSaleOutStatus(c *gin.Context) {
	var req dto.SaleDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSaleOutStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteSaleOut 删除销售出库单（仅未审核）。
func (h *Handler) DeleteSaleOut(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteSaleOut(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportSaleOuts 导出销售出库单。
func (h *Handler) ExportSaleOuts(c *gin.Context) {
	var req dto.SaleOutPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportSaleOuts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-sale-outs", "销售出库单", data)
}

// RegisterSaleOutRoutes 注册销售出库单路由（静态路由先于 /:id）。
func (h *Handler) RegisterSaleOutRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/sale-out")
	doc.GET("/page", h.requirePermission("erp:sale-out:query"), h.PageSaleOuts)
	doc.GET("/export-excel", h.requirePermission("erp:sale-out:export"), h.ExportSaleOuts)
	doc.PUT("/update-status", h.requirePermission("erp:sale-out:approve"), h.UpdateSaleOutStatus)
	doc.GET("/:id", h.requirePermission("erp:sale-out:query"), h.GetSaleOut)
	doc.POST("", h.requirePermission("erp:sale-out:create"), h.CreateSaleOut)
	doc.PUT("", h.requirePermission("erp:sale-out:update"), h.UpdateSaleOut)
	doc.DELETE("/:id", h.requirePermission("erp:sale-out:delete"), h.DeleteSaleOut)
}
