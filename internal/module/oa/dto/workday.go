// Package dto 是 oa 模块的请求/响应 DTO。
package dto

// ---------- 工作日历（issue #50，docs/oa-mvp-spec.md §3.3/§5）----------

// WorkdayDay 是月视图的一行：整月每天一条，无显式记录的日期由默认规则
// 合成（explicit=false，id/remark 为空）。
type WorkdayDay struct {
	// Date 日期 YYYY-MM-DD。
	Date string `json:"date"`
	// Weekday 星期几：0 周日 / 1 周一 … 6 周六（与 time.Weekday 对齐）。
	Weekday int `json:"weekday"`
	// Type 日类型：0 工作日（含周末调休上班）/ 1 休息日 / 2 节假日。
	Type int8 `json:"type"`
	// Explicit 是否来自显式记录（false = 默认规则合成）。
	Explicit bool `json:"explicit"`
	// ID 显式记录 ID（无显式记录为 0）。
	ID int64 `json:"id"`
	// Remark 备注（节假日名称、调休说明等）。
	Remark string `json:"remark"`
}

// WorkdayMonthResp 是按月查看响应（GET /oa/workday/month）。
type WorkdayMonthResp struct {
	// Month 月份 YYYY-MM（回显请求值）。
	Month string `json:"month"`
	// Days 整月逐日，按日升序，覆盖该月每一天。
	Days []WorkdayDay `json:"days"`
}

// WorkdaySetReq 是单日设置请求（PUT /oa/workday）：显式记录 upsert，
// type=0 可表达周末调休上班。
type WorkdaySetReq struct {
	// Date 日期 YYYY-MM-DD。
	Date string `json:"date" binding:"required"`
	// Type 日类型：0 工作日 / 1 休息日 / 2 节假日。
	Type int8 `json:"type"`
	// Remark 备注 ≤200 字。
	Remark string `json:"remark"`
}

// WorkdayBatchReq 是批量设置请求（POST /oa/workday/batch）：dates 优先；
// 未提供 dates 时按 [startDate, endDate] 闭区间展开（weekdays 可选过滤，
// 0=周日 … 6=周六，如整年周末调整为工作日）。
type WorkdayBatchReq struct {
	// Dates 显式日期列表（YYYY-MM-DD，去重后 ≤400 条）。
	Dates []string `json:"dates"`
	// StartDate 区间起点（含），与 EndDate 成对提供。
	StartDate string `json:"startDate"`
	// EndDate 区间终点（含），跨度 ≤400 天。
	EndDate string `json:"endDate"`
	// Weekdays 星期过滤（0 周日 … 6 周六），空 = 区间全部日期。
	Weekdays []int `json:"weekdays"`
	// Type 日类型：0 工作日 / 1 休息日 / 2 节假日。
	Type int8 `json:"type"`
	// Remark 备注 ≤200 字，写入本批全部日期。
	Remark string `json:"remark"`
}

// WorkdayBatchResp 是批量设置响应：count = 实际写入的日期数（去重后）。
type WorkdayBatchResp struct {
	Count int `json:"count"`
}
