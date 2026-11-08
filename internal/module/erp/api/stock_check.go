package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 库存盘点单（issue #93，docs/erp-mvp-spec.md §3.5/§4.1/§4.6/§5）----------
// 单号由服务端发放（入参无单号字段，spec §4.8）；审核/反审核走 update-status
//（幂等目标态），权限码独立 erp:stock-check:approve（spec §4.1）。

// PageStockChecks 库存盘点单分页。
func (h *Handler) PageStockChecks(c *gin.Context) {
	var req dto.StockCheckPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListStockChecks(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetStockCheck 库存盘点单详情（带头 + 明细，含 baseCount 快照）。
func (h *Handler) GetStockCheck(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetStockCheck(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateStockCheck 新建库存盘点单（未审核），返回完整主表。
func (h *Handler) CreateStockCheck(c *gin.Context) {
	var req dto.StockCheckCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateStockCheck(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateStockCheck 修改库存盘点单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdateStockCheck(c *gin.Context) {
	var req dto.StockCheckUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockCheck(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateStockCheckStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateStockCheckStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockCheckStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteStockCheck 删除库存盘点单（仅未审核）。
func (h *Handler) DeleteStockCheck(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteStockCheck(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportStockChecks 导出库存盘点单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportStockChecks(c *gin.Context) {
	var req dto.StockCheckPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportStockChecks(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-stock-checks", "库存盘点单", data)
}

// RegisterStockCheckRoutes 注册库存盘点单路由（静态路由先于 /:id）。
func (h *Handler) RegisterStockCheckRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/stock-check")
	doc.GET("/page", h.requirePermission("erp:stock-check:query"), h.PageStockChecks)
	doc.GET("/export-excel", h.requirePermission("erp:stock-check:export"), h.ExportStockChecks)
	doc.PUT("/update-status", h.requirePermission("erp:stock-check:approve"), h.UpdateStockCheckStatus)
	doc.GET("/:id", h.requirePermission("erp:stock-check:query"), h.GetStockCheck)
	doc.POST("", h.requirePermission("erp:stock-check:create"), h.CreateStockCheck)
	doc.PUT("", h.requirePermission("erp:stock-check:update"), h.UpdateStockCheck)
	doc.DELETE("/:id", h.requirePermission("erp:stock-check:delete"), h.DeleteStockCheck)
}
