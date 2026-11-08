package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// sale_stats.go 是销售链路的占用统计与进度回写数据访问（issue #92，
// docs/erp-mvp-spec.md §4.5 镜像 §4.4）。
//
// 占用校验口径（超量 409，未审核也占用，防超卖）：
//   - 订单剩余可出（按产品聚合）= 订单数量 −（未审核+已审核出库）−
//     （未审核+已审核退货，经出库单归属到订单）；
//   - 出库剩余可退（按产品聚合）= 出库数量 −（未审核+已审核退货）。
//
// 聚合查询跨明细/主表两表，租户插件只覆盖主模型表，因此全部走显式租户参数的
// 裸 SQL（与库存入账原语/序列发放同口径）；软删行（deleted_at IS NOT NULL）
// 一律不计——删除未审核单即释放占用。
//
// 进度回写（out_count/return_count）只统计已审核单（spec §4.5），审批/反审核
// 在 service 层重算（按订单×产品聚合后依订单明细 ID 序填充，见 service/sale.go）。

// ProductCountSum 是一条按产品聚合的数量和（占用/进度共用）。
type ProductCountSum struct {
	ProductID int64
	Total     qty.Qty
}

// countRows 通用引用计数（软删行不算引用；租户条件由插件注入）。
func countRows(ctx context.Context, db *gorm.DB, table any, cond string, args ...any) (int64, error) {
	var n int64
	if err := db.WithContext(ctx).Model(table).Where(cond, args...).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CountSaleOrderItemsByProduct 产品删除引用计数的一部分：销售订单明细引用该
// 产品的行数（软删行不算引用，spec §4.9）。
func (r *Repository) CountSaleOrderItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countRows(ctx, r.db, &model.SaleOrderItem{}, "product_id = ?", productID)
}

// CountSaleOutItemsByProduct 产品删除引用计数的一部分：销售出库明细引用行数。
func (r *Repository) CountSaleOutItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countRows(ctx, r.db, &model.SaleOutItem{}, "product_id = ?", productID)
}

// CountSaleReturnItemsByProduct 产品删除引用计数的一部分：销售退货明细引用行数。
func (r *Repository) CountSaleReturnItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countRows(ctx, r.db, &model.SaleReturnItem{}, "product_id = ?", productID)
}

// ---------- 占用聚合（未审核 + 已审核，anyStatus；软删行不计） ----------

// FindCustomerNamesIn 返回租户内 ERP 客户 ID→名称映射（未删除行）。
func (r *Repository) FindCustomerNamesIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNamesIn(ctx, r.db, &model.Customer{}, ids)
}

// FindSaleOrderNosIn 返回租户内销售订单 ID→订单号映射（未删除行，出库/退货
// 单列表挂单列展示用）。
func (r *Repository) FindSaleOrderNosIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID      int64
		OrderNo string
	}
	if err := r.db.WithContext(ctx).Model(&model.SaleOrder{}).
		Select("id, order_no").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.OrderNo
	}
	return out, nil
}

// FindSaleOutNosIn 返回租户内销售出库单 ID→单号映射（退货单列表挂单列展示用）。
func (r *Repository) FindSaleOutNosIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID int64
		No string
	}
	if err := r.db.WithContext(ctx).Model(&model.SaleOut{}).
		Select("id, no").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.No
	}
	return out, nil
}

// SumSaleOutCountByOrder 订单×产品维度的出库数量聚合（含未审核，即占用）；
// excludeOutID > 0 时排除该出库单（编辑自检不重复计自己）。
func (r *Repository) SumSaleOutCountByOrder(ctx context.Context, tenantID, orderID, excludeOutID int64) ([]ProductCountSum, error) {
	exclude := ""
	args := []any{tenantID, orderID}
	if excludeOutID > 0 {
		exclude = " AND d.`id` <> ?"
		args = append(args, excludeOutID)
	}
	return sumProductCounts(ctx, r.db,
		"SELECT i.`product_id` AS product_id, SUM(i.`count`) AS total "+
			"FROM `erp_sale_out_items` i "+
			"JOIN `erp_sale_out` d ON i.`sale_out_id` = d.`id` AND i.`tenant_id` = d.`tenant_id` "+
			"WHERE d.`tenant_id` = ? AND d.`order_id` = ?"+exclude+
			" AND d.`deleted_at` IS NULL AND i.`deleted_at` IS NULL "+
			"GROUP BY i.`product_id`", args)
}

// SumSaleReturnCountByOrder 订单×产品维度的退货数量聚合（退货经挂单出库单归属
// 到订单；含未审核，即占用）。
func (r *Repository) SumSaleReturnCountByOrder(ctx context.Context, tenantID, orderID int64) ([]ProductCountSum, error) {
	return sumProductCounts(ctx, r.db,
		"SELECT i.`product_id` AS product_id, SUM(i.`count`) AS total "+
			"FROM `erp_sale_return_items` i "+
			"JOIN `erp_sale_return` d ON i.`sale_return_id` = d.`id` AND i.`tenant_id` = d.`tenant_id` "+
			"JOIN `erp_sale_out` o ON d.`out_id` = o.`id` "+
			"WHERE o.`tenant_id` = ? AND o.`order_id` = ? "+
			"AND o.`deleted_at` IS NULL AND d.`deleted_at` IS NULL AND i.`deleted_at` IS NULL "+
			"GROUP BY i.`product_id`", []any{tenantID, orderID})
}

