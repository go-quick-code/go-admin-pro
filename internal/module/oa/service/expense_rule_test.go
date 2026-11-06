// expense_rule_test.go 表驱动锁定报销单的纯规则（issue #54，
// docs/oa-mvp-spec.md §4.9）：
//
//   - 明细合计：至少一条、每笔金额必须为正、合计由服务端逐笔相加（int64 分，
//     不经过 float64），合计越界拒绝对应 decimal(12,2) 范围；
//   - 状态守卫：仅草稿/已驳回可编辑、删除、提交（草稿 → 待审属 #55，守卫先定）；
//   - 费用类别：取系统字典 oa_expense_category 的启用值，去首尾空白后匹配。
//
// 期望值手算核对（独立于实现）：12.34 + 0.66 = 13.00；0.01 + 0.02 = 0.03；
// 4,999,999,999.99 × 2 = 9,999,999,999.98（decimal(12,2) 上限内）。
package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// mustMoney 解析金额字面量（测试输入用；失败直接 Fatal）。
func mustMoney(t *testing.T, text string) money.Money {
	t.Helper()
	value, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("测试金额 %q 非法: %v", text, err)
	}
	return value
}

// TestValidateExpenseItems 明细校验与合计：空明细、零/负金额、越界合计一律
// 400；合法明细返回逐笔相加的合计。
func TestValidateExpenseItems(t *testing.T) {
	cases := []struct {
		name    string
		amounts []string
		want    string
		wantErr bool
	}{
		{name: "空明细被拒绝", amounts: nil, wantErr: true},
		{name: "单笔最小金额", amounts: []string{"0.01"}, want: "0.01"},
		{name: "单笔普通金额", amounts: []string{"128.50"}, want: "128.50"},
		{name: "多笔求和", amounts: []string{"12.34", "0.66"}, want: "13.00"},
		{name: "三笔求和", amounts: []string{"0.01", "0.02", "1.00"}, want: "1.03"},
		{name: "零金额被拒绝", amounts: []string{"0.00"}, wantErr: true},
		{name: "单笔负金额被拒绝", amounts: []string{"-1.00"}, wantErr: true},
		{name: "一笔负金额整单被拒绝", amounts: []string{"10.00", "-0.01"}, wantErr: true},
		{name: "零金额混在合法明细中也被拒绝", amounts: []string{"10.00", "0.00"}, wantErr: true},
		{name: "合计恰好到上限", amounts: []string{"4999999999.99", "4999999999.99"}, want: "9999999999.98"},
		{name: "合计越过上限被拒绝", amounts: []string{"9999999999.99", "0.01"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			amounts := make([]money.Money, 0, len(tc.amounts))
			for _, text := range tc.amounts {
				amounts = append(amounts, mustMoney(t, text))
			}
			total, err := ValidateExpenseItems(amounts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望拒绝，实际通过（合计 %s）", total.String())
				}
				if !strings.Contains(err.Error(), "明细") && !strings.Contains(err.Error(), "金额") {
					t.Fatalf("错误信息应可读地说明明细/金额问题，实际 %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过，实际 %v", err)
			}
			if got := total.String(); got != tc.want {
				t.Fatalf("合计 = %s，期望 %s", got, tc.want)
			}
		})
	}
}

