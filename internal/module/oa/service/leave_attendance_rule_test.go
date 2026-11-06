package service

import (
	"math"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// 请假—考勤联动纯函数表驱动测试（issue #62，docs/oa-mvp-spec.md §4.8/§4.10）：
//   - 请假天数口径：整天 = 1、单侧半天 = 0.5、跨天逐日；休息日/节假日不贡献
//     （与 #50 槽位口径一致）；
//   - 月度汇总口径：按生效状态（override 优先）计数，请假天数累加 leave_days，
//     与每日结果逐项一致。

// leaveDay 构造 2026-03 的 UTC 零点日期（2026-03-02 是周一，03-07/03-08 周末）。
func leaveDay(day int) time.Time {
	return time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC)
}

// TestLeaveDaysOnDate 锁定单日请假天数映射（生产取数按日调用）。
func TestLeaveDaysOnDate(t *testing.T) {
	cases := []struct {
		name      string
		date      string
		start     string
		startHalf int8
		end       string
		endHalf   int8
		explicit  ExplicitDays
		want      float64
	}{
		{
			name: "单日整天_上午到下午",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-02", endHalf: HalfPM, want: 1,
		},
		{
			name: "单日上午半天",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-02", endHalf: HalfAM, want: 0.5,
		},
		{
			name: "单日下午半天",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfPM,
			end: "2026-03-02", endHalf: HalfPM, want: 0.5,
		},
		{
			name: "跨天整天_首日",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-04", endHalf: HalfPM, want: 1,
		},
		{
			name: "跨天整天_中间日",
			date: "2026-03-03", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-04", endHalf: HalfPM, want: 1,
		},
		{
			name: "跨天整天_末日",
			date: "2026-03-04", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-04", endHalf: HalfPM, want: 1,
		},
		{
			name: "跨天_首日下午起",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfPM,
			end: "2026-03-03", endHalf: HalfPM, want: 0.5,
		},
		{
			name: "跨天_末日上午止",
			date: "2026-03-03", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-03", endHalf: HalfAM, want: 0.5,
		},
		{
			name: "跨周末_周五整天",
			date: "2026-03-06", start: "2026-03-06", startHalf: HalfAM,
			end: "2026-03-09", endHalf: HalfPM, want: 1,
		},
		{
			name: "跨周末_周六不贡献",
			date: "2026-03-07", start: "2026-03-06", startHalf: HalfAM,
			end: "2026-03-09", endHalf: HalfPM, want: 0,
		},
		{
			name: "跨周末_周日不贡献",
			date: "2026-03-08", start: "2026-03-06", startHalf: HalfAM,
			end: "2026-03-09", endHalf: HalfPM, want: 0,
		},
		{
			name: "跨周末_周一整天",
			date: "2026-03-09", start: "2026-03-06", startHalf: HalfAM,
			end: "2026-03-09", endHalf: HalfPM, want: 1,
		},
		{
			name: "显式休息日_不贡献",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-02", endHalf: HalfPM,
			explicit: ExplicitDays{"2026-03-02": WorkdayTypeRest}, want: 0,
		},
		{
			name: "显式节假日_不贡献",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-02", endHalf: HalfPM,
			explicit: ExplicitDays{"2026-03-02": WorkdayTypeHoliday}, want: 0,
		},
		{
			name: "显式调休上班_周六整天",
			date: "2026-03-07", start: "2026-03-07", startHalf: HalfAM,
			end: "2026-03-07", endHalf: HalfPM,
			explicit: ExplicitDays{"2026-03-07": WorkdayTypeWorking}, want: 1,
		},
		{
			name: "区间外日期",
			date: "2026-03-10", start: "2026-03-02", startHalf: HalfAM,
			end: "2026-03-04", endHalf: HalfPM, want: 0,
		},
		{
			name: "非法半天取值",
			date: "2026-03-02", start: "2026-03-02", startHalf: 9,
			end: "2026-03-02", endHalf: HalfPM, want: 0,
		},
		{
			name: "同日方向颠倒",
			date: "2026-03-02", start: "2026-03-02", startHalf: HalfPM,
			end: "2026-03-02", endHalf: HalfAM, want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LeaveDaysOnDate(mustDate(tc.date), mustDate(tc.start), mustDate(tc.end),
				tc.startHalf, tc.endHalf, tc.explicit)
			if got != tc.want {
				t.Fatalf("LeaveDaysOnDate(%s, %s..%s) = %v，期望 %v",
					tc.date, tc.start, tc.end, got, tc.want)
			}
		})
	}
}

// attMonthRow 构造一条结果行（月汇总口径测试用）。
func attMonthRow(userID int64, day int, status int8, leaveDays float64, late, early int, override *int8) model.AttendanceResult {
	return model.AttendanceResult{
		UserID:         userID,
		WorkDate:       leaveDay(day),
		Status:         status,
		LeaveDays:      leaveDays,
		LateMinutes:    late,
		EarlyMinutes:   early,
		OverrideStatus: override,
	}
}