// SumSaleReturnCountByOut 出库×产品维度的退货数量聚合（含未审核，即占用）；
// excludeReturnID > 0 时排除该退货单（编辑自检不重复计自己）。
func (r *Repository) SumSaleReturnCountByOut(ctx context.Context, tenantID, outID, excludeReturnID int64) ([]ProductCountSum, error) {
	exclude := ""
	args := []any{tenantID, outID}
	if excludeReturnID > 0 {
		exclude = " AND d.`id` <> ?"
		args = append(args, excludeReturnID)
	}
	return sumProductCounts(ctx, r.db,
		"SELECT i.`product_id` AS product_id, SUM(i.`count`) AS total "+
			"FROM `erp_sale_return_items` i "+
			"JOIN `erp_sale_return` d ON i.`sale_return_id` = d.`id` AND i.`tenant_id` = d.`tenant_id` "+
			"WHERE d.`tenant_id` = ? AND d.`out_id` = ?"+exclude+
			" AND d.`deleted_at` IS NULL AND i.`deleted_at` IS NULL "+
			"GROUP BY i.`product_id`", args)
}

// sumProductCounts 执行按产品聚合的数量和查询（total 为 NULL 即无行，不返回）。
func sumProductCounts(ctx context.Context, db *gorm.DB, sql string, args []any) ([]ProductCountSum, error) {
	var rows []ProductCountSum
	if err := db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ---------- 进度回写（只计已审核，spec §4.5） ----------

// SumApprovedSaleOutCountByOrder 已审核出库的订单×产品数量聚合（进度 out_count）。
func (r *Repository) SumApprovedSaleOutCountByOrder(ctx context.Context, tenantID, orderID int64) ([]ProductCountSum, error) {
	return sumProductCounts(ctx, r.db,
		"SELECT i.`product_id` AS product_id, SUM(i.`count`) AS total "+
			"FROM `erp_sale_out_items` i "+
			"JOIN `erp_sale_out` d ON i.`sale_out_id` = d.`id` AND i.`tenant_id` = d.`tenant_id` "+
			"WHERE d.`tenant_id` = ? AND d.`order_id` = ? AND d.`status` = ? "+
			"AND d.`deleted_at` IS NULL AND i.`deleted_at` IS NULL "+
			"GROUP BY i.`product_id`", []any{tenantID, orderID, model.DocStatusApproved})
}

// SumApprovedSaleReturnCountByOrder 已审核退货的订单×产品数量聚合（进度
// return_count；退货经挂单出库单归属到订单）。
func (r *Repository) SumApprovedSaleReturnCountByOrder(ctx context.Context, tenantID, orderID int64) ([]ProductCountSum, error) {
	return sumProductCounts(ctx, r.db,
		"SELECT i.`product_id` AS product_id, SUM(i.`count`) AS total "+
			"FROM `erp_sale_return_items` i "+
			"JOIN `erp_sale_return` d ON i.`sale_return_id` = d.`id` AND i.`tenant_id` = d.`tenant_id` "+
			"JOIN `erp_sale_out` o ON d.`out_id` = o.`id` "+
			"WHERE o.`tenant_id` = ? AND o.`order_id` = ? AND d.`status` = ? "+
			"AND o.`deleted_at` IS NULL AND d.`deleted_at` IS NULL AND i.`deleted_at` IS NULL "+
			"GROUP BY i.`product_id`", []any{tenantID, orderID, model.DocStatusApproved})
}

// CountApprovedSaleReturnsByOrder 已审核退货单数（退货经挂单出库单归属到订单，
// 订单反审核禁令用；软删行不计）。
func (r *Repository) CountApprovedSaleReturnsByOrder(ctx context.Context, tenantID, orderID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(DISTINCT d.`id`) FROM `erp_sale_return` d "+
			"JOIN `erp_sale_out` o ON d.`out_id` = o.`id` "+
			"WHERE o.`tenant_id` = ? AND o.`order_id` = ? AND d.`status` = ? "+
			"AND d.`deleted_at` IS NULL AND o.`deleted_at` IS NULL",
		tenantID, orderID, model.DocStatusApproved,
	).Scan(&n).Error
	return n, err
}

// FindSaleOrderItemsByOrder 查询订单全部明细（按明细 ID 升序，进度填充顺序）。
func (r *Repository) FindSaleOrderItemsByOrder(ctx context.Context, orderID int64) ([]model.SaleOrderItem, error) {
	var items []model.SaleOrderItem
	if err := r.db.WithContext(ctx).
		Where("sale_order_id = ?", orderID).
		Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// UpdateSaleOrderItemProgress 回写单个订单明细的进度列（out_count/return_count）。
func (r *Repository) UpdateSaleOrderItemProgress(ctx context.Context, itemID int64, outCount, returnCount qty.Qty) error {
	return r.db.WithContext(ctx).Model(&model.SaleOrderItem{}).
		Where("id = ?", itemID).
		Updates(map[string]any{
			"out_count":    outCount,
			"return_count": returnCount,
		}).Error
}
