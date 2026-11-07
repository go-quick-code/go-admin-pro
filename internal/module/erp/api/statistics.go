package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 首页统计（issue #95，docs/erp-mvp-spec.md §5/§6）----------
// 四个只读接口：销售/采购 × summary/time-summary，统一权限码
// erp:statistics:query（菜单 701）。口径：已审核出库/入库 − 已审核退货，
// 按 out_time/in_time/return_time 落业务时间区间（业务时区，ADR 0006）；
// 数据范围与租户隔离由 service/仓储保证。

// SaleStatisticsSummary 销售汇总（今日/昨日/本月/今年）。
func (h *Handler) SaleStatisticsSummary(c *gin.Context) {
	row, err := h.svc.SaleStatisticsSummary(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// SaleStatisticsTimeSummary 销售近 N 个月（默认 6）按月趋势。
func (h *Handler) SaleStatisticsTimeSummary(c *gin.Context) {
	var req dto.StatisticsTimeSummaryReq
	if !bindQuery(c, &req) {
		return
	}
	row, err := h.svc.SaleStatisticsTimeSummary(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// PurchaseStatisticsSummary 采购汇总（今日/昨日/本月/今年）。
func (h *Handler) PurchaseStatisticsSummary(c *gin.Context) {
	row, err := h.svc.PurchaseStatisticsSummary(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// PurchaseStatisticsTimeSummary 采购近 N 个月（默认 6）按月趋势。
func (h *Handler) PurchaseStatisticsTimeSummary(c *gin.Context) {
	var req dto.StatisticsTimeSummaryReq
	if !bindQuery(c, &req) {
		return
	}
	row, err := h.svc.PurchaseStatisticsTimeSummary(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// RegisterStatisticsRoutes 注册销售/采购统计路由。
func (h *Handler) RegisterStatisticsRoutes(rg *gin.RouterGroup) {
	sale := rg.Group("/erp/sale-statistics")
	sale.GET("/summary", h.requirePermission("erp:statistics:query"), h.SaleStatisticsSummary)
	sale.GET("/time-summary", h.requirePermission("erp:statistics:query"), h.SaleStatisticsTimeSummary)
	purchase := rg.Group("/erp/purchase-statistics")
	purchase.GET("/summary", h.requirePermission("erp:statistics:query"), h.PurchaseStatisticsSummary)
	purchase.GET("/time-summary", h.requirePermission("erp:statistics:query"), h.PurchaseStatisticsTimeSummary)
}
