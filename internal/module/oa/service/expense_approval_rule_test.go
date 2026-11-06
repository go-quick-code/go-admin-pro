// expense_approval_rule_test.go 表驱动锁定报销提交审批的纯规则（issue #55，
// docs/oa-mvp-spec.md §4.9）：
//
//   - 报销单展示单号：无独立单号列（#54 不新增表/列），由 ID 派生为定宽
//     `BX-%06d`，稳定且可读；
//   - 审批快照：标题/摘要必须含单号、金额与报销人部门（供通用待办页展示）；
//     超长按 bpm 快照列宽（标题 128 / 摘要 500）截断，不切坏多字节字符。
//
// 期望值手算核对（独立于实现）：id 1 → BX-000001；128 字标题截断后长度恰为
// 128；摘要超长截断后长度恰为 500。
package service

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
)

// TestExpenseClaimNo 报销单展示单号：定宽 6 位，超宽自然增长。
func TestExpenseClaimNo(t *testing.T) {
	cases := []struct {
		id   int64
		want string
	}{
		{id: 1, want: "BX-000001"},
		{id: 42, want: "BX-000042"},
		{id: 999999, want: "BX-999999"},
		{id: 1000000, want: "BX-1000000"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("id=%d", tc.id), func(t *testing.T) {
			if got := expenseClaimNo(tc.id); got != tc.want {
				t.Fatalf("单号 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestExpenseApprovalSnapshot 审批快照：标题/摘要含单号、金额与部门；
// 空部门回退占位；超长截断到 snapshots 列宽且不切坏字符。
func TestExpenseApprovalSnapshot(t *testing.T) {
	t.Run("常规快照", func(t *testing.T) {
		title, summary := expenseApprovalSnapshot("BX-000007", "十月客户拜访差旅", "研发部", mustMoney(t, "1280.50"), "客户拜访")
		if want := "报销审批：BX-000007 十月客户拜访差旅"; title != want {
			t.Fatalf("标题 = %q，期望 %q", title, want)
		}
		for _, must := range []string{"BX-000007", "研发部", "1280.50", "客户拜访"} {
			if !strings.Contains(summary, must) {
				t.Fatalf("摘要 %q 应包含 %q", summary, must)
			}
		}
	})

	t.Run("部门缺失回退占位", func(t *testing.T) {
		_, summary := expenseApprovalSnapshot("BX-000008", "打车费", "", mustMoney(t, "12.00"), "打车")
		if !strings.Contains(summary, "未知部门") {
			t.Fatalf("摘要 %q 应包含部门占位「未知部门」", summary)
		}
		if !strings.Contains(summary, "12.00") {
			t.Fatalf("摘要 %q 应包含金额 12.00", summary)
		}
	})

	t.Run("超长按列宽截断", func(t *testing.T) {
		title, summary := expenseApprovalSnapshot(
			"BX-000009",
			strings.Repeat("长", 300),
			strings.Repeat("部", 80),
			mustMoney(t, "9999999999.99"),
			strings.Repeat("由", 800),
		)
		if n := utf8.RuneCountInString(title); n != bpmservice.MaxInstanceTitleLen {
			t.Fatalf("标题长度 = %d，期望 %d", n, bpmservice.MaxInstanceTitleLen)
		}
		if n := utf8.RuneCountInString(summary); n != bpmservice.MaxInstanceSummaryLen {
			t.Fatalf("摘要长度 = %d，期望 %d", n, bpmservice.MaxInstanceSummaryLen)
		}
	})
}
