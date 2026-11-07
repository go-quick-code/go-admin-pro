package dto

import "github.com/go-quick-code/go-admin-pro/internal/pkg/money"

// statistics.go 是 ERP 首页统计的出入参（issue #95，docs/erp-mvp-spec.md §5/§6）。
// 金额一律十进制字符串（internal/pkg/money，ADR 0007）；口径见 service
// statistics.go：已审核出库/入库 − 已审核退货。
//
// summary 返回今日/昨日/本月/今年四个净额；time-summary 返回近 N 个自然月
// （默认 6）按月的净额，空月补 0.00。

// StatisticsSummaryRow 是 GET /erp/(sale|purchase)-statistics/summary 的响应。
type StatisticsSummaryRow struct {
	Today     money.Money `json:"today"`
	Yesterday money.Money `json:"yesterday"`
	Month     money.Money `json:"month"`
	Year      money.Money `json:"year"`
}

// StatisticsTrendPoint 是趋势图的一个自然月（month 形如 2026-11）。
type StatisticsTrendPoint struct {
	Month  string      `json:"month"`
	Amount money.Money `json:"amount"`
}

// StatisticsTimeSummaryReq 是 time-summary 的查询参数：months 缺省 6。
type StatisticsTimeSummaryReq struct {
	Months int `form:"months"`
}

// StatisticsTimeSummaryRow 是 GET /erp/(sale|purchase)-statistics/time-summary
// 的响应：按月升序、含本月、当前月累计至今。
type StatisticsTimeSummaryRow struct {
	Months int                    `json:"months"`
	List   []StatisticsTrendPoint `json:"list"`
}
