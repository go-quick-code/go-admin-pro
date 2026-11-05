package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 仪表盘纯规则（issue #19，docs/crm-mvp-spec.md §4.1/§6/§8）----------
// 本文件只做与存储无关的派生计算，HTTP/仓储边界之外可直接单测：
//   - 近 12 个月序列（含本月）与月份归集：日期先转换到应用时区（ADR 0006），
//     再按日历月归月，空月补 0.00；窗口外的数据丢弃；
//   - 逾期判定（spec §3/§4.1）：due_date 严格早于今天且已收 < 计划额，才算逾期；
//     逾期未收金额 = 计划额 - 已收（已收齐/超额不再标逾期）；
//   - 商机漏斗固定 1–6 档补零；「进行中商机金额」只计 stage 1–4（5/6 是终态，
//     spec §4.4）。
//
// 金额累加以分为单位做整数加法后收口为 money.Money（ADR 0007：不用浮点）。

// DashboardTrendMonths 是趋势图覆盖的月数（含本月）。
const DashboardTrendMonths = 12

// DashboardMonth 是一个自然月（年 + 月），Key 形如 2026-11。
type DashboardMonth struct {
	Year  int
	Month time.Month
}

// Key 返回 yyyy-MM 形式的月份键（前端图表轴与归集键共用）。
func (m DashboardMonth) Key() string {
	return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month))
}

// Start 返回该月在 loc 时区的零点（loc 为空时用 time.Local）。
func (m DashboardMonth) Start(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	return time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, loc)
}

// DashboardMonths 返回含 now 所在月在内的最近 count 个自然月，按时间升序。
// now 必须已转换到应用时区（ADR 0006）：本函数按 now 自身的 Location 取年月。
func DashboardMonths(now time.Time, count int) []DashboardMonth {
	if count <= 0 {
		return nil
	}
	year, month, _ := now.Date()
	first := time.Date(year, month, 1, 0, 0, 0, 0, now.Location()).
		AddDate(0, -(count - 1), 0)
	months := make([]DashboardMonth, 0, count)
	for i := 0; i < count; i++ {
		t := first.AddDate(0, i, 0)
		months = append(months, DashboardMonth{Year: t.Year(), Month: t.Month()})
	}
	return months
}

// DashboardStageTotal 是仓储按阶段聚合后的原始行。
type DashboardStageTotal struct {
	Stage  int8
	Count  int64
	Amount money.Money
}

// DashboardFunnelItem 是漏斗图的一项（响应元素：stage + 名称 + 数量 + 金额）。
type DashboardFunnelItem struct {
	Stage     int8        `json:"stage"`
	StageName string      `json:"stageName"`
	Count     int64       `json:"count"`
	Amount    money.Money `json:"amount"`
}

// OpportunityFunnelStages 是漏斗固定阶段 1–6（spec §3）。
var OpportunityFunnelStages = []int8{
	model.OpportunityStageInitial,
	model.OpportunityStageDemand,
	model.OpportunityStageQuote,
	model.OpportunityStageNegotiate,
	model.OpportunityStageWon,
	model.OpportunityStageLost,
}

// BuildOpportunityFunnel 把按阶段聚合的行补齐为固定 6 档（无数据补 0），
// 按阶段升序返回；阶段名统一取 OpportunityStageNames。非法阶段行忽略。
func BuildOpportunityFunnel(rows []DashboardStageTotal) []DashboardFunnelItem {
	byStage := make(map[int8]DashboardStageTotal, len(rows))
	for _, row := range rows {
		if model.ValidOpportunityStage(row.Stage) {
			byStage[row.Stage] = row
		}
	}
	items := make([]DashboardFunnelItem, 0, len(OpportunityFunnelStages))
	for _, stage := range OpportunityFunnelStages {
		total := byStage[stage]
		items = append(items, DashboardFunnelItem{
			Stage:     stage,
			StageName: OpportunityStageNames[stage],
			Count:     total.Count,
			Amount:    total.Amount,
		})
	}
	return items
}

// SumInProgressOpportunityAmount 汇总漏斗中 stage 1–4 的金额：
// 赢单/输单是终态，不计入「进行中商机金额」（spec §4.4）。
func SumInProgressOpportunityAmount(items []DashboardFunnelItem) money.Money {
	var cents int64
	for _, item := range items {
		if item.Stage >= model.OpportunityStageInitial && item.Stage <= model.OpportunityStageNegotiate {
			cents += item.Amount.Cents()
		}
	}
	return money.FromCents(cents)
}

// DashboardDatedAmount 是带日期的金额行（合同签订 / 回款到账）。
type DashboardDatedAmount struct {
	Date   time.Time
	Amount money.Money
}

// DashboardTrendPoint 是趋势图的一个月（响应元素）。
type DashboardTrendPoint struct {
	Month          string      `json:"month"`
	ContractAmount money.Money `json:"contractAmount"`
	ReceiptAmount  money.Money `json:"receiptAmount"`
}

// BuildDashboardTrend 把合同签订金额与回款金额按自然月（应用时区）归集到
// months 覆盖的月份，缺失月份补 0.00，按 months 顺序返回；窗口外数据丢弃。
func BuildDashboardTrend(months []DashboardMonth, contracts, receipts []DashboardDatedAmount, loc *time.Location) []DashboardTrendPoint {
	if loc == nil {
		loc = time.Local
	}
	points := make([]DashboardTrendPoint, len(months))
	index := make(map[string]int, len(months))
	for i, month := range months {
		points[i] = DashboardTrendPoint{Month: month.Key()}
		index[month.Key()] = i
	}
	contractCents := make([]int64, len(months))
	receiptCents := make([]int64, len(months))
	for _, row := range contracts {
		if i, ok := index[dashboardMonthKey(row.Date, loc)]; ok {
			contractCents[i] += row.Amount.Cents()
		}
	}
	for _, row := range receipts {
		if i, ok := index[dashboardMonthKey(row.Date, loc)]; ok {
			receiptCents[i] += row.Amount.Cents()
		}
	}
	for i := range points {
		points[i].ContractAmount = money.FromCents(contractCents[i])
		points[i].ReceiptAmount = money.FromCents(receiptCents[i])
	}
	return points
}

// dashboardMonthKey 把时间转换到应用时区后取 yyyy-MM。
func dashboardMonthKey(t time.Time, loc *time.Location) string {
	local := t.In(loc)
	return fmt.Sprintf("%04d-%02d", local.Year(), int(local.Month()))
}

// DashboardDuePlan 是逾期统计所需的计划快照（只读派生，不落库）。
type DashboardDuePlan struct {
	Amount         money.Money
	ReceivedAmount money.Money
	DueDate        time.Time
}

// SummarizeOverduePlans 按 spec 判定逾期并汇总：到期日严格早于今天（按应用
// 时区取日历日，到期日当天不算）且已收 < 计划额，返回逾期期数与未收金额合计。
func SummarizeOverduePlans(plans []DashboardDuePlan, today time.Time, loc *time.Location) (int64, money.Money) {
	if loc == nil {
		loc = today.Location()
	}
	current := dateOnlyIn(today, loc)
	var count int64
	var cents int64
	for _, plan := range plans {
		if plan.ReceivedAmount.Cents() >= plan.Amount.Cents() {
			continue
		}
		if !dateOnlyIn(plan.DueDate, loc).Before(current) {
			continue
		}
		count++
		cents += plan.Amount.Cents() - plan.ReceivedAmount.Cents()
	}
	return count, money.FromCents(cents)
}
