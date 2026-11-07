package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"gorm.io/gorm"
)

// statistics.go 是 ERP 首页统计的只读聚合（issue #95，docs/erp-mvp-spec.md
// §5/§8.2）：零迁移、不新增表。
//
// 口径：Σ已审核单据 total_price 按业务时间列落左闭右开区间 [from, until)；
// 退货按自身的 owner/时间列独立过滤（与列表同口径，spec §4.10）。软删行
// 由 GORM 自动追加 deleted_at IS NULL；租户条件由租户插件在主模型上追加
//（不 JOIN，无插件漏追加问题；聚合统一用 Find 走 query 回调）。
//
// 无数据时 SUM 为 NULL，COALESCE 收口为 0.00，不报错（空数据零值）。
// 数据范围谓词复用 scope.go 的 applyOwnerScope（与各单据列表同一翻译）。

// StatisticsFilter 是统计接口的数据范围谓词，语义与各单据 Filter 一致：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed）。
type StatisticsFilter struct {
	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// sumApprovedDocRange 汇总某张单据主表在 [from, until) 内已审核单据的金额。
func sumApprovedDocRange(ctx context.Context, db *gorm.DB, table any, timeColumn string, f StatisticsFilter, from, until time.Time) (money.Money, error) {
	var row struct {
		Total money.Money `gorm:"column:total"`
	}
	q := db.WithContext(ctx).Model(table).
		Select("COALESCE(SUM(total_price), 0) AS total").
		Where("status = ?", model.DocStatusApproved).
		Where(timeColumn+" >= ? AND "+timeColumn+" < ?", from, until)
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	if err := q.Find(&row).Error; err != nil {
		return money.Money{}, err
	}
	return row.Total, nil
}

// SumApprovedSaleOutRange 已审核销售出库金额（按 out_time 落区间）。
func (r *Repository) SumApprovedSaleOutRange(ctx context.Context, f StatisticsFilter, from, until time.Time) (money.Money, error) {
	return sumApprovedDocRange(ctx, r.db, &model.SaleOut{}, "out_time", f, from, until)
}

// SumApprovedSaleReturnRange 已审核销售退货金额（按 return_time 落区间）。
func (r *Repository) SumApprovedSaleReturnRange(ctx context.Context, f StatisticsFilter, from, until time.Time) (money.Money, error) {
	return sumApprovedDocRange(ctx, r.db, &model.SaleReturn{}, "return_time", f, from, until)
}

// SumApprovedPurchaseInRange 已审核采购入库金额（按 in_time 落区间）。
func (r *Repository) SumApprovedPurchaseInRange(ctx context.Context, f StatisticsFilter, from, until time.Time) (money.Money, error) {
	return sumApprovedDocRange(ctx, r.db, &model.PurchaseIn{}, "in_time", f, from, until)
}

// SumApprovedPurchaseReturnRange 已审核采购退货金额（按 return_time 落区间）。
func (r *Repository) SumApprovedPurchaseReturnRange(ctx context.Context, f StatisticsFilter, from, until time.Time) (money.Money, error) {
	return sumApprovedDocRange(ctx, r.db, &model.PurchaseReturn{}, "return_time", f, from, until)
}
