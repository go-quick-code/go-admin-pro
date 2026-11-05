// workday_rule.go 承载工作日历的核心判定规则（issue #50，docs/oa-mvp-spec.md
// §3.3/§4.8/§4.10）。全部为**纯函数**（无存储访问、无 HTTP 上下文），显式记录
// 以 ExplicitDays 集合入参，供请假时长（#52 槽位）与考勤派生（#61 非工作日
// 判定）直接复用——查询编排与落库不在此文件（见 workday.go / repository）。
//
// 语义（spec §3.3）：
//   - 无显式记录：周一至周五默认工作日，周六周日默认休息日；
//   - 有显式记录：以记录为准，type=0（工作日）可表达周末调休上班，
//     type=2（节假日）可把工作日标记为节假日，type=1（休息日）可把
//     工作日标记为休息日。
package service

import (
	"fmt"
	"time"
)

// DateLayout 是工作日历对外日期格式（YYYY-MM-DD）。
const DateLayout = "2006-01-02"

// 日期三态（spec §3.3：0 工作日 / 1 休息日 / 2 节假日）。
const (
	WorkdayTypeWorking = int8(0) // 工作日（含周末调休上班）
	WorkdayTypeRest    = int8(1) // 休息日
	WorkdayTypeHoliday = int8(2) // 节假日
)

// WorkdayTypeName 返回日类型的中文展示名。
func WorkdayTypeName(t int8) string {
	switch t {
	case WorkdayTypeWorking:
		return "工作日"
	case WorkdayTypeRest:
		return "休息日"
	case WorkdayTypeHoliday:
		return "节假日"
	default:
		return fmt.Sprintf("未知类型(%d)", t)
	}
}

// ValidWorkdayType 报告日类型取值是否合法（service 边界校验用）。
func ValidWorkdayType(t int8) bool {
	return t >= WorkdayTypeWorking && t <= WorkdayTypeHoliday
}

// ExplicitDays 是显式日历记录集合（存储无关）：键为 YYYY-MM-DD 归一化日期
// 串（DateKey 产出），值为日类型。调用方从仓储取行后自行构造。
type ExplicitDays map[string]int8

// DateKey 把任意时区的时间归一化为日期键 YYYY-MM-DD：按该时间自身位置的
// 年月日重建 UTC 零点再格式化，因此同一日历日期无论带哪个时区偏移都映射到
// 同一个键（MySQL DATE 列扫描结果也适用）。
func DateKey(t time.Time) string {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Format(DateLayout)
}

// NormalizeDate 严格解析 YYYY-MM-DD（不含时间部分、必须前导零），返回 UTC
// 零点时间。API 边界校验用：非该格式的输入一律拒绝。
func NormalizeDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation(DateLayout, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("日期格式应为 YYYY-MM-DD：%w", err)
	}
	return t, nil
}

// NormalizeMonth 严格解析 YYYY-MM，返回该月首日的 UTC 零点时间。
func NormalizeMonth(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("月份格式应为 YYYY-MM：%w", err)
	}
	return t, nil
}

// DefaultDayType 默认规则（spec §3.3）：周一至周五工作日，周六周日休息。
func DefaultDayType(date time.Time) int8 {
	switch date.Weekday() {
	case time.Saturday, time.Sunday:
		return WorkdayTypeRest
	default:
		return WorkdayTypeWorking
	}
}

// ResolveDayType 单日合成（纯函数）：显式记录优先，无记录走默认规则。
// 显式集合中的非法类型值视为无记录（走默认规则）——集合由仓储构造，
// 该兜底只防御脏数据，不替代 service 边界的入参校验。
func ResolveDayType(date time.Time, explicit ExplicitDays) int8 {
	if t, ok := explicit[DateKey(date)]; ok && ValidWorkdayType(t) {
		return t
	}
	return DefaultDayType(date)
}

