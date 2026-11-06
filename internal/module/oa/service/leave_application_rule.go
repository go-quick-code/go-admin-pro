// leave_application_rule.go 承载请假申请提交的核心判定规则（issue #52，
// docs/oa-mvp-spec.md §4.8）。全部为**纯函数**（无存储访问、无 HTTP 上下文）：
//   - 区间与半天方向校验（同日「下午→上午」非法、跨自然年拒绝）；
//   - 槽位时长（时长 = 工作日槽位数 / 2，非工作日不贡献槽位）；
//   - 同一用户待审/已通过请假的槽位重叠判定（任何 (日期, 半天) 相交即重叠）。
//
// 槽位本身由 #50 的 ResolveWorkdaySlots 计算（工作日历纯函数），本文件只消费
// []Slot。表驱动测试锁定语义（leave_application_rule_test.go），期望值手算核对。
package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// HalfDayCents 是半天在 decimal(5,1) 天数中的分值（0.5 天 = 50 分）。
const HalfDayCents = 50

// ValidateLeaveSpan 校验请假区间与半天边界（spec §4.8）：
//   - 半天取值只能是 0 上午 / 1 下午；
//   - 结束日期不能早于开始日期（同日允许）；
//   - 同一天「下午→上午」非法；
//   - 跨自然年拒绝提交（v1 要求拆成两单）。
//
// 纯函数：日期按自身年月日归一化比较，与入参时区无关。
func ValidateLeaveSpan(start, end time.Time, startHalf, endHalf int8) error {
	if !ValidHalf(startHalf) || !ValidHalf(endHalf) {
		return xerr.BadRequest("请假半天取值应为 0（上午）/ 1（下午）")
	}
	startKey, endKey := DateKey(start), DateKey(end)
	if startKey > endKey {
		return xerr.BadRequest("开始日期不能晚于结束日期")
	}
	if startKey == endKey && startHalf > endHalf {
		return xerr.BadRequest("同一天的请假不能从下午开始、到上午结束")
	}
	if start.Year() != end.Year() {
		return xerr.BadRequest("请假不能跨自然年，请拆成两单提交")
	}
	return nil
}

// LeaveDaysOfSlots 返回槽位对应的请假时长（天）：每个槽位半天，
// 即 len(slots)/2（spec §4.8）。槽位为空时返回 0，是否允许提交由调用方判定。
func LeaveDaysOfSlots(slots []Slot) money.Money {
	return money.FromCents(int64(len(slots)) * HalfDayCents)
}

// ValidateLeaveSlots 校验收到的槽位非空（spec §4.8：总槽位为 0 时拒绝提交）：
// 区间可以落在休息日/节假日上，但必须至少覆盖一个工作日半天。
func ValidateLeaveSlots(slots []Slot) error {
	if len(slots) == 0 {
		return xerr.BadRequest("请假区间内没有工作日，时长不能为 0（请检查起止日期与工作日历）")
	}
	return nil
}

// CheckLeaveBalance 校验占用额度类型的可用余额：available < duration 时
// 返回可读的 400（spec §4.8：可用额度 = quota + adjustment - used，不足拒绝提交）。
func CheckLeaveBalance(typeName string, available, duration money.Money) error {
	if available.Cents() >= duration.Cents() {
		return nil
	}
	return xerr.BadRequest(fmt.Sprintf(
		"「%s」可用额度不足：本次需要 %s 天，当前可用 %s 天",
		typeName, formatLeaveDays(duration), formatLeaveDays(available),
	))
}

// slotKey 是槽位的判重键：(日期, 半天)。
type slotKey struct {
	date string
	half int8
}

// SlotsOverlap 报告两组槽位是否存在交集（spec §4.8：同一用户待审/已通过
// 请假按槽位判重，任何槽位重叠即拒绝）。同一时刻的一个半天只能被占用一次。
func SlotsOverlap(a, b []Slot) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	// 以较小集合建表，降低内存与比较次数。
	if len(a) > len(b) {
		a, b = b, a
	}
	occupied := make(map[slotKey]struct{}, len(a))
	for _, slot := range a {
		occupied[slotKey{date: DateKey(slot.Date), half: slot.Half}] = struct{}{}
	}
	for _, slot := range b {
		if _, ok := occupied[slotKey{date: DateKey(slot.Date), half: slot.Half}]; ok {
			return true
		}
	}
	return false
}
