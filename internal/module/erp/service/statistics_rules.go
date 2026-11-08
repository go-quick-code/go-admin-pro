package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// statistics_rules.go 是 ERP 首页统计的时间窗纯规则（issue #95，
// docs/erp-mvp-spec.md §5/§6/§8.2）：与存储无关、可直接单测。
//
// 口径（spec §5）：
//   - 销售额 = Σ已审核销售出库 total_price（按 out_time 落区间）
//     − Σ已审核销售退货 total_price（按 return_time 落区间）；
//   - 采购额 = Σ已审核采购入库 − Σ已审核采购退货（in_time/return_time）；
//   - summary 的今日/昨日是自然日 [0 点, 次日 0 点)；本月/今年是期初到明日
//     零点（累计至今，今日 23:59 的单据也计入，未来日期不计）；
//   - time-summary 取含本月在内的近 N 个自然月（默认 6），历史月取整月，
//     当前月与「本月」同口径截到明日零点；空月补 0.00。
//
// 时间一律在业务时区（ADR 0006）下构造：调用方传入的 now 必须已
// In(businessLocation())，本文件按 now 自身的 Location 取年月日。

// StatisticsDefaultMonths 是 time-summary 的默认月数（spec §5：近 N 个月，
// 默认 6）。
const StatisticsDefaultMonths = 6

// StatisticsMaxMonths 是 time-summary 可请求的最大月数（防超长区间拖慢聚合）。
const StatisticsMaxMonths = 24

// StatisticsPeriod 是左闭右开的统计区间 [Start, End)。
type StatisticsPeriod struct {
	Start time.Time
	End   time.Time
}

// StatisticsWindows 是 summary 的四个时值区间。
type StatisticsWindows struct {
	Today     StatisticsPeriod
	Yesterday StatisticsPeriod
	Month     StatisticsPeriod
	Year      StatisticsPeriod
}

// dayStart 返回 t 所在自然日的零点（保持 t 的时区）。
func dayStart(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

// BuildStatisticsWindows 按业务时区的 now 构造四时值区间。
func BuildStatisticsWindows(now time.Time) StatisticsWindows {
	today := dayStart(now)
	tomorrow := today.AddDate(0, 0, 1)
	year, month, _ := now.Date()
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
	yearStart := time.Date(year, time.January, 1, 0, 0, 0, 0, now.Location())
	return StatisticsWindows{
		Today:     StatisticsPeriod{Start: today, End: tomorrow},
		Yesterday: StatisticsPeriod{Start: today.AddDate(0, 0, -1), End: today},
		Month:     StatisticsPeriod{Start: monthStart, End: tomorrow},
		Year:      StatisticsPeriod{Start: yearStart, End: tomorrow},
	}
}

// StatisticsMonth 是一个自然月（年 + 月）。
type StatisticsMonth struct {
	Year  int
	Month time.Month
}

// Key 返回 yyyy-MM 形式的月份键（响应与前端图表轴共用）。
func (m StatisticsMonth) Key() string {
	return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month))
}

// StatisticsMonthPeriod 是趋势图单个自然月的归集区间。
type StatisticsMonthPeriod struct {
	Month StatisticsMonth
	Start time.Time
	End   time.Time
}

// BuildStatisticsMonthPeriods 返回含 now 所在月在内的最近 count 个自然月，
// 按时间升序。历史月取整月，当前月截到明日零点（与 summary「本月」一致）；
// count <= 0 返回 nil（调用方负责默认值）。
func BuildStatisticsMonthPeriods(now time.Time, count int) []StatisticsMonthPeriod {
	if count <= 0 {
		return nil
	}
	today := dayStart(now)
	tomorrow := today.AddDate(0, 0, 1)
	year, month, _ := now.Date()
	first := time.Date(year, month, 1, 0, 0, 0, 0, now.Location()).AddDate(0, -(count - 1), 0)
	out := make([]StatisticsMonthPeriod, 0, count)
	for i := 0; i < count; i++ {
		start := first.AddDate(0, i, 0)
		end := start.AddDate(0, 1, 0)
		if i == count-1 {
			// 当前月：累计至今（明日零点截止，今日单据不因执行时刻漏计）。
			end = tomorrow
		}
		out = append(out, StatisticsMonthPeriod{
			Month: StatisticsMonth{Year: start.Year(), Month: start.Month()},
			Start: start,
			End:   end,
		})
	}
	return out
}

// NetStatisticsAmount 计算净额 = 已审核出库/入库金额 − 已审核退货金额
// （spec §5；退货跨期大于当期出库时允许为负，不截断）。
func NetStatisticsAmount(gross, returns money.Money) (money.Money, error) {
	return gross.Sub(returns)
}
