// leave_rule_test.go 锁定额度算术的纯函数规则（issue #51，docs/oa-mvp-spec.md
// §4.8）：可用额度、扣减/返还（返还夹零）、初始化幂等计划、天数量精度校验。
// 期望值来自手算与规格定义（独立于实现推导）。
package service

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// m 是测试速记：解析十进制天数（一位小数，如 "5"、"2.5"）。
func m(t *testing.T, s string) money.Money {
	t.Helper()
	v, err := money.ParseMoney(s)
	if err != nil {
		t.Fatalf("测试字面量 %q 非法: %v", s, err)
	}
	return v
}

// TestAvailableDays 可用额度 = quota + adjustment - used（spec §4.8）。
// 独立核对：5 + 2 - 3 = 4；0 + (-1) - 0.5 = -1.5（调整可为负，可用可为负）。
func TestAvailableDays(t *testing.T) {
	cases := []struct {
		name                   string
		quota, adjustment, use string
		want                   string
	}{
		{"初始额度减已用", "5", "0", "3", "2.00"},
		{"加调整后计算", "5", "2", "3", "4.00"},
		{"负调整", "5", "-2", "1", "2.00"},
		{"零额度", "0", "0", "0", "0.00"},
		{"可用为负（超用）", "1", "0", "2.5", "-1.50"},
		{"无额度负调整", "0", "-1", "0.5", "-1.50"},
		{"半天粒度", "2.5", "0.5", "1.5", "1.50"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AvailableDays(m(t, tc.quota), m(t, tc.adjustment), m(t, tc.use))
			if got.String() != tc.want {
				t.Fatalf("AvailableDays(%s, %s, %s) = %s，期望 %s", tc.quota, tc.adjustment, tc.use, got, tc.want)
			}
		})
	}
}

// TestConsumeDays 扣减：used + duration（spec §4.8 通过时 used += duration）。
func TestConsumeDays(t *testing.T) {
	cases := []struct {
		name       string
		used, days string
		want       string
	}{
		{"整天扣减", "3", "2", "5.00"},
		{"半天扣减", "1.5", "0.5", "2.00"},
		{"零值", "2", "0", "2.00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ConsumeDays(m(t, tc.used), m(t, tc.days))
			if got.String() != tc.want {
				t.Fatalf("ConsumeDays(%s, %s) = %s，期望 %s", tc.used, tc.days, got, tc.want)
			}
		})
	}
}

// TestRefundDays 返还：used - duration，负值夹零（返回不得超过已用，
// 数据不一致时宁可归零也不落负数污染后续余额）。
func TestRefundDays(t *testing.T) {
	cases := []struct {
		name       string
		used, days string
		want       string
	}{
		{"正常返还", "5", "2", "3.00"},
		{"半天返还", "1.5", "0.5", "1.00"},
		{"全部返还", "2", "2", "0.00"},
		{"超额返还夹零", "1", "3", "0.00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RefundDays(m(t, tc.used), m(t, tc.days))
			if got.String() != tc.want {
				t.Fatalf("RefundDays(%s, %s) = %s，期望 %s", tc.used, tc.days, got, tc.want)
			}
		})
	}
}

// TestMissingInitKeys 初始化幂等计划（纯函数）：wanted = 全部应存在的
// (用户, 类型, 年) 组合（同批去重），existing = 库中已有行；输出缺失组合。
// 重复初始化不重复建行的核心：existing 与 wanted 重叠部分不再输出。
func TestMissingInitKeys(t *testing.T) {
	key := func(u, ty int64, y int) BalanceKey {
		return BalanceKey{UserID: u, LeaveTypeID: ty, Year: y}
	}
	cases := []struct {
		name     string
		wanted   []BalanceKey
		existing []BalanceKey
		want     []BalanceKey
	}{
		{
			name:   "空库全量初始化",
			wanted: []BalanceKey{key(10, 1, 2026), key(11, 1, 2026)},
			want:   []BalanceKey{key(10, 1, 2026), key(11, 1, 2026)},
		},
		{
			name:     "已有行跳过（幂等）",
			wanted:   []BalanceKey{key(10, 1, 2026), key(11, 1, 2026)},
			existing: []BalanceKey{key(10, 1, 2026)},
			want:     []BalanceKey{key(11, 1, 2026)},
		},
		{
			name:     "全部已存在则无缺失",
			wanted:   []BalanceKey{key(10, 1, 2026)},
			existing: []BalanceKey{key(10, 1, 2026)},
			want:     nil,
		},
		{
			name:     "同批重复组合去重",
			wanted:   []BalanceKey{key(10, 1, 2026), key(10, 1, 2026)},
			existing: nil,
			want:     []BalanceKey{key(10, 1, 2026)},
		},
		{
			name:     "年份/类型不同视为不同行",
			wanted:   []BalanceKey{key(10, 1, 2026), key(10, 2, 2026), key(10, 1, 2027)},
			existing: []BalanceKey{key(10, 1, 2026)},
			want:     []BalanceKey{key(10, 2, 2026), key(10, 1, 2027)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MissingInitKeys(tc.wanted, tc.existing)
			if len(got) != len(tc.want) {
				t.Fatalf("缺失组合 %v，期望 %v", got, tc.want)
			}
			seen := map[BalanceKey]bool{}
			for _, k := range got {
				if seen[k] {
					t.Fatalf("输出重复组合 %v", k)
				}
				seen[k] = true
			}
			for _, w := range tc.want {
				if !seen[w] {
					t.Fatalf("缺失期望组合 %v（实际 %v）", w, got)
				}
			}
		})
	}
}

// TestIsDayPrecision 天数量精度校验：额度/调整/时长都按 decimal(5,1) 存储，
// 最多一位小数（半天 0.5）；两位小数会被列的四舍五入吞掉，必须边界拒绝。
func TestIsDayPrecision(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"0", true}, {"5", true}, {"2.5", true}, {"-1.5", true},
		{"2.55", false}, {"0.01", false}, {"0.05", false},
	}
	for _, tc := range cases {
		got := IsDayPrecision(m(t, tc.input))
		if got != tc.want {
			t.Fatalf("IsDayPrecision(%s) = %v，期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestIsValidYear 年度取值范围（spec §3.5 按自然年；防手滑 0/负数/远未来）。
func TestIsValidYear(t *testing.T) {
	for _, ok := range []int{2026, 2025, 2100} {
		if !IsValidYear(ok) {
			t.Fatalf("年份 %d 应合法", ok)
		}
	}
	for _, bad := range []int{0, -1, 1999, 2101} {
		if IsValidYear(bad) {
			t.Fatalf("年份 %d 应非法", bad)
		}
	}
}