func int8Ptr(v int8) *int8 { return &v }

// TestAggregateAttendanceMonth 锁定单用户月度汇总口径：生效状态（override 优先）
// 逐项计数、出勤天数 = 正常+迟到+早退+迟到且早退、请假天数累加 leave_days
// （半天按 0.5 计入）、休息日计入自然日但不计工作日/出勤。
func TestAggregateAttendanceMonth(t *testing.T) {
	rows := []model.AttendanceResult{
		attMonthRow(7, 2, model.AttendanceResultNormal, 0, 0, 0, nil),
		attMonthRow(7, 3, model.AttendanceResultLate, 0, 15, 0, nil),
		attMonthRow(7, 4, model.AttendanceResultEarly, 0, 0, 20, nil),
		attMonthRow(7, 5, model.AttendanceResultLateAndEarly, 0, 5, 10, nil),
		attMonthRow(7, 6, model.AttendanceResultMissing, 0, 0, 0, nil),
		// 半天请假：只记录 leave_days=0.5，状态仍是正常（spec §4.10）。
		attMonthRow(7, 9, model.AttendanceResultNormal, 0.5, 0, 0, nil),
		// 整天请假：状态=请假，leave_days=1。
		attMonthRow(7, 10, model.AttendanceResultLeave, 1, 0, 0, nil),
		// 休息日。
		attMonthRow(7, 7, model.AttendanceResultRest, 0, 0, 0, nil),
		attMonthRow(7, 8, model.AttendanceResultRest, 0, 0, 0, nil),
		// 校正优先：派生缺卡被校正为正常，汇总按生效状态计入正常。
		attMonthRow(7, 11, model.AttendanceResultMissing, 0, 0, 0, int8Ptr(model.AttendanceResultNormal)),
	}

	got := AggregateAttendanceMonth("2026-03", 7, rows)

	want := AttendanceSummary{
		UserID:         7,
		Month:          "2026-03",
		Days:           10,
		WorkDays:       8,
		AttendanceDays: 6, // 正常×3（含半天日与校正日）+ 迟到 + 早退 + 迟到且早退
		Normal:         3,
		Late:           1,
		Early:          1,
		LateAndEarly:   1,
		Missing:        1,
		Rest:           2,
		Leave:          1,
		LeaveDays:      1.5,
		LateMinutes:    20,
		EarlyMinutes:   30,
	}
	if got != want {
		t.Fatalf("月度汇总不符：\n got=%+v\nwant=%+v", got, want)
	}
}

// TestAggregateAttendanceMonthEmpty 空月份：所有计数为 0（含不传行/用户无结果）。
func TestAggregateAttendanceMonthEmpty(t *testing.T) {
	got := AggregateAttendanceMonth("2026-04", 9, nil)
	if got.UserID != 9 || got.Month != "2026-04" || got.Days != 0 || got.WorkDays != 0 ||
		got.AttendanceDays != 0 || got.LeaveDays != 0 {
		t.Fatalf("空月份汇总应为零值（仅回显用户/月份）：%+v", got)
	}
}

// TestAggregateAttendanceMonthByUser 多用户聚合：按用户升序分组，互不串行；
// 结果与逐用户聚合一致。
func TestAggregateAttendanceMonthByUser(t *testing.T) {
	rows := []model.AttendanceResult{
		attMonthRow(5, 2, model.AttendanceResultNormal, 0, 0, 0, nil),
		attMonthRow(3, 2, model.AttendanceResultLate, 0, 10, 0, nil),
		attMonthRow(5, 3, model.AttendanceResultRest, 0, 0, 0, nil),
		attMonthRow(3, 3, model.AttendanceResultLeave, 1, 0, 0, nil),
	}
	got := AggregateAttendanceMonthByUser("2026-03", rows)
	if len(got) != 2 {
		t.Fatalf("应聚合出 2 个用户，实际 %d：%+v", len(got), got)
	}
	if got[0].UserID != 3 || got[1].UserID != 5 {
		t.Fatalf("用户应按 ID 升序输出：%+v", got)
	}
	if got[0].Late != 1 || got[0].Leave != 1 || got[0].LeaveDays != 1 {
		t.Fatalf("用户 3 汇总不符：%+v", got[0])
	}
	if got[1].Normal != 1 || got[1].Rest != 1 || got[1].WorkDays != 1 || got[1].Days != 2 {
		t.Fatalf("用户 5 汇总不符：%+v", got[1])
	}
	// 与逐用户聚合一致（同一份口径）。
	single := AggregateAttendanceMonth("2026-03", 5, []model.AttendanceResult{rows[0], rows[2]})
	if got[1] != single {
		t.Fatalf("按用户聚合与逐用户聚合不一致：\nbyUser=%+v\nsingle=%+v", got[1], single)
	}
	if math.Abs(got[0].LeaveDays-1) > 1e-9 {
		t.Fatalf("请假天数应为 1：%+v", got[0])
	}
}
