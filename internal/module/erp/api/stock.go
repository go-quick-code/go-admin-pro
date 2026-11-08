package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 库存（issue #90，docs/erp-mvp-spec.md §5：仓库×产品余额 + get-count）----------
// 库存行无 owner 语义（仓库不做独立权限，spec §4.10），租户内全量可见。

// PageStocks 库存余额分页（仓库×产品，名称服务端解析）。
func (h *Handler) PageStocks(c *gin.Context) {
	var req dto.StockPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListStocks(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetStockCount 产品库存合计（warehouseId 可选：带则单仓余额，不带全仓合计）。
func (h *Handler) GetStockCount(c *gin.Context) {
	var req dto.StockCountQuery
	if !bindQuery(c, &req) {
		return
	}
	total, err := h.svc.GetStockCount(c.Request.Context(), req.ProductId, req.WarehouseId)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dto.StockCountResp{
		ProductId:   req.ProductId,
		WarehouseId: req.WarehouseId,
		Count:       total,
	})
}

// ExportStocks 导出库存余额（复用列表筛选口径，spec §4.11）。
func (h *Handler) ExportStocks(c *gin.Context) {
	var req dto.StockPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportStocks(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-stocks", "产品库存", data)
}

// RegisterStockRoutes 注册库存路由（静态路由先于参数路由，权限码与菜单 seed 一致）。
func (h *Handler) RegisterStockRoutes(rg *gin.RouterGroup) {
	stock := rg.Group("/erp/stock")
	stock.GET("/page", h.requirePermission("erp:stock:query"), h.PageStocks)
	stock.GET("/get-count", h.requirePermission("erp:stock:query"), h.GetStockCount)
	stock.GET("/export-excel", h.requirePermission("erp:stock:export"), h.ExportStocks)
}