// ResolvedDay 是逐日合成结果（区间/月视图的行）。
type ResolvedDay struct {
	// Date 日期（UTC 零点）。
	Date time.Time
	// Weekday 星期几（time.Sunday=0 … time.Saturday=6）。
	Weekday time.Weekday
	// Type 合成后的日类型（0 工作日 / 1 休息日 / 2 节假日）。
	Type int8
	// Explicit 是否来自显式记录（false = 默认规则推出）。
	Explicit bool
}

// ResolveRange 逐日合成（纯函数）：[start, end] 闭区间按日升序，每天一行。
// start/end 先经 DateKey 归一化，区间顺序颠倒时返回空切片（调用方在 API
// 边界给出可读错误，这里不报错）。
func ResolveRange(start, end time.Time, explicit ExplicitDays) []ResolvedDay {
	startKey, endKey := DateKey(start), DateKey(end)
	if startKey > endKey {
		return nil
	}
	cursor := mustUTCDate(start)
	days := make([]ResolvedDay, 0, 16)
	for key := startKey; key <= endKey; key = nextDateKey(key) {
		t, ok := explicit[key]
		day := ResolvedDay{Date: cursor, Weekday: cursor.Weekday()}
		if ok && ValidWorkdayType(t) {
			day.Type = t
			day.Explicit = true
		} else {
			day.Type = DefaultDayType(cursor)
		}
		days = append(days, day)
		cursor = cursor.AddDate(0, 0, 1)
	}
	return days
}

// mustUTCDate 归一化为 UTC 零点（保留原年月日）。
func mustUTCDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// nextDateKey 返回下一日的日期键（键串之间按字典序即时间序比较）。
func nextDateKey(key string) string {
	t, err := time.ParseInLocation(DateLayout, key, time.UTC)
	if err != nil {
		// 键由 DateKey 产出，格式恒合法；防御分支直接终止循环。
		return key
	}
	return t.AddDate(0, 0, 1).Format(DateLayout)
}

// 半天槽位（spec §4.8：0 上午 / 1 下午）。
const (
	HalfAM = int8(0)
	HalfPM = int8(1)
)

// ValidHalf 报告半天取值是否合法（0 上午 / 1 下午）。
func ValidHalf(h int8) bool {
	return h == HalfAM || h == HalfPM
}

// Slot 是一个 (日期, 半天) 工作日槽位（spec §4.8：请假时长按槽位计算）。
type Slot struct {
	Date time.Time
	Half int8 // HalfAM / HalfPM
}

// ResolveWorkdaySlots 计算请假区间的工作日槽位（纯函数，#52 请假时长复用）：
// [start, end] 内每个工作日贡献上午/下午两个槽位，按起止半天边界裁剪；
// 非工作日（休息日/节假日）不贡献。起止颠倒、同日「下午→上午」、半天值
// 非法时返回空（service 层据此拒绝提交）。
func ResolveWorkdaySlots(start, end time.Time, startHalf, endHalf int8, explicit ExplicitDays) []Slot {
	if !ValidHalf(startHalf) || !ValidHalf(endHalf) {
		return nil
	}
	days := ResolveRange(start, end, explicit)
	if len(days) == 0 {
		return nil
	}
	// 同日时半区间颠倒（下午→上午）直接为空。
	firstKey, lastKey := DateKey(days[0].Date), DateKey(days[len(days)-1].Date)
	if firstKey == lastKey && startHalf > endHalf {
		return nil
	}

	slots := make([]Slot, 0, len(days)*2)
	for i, day := range days {
		if day.Type != WorkdayTypeWorking {
			continue // 非工作日不贡献槽位
		}
		halves := []int8{HalfAM, HalfPM}
		if i == 0 && startHalf == HalfPM {
			halves = []int8{HalfPM} // 起始日下午起
		}
		if i == len(days)-1 && endHalf == HalfAM {
			if i == 0 && startHalf == HalfPM {
				halves = nil // 单日且下午起上午止 → 空槽位（上面已拦截）
			} else {
				halves = []int8{HalfAM} // 结束日上午止
			}
		}
		for _, half := range halves {
			slots = append(slots, Slot{Date: day.Date, Half: half})
		}
	}
	return slots
}
