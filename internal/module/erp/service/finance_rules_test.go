package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// finance_rules_test.go 锁定财务核销的纯函数（次 seam，spec §8.1：核销上限
// 计算表驱动）。期望值来自 spec §4.7 的字面公式手算，不与实现复用同一套代码。

func cents(v int64) money.Money { return money.FromCents(v) }

func assertFinanceConflict(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 409 冲突，得到 nil")
	}
	var xe *xerr.Error
	if !errors.As(err, &xe) || xe.Status != http.StatusConflict {
		t.Fatalf("期望 409 冲突，得到 %v", err)
	}
}

// TestGroupFinanceDrafts 明细按（biz_type,biz_id）聚合：同一单据多行合并，
// 不同单据分开；合计溢出 decimal(12,2) 报 400。
func TestGroupFinanceDrafts(t *testing.T) {
	grouped, err := groupFinanceDrafts([]financeItemDraft{
		{BizType: model.FinanceBizSaleOut, BizID: 7, Price: cents(3000)},
		{BizType: model.FinanceBizSaleOut, BizID: 7, Price: cents(2500)},
		{BizType: model.FinanceBizSaleReturn, BizID: 9, Price: cents(100)},
	})
	if err != nil {
		t.Fatalf("聚合明细：%v", err)
	}
	if got := grouped[financeBizKey{model.FinanceBizSaleOut, 7}].Cents(); got != 5500 {
		t.Fatalf("销售出库 7 核销合计 = %d 分，期望 5500", got)
	}
	if got := grouped[financeBizKey{model.FinanceBizSaleReturn, 9}].Cents(); got != 100 {
		t.Fatalf("销售退货 9 核销合计 = %d 分，期望 100", got)
	}

	// 两行各自合法但合计越界：聚合入口必须报 400（不得溢出）。
	_, err = groupFinanceDrafts([]financeItemDraft{
		{BizType: model.FinanceBizSaleOut, BizID: 7, Price: money.FromCents(900_000_000_000)},
		{BizType: model.FinanceBizSaleOut, BizID: 7, Price: money.FromCents(100_000_000_000)},
	})
	if err == nil {
		t.Fatalf("期望聚合溢出报错，得到 nil")
	}
	var xe *xerr.Error
	if !errors.As(err, &xe) || xe.Status != http.StatusBadRequest {
		t.Fatalf("期望 400，得到 %v", err)
	}
}

// TestCheckFinanceLimit 核销上限（spec §4.7）：本单明细合计 + 该单据已被其它
// 已审核收付款核销金额 ≤ 单据 total_price；等于上限放行，超出 409。
func TestCheckFinanceLimit(t *testing.T) {
	key := financeBizKey{model.FinanceBizSaleOut, 7}
	otherKey := financeBizKey{model.FinanceBizSaleOut, 8}
	totals := map[financeBizKey]money.Money{key: cents(10000), otherKey: cents(5000)}
	nos := map[financeBizKey]string{key: "CK202601010001", otherKey: "CK202601010002"}

	cases := []struct {
		name      string
		own       map[financeBizKey]money.Money
		allocated map[financeBizKey]money.Money
		wantErr   bool
	}{
		{
			name:      "恰好等于上限放行",
			own:       map[financeBizKey]money.Money{key: cents(10000)},
			allocated: map[financeBizKey]money.Money{},
		},
		{
			name:      "已核销 6000 后本单最多 4000",
			own:       map[financeBizKey]money.Money{key: cents(4000)},
			allocated: map[financeBizKey]money.Money{key: cents(6000)},
		},
		{
			name:      "已核销 6000 后本单 4001 超限",
			own:       map[financeBizKey]money.Money{key: cents(4001)},
			allocated: map[financeBizKey]money.Money{key: cents(6000)},
			wantErr:   true,
		},
		{
			name: "多单据互不影响",
			own: map[financeBizKey]money.Money{
				key:      cents(5000),
				otherKey: cents(5000),
			},
			allocated: map[financeBizKey]money.Money{key: cents(5000)},
		},
		{
			name:      "单据未出现在总额表（配额 0）拒绝",
			own:       map[financeBizKey]money.Money{{BizType: model.FinanceBizPurchaseIn, BizID: 999}: cents(1)},
			allocated: map[financeBizKey]money.Money{},
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFinanceLimit(tc.own, totals, tc.allocated, nos)
			if tc.wantErr {
				assertFinanceConflict(t, err)
				return
			}
			if err != nil {
				t.Fatalf("期望放行，得到 %v", err)
			}
		})
	}
}

// TestFoldAccountBalances 余额 = 已审核收款 − 已审核付款（纯展示，不落库）。
func TestFoldAccountBalances(t *testing.T) {
	balances, err := foldAccountBalances(
		map[int64]money.Money{1: cents(10000), 2: cents(500)},
		map[int64]money.Money{1: cents(3000), 3: cents(200)},
	)
	if err != nil {
		t.Fatalf("计算余额：%v", err)
	}
	want := map[int64]int64{1: 7000, 2: 500, 3: -200}
	for accountID, wantCents := range want {
		if got := balances[accountID].Cents(); got != wantCents {
			t.Fatalf("账户 %d 余额 = %d 分，期望 %d 分", accountID, got, wantCents)
		}
	}
	if len(balances) != len(want) {
		t.Fatalf("余额条数 = %d，期望 %d", len(balances), len(want))
	}
}

// TestSortedFinanceKeys 锁序/校验序确定：先按 biz_type 字典序、再按 biz_id 升序。
func TestSortedFinanceKeys(t *testing.T) {
	keys := sortedFinanceKeys(map[financeBizKey]money.Money{
		{model.FinanceBizSaleReturn, 2}:  cents(1),
		{model.FinanceBizSaleOut, 9}:     cents(1),
		{model.FinanceBizSaleOut, 3}:     cents(1),
		{model.FinanceBizPurchaseIn, 10}: cents(1),
	})
	want := []financeBizKey{
		{model.FinanceBizPurchaseIn, 10},
		{model.FinanceBizSaleOut, 3},
		{model.FinanceBizSaleOut, 9},
		{model.FinanceBizSaleReturn, 2},
	}
	if len(keys) != len(want) {
		t.Fatalf("键数 = %d，期望 %d", len(keys), len(want))
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("第 %d 个键 = %+v，期望 %+v", i, keys[i], want[i])
		}
	}
}