// TestValidateExpenseAction 状态守卫矩阵（spec §4.9/§4.14）：
// 草稿/已驳回可编辑、删除、提交；待审/通过/撤回一律 409。
func TestValidateExpenseAction(t *testing.T) {
	cases := []struct {
		name     string
		action   ExpenseAction
		status   int8
		wantErr  bool
		wantHTTP int
	}{
		{name: "草稿可编辑", action: ExpenseActionUpdate, status: model.ExpenseStatusDraft},
		{name: "驳回可编辑", action: ExpenseActionUpdate, status: model.ExpenseStatusRejected},
		{name: "待审不可编辑", action: ExpenseActionUpdate, status: model.ExpenseStatusPending, wantErr: true, wantHTTP: 409},
		{name: "通过不可编辑", action: ExpenseActionUpdate, status: model.ExpenseStatusApproved, wantErr: true, wantHTTP: 409},
		{name: "撤回不可编辑", action: ExpenseActionUpdate, status: model.ExpenseStatusWithdrawn, wantErr: true, wantHTTP: 409},

		{name: "草稿可删除", action: ExpenseActionDelete, status: model.ExpenseStatusDraft},
		{name: "驳回可删除", action: ExpenseActionDelete, status: model.ExpenseStatusRejected},
		{name: "待审不可删除", action: ExpenseActionDelete, status: model.ExpenseStatusPending, wantErr: true, wantHTTP: 409},
		{name: "通过不可删除", action: ExpenseActionDelete, status: model.ExpenseStatusApproved, wantErr: true, wantHTTP: 409},
		{name: "撤回不可删除", action: ExpenseActionDelete, status: model.ExpenseStatusWithdrawn, wantErr: true, wantHTTP: 409},

		// 提交逻辑由 #55 承接，守卫先定：草稿与已驳回（驳回后修改重提）。
		{name: "草稿可提交", action: ExpenseActionSubmit, status: model.ExpenseStatusDraft},
		{name: "驳回可重提", action: ExpenseActionSubmit, status: model.ExpenseStatusRejected},
		{name: "待审不可重复提交", action: ExpenseActionSubmit, status: model.ExpenseStatusPending, wantErr: true, wantHTTP: 409},
		{name: "通过不可提交", action: ExpenseActionSubmit, status: model.ExpenseStatusApproved, wantErr: true, wantHTTP: 409},
		{name: "撤回不可提交", action: ExpenseActionSubmit, status: model.ExpenseStatusWithdrawn, wantErr: true, wantHTTP: 409},

		{name: "未知操作被拒绝", action: ExpenseAction(99), status: model.ExpenseStatusDraft, wantErr: true, wantHTTP: 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateExpenseAction(tc.action, tc.status)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("期望允许，实际 %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("期望拒绝，实际通过")
			}
			var bizErr *xerr.Error
			if !errors.As(err, &bizErr) {
				t.Fatalf("期望业务错误（xerr），实际 %T: %v", err, err)
			}
			if bizErr.Status != tc.wantHTTP {
				t.Fatalf("HTTP 状态 = %d，期望 %d（err=%v）", bizErr.Status, tc.wantHTTP, err)
			}
		})
	}
}

// TestNormalizeExpenseCategory 类别必须来自字典启用值（去首尾空白后精确匹配），
// 长度不超过列宽 50。
func TestNormalizeExpenseCategory(t *testing.T) {
	allowed := []string{"交通", "餐饮", "住宿", "办公", "其他"}
	long50 := strings.Repeat("费", 50)
	long51 := strings.Repeat("费", 51)
	cases := []struct {
		name    string
		raw     string
		allowed []string
		want    string
		wantErr bool
	}{
		{name: "字典内类别", raw: "交通", allowed: allowed, want: "交通"},
		{name: "去除首尾空白", raw: "  餐饮 ", allowed: allowed, want: "餐饮"},
		{name: "空类别被拒绝", raw: "", allowed: allowed, wantErr: true},
		{name: "纯空白被拒绝", raw: "   ", allowed: allowed, wantErr: true},
		{name: "字典外类别被拒绝", raw: "机票", allowed: allowed, wantErr: true},
		{name: "字典未配置被拒绝", raw: "交通", allowed: nil, wantErr: true},
		{name: "50 字类别可通过匹配", raw: long50, allowed: []string{long50}, want: long50},
		{name: "51 字类别被拒绝", raw: long51, allowed: []string{long51}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeExpenseCategory(tc.raw, tc.allowed)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望拒绝，实际得到 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过，实际 %v", err)
			}
			if got != tc.want {
				t.Fatalf("类别 = %q，期望 %q", got, tc.want)
			}
		})
	}
}
