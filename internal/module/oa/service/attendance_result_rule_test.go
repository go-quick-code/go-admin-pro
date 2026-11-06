package service

import (
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// intPtr 返回分钟数的指针（区分「无打卡」与「零点打卡」）。
func intPtr(v int) *int { return &v }

// TestDeriveAttendanceResult 表驱动锁定 spec §4.10 的派生优先级与宽限边界：
// 非工作日 → 休息；整天请假 → 请假；缺卡 → 缺卡；迟到/早退/两者 → 对应状态；
// 否则正常。宽限内不计迟到/早退，超出部分按分钟记录。
func TestDeriveAttendanceResult(t *testing.T) {
	working := AttendanceDeriveInput{
		DayType:      WorkdayTypeWorking,
		HasShift:     true,
		ShiftStart:   "09:00",
		ShiftEnd:     "18:00",
		GraceMinutes: 10,
		InMinutes:    intPtr(9 * 60),
		OutMinutes:   intPtr(18 * 60),
	}

	cases := []struct {
		name  string
		input AttendanceDeriveInput
		want  AttendanceDerivation
	}{
		{
			name:  "工作日上午九点准时上下班_正常",
			input: working,
			want:  AttendanceDerivation{Status: model.AttendanceResultNormal},
		},
		{
			name: "宽限整点不算迟到",
			input: func() AttendanceDeriveInput {
				in := working
				in.InMinutes = intPtr(9*60 + 10) // 09:10，恰好在宽限上界
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultNormal},
		},
		{
			name: "超出宽限一分钟计迟到一分钟",
			input: func() AttendanceDeriveInput {
				in := working
				in.InMinutes = intPtr(9*60 + 11)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultLate, LateMinutes: 1},
		},
		{
			name: "迟到二十分钟扣宽限十分钟计十分钟",
			input: func() AttendanceDeriveInput {
				in := working
				in.InMinutes = intPtr(9*60 + 20)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultLate, LateMinutes: 10},
		},
		{
			name: "早退宽限整点不算早退",
			input: func() AttendanceDeriveInput {
				in := working
				in.OutMinutes = intPtr(18*60 - 10)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultNormal},
		},
		{
			name: "超出宽限一分钟计早退一分钟",
			input: func() AttendanceDeriveInput {
				in := working
				in.OutMinutes = intPtr(18*60 - 11)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultEarly, EarlyMinutes: 1},
		},
		{
			name: "迟到且早退",
			input: func() AttendanceDeriveInput {
				in := working
				in.InMinutes = intPtr(9*60 + 20)   // 迟到 10
				in.OutMinutes = intPtr(18*60 - 25) // 早退 15
				return in
			}(),
			want: AttendanceDerivation{
				Status: model.AttendanceResultLateAndEarly, LateMinutes: 10, EarlyMinutes: 15,
			},
		},
		{
			name: "缺上班卡",
			input: func() AttendanceDeriveInput {
				in := working
				in.InMinutes = nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultMissing, MissingIn: 1},
		},
		{
			name: "缺下班卡",
			input: func() AttendanceDeriveInput {
				in := working
				in.OutMinutes = nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultMissing, MissingOut: 1},
		},
		{
			name: "上下班卡都缺",
			input: func() AttendanceDeriveInput {
				in := working
				in.InMinutes, in.OutMinutes = nil, nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultMissing, MissingIn: 1, MissingOut: 1},
		},
		{
			name: "休息日有上下班卡也只判休息_不算出勤",
			input: func() AttendanceDeriveInput {
				in := working
				in.DayType = WorkdayTypeRest
				in.InMinutes = intPtr(10 * 60)
				in.OutMinutes = intPtr(12 * 60)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultRest},
		},
		{
			name: "节假日无卡判休息",
			input: func() AttendanceDeriveInput {
				in := working
				in.DayType = WorkdayTypeHoliday
				in.InMinutes, in.OutMinutes = nil, nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultRest},
		},
		{
			name: "整天请假优先于缺卡",
			input: func() AttendanceDeriveInput {
				in := working
				in.LeaveDays = 1
				in.InMinutes, in.OutMinutes = nil, nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultLeave},
		},
		{
			name: "整天请假即使有迟到卡也只判请假",
			input: func() AttendanceDeriveInput {
				in := working
				in.LeaveDays = 1
				in.InMinutes = intPtr(10 * 60)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultLeave},
		},
		{
			name: "半天请假不改变状态_准时为正常",
			input: func() AttendanceDeriveInput {
				in := working
				in.LeaveDays = 0.5
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultNormal},
		},
		{
			name: "半天请假也不豁免缺卡",
			input: func() AttendanceDeriveInput {
				in := working
				in.LeaveDays = 0.5
				in.InMinutes = nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultMissing, MissingIn: 1},
		},
		{
			name: "无班次双卡按正常_不做迟到早退判定",
			input: func() AttendanceDeriveInput {
				in := working
				in.HasShift = false
				in.InMinutes = intPtr(23 * 60)
				in.OutMinutes = intPtr(23*60 + 30)
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultNormal},
		},
		{
			name: "无班次缺卡仍判缺卡",
			input: func() AttendanceDeriveInput {
				in := working
				in.HasShift = false
				in.InMinutes, in.OutMinutes = nil, nil
				return in
			}(),
			want: AttendanceDerivation{Status: model.AttendanceResultMissing, MissingIn: 1, MissingOut: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveAttendanceResult(tc.input)
			if got != tc.want {
				t.Fatalf("派生结果不符：got %+v，want %+v", got, tc.want)
			}
		})
	}
}

// TestSelectFirstInLastOut 锁定「取最早上班与最晚下班」：同日多次打卡时
// 上班取最早、下班取最晚；顺序颠倒的输入不受影响；只有单类型时另一侧为空。
func TestSelectFirstInLastOut(t *testing.T) {
	sample := func(kind int8, hh, mm int) PunchSample {
		return PunchSample{Type: kind, Time: time.Date(2026, 3, 2, hh, mm, 0, 0, time.UTC)}
	}

	cases := []struct {
		name    string
		samples []PunchSample
		wantIn  int
		wantOut int
	}{
		{
			name: "多次打卡取最早上班与最晚下班",
			samples: []PunchSample{
				sample(model.PunchTypeIn, 9, 30),
				sample(model.PunchTypeOut, 17, 0),
				sample(model.PunchTypeIn, 8, 50),
				sample(model.PunchTypeOut, 18, 40),
			},
			wantIn: 2, wantOut: 3,
		},
		{
			name: "只有上班卡",
			samples: []PunchSample{
				sample(model.PunchTypeIn, 9, 0),
				sample(model.PunchTypeIn, 9, 5),
			},
			wantIn: 0, wantOut: -1,
		},
		{
			name:    "只有下班卡",
			samples: []PunchSample{sample(model.PunchTypeOut, 18, 0)},
			wantIn:  -1, wantOut: 0,
		},
		{
			name:    "无打卡",
			samples: nil,
			wantIn:  -1, wantOut: -1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIn, gotOut := SelectFirstInLastOut(tc.samples)
			if gotIn != tc.wantIn || gotOut != tc.wantOut {
				t.Fatalf("选择结果不符：got (%d,%d)，want (%d,%d)", gotIn, gotOut, tc.wantIn, tc.wantOut)
			}
		})
	}
}

