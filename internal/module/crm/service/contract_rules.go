package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/money"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// 合同规则（issue #16，docs/crm-mvp-spec.md §4.4 状态机、§4.3 金额不变量）：
//   - 状态机：1 草稿 → 2 待审（提交）→ 3 已签（审批通过）；待审可驳回回草稿；
//     草稿/待审/已签均可作废（4），作废为终态。状态变更唯一入口是
//     PUT /crm/contract/status（条件更新防并发），非法流转 400；
//   - 金额不变量：有行项目时合同金额恒等于 SUM(行项目金额)，由服务端计算落库
//     （客户端提交的金额忽略——不变量由服务端保证，而非靠客户端自觉一致）；
//     无行项目时手填且 ≥ 0；
//   - 行项目：与商机共享同一套校验（产品必填、数量正整数、单价非负、不重复，
//     金额 = 数量 × 单价由服务端按 money.Mul 计算）。
//
// 纯函数集中在本文件，HTTP/仓储边界之外可直接单测。

// BizTypeContract 是合同编号在 crm_sequence 中的业务类型。
const BizTypeContract = "contract"

// ContractNoPrefix 是合同编号前缀。
const ContractNoPrefix = "HT"

// FormatContractNo 把日期键与序号格式化为合同编号：HT + yyyyMMdd + 至少 4 位序号。
// 序号超过 9999 时自然扩位：唯一性优先于固定位数，绝不截断（与 KH/SJ 同款）。
func FormatContractNo(dateKey string, seq int64) string {
	return ContractNoPrefix + dateKey + fmt.Sprintf("%04d", seq)
}

// ContractStatusNames 是合同状态的中文文案（展示与错误信息共用）。
var ContractStatusNames = map[int8]string{
	model.ContractStatusDraft:   "草稿",
	model.ContractStatusPending: "待审",
	model.ContractStatusSigned:  "已签",
	model.ContractStatusVoid:    "作废",
}

// contractStatusTransitions 是合法流转表（事实来源，CanTransitionContractStatus 依此判定）。
var contractStatusTransitions = map[int8]map[int8]bool{
	// 草稿：提交审批、作废。
	model.ContractStatusDraft: {model.ContractStatusPending: true, model.ContractStatusVoid: true},
	// 待审：审批通过、驳回回草稿、作废。
	model.ContractStatusPending: {model.ContractStatusSigned: true, model.ContractStatusDraft: true, model.ContractStatusVoid: true},
	// 已签：只能作废（生效合同不再回退草稿/重走审批）。
	model.ContractStatusSigned: {model.ContractStatusVoid: true},
	// 作废为终态：无任何出边。
	model.ContractStatusVoid: {},
}

// ValidateContractStatus 校验合同状态枚举，非法值返回用户可读的 400 错误。
func ValidateContractStatus(status int8) error {
	if !model.ValidContractStatus(status) {
		return xerr.BadRequest(fmt.Sprintf("合同状态 %d 不合法：期望 1 草稿 / 2 待审 / 3 已签 / 4 作废", status))
	}
	return nil
}

// CanTransitionContractStatus 校验状态流转合法性：非法枚举与流转表之外的迁移
// 一律 400（含同状态重复提交——合同的动作是显式的提交/审批/驳回/作废，
// 无商机阶段那种「同阶段幂等放行」的语义）。
func CanTransitionContractStatus(from, to int8) error {
	if !model.ValidContractStatus(from) || !model.ValidContractStatus(to) {
		return xerr.BadRequest(fmt.Sprintf("合同状态 %d/%d 不合法：期望 1 草稿 / 2 待审 / 3 已签 / 4 作废", from, to))
	}
	if !contractStatusTransitions[from][to] {
		if from == to {
			return xerr.BadRequest(fmt.Sprintf("合同已是「%s」状态，无需重复操作", ContractStatusNames[from]))
		}
		return xerr.BadRequest(fmt.Sprintf("合同当前为「%s」状态，不能变更为「%s」", ContractStatusNames[from], ContractStatusNames[to]))
	}
	return nil
}

// ResolveContractAmount 落库金额裁决（金额不变量的唯一入口）：
//   - 有行项目（lineAmounts 非空）：金额 = SUM(行项目金额)，handFilled 忽略
//     （服务端计算保证不变量，spec §4.3）；
//   - 无行项目：手填金额生效，负数拒绝（合同金额 ≥ 0）。
//
// 合计越界透传 money 错误（调用方转成用户可读文案）。
func ResolveContractAmount(handFilled money.Money, lineAmounts []money.Money) (money.Money, error) {
	if len(lineAmounts) > 0 {
		total, err := SumLineAmounts(lineAmounts)
		if err != nil {
			// 行项目各自合法但合计越界（decimal(12,2) 上限）：转成用户可读 400，
			// 保留底层 money 错误供 errors.Is 判断。
			return money.Money{}, xerr.Wrap(err, 400, 400, "行项目合计超出金额范围（最大 9999999999.99）")
		}
		return total, nil
	}
	if handFilled.Cents() < 0 {
		return money.Money{}, xerr.BadRequest(fmt.Sprintf("合同金额 %s 不合法：无行项目时金额不能为负数", handFilled.String()))
	}
	return handFilled, nil
}

// ValidateContractLineItems 校验合同行项目：与商机共享同一套规则
// （产品必填、数量正整数、单价非负、同一合同内产品不可重复，空列表放行）。
func ValidateContractLineItems(items []lineItem) error {
	return ValidateLineItems(items)
}

// ParseContractDate 解析合同日期（签订/起止）：空值放行（返回 nil），
// 非法格式返回 400 并带回原始输入（与商机预计成交日期同一约定）。
// 日期文本按 loc（业务时区，ADR 0006）解释，调用方传 Service.businessLocation()。
func ParseContractDate(value string, loc *time.Location) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if t, ok := parseDateIn(crmDateLayouts, value, loc); ok {
		return &t, nil
	}
	return nil, xerr.BadRequest(fmt.Sprintf("合同日期 %q 格式不正确：应为 2026-01-02 形式", value))
}

// ValidateContractDateRange 校验起止日期：缺一放行；起始晚于失效日期拒绝（400），
// 同日放行（当日生效当日失效是合法的短期合同）。
func ValidateContractDateRange(start, end *time.Time) error {
	if start == nil || end == nil {
		return nil
	}
	if start.After(*end) {
		return xerr.BadRequest("生效日期不能晚于失效日期")
	}
	return nil
}
