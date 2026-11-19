package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
)

// trade_cart.go 是购物车的数据访问原语与查询（issue #179，docs/mall-mvp-spec.md
// §3.3/§4）。
//
// UNIQUE(tenant_id, user_id, sku_id) 兜底「同 SKU 加购合并」的并发：加购走
// upsert 原子递增（首行插入或已有行计数相加，行锁内串行化，照 sequence
// upsert 形状），不走「读改写」两步（并发首购同 SKU 会撞唯一键）。
//
// 删除是物理删除（Unscoped）：购物车是即时会话数据，无审计诉求；软删行会在
// 唯一键上占位（键不含 deleted_at），后续同 SKU 加购 upsert 命中死行要么
// 复活要么分叉——硬删一劳永逸，历史行为由订单与积分流水承载。
//
// upsert 与库存扣减同理走显式租户参数裸 SQL（不经租户插件），保证唯一键
// 三元组与租户条件在同一语句口径上；其余查询/更新走 gorm 模型，租户条件由
// 插件自动追加，user_id 越权谓词在 service 层强制。

// UpsertCartMerge 加购合并（INSERT ... ON DUPLICATE KEY UPDATE 计数相加 +
// 恢复选中）：行不存在插入新行（默认选中），存在则 count 相加（并发同 SKU
// 加购在唯一键行锁上串行）。传入 row.TenantID 显式落租户（裸 SQL 不经插件）。
func (r *Repository) UpsertCartMerge(ctx context.Context, row *model.TradeCart) error {
	now := time.Now()
	user := database.AuditUser(ctx)
	return r.db.WithContext(ctx).Exec(
		"INSERT INTO `trade_cart` "+
			"(`tenant_id`, `user_id`, `spu_id`, `sku_id`, `count`, `selected`, `created_at`, `updated_at`, `creator`, `updater`) "+
			"VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE `count` = `count` + ?, `selected` = 1, `updated_at` = ?, `updater` = ?",
		row.TenantID, row.UserID, row.SPUID, row.SKUID, row.Count, now, now, user, user,
		row.Count, now, user,
	).Error
}

// FindCartByID 按 ID 查购物车行（租户插件隔离；user_id 谓词由 service 层附加）。
// 未找到返回 (nil, nil)。
func (r *Repository) FindCartByID(ctx context.Context, id int64) (*model.TradeCart, error) {
	var row model.TradeCart
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// UpdateCartCount 修改加购数量（绝对值覆盖，正整数由 service 层校验）。
// user_id 谓词防越权（他人购物车行不可见），影响 0 行 → service 层 404
// （不存在/跨租户/跨会员不可区分）。
func (r *Repository) UpdateCartCount(ctx context.Context, id, userID int64, count int) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.TradeCart{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("count", count)
	return res.RowsAffected, res.Error
}

// UpdateCartSelected 切换结算选中位（selected 0/1，取值由 service 层校验）。
func (r *Repository) UpdateCartSelected(ctx context.Context, id, userID int64, selected int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.TradeCart{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("selected", selected)
	return res.RowsAffected, res.Error
}

// DeleteCartRows 物理删除购物车行（本人名下；IDs 由 service 层校验非空）。
// 返回删除行数（0 行 = 行不存在/跨租户/跨会员 → service 层 404）。
func (r *Repository) DeleteCartRows(ctx context.Context, userID int64, ids []int64) (int64, error) {
	res := r.db.WithContext(ctx).Unscoped().
		Where("user_id = ? AND id IN ?", userID, ids).
		Delete(&model.TradeCart{})
	return res.RowsAffected, res.Error
}

// cartQuery 组装购物车筛选查询（列表/结算共用谓词，租户插件自动隔离）。
func (r *Repository) cartQuery(ctx context.Context, userID int64, selected *int8) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.TradeCart{}).Where("user_id = ?", userID)
	if selected != nil {
		q = q.Where("selected = ?", *selected)
	}
	return q
}

// ListCartsByUser 全量购物车行（我的购物车页，不分页——购物车行数量级小，
// 照地址清单 ListAllAddressesByUser 口径）。
func (r *Repository) ListCartsByUser(ctx context.Context, userID int64) ([]model.TradeCart, error) {
	var list []model.TradeCart
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("id ASC").Find(&list).Error
	return list, err
}

// ListSelectedCarts 结算选中行（试算与下单共用谓词，selected=1）。
func (r *Repository) ListSelectedCarts(ctx context.Context, userID int64) ([]model.TradeCart, error) {
	var list []model.TradeCart
	var selected int8 = 1
	err := r.cartQuery(ctx, userID, &selected).Order("id ASC").Find(&list).Error
	return list, err
}

// DeleteSelectedCarts 物理删除结算选中行（下单成功后清空已结算项，与订单
// 落库同事务——service 层调用）。
func (r *Repository) DeleteSelectedCarts(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Unscoped().
		Where("user_id = ? AND selected = 1", userID).
		Delete(&model.TradeCart{}).Error
}
