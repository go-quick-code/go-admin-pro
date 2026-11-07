package service

import (
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// statistics_rules_test.go 锁定 ERP 首页统计的时间窗纯规则（次 seam，spec §5）：
// 今日/昨日/本月/今年的区间边界、近 N 月序列（含本月、升序、当前月截到明天）
// 与净额口径（出库/入库金额 − 退货金额）。期望值来自 spec 字面区间手算，
// 不复用实现代码。

// mustLoadLocation 加载业务时区（测试进程与 config 同口径 Asia/Shanghai）。
func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("加载时区 %s: %v", name, err)
	}
	return loc
}

// assertPeriod 断言区间起止的秒级相等（含时区偏移）。
func assertPeriod(t *testing.T, label string, got StatisticsPeriod, wantStart, wantEnd time.Time) {
	t.Helper()
	if !got.Start.Equal(wantStart) || !got.End.Equal(wantEnd) {
		t.Fatalf("%s = [%s, %s)，期望 [%s, %s)",
			label, got.Start.Format(time.RFC3339), got.End.Format(time.RFC3339),
			wantStart.Format(time.RFC3339), wantEnd.Format(time.RFC3339))
	}
}

// TestBuildStatisticsWindows 四时值区间（spec §5：按 out_time/in_time/return_time
// 落区间；今日=当天，昨日=前一天，本月/今年=期初至明日零点）。
func TestBuildStatisticsWindows(t *testing.T) {
	loc := mustLoadLocation(t, "Asia/Shanghai")
	at := func(y int, m time.Month, d, h int) time.Time {
		return time.Date(y, m, d, h, 30, 0, 0, loc)
	}

	// 月中：2026-11-08 10:30。
	got := BuildStatisticsWindows(at(2026, time.November, 8, 10))
	assertPeriod(t, "今日", got.Today,
		time.Date(2026, time.November, 8, 0, 0, 0, 0, loc),
		time.Date(2026, time.November, 9, 0, 0, 0, 0, loc))
	assertPeriod(t, "昨日", got.Yesterday,
		time.Date(2026, time.November, 7, 0, 0, 0, 0, loc),
		time.Date(2026, time.November, 8, 0, 0, 0, 0, loc))
	assertPeriod(t, "本月", got.Month,
		time.Date(2026, time.November, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.November, 9, 0, 0, 0, 0, loc))
	assertPeriod(t, "今年", got.Year,
		time.Date(2026, time.January, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.November, 9, 0, 0, 0, 0, loc))

	// 元旦零点：昨日跨年（12-31），本月/今年同起点，止于 1 月 2 日零点。
	got = BuildStatisticsWindows(at(2026, time.January, 1, 0))
	assertPeriod(t, "元旦今日", got.Today,
		time.Date(2026, time.January, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.January, 2, 0, 0, 0, 0, loc))
	assertPeriod(t, "元旦昨日", got.Yesterday,
		time.Date(2025, time.December, 31, 0, 0, 0, 0, loc),
		time.Date(2026, time.January, 1, 0, 0, 0, 0, loc))
	assertPeriod(t, "元旦本月", got.Month,
		time.Date(2026, time.January, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.January, 2, 0, 0, 0, 0, loc))
	assertPeriod(t, "元旦今年", got.Year,
		time.Date(2026, time.January, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.January, 2, 0, 0, 0, 0, loc))
}

// TestBuildStatisticsMonthPeriods 近 N 月（默认 6）序列：升序、含本月、当前月
// 截到明日零点（与「本月」统计口径一致），历史月取整月。
func TestBuildStatisticsMonthPeriods(t *testing.T) {
	loc := mustLoadLocation(t, "Asia/Shanghai")
	now := time.Date(2026, time.November, 8, 10, 30, 0, 0, loc)

	periods := BuildStatisticsMonthPeriods(now, 6)
	if len(periods) != 6 {
		t.Fatalf("近 6 个月应返回 6 项，实际 %d", len(periods))
	}
	wantKeys := []string{"2026-06", "2026-07", "2026-08", "2026-09", "2026-10", "2026-11"}
	for i, period := range periods {
		if got := period.Month.Key(); got != wantKeys[i] {
			t.Fatalf("第 %d 个月 = %s，期望 %s", i, got, wantKeys[i])
		}
	}
	// 历史月：整月区间。
	assertPeriod(t, "2026-10", StatisticsPeriod{Start: periods[4].Start, End: periods[4].End},
		time.Date(2026, time.October, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.November, 1, 0, 0, 0, 0, loc))
	// 当前月：截到明日零点（今日 23:59 的单据计入，未来日期不计）。
	assertPeriod(t, "2026-11", StatisticsPeriod{Start: periods[5].Start, End: periods[5].End},
		time.Date(2026, time.November, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.November, 9, 0, 0, 0, 0, loc))

	// N=1 只含本月；跨年回退。
	one := BuildStatisticsMonthPeriods(time.Date(2026, time.January, 31, 23, 0, 0, 0, loc), 2)
	if len(one) != 2 || one[0].Month.Key() != "2025-12" || one[1].Month.Key() != "2026-01" {
		t.Fatalf("跨年近 2 月 = %v，期望 2025-12/2026-01", one)
	}
	assertPeriod(t, "跨年当前月", StatisticsPeriod{Start: one[1].Start, End: one[1].End},
		time.Date(2026, time.January, 1, 0, 0, 0, 0, loc),
		time.Date(2026, time.February, 1, 0, 0, 0, 0, loc))

	if periods := BuildStatisticsMonthPeriods(now, 0); periods != nil {
		t.Fatalf("N=0 应返回 nil，实际 %v", periods)
	}
}

// TestNetStatisticsAmount 净额口径：已审核出库/入库金额 − 已审核退货金额
// （spec §5）；零值与负数（退货跨期大于当期出库）都要成立。
func TestNetStatisticsAmount(t *testing.T) {
	parse := func(text string) money.Money {
		m, err := money.ParseMoney(text)
		if err != nil {
			t.Fatalf("解析金额 %s: %v", text, err)
		}
		return m
	}
	cases := []struct {
		name             string
		gross, returnOut string
		want             string
	}{
		{name: "正数", gross: "1000.00", returnOut: "250.50", want: "749.50"},
		{name: "零值", gross: "0.00", returnOut: "0.00", want: "0.00"},
		{name: "负数", gross: "100.00", returnOut: "160.00", want: "-60.00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NetStatisticsAmount(parse(tc.gross), parse(tc.returnOut))
			if err != nil {
				t.Fatalf("净额计算失败: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("净额 = %s，期望 %s", got.String(), tc.want)
			}
		})
	}
}
