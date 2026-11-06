// leave_rule.go 承载请假额度的核心算术规则（issue #51，docs/oa-mvp-spec.md
// §3.5/§4.8）。全部为**纯函数**（无存储访问、无 HTTP 上下文），供服务编排
// （leave.go）与 #52 的提交校验/审批回调直接复用：
//   - 可用额度 = quota + adjustment - used（balance_managed=true 的类型检查）；
//   - 通过扣减 used += duration、通过后取消返还 used -= duration（夹零）；
//   - 初始化幂等计划 MissingInitKeys（重复初始化不重复建行的核心）。
//
// 表驱动测试锁定语义（leave_rule_test.go），期望值手算核对。
package service

import "github.com/go-quick-code/go-admin-pro/internal/pkg/money"

// dayRange 是 decimal(5,1) 列的取值上下限（±9999.9 天）。
const dayMaxCents = 99999 * 10 // 9999.9 天 = 99999 个 0.1 天 = 999990 分

// AvailableDays 返回可用额度 = quota + adjustment - used（spec §4.8）。
// 三个入参都按 decimal(5,1) 边界校验（≤9999.9），分值算术不会溢出；
// 结果可为负（调整缩水后超用），由调用方决定是否拒绝提交。
func AvailableDays(quota, adjustment, used money.Money) money.Money {
	return money.FromCents(quota.Cents() + adjustment.Cents() - used.Cents())
}

// ConsumeDays 返回扣减后的 used = used + duration（spec §4.8：通过时
// used += duration）。#52 审批通过回调复用。
func ConsumeDays(used, duration money.Money) money.Money {
	return money.FromCents(used.Cents() + duration.Cents())
}

// RefundDays 返回返还后的 used = used - duration，负值夹零（spec §4.8：
// 通过后取消时 used -= duration）。返还不得使 used 为负——数据不一致
// （如重复回调）时宁可归零，不污染后续可用额度。#52 取消回调复用。
func RefundDays(used, duration money.Money) money.Money {
	c := used.Cents() - duration.Cents()
	if c < 0 {
		c = 0
	}
	return money.FromCents(c)
}

// BalanceKey 是额度的逻辑主键（租户由查询上下文隐含）：(用户, 类型, 自然年)。
type BalanceKey struct {
	UserID      int64 `json:"userId"`
	LeaveTypeID int64 `json:"leaveTypeId"`
	Year        int   `json:"year"`
}

// MissingInitKeys 计算初始化缺失组合（纯函数）：wanted 为本次应存在的全部
// 组合（同批重复自动去重、保持首次出现顺序），existing 为库中已有行；
// 返回 wanted 中 existing 没有的组合。重复初始化不重复建行由此锁定。
func MissingInitKeys(wanted, existing []BalanceKey) []BalanceKey {
	have := make(map[BalanceKey]struct{}, len(existing))
	for _, k := range existing {
		have[k] = struct{}{}
	}
	seen := make(map[BalanceKey]struct{}, len(wanted))
	out := make([]BalanceKey, 0, len(wanted))
	for _, k := range wanted {
		if _, ok := have[k]; ok {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// IsDayPrecision 报告天数是否符合 decimal(5,1) 精度（最多一位小数，
// 半天 0.5）。两位小数会被列的四舍五入吞掉，service 边界必须拒绝。
func IsDayPrecision(v money.Money) bool {
	return v.Cents()%10 == 0
}

// IsDayRange 报告天数绝对值是否落在 decimal(5,1) 范围内（≤9999.9 天）。
func IsDayRange(v money.Money) bool {
	c := v.Cents()
	if c < 0 {
		c = -c
	}
	return c <= dayMaxCents
}

// IsValidYear 报告自然年是否在允许范围（2000–2100，防手滑 0/负数/远未来）。
func IsValidYear(year int) bool {
	return year >= 2000 && year <= 2100
}
