package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 回款规则（issue #18，docs/crm-mvp-spec.md §3 回款节/§4.3 金额不变量）：
//   - 计划不存状态与已收金额，派生状态优先级：已回款（已收 ≥ 计划额）→ 逾期
//     （due_date < 今天，严格早于今天）→ 部分回款（已收 > 0）→ 待回款；
//   - 金额不变量：SUM(计划金额) ≤ 合同金额；SUM(记录金额) ≤ 计划金额。
//     违反返回用户可读 400，绝不静默截断；
//   - 记录编号 HK + yyyyMMdd + 至少 4 位序号（超 9999 自然扩位，绝不截断）。
//
// 纯函数集中在本文件，HTTP/仓储边界之外可直接单测。

// BizTypeReceivableRecord 是回款记录编号在 crm_sequence 中的业务类型。
const BizTypeReceivableRecord = "receivable_record"

// ReceivableRecordNoPrefix 是回款记录编号前缀。
const ReceivableRecordNoPrefix = "HK"

// FormatReceivableRecordNo 把日期键与序号格式化为回款记录编号：
// HK + yyyyMMdd + 至少 4 位序号。序号超过 9999 时自然扩位：唯一性优先于
// 固定位数，绝不截断（与 KH/SJ/HT 同款）。
func FormatReceivableRecordNo(dateKey string, seq int64) string {
	return ReceivableRecordNoPrefix + dateKey + fmt.Sprintf("%04d", seq)
}

// ReceivableStatusNames 是回款计划派生状态的中文文案（展示与错误信息共用）。
var ReceivableStatusNames = map[int8]string{
	model.ReceivableStatusPending:  "待回款",
	model.ReceivableStatusPartial:  "部分回款",
	model.ReceivableStatusReceived: "已回款",
	model.ReceivableStatusOverdue:  "逾期",
}

// DeriveReceivableStatus 派生回款计划状态（spec §3：状态不落库，读时计算）。
// 判定优先级：已回款 → 逾期 → 部分回款 → 待回款；返回的 overdue 与状态联动，
// 已回款即便过了到期日也不标逾期（钱已收齐）。时间比较按 today 的 Location
// 取日历日（应用时区，ADR 0006），到期日当天不算逾期。
func DeriveReceivableStatus(planAmount, receivedAmount money.Money, dueDate, today time.Time) (status int8, overdue bool) {
	if receivedAmount.Cents() >= planAmount.Cents() {
		return model.ReceivableStatusReceived, false
	}
	location := today.Location()
	due := dateOnlyIn(dueDate, location)
	current := dateOnlyIn(today, location)
	if due.Before(current) {
		return model.ReceivableStatusOverdue, true
	}
	if receivedAmount.Cents() > 0 {
		return model.ReceivableStatusPartial, false
	}
	return model.ReceivableStatusPending, false
}

// dateOnlyIn 把时间转换为 loc 时区下的日历日零点（到期日与「今天」同一口径比较）。
func dateOnlyIn(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// ValidateReceivableStatusFilter 校验列表的派生状态筛选参数，非法值 400
// （绝不静默当成「无此筛选」）。
func ValidateReceivableStatusFilter(status int8) error {
	if !model.ValidReceivableStatus(status) {
		return xerr.BadRequest(fmt.Sprintf(
			"回款状态 %d 不合法：期望 1 待回款 / 2 部分回款 / 3 已回款 / 4 逾期", status))
	}
	return nil
}

// ValidateReceivablePeriodNo 校验期次为正整数（计划不单独编号，期次标识合同内第几期）。
func ValidateReceivablePeriodNo(periodNo int) error {
	if periodNo <= 0 {
		return xerr.BadRequest(fmt.Sprintf("期次 %d 不合法：应为正整数（第几期回款）", periodNo))
	}
	return nil
}

// ValidateReceivablePlanAmount 校验计划金额不变量（spec §4.3）：
// 金额非负；otherPlanTotal + amount ≤ 合同金额（otherPlanTotal 是该合同其余
// 计划的合计，更新时排除自身，由调用方提供）。违反返回用户可读 400。
func ValidateReceivablePlanAmount(amount, contractAmount, otherPlanTotal money.Money) error {
	if amount.Cents() < 0 {
		return xerr.BadRequest(fmt.Sprintf("回款计划金额 %s 不合法：金额不能为负数", amount.String()))
	}
	total, err := otherPlanTotal.Add(amount)
	if err != nil {
		return xerr.Wrap(err, 400, 400, "回款计划合计超出金额范围（最大 9999999999.99）")
	}
	if total.Cents() > contractAmount.Cents() {
		return xerr.BadRequest(fmt.Sprintf(
			"回款计划合计 %s 超出合同金额 %s：本期 %s 与其他计划 %s 之和不得超过合同金额",
			total.String(), contractAmount.String(), amount.String(), otherPlanTotal.String()))
	}
	return nil
}

// ValidateReceivableRecordAmount 校验回款记录金额不变量（spec §4.3）：
// 金额非负；otherRecordTotal + amount ≤ 计划金额（otherRecordTotal 是该计划
// 其余记录的合计）。违反返回用户可读 400，不静默截断。
func ValidateReceivableRecordAmount(amount, planAmount, otherRecordTotal money.Money) error {
	if amount.Cents() < 0 {
		return xerr.BadRequest(fmt.Sprintf("回款金额 %s 不合法：金额不能为负数", amount.String()))
	}
	total, err := otherRecordTotal.Add(amount)
	if err != nil {
		return xerr.Wrap(err, 400, 400, "回款记录合计超出金额范围（最大 9999999999.99）")
	}
	if total.Cents() > planAmount.Cents() {
		return xerr.BadRequest(fmt.Sprintf(
			"回款记录合计 %s 超出计划金额 %s：本次 %s 与该计划已有回款 %s 之和不得超过计划金额",
			total.String(), planAmount.String(), amount.String(), otherRecordTotal.String()))
	}
	return nil
}

// ParseReceivableDate 解析回款日期（到期日/到账日期）：空值放行（返回 nil），
// 非法格式返回 400 并带回原始输入与字段名（与合同/商机日期同一约定）。
// 日期文本按 loc（业务时区，ADR 0006）解释，调用方传 Service.businessLocation()。
func ParseReceivableDate(field, value string, loc *time.Location) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if t, ok := parseDateIn(crmDateLayouts, value, loc); ok {
		return &t, nil
	}
	return nil, xerr.BadRequest(fmt.Sprintf("%s %q 格式不正确：应为 2026-01-02 形式", field, value))
}
