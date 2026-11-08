// finance_rules.go 是财务核销的共享规则（issue #94，docs/erp-mvp-spec.md
// §4.7/§4.8）：核销明细按业务单据聚合、核销上限裁决（纯函数，次 seam 表驱动
// 用例见 finance_rules_test.go）、单据加锁顺序、账户余额实时汇总。
//
// 上限口径（spec §4.7 字面）：本单明细 price 合计 + 该单据已被其它已审核
// 收付款核销金额 ≤ 单据 total_price，审核时 409。余额口径（spec §3.6/§4.7）：
// 余额 = 该账户已审核收款 − 已审核付款，纯展示、不落库。
package service

import (
	"fmt"
	"sort"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// financeBizKey 唯一标识被核销的业务单据（biz_type + biz_id）。
type financeBizKey struct {
	BizType string
	BizID   int64
}

// financeItemDraft 是核销明细草稿（引用单据与金额已在创建/编辑/审核入口校验）。
type financeItemDraft struct {
	BizType string
	BizID   int64
	Price   money.Money
}

// groupFinanceDrafts 按业务单据聚合明细金额：同一单据多行合并（审核上限按
// 单据维度裁决），不同单据分开。合计超出 decimal(12,2) 报 400。
func groupFinanceDrafts(drafts []financeItemDraft) (map[financeBizKey]money.Money, error) {
	grouped := make(map[financeBizKey]money.Money, len(drafts))
	for _, draft := range drafts {
		key := financeBizKey{BizType: draft.BizType, BizID: draft.BizID}
		sum, err := grouped[key].Add(draft.Price)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("单据 %s#%d 核销金额累计超出范围", key.BizType, key.BizID))
		}
		grouped[key] = sum
	}
	return grouped, nil
}

// sortedFinanceKeys 返回稳定的业务单据键顺序（biz_type 字典序 + biz_id 升序）：
// 多单据审核/反审核按同一顺序锁行，防交叉死锁；校验顺序与锁序一致。
func sortedFinanceKeys(allocations map[financeBizKey]money.Money) []financeBizKey {
	keys := make([]financeBizKey, 0, len(allocations))
	for key := range allocations {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].BizType != keys[j].BizType {
			return keys[i].BizType < keys[j].BizType
		}
		return keys[i].BizID < keys[j].BizID
	})
	return keys
}

// checkFinanceLimit 核销上限裁决（spec §4.7）：对每个被核销单据，
// 本单核销额 + 其它已审核核销额 ≤ 单据 total_price；超出 409。
// 单据未出现在总额表（不存在或不可核销）时配额为 0，fail-closed 拒绝。
func checkFinanceLimit(own, docTotals, allocated map[financeBizKey]money.Money, docNos map[financeBizKey]string) error {
	for _, key := range sortedFinanceKeys(own) {
		total := docTotals[key]
		used, err := allocated[key].Add(own[key])
		if err != nil {
			return xerr.Conflict(fmt.Sprintf("单据 %s 核销金额累计超出范围", financeDocLabel(key, docNos)))
		}
		if used.Cents() > total.Cents() {
			return xerr.Conflict(fmt.Sprintf(
				"单据 %s 核销金额 %s 超出单据总额 %s（已被其它已审核收付款核销 %s）",
				financeDocLabel(key, docNos), own[key], total, allocated[key]))
		}
	}
	return nil
}

// financeDocLabel 是 409 文案的单据标签（优先展示单号）。
func financeDocLabel(key financeBizKey, docNos map[financeBizKey]string) string {
	if no := docNos[key]; no != "" {
		return no
	}
	return fmt.Sprintf("%s#%d", key.BizType, key.BizID)
}

// foldAccountBalances 计算结算账户余额（纯展示，不落库）：
// 余额 = Σ该账户已审核收款核销额 − Σ已审核付款核销额（spec §3.6/§4.7）。
func foldAccountBalances(receiptSums, paymentSums map[int64]money.Money) (map[int64]money.Money, error) {
	balances := make(map[int64]money.Money, len(receiptSums)+len(paymentSums))
	for accountID, sum := range receiptSums {
		balances[accountID] = sum
	}
	for accountID, sum := range paymentSums {
		balance, err := balances[accountID].Sub(sum)
		if err != nil {
			return nil, xerr.Conflict(fmt.Sprintf("账户 %d 余额累计超出范围", accountID))
		}
		balances[accountID] = balance
	}
	return balances, nil
}

// financeBizTypeNames 是核销明细的业务类型中文展示（与迁移注释一致）。
var financeBizTypeNames = map[string]string{
	model.FinanceBizSaleOut:        "销售出库",
	model.FinanceBizSaleReturn:     "销售退货",
	model.FinanceBizPurchaseIn:     "采购入库",
	model.FinanceBizPurchaseReturn: "采购退货",
}
