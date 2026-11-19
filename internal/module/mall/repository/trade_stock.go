package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
)

// trade_stock.go 是 SKU 库存的行锁扣减/回补原语（issue #179，docs/mall-mvp-spec.md
// §3.4「库存不变量」）。
//
// 走显式租户参数的裸 SQL（不经租户插件，同积分入账/erp_stock 口径）：条件
// UPDATE 在 SKU 行锁内完成「库存校验 + 扣减」，影响行数 0 = 库存不足（或行
// 不存在/已软删/跨租户），由 service 层翻译 409——并发下单不超卖的最终裁决
// 就是这条语句（读改写两步有并发窗，一律不得使用）。只能在事务内调用
//（扣减/回补与订单状态迁移同事务，失败整体回滚）。

// DeductSKUStock 行锁扣减库存（下单，spec §3.4「下单即扣减」）：
// UPDATE product_sku SET stock = stock - ? WHERE id=? AND tenant_id=? AND
// stock >= ? AND deleted_at IS NULL。返回影响行数（0 = 库存不足/行不可见）。
func (r *Repository) DeductSKUStock(ctx context.Context, tenantID, skuID int64, qty int) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `product_sku` SET `stock` = `stock` - ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `stock` >= ? AND `deleted_at` IS NULL",
		qty, time.Now(), database.AuditUser(ctx), skuID, tenantID, qty,
	)
	return res.RowsAffected, res.Error
}

// RestoreSKUStock 回补库存（取消/关单，spec §3.4「取消/关单回补」，同形状
// stock + ?）：只按 ID 与租户定位，不做库存上限校验（回补方向不可能为负）。
// 返回影响行数（0 = 行不存在/跨租户，异常分支由 service 层报错回滚）。
//
// 刻意不过滤软删（无 `deleted_at IS NULL`）——这是簿记动作而非售卖动作：
// SPU 更新是「SKU 集全量替换」（UpdateProductSPU 每次保存都软删全部旧
// SKU，无「被订单引用禁改」守卫），在途订单引用的旧 SKU 行在保存后只以
// 软删形态存在；取消/超时关单的回补必须能命中软删行，否则在途订单永久
// 卡死（取消 409、关单 job 中断——PR #184 审查 P1）。扣减（DeductSKUStock）
// 仍过滤软删：已删 SKU 不可再售。
func (r *Repository) RestoreSKUStock(ctx context.Context, tenantID, skuID int64, qty int) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `product_sku` SET `stock` = `stock` + ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ?",
		qty, time.Now(), database.AuditUser(ctx), skuID, tenantID,
	)
	return res.RowsAffected, res.Error
}

// ---------- 订单与订单项 ----------

// CreateOrder 创建订单主行（与订单项/库存扣减/清购物车同事务，service 层）。
// 撞 UNIQUE(tenant_id, no) 返回规整后的 ErrDuplicatedKey（理论分支：序号
// 不重复，兜底翻译）。
func (r *Repository) CreateOrder(ctx context.Context, row *model.TradeOrder) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// CreateOrderItems 批量创建订单项（与主行同事务）。
func (r *Repository) CreateOrderItems(ctx context.Context, rows []model.TradeOrderItem) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&rows).Error
}

// FindOrderByID 按 ID 查订单（租户插件隔离；user_id 越权谓词由 service 层
// 强制）。未找到返回 (nil, nil)。
func (r *Repository) FindOrderByID(ctx context.Context, id int64) (*model.TradeOrder, error) {
	var row model.TradeOrder
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ListOrderItemsByOrder 查订单全部订单项（详情返回，固定 id ASC）。
func (r *Repository) ListOrderItemsByOrder(ctx context.Context, orderID int64) ([]model.TradeOrderItem, error) {
	var list []model.TradeOrderItem
	err := r.db.WithContext(ctx).
		Where("order_id = ?", orderID).
		Order("id ASC").Find(&list).Error
	return list, err
}
