// leave_attendance_rule.go 承载请假—考勤联动的**纯函数**（issue #62，
// docs/oa-mvp-spec.md §4.8/§4.10）。无存储访问、无 HTTP 上下文：
//
//   - 请假天数口径：复用 #50 的槽位规则（ResolveWorkdaySlots）——整天请假
//     一天贡献 2 个槽位 = 1 天，单侧半天 = 0.5 天，跨天逐日累加；休息日/
//     节假日不贡献（非工作日不产生请假天数）；
//   - 月度汇总口径：按**生效状态**（override 优先，与列表/每日结果一致）
//     逐项计数，请假天数累加结果行的 leave_days（半天 0.5 计入），
//     出勤天数 = 正常 + 迟到 + 早退 + 迟到且早退。
//
// 生产取数编排见 leave_attendance.go（LeaveDaysProvider）与
// attendance_summary.go（月度汇总 service）。
package service

import (
	"sort"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// LeaveDaysOnDate 返回请假区间 [start, end]（含起止半天）在 date 当天贡献的
// 请假天数（0 / 0.5 / 1）。槽位口径与 #52 请假时长完全一致：非工作日不贡献，
// 起始日下午起、结束日上午止各按半天裁剪；非法区间/半天取值返回 0。
func LeaveDaysOnDate(date, start, end time.Time, startHalf, endHalf int8, explicit ExplicitDays) float64 {
	key := DateKey(date)
	slots := ResolveWorkdaySlots(start, end, startHalf, endHalf, explicit)
	count := 0
	for _, slot := range slots {
		if DateKey(slot.Date) == key {
			count++
		}
	}
	return float64(count) / 2
}

// EffectiveAttendanceResultStatus 返回结果行的生效状态：管理员校正
// （override_status）优先于派生 status，非法 override 值（脏数据）回落派生。
func EffectiveAttendanceResultStatus(row model.AttendanceResult) int8 {
	if row.OverrideStatus != nil && model.ValidAttendanceResultStatus(*row.OverrideStatus) {
		return *row.OverrideStatus
	}
	return row.Status
}

// AttendanceSummary 是某用户某月的考勤汇总（纯聚合结果，与 API 响应同构）：
//   - Days 结果行覆盖的自然日数（含休息日；无结果的日子不计）；
//   - WorkDays 工作日数（生效状态非休息的自然日）；
//   - AttendanceDays 出勤天数 = Normal + Late + Early + LateAndEarly
//     （上下班卡齐全、未整天请假的日子）；
//   - Rest/Leave 休息/整天请假天数；LeaveDays 请假天数合计（含半天 0.5）；
//   - LateMinutes/EarlyMinutes 迟到/早退分钟合计。
type AttendanceSummary struct {
	UserID         int64
	Month          string
	Days           int
	WorkDays       int
	AttendanceDays int
	Normal         int
	Late           int
	Early          int
	LateAndEarly   int
	Missing        int
	Rest           int
	Leave          int
	LeaveDays      float64
	LateMinutes    int
	EarlyMinutes   int
}

// AggregateAttendanceMonth 聚合单用户一个月的每日结果（纯函数）：只应传入该
// 用户的当月结果行；空切片返回仅回显 UserID/Month 的零值（空月份）。
func AggregateAttendanceMonth(month string, userID int64, rows []model.AttendanceResult) AttendanceSummary {
	out := AttendanceSummary{UserID: userID, Month: month}
	for i := range rows {
		row := &rows[i]
		out.Days++
		status := EffectiveAttendanceResultStatus(*row)
		if status != model.AttendanceResultRest {
			out.WorkDays++
		}
		switch status {
		case model.AttendanceResultNormal:
			out.Normal++
			out.AttendanceDays++
		case model.AttendanceResultLate:
			out.Late++
			out.AttendanceDays++
		case model.AttendanceResultEarly:
			out.Early++
			out.AttendanceDays++
		case model.AttendanceResultLateAndEarly:
			out.LateAndEarly++
			out.AttendanceDays++
		case model.AttendanceResultMissing:
			out.Missing++
		case model.AttendanceResultRest:
			out.Rest++
		case model.AttendanceResultLeave:
			out.Leave++
		}
		out.LeaveDays += row.LeaveDays
		out.LateMinutes += row.LateMinutes
		out.EarlyMinutes += row.EarlyMinutes
	}
	return out
}

// AggregateAttendanceMonthByUser 把一组结果行按用户聚合为月度汇总（用户 ID
// 升序，保证分页稳定）。用户集合 = 结果行里出现过的用户（没有结果的日子不
// 造行：未入组/未生成结果的人不出现在汇总里）。
func AggregateAttendanceMonthByUser(month string, rows []model.AttendanceResult) []AttendanceSummary {
	grouped := make(map[int64][]model.AttendanceResult)
	ids := make([]int64, 0, len(rows))
	for i := range rows {
		row := rows[i]
		if _, ok := grouped[row.UserID]; !ok {
			ids = append(ids, row.UserID)
		}
		grouped[row.UserID] = append(grouped[row.UserID], row)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]AttendanceSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, AggregateAttendanceMonth(month, id, grouped[id]))
	}
	return out
}
