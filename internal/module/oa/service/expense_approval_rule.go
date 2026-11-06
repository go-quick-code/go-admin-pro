// expense_approval_rule.go 承载报销提交审批的纯规则（issue #55，
// docs/oa-mvp-spec.md §4.9）：报销单展示单号与 bpm 实例标题/摘要快照。
//
// 报销单没有独立单号列（#54 的表结构不新增列、本工单不新增迁移），展示单号
// 由主键派生为定宽 `BX-%06d`：稳定、可读、跨重启不变，足够在待办页与审批
// 时间线里指认单据。标题/摘要是 bpm 的快照字段（ADR 0008），只放展示所需的
// 最小信息：单号、金额、报销人部门与事由；超长按列宽截断（不切坏多字节字符）。
package service

import (
	"fmt"
	"strings"

	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// expenseClaimNo 返回报销单展示单号（BX-000123）：6 位定宽，超宽自然增长。
func expenseClaimNo(id int64) string {
	return fmt.Sprintf("BX-%06d", id)
}

// expenseApprovalSnapshot 构造 bpm 实例标题与摘要快照（纯函数，表驱动测试
// 锁定）：
//   - 标题：`报销审批：{单号} {标题}`；
//   - 摘要：单号、报销人部门、金额（元）、事由，部门缺失时回退「未知部门」；
//   - 两者按 bpm 快照列宽（标题 128 / 摘要 500）截断。
func expenseApprovalSnapshot(claimNo, title, deptName string, amount money.Money, reason string) (string, string) {
	if strings.TrimSpace(deptName) == "" {
		deptName = "未知部门"
	}
	titleSnap := truncateRunes(fmt.Sprintf("报销审批：%s %s", claimNo, strings.TrimSpace(title)),
		bpmservice.MaxInstanceTitleLen)
	summarySnap := truncateRunes(
		fmt.Sprintf("单号：%s；部门：%s；金额：%s 元；事由：%s",
			claimNo, deptName, amount.String(), strings.TrimSpace(reason)),
		bpmservice.MaxInstanceSummaryLen,
	)
	return titleSnap, summarySnap
}
