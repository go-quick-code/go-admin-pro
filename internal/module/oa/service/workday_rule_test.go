package service

import (
	"fmt"
	"testing"
	"time"
)

// 工作日历纯函数表驱动测试（issue #50，docs/oa-mvp-spec.md §3.3/§4.8/§4.10）：
// 锁定默认规则（周一至周五工作日、周六周日休息）、显式记录优先（调休上班、
// 节假日）、逐日合成与工作日槽位（请假 #52 / 考勤 #61 复用）。
//
// 便于阅读的日期速记：所有用例基于 2026-11（周一 = 2026-11-02）。

const workdayTestMonth = "2026-11"

// d 按日构造 UTC 零点时间（2026-11-02 是周一）。
func workdayDay(day int) time.Time {
	return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC)
}

// mustDate 解析 YYYY-MM-DD 字面量，失败直接 panic（测试基建）。
func mustDate(s string) time.Time {
	t, err := NormalizeDate(s)
	if err != nil {
		panic(fmt.Sprintf("测试日期字面量非法 %q: %v", s, err))
	}
	return t
}

// weekdayNameOf 星期几中文名（断言可读性）。
func weekdayNameOf(w time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[int(w)]
}

func TestNormalizeDate(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string // 规整后的 YYYY-MM-DD；空串表示期望报错
		wantErr bool
	}{
		{name: "合法日期", in: "2026-11-02", want: "2026-11-02"},
		{name: "年初", in: "2027-01-01", want: "2027-01-01"},
		{name: "年末", in: "2026-12-31", want: "2026-12-31"},
		{name: "闰年2月29", in: "2028-02-29", want: "2028-02-29"},
		{name: "非闰年2月29拒绝", in: "2026-02-29", wantErr: true},
		{name: "缺前导零拒绝", in: "2026-11-7", wantErr: true},
		{name: "月份越界拒绝", in: "2026-13-01", wantErr: true},
		{name: "带时间后缀拒绝", in: "2026-11-02 08:00:00", wantErr: true},
		{name: "斜杠格式拒绝", in: "2026/11/02", wantErr: true},
		{name: "空串拒绝", in: "", wantErr: true},
		{name: "纯数字拒绝", in: "20261102", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeDate(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeDate(%q) 应报错，实际得到 %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeDate(%q) 意外报错: %v", tc.in, err)
			}
			if s := got.Format(DateLayout); s != tc.want {
				t.Fatalf("NormalizeDate(%q) = %s，期望 %s", tc.in, s, tc.want)
			}
		})
	}
}

