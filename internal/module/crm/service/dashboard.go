package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 仪表盘（issue #19，docs/crm-mvp-spec.md §5/§6/§8）----------
// 一次返回销售/主管首页需要的全部聚合：4 张卡片 + 商机阶段漏斗 +
// 近 12 个月合同/回款趋势。所有指标套用当前用户的数据范围（spec §4.1）：
// 客户/商机/合同按自身 owner_*，回款计划/记录 join 合同后套合同 owner 谓词；
// 复用 applyCustomerScope（与客户/商机/合同/回款列表同一翻译，ADR 0004）。
//
// 分工：仓储只做范围过滤与分组聚合；月份补齐、逾期判定与金额合计口径
// 由 dashboard_rules.go 的纯函数负责（可单测，不连数据库）。时间统一在
// 应用时区（ADR 0006）下取「今天」与月份边界，SQL 只做日期比较。

// DashboardCards 是仪表盘 4 张卡片。
type DashboardCards struct {
	// CustomerCount 是当前数据范围内可见的客户总数。
	CustomerCount int64 `json:"customerCount"`
	// InProgressOpportunityAmount 是进行中商机（stage 1–4）的预计金额合计。
	InProgressOpportunityAmount money.Money `json:"inProgressOpportunityAmount"`
	// SignedContractAmount 是已签合同（status=3）的金额合计（不限签订日期）。
	SignedContractAmount money.Money `json:"signedContractAmount"`
	// OverdueReceivableCount/Amount 是逾期回款期数与未收金额合计。
	OverdueReceivableCount  int64       `json:"overdueReceivableCount"`
	OverdueReceivableAmount money.Money `json:"overdueReceivableAmount"`
}

// DashboardSummary 是 GET /crm/dashboard/summary 的响应体。
type DashboardSummary struct {
	Cards  DashboardCards        `json:"cards"`
	Funnel []DashboardFunnelItem `json:"funnel"`
	Trend  []DashboardTrendPoint `json:"trend"`
}

// SetLocation 注入业务时区（ADR 0006：唯一来源是 config.Location()，
// 由 internal/app 装配时调用）。nil 忽略，保留当前值。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区：app 装配后是配置的 app.timezone；
// 未注入（如直接构造 Service 的测试）时退回 time.Local，与服务层既有
// 时间口径保持一致。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// businessNow 返回业务时区下的「现在」：编号日期键、派生状态与公海回收的
// 「今天」一律走这里，不直接用 time.Now()（其 Location 是服务器本地时区，
// 与 app.timezone 可能不同，跨日边界会落错日期——ADR 0006，PR #39 审查 P2）。
func (s *Service) businessNow() time.Time {
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	return clock().In(s.businessLocation())
}

// DashboardSummary 汇总当前用户可见范围内的仪表盘指标。
func (s *Service) DashboardSummary(ctx context.Context) (*DashboardSummary, error) {
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	var ownerDeptIDs []int64
	var selfOwnerUserID int64
	applyCustomerScope(scope, &ownerDeptIDs, &selfOwnerUserID)
	// 公海可见性（spec §4.1）：只有持 crm:customer:pool:view 的用户才把
	// 公海客户计入客户总数（super_admin 以 "*" 通配）；无权限者只统计有负责人客户。
	includePool, err := s.sys.UserHasPermission(ctx, CurrentUserID(ctx), PermCustomerPoolView)
	if err != nil {
		return nil, err
	}
	filter := repository.DashboardFilter{
		OwnerDeptIDs:    ownerDeptIDs,
		SelfOwnerUserID: selfOwnerUserID,
		IncludePool:     includePool,
	}

	loc := s.businessLocation()
	now := s.businessNow()

	customerCount, err := s.repo.CountDashboardCustomers(ctx, filter)
	if err != nil {
		return nil, err
	}
	stageRows, err := s.repo.SumDashboardOpportunityAmountsByStage(ctx, filter, OpportunityFunnelStages)
	if err != nil {
		return nil, err
	}
	signedAmount, err := s.repo.SumDashboardSignedContractAmount(ctx, filter)
	if err != nil {
		return nil, err
	}
	dueRows, err := s.repo.ListDashboardDuePlans(ctx, filter, now.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}

	months := DashboardMonths(now, DashboardTrendMonths)
	from := months[0].Start(loc).Format("2006-01-02")
	until := months[len(months)-1].Start(loc).AddDate(0, 1, 0).Format("2006-01-02")
	contractRows, err := s.repo.ListDashboardSignedContractAmounts(ctx, filter, from, until)
	if err != nil {
		return nil, err
	}
	receiptRows, err := s.repo.ListDashboardReceiptAmounts(ctx, filter, from, until)
	if err != nil {
		return nil, err
	}

	funnel := BuildOpportunityFunnel(dashboardStageTotals(stageRows))
	overdueCount, overdueAmount := SummarizeOverduePlans(dashboardDuePlans(dueRows), now, loc)
	return &DashboardSummary{
		Cards: DashboardCards{
			CustomerCount:               customerCount,
			InProgressOpportunityAmount: SumInProgressOpportunityAmount(funnel),
			SignedContractAmount:        signedAmount,
			OverdueReceivableCount:      overdueCount,
			OverdueReceivableAmount:     overdueAmount,
		},
		Funnel: funnel,
		Trend:  BuildDashboardTrend(months, dashboardDatedAmounts(contractRows), dashboardDatedAmounts(receiptRows), loc),
	}, nil
}

// dashboardStageTotals 把仓储聚合行转成纯函数输入（隔离 DAL 类型）。
func dashboardStageTotals(rows []repository.DashboardStageRow) []DashboardStageTotal {
	out := make([]DashboardStageTotal, 0, len(rows))
	for _, row := range rows {
		out = append(out, DashboardStageTotal{Stage: row.Stage, Count: row.Count, Amount: row.Amount})
	}
	return out
}

// dashboardDatedAmounts 把仓储日期金额行转成纯函数输入。
func dashboardDatedAmounts(rows []repository.DashboardDatedRow) []DashboardDatedAmount {
	out := make([]DashboardDatedAmount, 0, len(rows))
	for _, row := range rows {
		out = append(out, DashboardDatedAmount{Date: row.Date, Amount: row.Amount})
	}
	return out
}

// dashboardDuePlans 把仓储到期计划行转成纯函数输入（逾期判定在纯函数里复核）。
func dashboardDuePlans(rows []repository.DashboardDuePlanRow) []DashboardDuePlan {
	out := make([]DashboardDuePlan, 0, len(rows))
	for _, row := range rows {
		out = append(out, DashboardDuePlan{
			Amount:         row.Amount,
			ReceivedAmount: row.ReceivedAmount,
			DueDate:        row.DueDate,
		})
	}
	return out
}
