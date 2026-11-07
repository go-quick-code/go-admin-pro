package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 其它入库单（issue #90，docs/erp-mvp-spec.md §3.5/§4.1/§5）----------
// 单号由服务端发放（入参无单号字段，spec §4.8）；审核/反审核走 update-status
//（幂等目标态），权限码独立 erp:stock-in:approve（spec §4.1）。

// PageStockIns 其它入库单分页。
func (h *Handler) PageStockIns(c *gin.Context) {
	var req dto.StockInPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListStockIns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetStockIn 其它入库单详情（带头 + 明细）。
func (h *Handler) GetStockIn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetStockIn(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateStockIn 新建其它入库单（未审核），返回完整主表。
func (h *Handler) CreateStockIn(c *gin.Context) {
	var req dto.StockInCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateStockIn(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateStockIn 修改其它入库单（仅未审核；PUT 契约 id 在 body，与 #88/#89 一致）。
func (h *Handler) UpdateStockIn(c *gin.Context) {
	var req dto.StockInUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockIn(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateStockInStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateStockInStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockInStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteStockIn 删除其它入库单（仅未审核）。
func (h *Handler) DeleteStockIn(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteStockIn(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportStockIns 导出其它入库单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportStockIns(c *gin.Context) {
	var req dto.StockInPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportStockIns(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-stock-ins", "其它入库单", data)
}

// RegisterStockInRoutes 注册其它入库单路由（静态路由先于 /:id）。
func (h *Handler) RegisterStockInRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/stock-in")
	doc.GET("/page", h.requirePermission("erp:stock-in:query"), h.PageStockIns)
	doc.GET("/export-excel", h.requirePermission("erp:stock-in:export"), h.ExportStockIns)
	doc.PUT("/update-status", h.requirePermission("erp:stock-in:approve"), h.UpdateStockInStatus)
	doc.GET("/:id", h.requirePermission("erp:stock-in:query"), h.GetStockIn)
	doc.POST("", h.requirePermission("erp:stock-in:create"), h.CreateStockIn)
	doc.PUT("", h.requirePermission("erp:stock-in:update"), h.UpdateStockIn)
	doc.DELETE("/:id", h.requirePermission("erp:stock-in:delete"), h.DeleteStockIn)
}
