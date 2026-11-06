// leave_application_rule_test.go 表驱动锁定请假申请提交的纯函数语义
// （issue #52，docs/oa-mvp-spec.md §4.8）：半天方向、跨年拒绝、槽位时长
// （跨周末/节假日/调休上班）、同用户槽位重叠判定。期望值手算核对。
package service

import (
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// testDate 解析 YYYY-MM-DD（测试数据恒合法，解析失败直接 Fatal）。
func testDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := NormalizeDate(value)
	if err != nil {
		t.Fatalf("解析测试日期 %s: %v", value, err)
	}
	return parsed
}

// TestValidateLeaveSpan 区间与半天方向（spec §4.8）：
// 同日 AM→AM/AM→PM/PM→PM 合法，PM→AM 非法；起止颠倒、半天越界与跨年拒绝。
func TestValidateLeaveSpan(t *testing.T) {
	cases := []struct {
		name      string
		start     string
		end       string
		startHalf int8
		endHalf   int8
		wantErr   string
	}{
		{name: "同日_上午到上午", start: "2026-11-06", end: "2026-11-06", startHalf: HalfAM, endHalf: HalfAM},
		{name: "同日_上午到下午", start: "2026-11-06", end: "2026-11-06", startHalf: HalfAM, endHalf: HalfPM},
		{name: "同日_下午到下午", start: "2026-11-06", end: "2026-11-06", startHalf: HalfPM, endHalf: HalfPM},
		{name: "同日_下午到上午_非法", start: "2026-11-06", end: "2026-11-06", startHalf: HalfPM, endHalf: HalfAM, wantErr: "下午开始"},
		{name: "跨日_下午到上午_合法", start: "2026-11-06", end: "2026-11-07", startHalf: HalfPM, endHalf: HalfAM},
		{name: "起止颠倒", start: "2026-11-09", end: "2026-11-06", startHalf: HalfAM, endHalf: HalfAM, wantErr: "不能晚于"},
		{name: "半天越界", start: "2026-11-06", end: "2026-11-06", startHalf: 2, endHalf: HalfAM, wantErr: "半天取值"},
		{name: "跨自然年", start: "2026-12-31", end: "2027-01-04", startHalf: HalfAM, endHalf: HalfAM, wantErr: "跨自然年"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLeaveSpan(testDate(t, tc.start), testDate(t, tc.end), tc.startHalf, tc.endHalf)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望合法，实际 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("期望错误包含 %q，实际 %v", tc.wantErr, err)
			}
		})
	}
}

// TestLeaveDaysOfSlots 时长 = 槽位数 / 2（整天 2 槽、半天 1 槽）：分值口径
// 与 decimal(5,1) 对齐（0.5 天 = 50）。
func TestLeaveDaysOfSlots(t *testing.T) {
	date := testDate(t, "2026-11-06")
	cases := []struct {
		name  string
		slots []Slot
		want  money.Money
	}{
		{name: "空槽位", slots: nil, want: money.FromCents(0)},
		{name: "半天", slots: []Slot{{Date: date, Half: HalfPM}}, want: money.FromCents(50)},
		{name: "一天", slots: []Slot{{Date: date, Half: HalfAM}, {Date: date, Half: HalfPM}}, want: money.FromCents(100)},
		{name: "两天半", slots: make([]Slot, 5), want: money.FromCents(250)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LeaveDaysOfSlots(tc.slots); got.Cents() != tc.want.Cents() {
				t.Fatalf("时长 = %s，期望 %s", got.String(), tc.want.String())
			}
		})
	}
}

