package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 采购订单（CG，issue #91，docs/erp-mvp-spec.md §3.3/§4.1/§4.4/§5）----------
// 单号由服务端发放（入参无单号字段，spec §4.8）；审核/反审核走 update-status
//（幂等目标态），权限码独立 erp:purchase-order:approve（spec §4.1）。

// PagePurchaseOrders 采购订单分页。
func (h *Handler) PagePurchaseOrders(c *gin.Context) {
	var req dto.PurchaseOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListPurchaseOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetPurchaseOrder 采购订单详情（带头 + 明细 + 进度回写列）。
func (h *Handler) GetPurchaseOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetPurchaseOrder(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreatePurchaseOrder 新建采购订单（未审核），返回完整主表。
func (h *Handler) CreatePurchaseOrder(c *gin.Context) {
	var req dto.PurchaseOrderCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreatePurchaseOrder(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdatePurchaseOrder 修改采购订单（仅未审核；PUT 契约 id 在 body，与 #88–#90 一致）。
func (h *Handler) UpdatePurchaseOrder(c *gin.Context) {
	var req dto.PurchaseOrderUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePurchaseOrder(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdatePurchaseOrderStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdatePurchaseOrderStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePurchaseOrderStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeletePurchaseOrder 删除采购订单（仅未审核）。
func (h *Handler) DeletePurchaseOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeletePurchaseOrder(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportPurchaseOrders 导出采购订单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportPurchaseOrders(c *gin.Context) {
	var req dto.PurchaseOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportPurchaseOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-purchase-orders", "采购订单", data)
}

// RegisterPurchaseOrderRoutes 注册采购订单路由（静态路由先于 /:id）。
func (h *Handler) RegisterPurchaseOrderRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/purchase-order")
	doc.GET("/page", h.requirePermission("erp:purchase-order:query"), h.PagePurchaseOrders)
	doc.GET("/export-excel", h.requirePermission("erp:purchase-order:export"), h.ExportPurchaseOrders)
	doc.PUT("/update-status", h.requirePermission("erp:purchase-order:approve"), h.UpdatePurchaseOrderStatus)
	doc.GET("/:id", h.requirePermission("erp:purchase-order:query"), h.GetPurchaseOrder)
	doc.POST("", h.requirePermission("erp:purchase-order:create"), h.CreatePurchaseOrder)
	doc.PUT("", h.requirePermission("erp:purchase-order:update"), h.UpdatePurchaseOrder)
	doc.DELETE("/:id", h.requirePermission("erp:purchase-order:delete"), h.DeletePurchaseOrder)
}