func TestValidWorkdayType(t *testing.T) {
	cases := []struct {
		name string
		in   int8
		want bool
	}{
		{name: "工作日", in: WorkdayTypeWorking, want: true},
		{name: "休息日", in: WorkdayTypeRest, want: true},
		{name: "节假日", in: WorkdayTypeHoliday, want: true},
		{name: "负数拒绝", in: -1, want: false},
		{name: "越界拒绝", in: 3, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidWorkdayType(tc.in); got != tc.want {
				t.Fatalf("ValidWorkdayType(%d) = %v，期望 %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDefaultDayType(t *testing.T) {
	cases := []struct {
		name    string
		day     int
		want    int8
		wantWDE string
	}{
		{name: "周一工作日", day: 2, want: WorkdayTypeWorking, wantWDE: "周一"},
		{name: "周二工作日", day: 3, want: WorkdayTypeWorking, wantWDE: "周二"},
		{name: "周三工作日", day: 4, want: WorkdayTypeWorking, wantWDE: "周三"},
		{name: "周四工作日", day: 5, want: WorkdayTypeWorking, wantWDE: "周四"},
		{name: "周五工作日", day: 6, want: WorkdayTypeWorking, wantWDE: "周五"},
		{name: "周六休息", day: 7, want: WorkdayTypeRest, wantWDE: "周六"},
		{name: "周日休息", day: 8, want: WorkdayTypeRest, wantWDE: "周日"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			date := workdayDay(tc.day)
			if got := weekdayNameOf(date.Weekday()); got != tc.wantWDE {
				t.Fatalf("测试数据自检：2026-11-%02d 应为 %s，实际 %s", tc.day, tc.wantWDE, got)
			}
			if got := DefaultDayType(date); got != tc.want {
				t.Fatalf("DefaultDayType(2026-11-%02d) = %d，期望 %d", tc.day, got, tc.want)
			}
		})
	}
}

// TestResolveDayType 单日合成：显式记录优先，无记录走默认规则（spec §3.3）。
func TestResolveDayType(t *testing.T) {
	cases := []struct {
		name     string
		day      int
		explicit ExplicitDays
		want     int8
	}{
		{
			name:     "周六显式工作日=调休上班",
			day:      7,
			explicit: ExplicitDays{"2026-11-07": WorkdayTypeWorking},
			want:     WorkdayTypeWorking,
		},
		{
			name:     "周三显式节假日=优先于默认工作日",
			day:      4,
			explicit: ExplicitDays{"2026-11-04": WorkdayTypeHoliday},
			want:     WorkdayTypeHoliday,
		},
		{
			name:     "周二显式休息日=优先于默认工作日",
			day:      3,
			explicit: ExplicitDays{"2026-11-03": WorkdayTypeRest},
			want:     WorkdayTypeRest,
		},
		{
			name:     "显式记录只影响命中的日期",
			day:      2,
			explicit: ExplicitDays{"2026-11-07": WorkdayTypeWorking},
			want:     WorkdayTypeWorking,
		},
		{
			name:     "空显式集合走默认规则",
			day:      7,
			explicit: ExplicitDays{},
			want:     WorkdayTypeRest,
		},
		{
			name:     "nil 显式集合走默认规则",
			day:      2,
			explicit: nil,
			want:     WorkdayTypeWorking,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			date := workdayDay(tc.day)
			if got := ResolveDayType(date, tc.explicit); got != tc.want {
				t.Fatalf("ResolveDayType(2026-11-%02d) = %d，期望 %d", tc.day, got, tc.want)
			}
		})
	}
}

// TestResolveDayTypeTimezoneRobust 显式记录命中不受传入时区影响：
// 同一日历日期无论带哪个时区偏移，键归一化后都命中同一显式记录。
func TestResolveDayTypeTimezoneRobust(t *testing.T) {
	explicit := ExplicitDays{"2026-11-07": WorkdayTypeHoliday}
	for _, loc := range []string{"Asia/Shanghai", "America/New_York", "UTC"} {
		location, err := time.LoadLocation(loc)
		if err != nil {
			t.Fatalf("加载时区 %s: %v", loc, err)
		}
		date := time.Date(2026, 11, 7, 9, 30, 0, 0, location)
		if got := ResolveDayType(date, explicit); got != WorkdayTypeHoliday {
			t.Fatalf("时区 %s：ResolveDayType 应命中显式节假日，实际 %d", loc, got)
		}
		if got := DateKey(date); got != "2026-11-07" {
			t.Fatalf("时区 %s：DateKey = %s，期望 2026-11-07", loc, got)
		}
	}
}

// TestResolveRange 逐日合成：[start, end] 闭区间、按日升序、显式优先。
func TestResolveRange(t *testing.T) {
	t.Run("整周一到周日默认规则", func(t *testing.T) {
		days := ResolveRange(workdayDay(2), workdayDay(8), nil)
		wantTypes := []int8{0, 0, 0, 0, 0, 1, 1}
		if len(days) != len(wantTypes) {
			t.Fatalf("整周应 7 行，实际 %d", len(days))
		}
		for i, want := range wantTypes {
			if days[i].Type != want {
				t.Fatalf("第 %d 天（%s）类型 %d，期望 %d", i+1, DateKey(days[i].Date), days[i].Type, want)
			}
			if days[i].Explicit {
				t.Fatalf("第 %d 天不应标记显式", i+1)
			}
		}
		if days[0].Weekday != time.Monday || days[6].Weekday != time.Sunday {
			t.Fatalf("区间边界星期错误：%v … %v", days[0].Weekday, days[6].Weekday)
		}
	})

	t.Run("单日区间", func(t *testing.T) {
		days := ResolveRange(workdayDay(5), workdayDay(5), nil)
		if len(days) != 1 || days[0].Type != WorkdayTypeWorking {
			t.Fatalf("单日区间应 1 行工作日，实际 %+v", days)
		}
	})

	t.Run("起止颠倒返回空", func(t *testing.T) {
		if days := ResolveRange(workdayDay(9), workdayDay(2), nil); len(days) != 0 {
			t.Fatalf("起止颠倒应空，实际 %d 行", len(days))
		}
	})

	t.Run("节假日与调休混排", func(t *testing.T) {
		// 2026-11-05（周四）节假日；2026-11-07（周六）调休上班。
		explicit := ExplicitDays{"2026-11-05": WorkdayTypeHoliday, "2026-11-07": WorkdayTypeWorking}
		days := ResolveRange(workdayDay(2), workdayDay(8), explicit)
		want := map[string]struct {
			typ      int8
			explicit bool
		}{
			"2026-11-02": {WorkdayTypeWorking, false},
			"2026-11-03": {WorkdayTypeWorking, false},
			"2026-11-04": {WorkdayTypeWorking, false},
			"2026-11-05": {WorkdayTypeHoliday, true},
			"2026-11-06": {WorkdayTypeWorking, false},
			"2026-11-07": {WorkdayTypeWorking, true},
			"2026-11-08": {WorkdayTypeRest, false},
		}
		if len(days) != len(want) {
			t.Fatalf("应 %d 行，实际 %d", len(want), len(days))
		}
		for _, day := range days {
			key := DateKey(day.Date)
			w, ok := want[key]
			if !ok {
				t.Fatalf("多余日期 %s", key)
			}
			if day.Type != w.typ || day.Explicit != w.explicit {
				t.Fatalf("%s = (type %d, explicit %v)，期望 (type %d, explicit %v)", key, day.Type, day.Explicit, w.typ, w.explicit)
			}
		}
	})

	t.Run("跨月区间", func(t *testing.T) {
		// 2026-11-30（周一）… 2026-12-02（周三）。
		days := ResolveRange(workdayDay(30), mustDate("2026-12-02"), nil)
		if len(days) != 3 {
			t.Fatalf("跨月区间应 3 行，实际 %d", len(days))
		}
		for _, day := range days {
			if day.Type != WorkdayTypeWorking {
				t.Fatalf("%s 应为默认工作日，实际 %d", DateKey(day.Date), day.Type)
			}
		}
	})
}

// TestResolveWorkdaySlots 工作日槽位（spec §4.8）：区间内每个工作日贡献
// 上午/下午两个槽位，按起止半天边界裁剪；非工作日不贡献；总槽位 0 时空
// （service 层以 400 拒绝空槽位提交）。
func TestResolveWorkdaySlots(t *testing.T) {
	// slotKey 槽位速记 "YYYY-MM-DD/AM|PM"。
	slotKey := func(s Slot) string {
		half := "AM"
		if s.Half == HalfPM {
			half = "PM"
		}
		return DateKey(s.Date) + "/" + half
	}
	assertSlots := func(t *testing.T, got []Slot, want []string) {
		t.Helper()
		keys := make([]string, 0, len(got))
		for _, s := range got {
			keys = append(keys, slotKey(s))
		}
		if len(keys) != len(want) {
			t.Fatalf("槽位 %v，期望 %v（数量不符）", keys, want)
		}
		for i := range want {
			if keys[i] != want[i] {
				t.Fatalf("槽位[%d] = %s，期望 %s（全部：%v）", i, keys[i], want[i], keys)
			}
		}
	}

	t.Run("单工作日整天", func(t *testing.T) {
		got := ResolveWorkdaySlots(workdayDay(2), workdayDay(2), HalfAM, HalfPM, nil)
		assertSlots(t, got, []string{"2026-11-02/AM", "2026-11-02/PM"})
	})

	t.Run("单工作日下午到上午非法=空槽位", func(t *testing.T) {
		got := ResolveWorkdaySlots(workdayDay(2), workdayDay(2), HalfPM, HalfAM, nil)
		assertSlots(t, got, nil)
	})

	t.Run("跨周末不含周六周日", func(t *testing.T) {
		// 周四 11-05 下午 → 下周二 11-10 上午：11-05/PM、11-06 全天、
		// 11-07/11-08 休息日跳过、11-09 全天、11-10/AM。
		got := ResolveWorkdaySlots(workdayDay(5), workdayDay(10), HalfPM, HalfAM, nil)
		assertSlots(t, got, []string{
			"2026-11-05/PM", "2026-11-06/AM", "2026-11-06/PM",
			"2026-11-09/AM", "2026-11-09/PM", "2026-11-10/AM",
		})
	})

	t.Run("节假日不贡献槽位", func(t *testing.T) {
		explicit := ExplicitDays{"2026-11-06": WorkdayTypeHoliday}
		// 周五 11-06 被标记节假日 → 只剩 11-05 全天。
		got := ResolveWorkdaySlots(workdayDay(5), workdayDay(6), HalfAM, HalfPM, explicit)
		assertSlots(t, got, []string{"2026-11-05/AM", "2026-11-05/PM"})
	})

	t.Run("周末调休上班贡献槽位", func(t *testing.T) {
		explicit := ExplicitDays{"2026-11-07": WorkdayTypeWorking}
		// 周六 11-07 调休上班 → 两天共 4 槽位。
		got := ResolveWorkdaySlots(workdayDay(6), workdayDay(7), HalfAM, HalfPM, explicit)
		assertSlots(t, got, []string{
			"2026-11-06/AM", "2026-11-06/PM", "2026-11-07/AM", "2026-11-07/PM",
		})
	})

	t.Run("起止边界裁剪", func(t *testing.T) {
		// 周一上午起、周三下午止 → 周一 PM 算、周三 AM 算。
		got := ResolveWorkdaySlots(workdayDay(2), workdayDay(4), HalfPM, HalfAM, nil)
		assertSlots(t, got, []string{
			"2026-11-02/PM", "2026-11-03/AM", "2026-11-03/PM", "2026-11-04/AM",
		})
	})

	t.Run("全休区间空槽位", func(t *testing.T) {
		got := ResolveWorkdaySlots(workdayDay(7), workdayDay(8), HalfAM, HalfPM, nil)
		assertSlots(t, got, nil)
	})

	t.Run("非法半天值空槽位", func(t *testing.T) {
		got := ResolveWorkdaySlots(workdayDay(2), workdayDay(2), 5, HalfPM, nil)
		assertSlots(t, got, nil)
	})

	t.Run("起止颠倒空槽位", func(t *testing.T) {
		got := ResolveWorkdaySlots(workdayDay(9), workdayDay(2), HalfAM, HalfPM, nil)
		assertSlots(t, got, nil)
	})
}
