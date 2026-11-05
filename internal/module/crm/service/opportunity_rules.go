package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 商机规则（issue #15，docs/crm-mvp-spec.md §4.4 状态机、§4.3 金额口径）：
//   - 阶段 1–4 为进行中，可互相推进（含回退）；5 赢单、6 输单为终态，
//     进入后不可再变更阶段（编辑其他字段不受限，口径见 service.Opportunity）；
//   - 行项目：产品必填（租户内存在）、数量正整数、单价非负、金额 = 数量 × 单价
//     由服务端计算（客户端不提交金额），同一商机内产品不可重复；
//     无行项目允许（预计金额手填，规格 §4.3 的「金额=SUM(行项目)」只约束合同）。
//
// 纯函数集中在本文件，HTTP/仓储边界之外可直接单测。

// OpportunityStageNames 是阶段的中文文案（展示与错误信息共用）。
var OpportunityStageNames = map[int8]string{
	model.OpportunityStageInitial:   "初步",
	model.OpportunityStageDemand:    "需求",
	model.OpportunityStageQuote:     "报价",
	model.OpportunityStageNegotiate: "谈判",
	model.OpportunityStageWon:       "赢单",
	model.OpportunityStageLost:      "输单",
}

// CanAdvanceStage 校验阶段推进合法性：同阶段视为无操作放行（幂等）；
// 终态（赢单/输单）拒绝一切变更；非法枚举拒绝。违反时返回用户可读 400。
func CanAdvanceStage(from, to int8) error {
	if !model.ValidOpportunityStage(from) || !model.ValidOpportunityStage(to) {
		return xerr.BadRequest(fmt.Sprintf("商机阶段 %d/%d 不合法：期望 1 初步 / 2 需求 / 3 报价 / 4 谈判 / 5 赢单 / 6 输单", from, to))
	}
	if from == to {
		return nil
	}
	if model.IsTerminalOpportunityStage(from) {
		return xerr.BadRequest(fmt.Sprintf("商机已%s（终态），不可再变更阶段", OpportunityStageNames[from]))
	}
	return nil
}

// ValidateOpportunityStage 校验阶段枚举，非法值返回用户可读的 400 错误。
func ValidateOpportunityStage(stage int8) error {
	if !model.ValidOpportunityStage(stage) {
		return xerr.BadRequest(fmt.Sprintf("商机阶段 %d 不合法：期望 1 初步 / 2 需求 / 3 报价 / 4 谈判 / 5 赢单 / 6 输单", stage))
	}
	return nil
}

// lineItem 是行项目校验与计算的纯数据载体（service 内部用，不外泄到 dto/model）。
type lineItem struct {
	ProductID int64
	Quantity  int64
	Price     money.Money
}

// ValidateLineItems 校验行项目列表：产品必填、数量正整数、单价非负、
// 同一单据内产品不可重复（关联表复合主键去重的前提）。空列表放行。
// 错误文案带 1 起始的行序号，便于前端定位。
// 商机（#15）与合同（#16）行项目共用同一套规则，文案统一说「行项目」。
func ValidateLineItems(items []lineItem) error {
	seen := make(map[int64]bool, len(items))
	for i, it := range items {
		lineNo := i + 1
		if it.ProductID <= 0 {
			return xerr.BadRequest(fmt.Sprintf("第 %d 行项目缺少产品", lineNo))
		}
		if it.Quantity <= 0 {
			return xerr.BadRequest(fmt.Sprintf("第 %d 行项目数量 %d 不合法：数量必须为正整数", lineNo, it.Quantity))
		}
		if it.Price.Cents() < 0 {
			return xerr.BadRequest(fmt.Sprintf("第 %d 行项目单价不能为负数", lineNo))
		}
		if seen[it.ProductID] {
			return xerr.BadRequest(fmt.Sprintf("第 %d 行项目的产品与前面的行重复：同一单据内同一产品只保留一行", lineNo))
		}
		seen[it.ProductID] = true
	}
	return nil
}

// ComputeLineAmount 计算行项目金额 = 数量 × 单价（money.Mul，128 位中间结果防溢出）；
// 越界透传 money.ErrOutOfRange（调用方转成用户可读文案）。
func ComputeLineAmount(quantity int64, price money.Money) (money.Money, error) {
	return price.Mul(quantity)
}

// SumLineAmounts 求行项目合计；空列表合计 0.00。
func SumLineAmounts(amounts []money.Money) (money.Money, error) {
	total := money.Money{}
	for _, a := range amounts {
		var err error
		if total, err = total.Add(a); err != nil {
			return money.Money{}, err
		}
	}
	return total, nil
}

// ParseExpectedCloseDate 解析预计成交日期：空值放行（返回 nil），
// 非法格式返回 400 并带回原始输入（与跟进 nextFollowTime 同一约定）。
// 日期文本按 loc（业务时区，ADR 0006）解释，调用方传 Service.businessLocation()。
func ParseExpectedCloseDate(value string, loc *time.Location) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if t, ok := parseDateIn(crmDateLayouts, value, loc); ok {
		return &t, nil
	}
	return nil, xerr.BadRequest(fmt.Sprintf("预计成交日期 %q 格式不正确：应为 2026-01-02 形式", value))
}
