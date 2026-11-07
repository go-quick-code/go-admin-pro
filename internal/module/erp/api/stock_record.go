package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 出入库流水（issue #90，docs/erp-mvp-spec.md §5：查询页）----------
// 流水 append-only：只读页面（无写路径），按 biz_type/仓库/产品/时间筛选。

// PageStockRecords 流水分页。
func (h *Handler) PageStockRecords(c *gin.Context) {
	var req dto.StockRecordPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListStockRecords(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// ExportStockRecords 导出流水（复用列表筛选口径，spec §4.11）。
func (h *Handler) ExportStockRecords(c *gin.Context) {
	var req dto.StockRecordPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportStockRecords(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-stock-records", "出入库流水", data)
}

// RegisterStockRecordRoutes 注册流水路由。
func (h *Handler) RegisterStockRecordRoutes(rg *gin.RouterGroup) {
	record := rg.Group("/erp/stock-record")
	record.GET("/page", h.requirePermission("erp:stock-record:query"), h.PageStockRecords)
	record.GET("/export-excel", h.requirePermission("erp:stock-record:export"), h.ExportStockRecords)
}
