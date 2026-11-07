package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 采购退货单（RT，issue #91，docs/erp-mvp-spec.md §3.3/§4.1/§4.4/§5）----------
// 必挂已审核入库单（409 校验），仓库取自入库单（入参不收仓库字段）；审核经唯一
// 入账入口减库存并回写订单进度；单号由服务端发放；审核/反审核权限码
// erp:purchase-return:approve。

// PagePurchaseReturns 采购退货单分页。
func (h *Handler) PagePurchaseReturns(c *gin.Context) {
	var req dto.PurchaseReturnPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListPurchaseReturns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetPurchaseReturn 采购退货单详情（带头 + 明细）。
func (h *Handler) GetPurchaseReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetPurchaseReturn(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreatePurchaseReturn 新建采购退货单（未审核，必挂已审核入库单）。
func (h *Handler) CreatePurchaseReturn(c *gin.Context) {
	var req dto.PurchaseReturnCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreatePurchaseReturn(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdatePurchaseReturn 修改采购退货单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdatePurchaseReturn(c *gin.Context) {
	var req dto.PurchaseReturnUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePurchaseReturn(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdatePurchaseReturnStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdatePurchaseReturnStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePurchaseReturnStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeletePurchaseReturn 删除采购退货单（仅未审核；删除自然释放占用）。
func (h *Handler) DeletePurchaseReturn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeletePurchaseReturn(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportPurchaseReturns 导出采购退货单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportPurchaseReturns(c *gin.Context) {
	var req dto.PurchaseReturnPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportPurchaseReturns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-purchase-returns", "采购退货单", data)
}

// RegisterPurchaseReturnRoutes 注册采购退货单路由（静态路由先于 /:id）。
func (h *Handler) RegisterPurchaseReturnRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/purchase-return")
	doc.GET("/page", h.requirePermission("erp:purchase-return:query"), h.PagePurchaseReturns)
	doc.GET("/export-excel", h.requirePermission("erp:purchase-return:export"), h.ExportPurchaseReturns)
	doc.PUT("/update-status", h.requirePermission("erp:purchase-return:approve"), h.UpdatePurchaseReturnStatus)
	doc.GET("/:id", h.requirePermission("erp:purchase-return:query"), h.GetPurchaseReturn)
	doc.POST("", h.requirePermission("erp:purchase-return:create"), h.CreatePurchaseReturn)
	doc.PUT("", h.requirePermission("erp:purchase-return:update"), h.UpdatePurchaseReturn)
	doc.DELETE("/:id", h.requirePermission("erp:purchase-return:delete"), h.DeletePurchaseReturn)
}
