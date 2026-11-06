// attendance_summary.go 承载考勤月度汇总的请求/响应 DTO（issue #62，
// docs/oa-mvp-spec.md §4.10「月度汇总」）：
//   - 管理端汇总分页/单用户查询挂 oa:attendance:summary:query（菜单 534）；
//   - 员工本月汇总卡片在「我的考勤」登录即可（强制本人）；
//   - month 缺省取业务时区当月；聚合口径见 service.AggregateAttendanceMonth。
package dto

// AttendanceSummaryPageReq 管理端月度汇总分页：month=YYYY-MM（缺省当月）、
// userId 精确过滤（0=按可见范围）、pageNo/pageSize 按**用户**分页
// （每行是一个用户当月的汇总）。
type AttendanceSummaryPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Month    string `form:"month"`
	UserID   int64  `form:"userId"`
}

// AttendanceSummaryOfUserReq 单用户月度汇总（可见范围外/跨租户 404）。
type AttendanceSummaryOfUserReq struct {
	Month  string `form:"month"`
	UserID int64  `form:"userId"`
}

// MyAttendanceSummaryReq 员工本人月度汇总（服务端强制本人；month 缺省当月）。
type MyAttendanceSummaryReq struct {
	Month string `form:"month"`
}

// AttendanceSummaryResp 是某用户某月的考勤汇总响应行：
//   - days 结果行覆盖的自然日数；workDays 工作日数（生效状态非休息）；
//   - attendanceDays 出勤天数 = normal+late+early+lateAndEarly；
//   - normal/late/early/lateAndEarly/missing/rest/leave 按生效状态（校正优先）
//     计数；leaveDays 请假天数合计（含半天 0.5）；
//   - lateMinutes/earlyMinutes 迟到/早退分钟合计。
type AttendanceSummaryResp struct {
	UserID         int64   `json:"userId"`
	Nickname       string  `json:"nickname"`
	Month          string  `json:"month"`
	Days           int     `json:"days"`
	WorkDays       int     `json:"workDays"`
	AttendanceDays int     `json:"attendanceDays"`
	Normal         int     `json:"normal"`
	Late           int     `json:"late"`
	Early          int     `json:"early"`
	LateAndEarly   int     `json:"lateAndEarly"`
	Missing        int     `json:"missing"`
	Rest           int     `json:"rest"`
	Leave          int     `json:"leave"`
	LeaveDays      float64 `json:"leaveDays"`
	LateMinutes    int     `json:"lateMinutes"`
	EarlyMinutes   int     `json:"earlyMinutes"`
}
