// expense_rule.go 承载报销单的纯规则（issue #54，docs/oa-mvp-spec.md §4.9）：
//
//   - 明细校验与合计：至少一条明细、每笔金额 > 0、合计由服务端逐笔相加
//     （int64 分，经 money.Add 做 decimal(12,2) 范围校验，全程不经过 float64）；
//   - 状态守卫：仅草稿/已驳回可编辑、删除、提交（提交逻辑属 #55，本工单只
//     预留状态与守卫，保证状态机定义单点）；
//   - 费用类别：取系统字典 oa_expense_category 的启用值，去首尾空白后精确匹配。
//
// 全部为**纯函数**（无存储访问、无 HTTP 上下文），服务层只负责取数与事务编排；
// 表驱动测试锁定语义（expense_rule_test.go），期望值手算核对。
package service

import (
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// MaxExpenseCategoryLen 是费用类别落库上限（oa_expense_item.category 列宽）。
const MaxExpenseCategoryLen = 50

// ExpenseAction 是报销单可执行的业务操作。本工单实现编辑/删除；提交由 #55
// 承接，但守卫在此与它们同源，避免状态机出现第二份定义。
type ExpenseAction int8

const (
	// ExpenseActionUpdate 编辑表头与明细（替换行集并重算合计）。
	ExpenseActionUpdate ExpenseAction = 1
	// ExpenseActionDelete 删除整单（草稿/已驳回；明细级联软删）。
	ExpenseActionDelete ExpenseAction = 2
	// ExpenseActionSubmit 提交审批（#55 落地；草稿 → 待审）。
	ExpenseActionSubmit ExpenseAction = 3
)

// ValidateExpenseAction 判定操作在当前状态是否允许（spec §4.9/§4.14）：
// 草稿与已驳回可编辑、删除、提交（驳回后修改重提）；待审/通过/撤回一律 409
// ——通过即终态（不允许修改明细），待审材料随审批冻结。
func ValidateExpenseAction(action ExpenseAction, status int8) error {
	switch action {
	case ExpenseActionUpdate:
		if !expenseMutable(status) {
			return xerr.Conflict("仅草稿或已驳回的报销单可编辑")
		}
		return nil
	case ExpenseActionDelete:
		if !expenseMutable(status) {
			return xerr.Conflict("仅草稿或已驳回的报销单可删除")
		}
		return nil
	case ExpenseActionSubmit:
		if !expenseMutable(status) {
			return xerr.Conflict("当前状态不可提交（仅草稿或已驳回可提交）")
		}
		return nil
	default:
		return xerr.BadRequest("未知的报销操作")
	}
}

// expenseMutable 报告状态是否处于可写窗口：草稿或已驳回。
func expenseMutable(status int8) bool {
	return status == model.ExpenseStatusDraft || status == model.ExpenseStatusRejected
}

// ValidateExpenseItems 校验明细并返回合计（spec §4.9）：
//   - 至少一条明细；
//   - 每条金额 > 0；
//   - 合计 > 0 且落在 decimal(12,2) 范围内（逐笔 money.Add，越界即拒绝）。
//
// 入参是各行金额（分单位）；返回的表头金额由本函数产出，调用方不得透传请求
// 里的「合计」字段。
func ValidateExpenseItems(amounts []money.Money) (money.Money, error) {
	if len(amounts) == 0 {
		return money.Money{}, xerr.BadRequest("至少填写一条报销明细")
	}
	total := money.FromCents(0)
	for i, amount := range amounts {
		if amount.Cents() <= 0 {
			return money.Money{}, xerr.BadRequest(fmt.Sprintf(
				"第 %d 条明细的金额必须大于 0（当前 %s）", i+1, amount.String(),
			))
		}
		sum, err := total.Add(amount)
		if err != nil {
			return money.Money{}, xerr.BadRequest(fmt.Sprintf(
				"明细合计超出金额上限（decimal(12,2)，最大 9999999999.99）：%v", err,
			))
		}
		total = sum
	}
	if total.Cents() <= 0 {
		return money.Money{}, xerr.BadRequest("明细合计必须大于 0")
	}
	return total, nil
}

// NormalizeExpenseCategory 校验并归一化费用类别（spec §4.9：类别取系统字典
// oa_expense_category 的启用值）：
//   - 去首尾空白后不能为空、长度不超过列宽 50；
//   - 必须在允许集合内（字典为空视为未配置，fail-closed 拒绝）。
func NormalizeExpenseCategory(raw string, allowed []string) (string, error) {
	category := strings.TrimSpace(raw)
	if category == "" {
		return "", xerr.BadRequest("费用类别不能为空")
	}
	if len([]rune(category)) > MaxExpenseCategoryLen {
		return "", xerr.BadRequest(fmt.Sprintf("费用类别不能超过 %d 字", MaxExpenseCategoryLen))
	}
	if len(allowed) == 0 {
		return "", xerr.BadRequest("报销类别字典（oa_expense_category）未配置，请联系管理员")
	}
	for _, value := range allowed {
		if value == category {
			return category, nil
		}
	}
	return "", xerr.BadRequest(fmt.Sprintf(
		"费用类别「%s」不在字典 oa_expense_category 中（可选：%s）",
		category, strings.Join(allowed, "、"),
	))
}
