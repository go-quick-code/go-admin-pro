package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 销售订单（issue #92，docs/erp-mvp-spec.md §3.4/§4.1/§5）----------
// 单号（orderNo）由服务端发放（入参无单号字段，spec §4.8）；审核/反审核走
// update-status（幂等目标态），权限码独立 erp:sale-order:approve（spec §4.1）。

// PageSaleOrders 销售订单分页。
func (h *Handler) PageSaleOrders(c *gin.Context) {
	var req dto.SaleOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListSaleOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetSaleOrder 销售订单详情（带头 + 明细 + 进度）。
func (h *Handler) GetSaleOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetSaleOrder(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateSaleOrder 新建销售订单（未审核），返回完整主表。
func (h *Handler) CreateSaleOrder(c *gin.Context) {
	var req dto.SaleOrderCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateSaleOrder(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateSaleOrder 修改销售订单（仅未审核；PUT 契约 id 在 body，与 #88–#90 一致）。
func (h *Handler) UpdateSaleOrder(c *gin.Context) {
	var req dto.SaleOrderUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSaleOrder(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateSaleOrderStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateSaleOrderStatus(c *gin.Context) {
	var req dto.SaleDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateSaleOrderStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteSaleOrder 删除销售订单（仅未审核）。
func (h *Handler) DeleteSaleOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteSaleOrder(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportSaleOrders 导出销售订单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportSaleOrders(c *gin.Context) {
	var req dto.SaleOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportSaleOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-sale-orders", "销售订单", data)
}

// RegisterSaleOrderRoutes 注册销售订单路由（静态路由先于 /:id）。
func (h *Handler) RegisterSaleOrderRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/sale-order")
	doc.GET("/page", h.requirePermission("erp:sale-order:query"), h.PageSaleOrders)
	doc.GET("/export-excel", h.requirePermission("erp:sale-order:export"), h.ExportSaleOrders)
	doc.PUT("/update-status", h.requirePermission("erp:sale-order:approve"), h.UpdateSaleOrderStatus)
	doc.GET("/:id", h.requirePermission("erp:sale-order:query"), h.GetSaleOrder)
	doc.POST("", h.requirePermission("erp:sale-order:create"), h.CreateSaleOrder)
	doc.PUT("", h.requirePermission("erp:sale-order:update"), h.UpdateSaleOrder)
	doc.DELETE("/:id", h.requirePermission("erp:sale-order:delete"), h.DeleteSaleOrder)
}