// TestLeaveDurationByWorkdaySlots 组合 #50 槽位计算与 #52 时长：
// 跨周末、节假日（显式 2）、调休上班（周末显式 0）与整周区间。
func TestLeaveDurationByWorkdaySlots(t *testing.T) {
	cases := []struct {
		name      string
		start     string
		end       string
		startHalf int8
		endHalf   int8
		explicit  ExplicitDays
		wantDays  money.Money
	}{
		{
			name: "同日_上午半天", start: "2026-11-06", end: "2026-11-06",
			startHalf: HalfAM, endHalf: HalfAM, wantDays: money.FromCents(50),
		},
		{
			name: "同日_全天", start: "2026-11-06", end: "2026-11-06",
			startHalf: HalfAM, endHalf: HalfPM, wantDays: money.FromCents(100),
		},
		{
			name: "周五下午到周一上午_跨周末不计", start: "2026-11-06", end: "2026-11-09",
			startHalf: HalfPM, endHalf: HalfAM, wantDays: money.FromCents(100),
		},
		{
			name: "周六到周日_零工作日", start: "2026-11-07", end: "2026-11-08",
			startHalf: HalfAM, endHalf: HalfPM, wantDays: money.FromCents(0),
		},
		{
			name: "周一为节假日_不计", start: "2026-11-09", end: "2026-11-09",
			startHalf: HalfAM, endHalf: HalfPM,
			explicit: ExplicitDays{"2026-11-09": WorkdayTypeHoliday},
			wantDays: money.FromCents(0),
		},
		{
			name: "周六调休上班_计", start: "2026-11-06", end: "2026-11-07",
			startHalf: HalfPM, endHalf: HalfPM,
			explicit: ExplicitDays{"2026-11-07": WorkdayTypeWorking},
			wantDays: money.FromCents(150),
		},
		{
			name: "整周_周一上午到周五下午", start: "2026-11-02", end: "2026-11-06",
			startHalf: HalfAM, endHalf: HalfPM, wantDays: money.FromCents(500),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slots := ResolveWorkdaySlots(
				testDate(t, tc.start), testDate(t, tc.end), tc.startHalf, tc.endHalf, tc.explicit,
			)
			if got := LeaveDaysOfSlots(slots); got.Cents() != tc.wantDays.Cents() {
				t.Fatalf("时长 = %s（槽位 %d），期望 %s", got.String(), len(slots), tc.wantDays.String())
			}
		})
	}
}

// TestValidateLeaveSlots 提交槽位非空（spec §4.8：总槽位为 0 时拒绝提交）。
func TestValidateLeaveSlots(t *testing.T) {
	date := testDate(t, "2026-11-06")
	cases := []struct {
		name    string
		slots   []Slot
		wantErr string
	}{
		{name: "空槽位拒绝", slots: nil, wantErr: "没有工作日"},
		{name: "非空槽位通过", slots: []Slot{{Date: date, Half: HalfAM}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLeaveSlots(tc.slots)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望合法，实际 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("期望错误包含 %q，实际 %v", tc.wantErr, err)
			}
		})
	}
}

// TestCheckLeaveBalance 余额不足拒绝（可用 < 需要）；相等与富余通过。
func TestCheckLeaveBalance(t *testing.T) {
	cases := []struct {
		name      string
		available money.Money
		duration  money.Money
		wantErr   string
	}{
		{name: "余额不足", available: money.FromCents(250), duration: money.FromCents(300), wantErr: "额度不足"},
		{name: "余额恰好够", available: money.FromCents(300), duration: money.FromCents(300)},
		{name: "余额富余", available: money.FromCents(500), duration: money.FromCents(50)},
		{name: "未初始化零余额", available: money.FromCents(0), duration: money.FromCents(50), wantErr: "当前可用 0 天"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckLeaveBalance("年假", tc.available, tc.duration)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("期望错误包含 %q，实际 %v", tc.wantErr, err)
			}
		})
	}
}

// TestSlotsOverlap 槽位重叠：同一 (日期, 半天) 即重叠；相邻半天不算。
func TestSlotsOverlap(t *testing.T) {
	friday := testDate(t, "2026-11-06")
	monday := testDate(t, "2026-11-09")
	cases := []struct {
		name string
		a    []Slot
		b    []Slot
		want bool
	}{
		{name: "同日上午重叠", a: []Slot{{friday, HalfAM}}, b: []Slot{{friday, HalfAM}}, want: true},
		{name: "同日相邻半天不重叠", a: []Slot{{friday, HalfAM}}, b: []Slot{{friday, HalfPM}}, want: false},
		{name: "不同日期不重叠", a: []Slot{{friday, HalfAM}}, b: []Slot{{monday, HalfAM}}, want: false},
		{name: "多槽位任一相交即重叠", a: []Slot{{friday, HalfPM}, {monday, HalfAM}}, b: []Slot{{monday, HalfAM}, {monday, HalfPM}}, want: true},
		{name: "空集合不重叠", a: nil, b: []Slot{{friday, HalfAM}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SlotsOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("SlotsOverlap = %v，期望 %v", got, tc.want)
			}
		})
	}
}
