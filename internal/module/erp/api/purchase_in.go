package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 采购入库单（RK，issue #91，docs/erp-mvp-spec.md §3.3/§4.1/§4.4/§5）----------
// 必挂已审核订单（409 校验）；审核经唯一入账入口增库存并回写订单进度；
// 单号由服务端发放；审核/反审核权限码 erp:purchase-in:approve。

// PagePurchaseIns 采购入库单分页。
func (h *Handler) PagePurchaseIns(c *gin.Context) {
	var req dto.PurchaseInPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListPurchaseIns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetPurchaseIn 采购入库单详情（带头 + 明细）。
func (h *Handler) GetPurchaseIn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetPurchaseIn(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreatePurchaseIn 新建采购入库单（未审核，必挂已审核订单）。
func (h *Handler) CreatePurchaseIn(c *gin.Context) {
	var req dto.PurchaseInCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreatePurchaseIn(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdatePurchaseIn 修改采购入库单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdatePurchaseIn(c *gin.Context) {
	var req dto.PurchaseInUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePurchaseIn(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdatePurchaseInStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdatePurchaseInStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePurchaseInStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeletePurchaseIn 删除采购入库单（仅未审核；删除自然释放订单占用）。
func (h *Handler) DeletePurchaseIn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeletePurchaseIn(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportPurchaseIns 导出采购入库单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportPurchaseIns(c *gin.Context) {
	var req dto.PurchaseInPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportPurchaseIns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-purchase-ins", "采购入库单", data)
}

// RegisterPurchaseInRoutes 注册采购入库单路由（静态路由先于 /:id）。
func (h *Handler) RegisterPurchaseInRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/purchase-in")
	doc.GET("/page", h.requirePermission("erp:purchase-in:query"), h.PagePurchaseIns)
	doc.GET("/export-excel", h.requirePermission("erp:purchase-in:export"), h.ExportPurchaseIns)
	doc.PUT("/update-status", h.requirePermission("erp:purchase-in:approve"), h.UpdatePurchaseInStatus)
	doc.GET("/:id", h.requirePermission("erp:purchase-in:query"), h.GetPurchaseIn)
	doc.POST("", h.requirePermission("erp:purchase-in:create"), h.CreatePurchaseIn)
	doc.PUT("", h.requirePermission("erp:purchase-in:update"), h.UpdatePurchaseIn)
	doc.DELETE("/:id", h.requirePermission("erp:purchase-in:delete"), h.DeletePurchaseIn)
}
