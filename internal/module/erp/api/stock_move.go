package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 库存调拨单（issue #93，docs/erp-mvp-spec.md §3.5/§4.1/§4.6/§5）----------
// 单号由服务端发放（入参无单号字段，spec §4.8）；审核/反审核走 update-status
//（幂等目标态），权限码独立 erp:stock-move:approve（spec §4.1）。

// PageStockMoves 库存调拨单分页。
func (h *Handler) PageStockMoves(c *gin.Context) {
	var req dto.StockMovePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListStockMoves(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetStockMove 库存调拨单详情（带头 + 明细）。
func (h *Handler) GetStockMove(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetStockMove(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateStockMove 新建库存调拨单（未审核），返回完整主表。
func (h *Handler) CreateStockMove(c *gin.Context) {
	var req dto.StockMoveCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateStockMove(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateStockMove 修改库存调拨单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdateStockMove(c *gin.Context) {
	var req dto.StockMoveUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockMove(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateStockMoveStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateStockMoveStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockMoveStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteStockMove 删除库存调拨单（仅未审核）。
func (h *Handler) DeleteStockMove(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteStockMove(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportStockMoves 导出库存调拨单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportStockMoves(c *gin.Context) {
	var req dto.StockMovePageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportStockMoves(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-stock-moves", "库存调拨单", data)
}

// RegisterStockMoveRoutes 注册库存调拨单路由（静态路由先于 /:id）。
func (h *Handler) RegisterStockMoveRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/stock-move")
	doc.GET("/page", h.requirePermission("erp:stock-move:query"), h.PageStockMoves)
	doc.GET("/export-excel", h.requirePermission("erp:stock-move:export"), h.ExportStockMoves)
	doc.PUT("/update-status", h.requirePermission("erp:stock-move:approve"), h.UpdateStockMoveStatus)
	doc.GET("/:id", h.requirePermission("erp:stock-move:query"), h.GetStockMove)
	doc.POST("", h.requirePermission("erp:stock-move:create"), h.CreateStockMove)
	doc.PUT("", h.requirePermission("erp:stock-move:update"), h.UpdateStockMove)
	doc.DELETE("/:id", h.requirePermission("erp:stock-move:delete"), h.DeleteStockMove)
}
