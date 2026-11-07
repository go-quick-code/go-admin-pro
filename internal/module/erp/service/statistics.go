// statistics.go 是 ERP 首页统计的业务层（issue #95，docs/erp-mvp-spec.md
// §5/§8.2）：销售/采购 × summary/time-summary 四个只读接口，零迁移。
//
// 口径严格照 spec §5：
//   - 销售额 = Σ已审核销售出库 total_price（out_time 落区间）
//     − Σ已审核销售退货 total_price（return_time 落区间）；
//   - 采购额 = Σ已审核采购入库（in_time）− Σ已审核采购退货（return_time）。
//
// 数据范围与各单据列表同一套翻译（applyScope + 仓储 applyOwnerScope，
// spec §4.10）；租户隔离由租户插件保证。时间窗由 statistics_rules.go 的纯
// 函数构造（业务时区，ADR 0006），无数据返回 0.00 不报错。
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// statisticsSums 是一对金额聚合函数（出库/入库 与 退货），
// 让 summary/time-summary 复用同一段净额计算。
type statisticsSums struct {
	gross   func(context.Context, repository.StatisticsFilter, time.Time, time.Time) (money.Money, error)
	returns func(context.Context, repository.StatisticsFilter, time.Time, time.Time) (money.Money, error)
}

// SaleStatisticsSummary 销售汇总：今日/昨日/本月/今年净额。
func (s *Service) SaleStatisticsSummary(ctx context.Context) (*dto.StatisticsSummaryRow, error) {
	return s.statisticsSummary(ctx, statisticsSums{
		gross:   s.repo.SumApprovedSaleOutRange,
		returns: s.repo.SumApprovedSaleReturnRange,
	})
}

// SaleStatisticsTimeSummary 销售近 N 个月（默认 6）按月净额。
func (s *Service) SaleStatisticsTimeSummary(ctx context.Context, req *dto.StatisticsTimeSummaryReq) (*dto.StatisticsTimeSummaryRow, error) {
	return s.statisticsTimeSummary(ctx, statisticsSums{
		gross:   s.repo.SumApprovedSaleOutRange,
		returns: s.repo.SumApprovedSaleReturnRange,
	}, req.Months)
}

// PurchaseStatisticsSummary 采购汇总：今日/昨日/本月/今年净额。
func (s *Service) PurchaseStatisticsSummary(ctx context.Context) (*dto.StatisticsSummaryRow, error) {
	return s.statisticsSummary(ctx, statisticsSums{
		gross:   s.repo.SumApprovedPurchaseInRange,
		returns: s.repo.SumApprovedPurchaseReturnRange,
	})
}

// PurchaseStatisticsTimeSummary 采购近 N 个月（默认 6）按月净额。
func (s *Service) PurchaseStatisticsTimeSummary(ctx context.Context, req *dto.StatisticsTimeSummaryReq) (*dto.StatisticsTimeSummaryRow, error) {
	return s.statisticsTimeSummary(ctx, statisticsSums{
		gross:   s.repo.SumApprovedPurchaseInRange,
		returns: s.repo.SumApprovedPurchaseReturnRange,
	}, req.Months)
}

// statisticsFilter 解析当前用户的数据范围谓词（与列表同一翻译，spec §4.10）。
func (s *Service) statisticsFilter(ctx context.Context) (repository.StatisticsFilter, error) {
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.StatisticsFilter{}, err
	}
	var ownerDeptIDs []int64
	var selfOwnerUserID int64
	applyScope(scope, &ownerDeptIDs, &selfOwnerUserID)
	return repository.StatisticsFilter{
		OwnerDeptIDs:    ownerDeptIDs,
		SelfOwnerUserID: selfOwnerUserID,
	}, nil
}

// statisticsNet 计算单个区间的净额（gross − returns）。
func (s *Service) statisticsNet(ctx context.Context, sums statisticsSums, f repository.StatisticsFilter, period StatisticsPeriod) (money.Money, error) {
	gross, err := sums.gross(ctx, f, period.Start, period.End)
	if err != nil {
		return money.Money{}, err
	}
	returns, err := sums.returns(ctx, f, period.Start, period.End)
	if err != nil {
		return money.Money{}, err
	}
	return NetStatisticsAmount(gross, returns)
}

// statisticsSummary 汇总四个时间窗的净额。
func (s *Service) statisticsSummary(ctx context.Context, sums statisticsSums) (*dto.StatisticsSummaryRow, error) {
	f, err := s.statisticsFilter(ctx)
	if err != nil {
		return nil, err
	}
	windows := BuildStatisticsWindows(s.businessNow())
	row := &dto.StatisticsSummaryRow{}
	if row.Today, err = s.statisticsNet(ctx, sums, f, windows.Today); err != nil {
		return nil, err
	}
	if row.Yesterday, err = s.statisticsNet(ctx, sums, f, windows.Yesterday); err != nil {
		return nil, err
	}
	if row.Month, err = s.statisticsNet(ctx, sums, f, windows.Month); err != nil {
		return nil, err
	}
	if row.Year, err = s.statisticsNet(ctx, sums, f, windows.Year); err != nil {
		return nil, err
	}
	return row, nil
}

// statisticsTimeSummary 汇总近 N 个自然月的净额（升序、空月补 0.00）。
func (s *Service) statisticsTimeSummary(ctx context.Context, sums statisticsSums, months int) (*dto.StatisticsTimeSummaryRow, error) {
	if months == 0 {
		months = StatisticsDefaultMonths
	}
	if months < 1 || months > StatisticsMaxMonths {
		return nil, xerr.BadRequest(fmt.Sprintf("months 必须在 1–%d 之间", StatisticsMaxMonths))
	}
	f, err := s.statisticsFilter(ctx)
	if err != nil {
		return nil, err
	}
	periods := BuildStatisticsMonthPeriods(s.businessNow(), months)
	list := make([]dto.StatisticsTrendPoint, 0, len(periods))
	for _, period := range periods {
		amount, err := s.statisticsNet(ctx, sums, f, StatisticsPeriod{Start: period.Start, End: period.End})
		if err != nil {
			return nil, err
		}
		list = append(list, dto.StatisticsTrendPoint{Month: period.Month.Key(), Amount: amount})
	}
	return &dto.StatisticsTimeSummaryRow{Months: months, List: list}, nil
}
