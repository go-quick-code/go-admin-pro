package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// trade_order.go 是订单的数据访问原语与查询（issue #179/#180，docs/mall-mvp-spec.md
// §3.3/§3.4/§4）。
//
// 状态翻转只经条件 UPDATE（认领形状：WHERE 带 from 状态，影响行数 0 = 状态
// 已被并发改动/不可迁移，service 层翻译 409）——非法迁移的最终裁决是这条
// 语句，不是纯函数（纯函数 trade_rules.go 是读路径的快判与测试口径）。取消
// （状态 0→30 + cancel_time/reason）与库存回补同事务（service 层收口）。
//
// C 端我的订单查询恒带 user_id 谓词（越权防线，spec §4）：分页走 gorm 模型，
// 租户条件由插件自动追加；跨会员订单 ID 一律 404。管理端（issue #180，T7）
// 面向本租户全部会员订单：不加 user_id 谓词，租户隔离仍由插件保证。

// OrderFilter 是订单分页查询条件（C 端我的订单：user_id 恒必填——越权
// 谓词；status 可选过滤，合法性由 service 层校验。T7 管理端扩展：编号模糊/
// 时间范围，零值不参与谓词）。
type OrderFilter struct {
	UserID int64
	Status *int8
	// NoLike 是订单编号模糊过滤（LIKE %v%，管理端检索；空串不过滤）。
	NoLike string
	// CreateTimeBegin/CreateTimeEnd 是下单时间范围（created_at >= begin /
	// < end，上界排他——「含当日」语义由 service 层外推到次日 0 点）。
	CreateTimeBegin *time.Time
	CreateTimeEnd   *time.Time
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
	if f.NoLike != "" {
		q = q.Where("no LIKE ?", "%"+f.NoLike+"%")
	}
	if f.CreateTimeBegin != nil {
		q = q.Where("created_at >= ?", *f.CreateTimeBegin)
	}
	if f.CreateTimeEnd != nil {
		q = q.Where("created_at < ?", *f.CreateTimeEnd)
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

// FindOrderByNo 按 SO 单号查订单（T8 支付联动的定位键：pay_order.
// merchant_order_id = mall 订单号）。走 gorm 模型查询，租户插件按 ctx 追加
// tenant_id 条件——两条调用路径的 ctx 都携带租户：C 端发起支付（memberAuth
// 写入）与支付成功联动（service 层以 tenantID 显式补齐，照 pay 渠道回调
// guardTenantContext 先例）。未找到返回 (nil, nil)。
func (r *Repository) FindOrderByNo(ctx context.Context, no string) (*model.TradeOrder, error) {
	var row model.TradeOrder
	err := r.db.WithContext(ctx).First(&row, "no = ?", no).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// UpdateOrderPayOrder 记回支付单（T8：mall 订单已落库、pay CreateOrder 成功
// 后回写 pay_order_id/pay_order_no——「先落 mall 订单、再建 pay 单」两段式
// 的连接列；建单失败订单留在待支付、列保持 0，C 端重试发起支付时补建）。
// WHERE 追加 pay_order_id = 0：只补首次，已记录的绑定不覆盖（绑定关系以
// 首次建单为准，重复发起支付走 pay 侧 merchant_order_id 幂等复用原单）。
func (r *Repository) UpdateOrderPayOrder(ctx context.Context, tenantID, orderID, payOrderID int64, payOrderNo string) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `trade_order` SET `pay_order_id` = ?, `pay_order_no` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `pay_order_id` = 0 AND `deleted_at` IS NULL",
		payOrderID, payOrderNo, time.Now(), database.AuditUser(ctx),
		orderID, tenantID,
	).Error
}

// ClaimOrderPaid 支付成功认领（状态机 0→10 的条件 UPDATE，T8 联动的守卫
// 终审）：仅当行仍处于待支付时，置已支付 + pay_order_id/pay_order_no/
// pay_channel_code/pay_time，返回影响行数（0 = 状态已非待支付——取消/关单/
// 已被并发认领 → service 层报错使整笔支付认领事务回滚，见 HandlePaySuccess
// 守卫注释的窗口分析）。只能在与 pay 认领同一事务内调用（mallrepo.New 包装
// pay tx 底层句柄，同事务原子）。
func (r *Repository) ClaimOrderPaid(ctx context.Context, tenantID, orderID, payOrderID int64, payOrderNo, channelCode string, payTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `trade_order` SET `status` = ?, `pay_order_id` = ?, `pay_order_no` = ?, "+
			"`pay_channel_code` = ?, `pay_time` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = ? AND `deleted_at` IS NULL",
		model.TradeOrderStatusPaid, payOrderID, payOrderNo, channelCode, payTime, payTime, database.AuditUser(ctx),
		orderID, tenantID, model.TradeOrderStatusPending,
	)
	return res.RowsAffected, res.Error
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

// ClaimOrderCancelByAdmin 管理员取消认领（issue #180，T7；形状同
// ClaimOrderCancel 但无 user_id 谓词——管理员面向本租户全部会员的订单，
// 租户边界仍由显式 tenantID 保证）。仅当行仍处于待支付（status=0）时置
// 已取消 + cancel_time/cancel_reason，返回影响行数（0 = 状态已被并发改动/
// 跨租户不可见 → service 层 409/404）。只能在事务内调用（与库存回补同事务）。
func (r *Repository) ClaimOrderCancelByAdmin(ctx context.Context, orderID, tenantID int64, reason string, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `trade_order` SET `status` = ?, `cancel_time` = ?, `cancel_reason` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = ? AND `deleted_at` IS NULL",
		model.TradeOrderStatusCanceled, now, reason, now, database.AuditUser(ctx),
		orderID, tenantID, model.TradeOrderStatusPending,
	)
	return res.RowsAffected, res.Error
}

// ClaimOrderComplete 完成认领（状态机 10→20 的条件 UPDATE，issue #180，T7）：
// 仅当行仍处于已支付（status=10）时置已完成，返回影响行数（0 = 状态已被
// 并发改动/跨租户不可见 → service 层 409/404）。已完成态无独立时间列
// （spec §3.3 订单表无 complete_time，finish 语义只落在状态位）。
func (r *Repository) ClaimOrderComplete(ctx context.Context, orderID, tenantID int64, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `trade_order` SET `status` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = ? AND `deleted_at` IS NULL",
		model.TradeOrderStatusCompleted, now, database.AuditUser(ctx),
		orderID, tenantID, model.TradeOrderStatusPaid,
	)
	return res.RowsAffected, res.Error
}

// UpdateOrderAdminRemark 管理员备注（issue #180，T7；PUT /mall/trade/order/
// :id/remark）：任意状态可补注（终态也允许，spec §4「管理员备注」无状态
// 前置）。gorm 模型更新走租户插件（tenant_id 自动追加），updater/updated_at
// 由审计插件回填；返回影响行数（0 = 行不可见 → service 层 404）。
func (r *Repository) UpdateOrderAdminRemark(ctx context.Context, orderID int64, remark string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.TradeOrder{}).
		Where("id = ?", orderID).
		Update("admin_remark", remark)
	return res.RowsAffected, res.Error
}