// TestMinutesOfDayAndTimeOfDayMinutes 锁定 time-of-day 换算（墙钟语义）：
// 打卡与班次时间都按业务时区的墙钟比较，不受读回时区（loc=UTC/Local）影响。
func TestMinutesOfDayAndTimeOfDayMinutes(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区: %v", err)
	}
	// 同一墙钟 09:05 在 UTC 与 Asia/Shanghai 下都应是 545 分钟。
	if got := MinutesOfDay(time.Date(2026, 3, 2, 9, 5, 0, 0, time.UTC)); got != 9*60+5 {
		t.Fatalf("UTC 墙钟分钟：got %d", got)
	}
	if got := MinutesOfDay(time.Date(2026, 3, 2, 9, 5, 0, 0, shanghai)); got != 9*60+5 {
		t.Fatalf("上海墙钟分钟：got %d", got)
	}
	if got := MinutesOfDay(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)); got != 0 {
		t.Fatalf("零点：got %d", got)
	}

	valid := map[string]int{"00:00": 0, "09:00": 540, "18:30": 18*60 + 30, "23:59": 23*60 + 59}
	for in, want := range valid {
		if got := TimeOfDayMinutes(in); got != want {
			t.Fatalf("TimeOfDayMinutes(%q)：got %d，want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "9:00", "24:00", "09:60", "09:00:00", "09：00"} {
		if got := TimeOfDayMinutes(bad); got != -1 {
			t.Fatalf("非法时间 %q 应返回 -1，实际 %d", bad, got)
		}
	}
}

// TestValidAttendanceResultStatus 锁定结果状态枚举（派生与校正共用）：
// 0–6 合法，负值与 7 以上拒绝。
func TestValidAttendanceResultStatus(t *testing.T) {
	for s := int8(0); s <= 6; s++ {
		if !ValidAttendanceResultStatus(s) {
			t.Fatalf("状态 %d 应合法", s)
		}
	}
	for _, s := range []int8{-1, 7, 100} {
		if ValidAttendanceResultStatus(s) {
			t.Fatalf("状态 %d 应非法", s)
		}
	}
	if got := AttendanceResultStatusNameOf(model.AttendanceResultLateAndEarly); got != "迟到且早退" {
		t.Fatalf("状态名不符：%q", got)
	}
}
