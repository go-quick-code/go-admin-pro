package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// trade_order.go 是订单的数据访问原语与查询（issue #179，docs/mall-mvp-spec.md
// §3.3/§3.4/§4）。
//
// 状态翻转只经条件 UPDATE（认领形状：WHERE 带 from 状态，影响行数 0 = 状态
// 已被并发改动/不可迁移，service 层翻译 409）——非法迁移的最终裁决是这条
// 语句，不是纯函数（纯函数 trade_rules.go 是读路径的快判与测试口径）。取消
// （状态 0→30 + cancel_time/reason）与库存回补同事务（service 层收口）。
//
// 我的订单查询恒带 user_id 谓词（越权防线，spec §4）：分页走 gorm 模型，
// 租户条件由插件自动追加；跨会员订单 ID 一律 404。

// OrderFilter 是订单分页查询条件（C 端我的订单：user_id 恒必填——越权
// 谓词；status 可选过滤，合法性由 service 层校验）。
type OrderFilter struct {
	UserID int64
	Status *int8
}

// orderQuery 组装订单筛选查询（分页共用谓词，租户插件自动隔离）。
func (r *Repository) orderQuery(ctx context.Context, f OrderFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.TradeOrder{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListOrders 分页查询订单（id DESC 的 append 次序）。
func (r *Repository) ListOrders(ctx context.Context, f OrderFilter, page web.PageQuery) ([]model.TradeOrder, int64, error) {
	q := r.orderQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.TradeOrder
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ClaimOrderCancel 取消认领（状态机 0→30 的条件 UPDATE）：仅当行仍处于
// 待支付（status=0）且归属该会员时，置已取消 + cancel_time/cancel_reason，
// 返回影响行数（0 = 状态已被并发改动/越权/不可见 → service 层 409/404）。
// 只能在事务内调用（与库存回补同事务）。
func (r *Repository) ClaimOrderCancel(ctx context.Context, orderID, userID, tenantID int64, reason string, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `trade_order` SET `status` = ?, `cancel_time` = ?, `cancel_reason` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `user_id` = ? AND `status` = ? AND `deleted_at` IS NULL",
		model.TradeOrderStatusCanceled, now, reason, now, database.AuditUser(ctx),
		orderID, tenantID, userID, model.TradeOrderStatusPending,
	)
	return res.RowsAffected, res.Error
}
