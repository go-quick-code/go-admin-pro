package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// demo.go 是示例业务（示例订单/示例提现）的数据访问原语与查询（issue #137，
// docs/pay-mvp-spec.md §3.5/§4.8）。
//
// 分层口径同 recharge.go：
//   - 联动回写/状态认领（ClaimDemoOrderPaid、ApplyDemoOrderRefund、
//     ClaimDemoWithdrawSuccess、MarkDemoWithdrawTransferError、
//     ClaimDemoWithdrawClosed）走显式租户参数的裸 SQL——「认领」是回调联动的
//     幂等裁决（重复回调 0 行跳过），条件必须与租户条件同语句，不被插件改写；
//   - 插入/分页查询/行锁走 gorm 模型：租户插件自动回填/追加 tenant_id，跨
//     租户业务 ID 一律不可见（404）；
//   - 三个 Global 查询显式 tenant.Skip：demo 回调端点是公开路由（无登录上下文），
//     联动前按 data_id 定位消费行派生租户（签名先行校验，见 service/demo.go
//     头注释）——pay_order_id/pay_refund_id/pay_transfer_id 全局唯一可定位。
//
// 审批/关闭的并发串行化：LockDemoWithdraw 行锁（SELECT ... FOR UPDATE），
// 审批（创建转账单 + 回写 pay_transfer_id）与关闭（置 20）在锁内互斥——
// 防止「转账在途被关闭，回调联动无行可认领」的资金事实矛盾。

// DemoOrderFilter 是示例订单分页查询条件（管理端，spec §5）。
type DemoOrderFilter struct {
	UserID    int64
	PayStatus *int8
}

// InsertDemoOrder 插入示例订单（事务内：与挂单同事务）。
func (r *Repository) InsertDemoOrder(ctx context.Context, row *model.DemoOrder) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// UpdateDemoOrderPayOrder 回写示例订单的支付订单 ID（挂单同事务内的第二步：
// merchant_order_id 取示例订单 ID，先落示例订单拿到 ID 再建订单）。
func (r *Repository) UpdateDemoOrderPayOrder(ctx context.Context, id, payOrderID int64) error {
	return r.db.WithContext(ctx).Model(&model.DemoOrder{}).Where("id = ?", id).
		Updates(map[string]any{"pay_order_id": payOrderID, "updated_at": time.Now()}).Error
}

// FindDemoOrderByID 按 ID 查示例订单，不存在/跨租户返回 nil。
func (r *Repository) FindDemoOrderByID(ctx context.Context, id int64) (*model.DemoOrder, error) {
	var row model.DemoOrder
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindDemoOrderByPayOrderIDGlobal 按支付订单 ID 查示例订单（显式 tenant.Skip）：
// demo 支付/退款回调联动的定位键（支付：payload.DataID = pay_order.id；退款：
// payload.DataID = pay_refund.id 先派生租户再取其 order_id 回查——demo 行的
// pay_refund_id 在联动前尚未回写，不能作退款定位键，见 service/demo.go），
// 不存在返回 nil（非示例订单的回调——幂等应答不联动）。
func (r *Repository) FindDemoOrderByPayOrderIDGlobal(ctx context.Context, payOrderID int64) (*model.DemoOrder, error) {
	var row model.DemoOrder
	err := tenant.Skip(r.db.WithContext(ctx)).Where("pay_order_id = ?", payOrderID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ClaimDemoOrderPaid 示例订单支付认领（type=1 回调联动的幂等门槛，spec §4.8）：
// pay_status 0→1 并回写支付渠道/时间——0 行 = 已被并发方联动（重复回调），
// 调用方幂等跳过（同 ClaimRechargePaid 口径）。
func (r *Repository) ClaimDemoOrderPaid(ctx context.Context, tenantID, id int64, channelCode string, payTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_demo_order` SET `pay_status` = 1, `pay_channel_code` = ?, `pay_time` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `pay_status` = 0 AND `deleted_at` IS NULL",
		channelCode, payTime, time.Now(), database.AuditUser(ctx), id, tenantID,
	)
	return res.RowsAffected, res.Error
}

// ApplyDemoOrderRefund 示例订单退款列回写（type=2 回调联动）：pay_refund_id
// 指向末次退款单、refund_price 逐次累加（部分退款链路兼容）、refund_time 回写
// ——条件 `pay_refund_id <> ?`：同一退款单的重复回调 0 行幂等跳过（不重复
// 累加）；不同退款单（部分退款链）正常累加。
func (r *Repository) ApplyDemoOrderRefund(ctx context.Context, tenantID, id, payRefundID int64, refundPrice money.Money, refundTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_demo_order` SET `pay_refund_id` = ?, `refund_price` = `refund_price` + ?, "+
			"`refund_time` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `pay_refund_id` <> ? AND `deleted_at` IS NULL",
		payRefundID, refundPrice, refundTime, time.Now(), database.AuditUser(ctx), id, tenantID, payRefundID,
	)
	return res.RowsAffected, res.Error
}

// ListDemoOrders 按筛选分页查询示例订单（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListDemoOrders(ctx context.Context, f DemoOrderFilter, offset, limit int) ([]model.DemoOrder, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.DemoOrder{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.PayStatus != nil {
		q = q.Where("pay_status = ?", *f.PayStatus)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.DemoOrder
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// DemoWithdrawFilter 是示例提现分页查询条件（管理端，spec §5）。
type DemoWithdrawFilter struct {
	UserID int64
	Status *int8
}

// InsertDemoWithdraw 插入示例提现单。
func (r *Repository) InsertDemoWithdraw(ctx context.Context, row *model.DemoWithdraw) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// FindDemoWithdrawByID 按 ID 查示例提现单，不存在/跨租户返回 nil。
func (r *Repository) FindDemoWithdrawByID(ctx context.Context, id int64) (*model.DemoWithdraw, error) {
	var row model.DemoWithdraw
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// LockDemoWithdraw 示例提现单行锁（审批/关闭的并发串行化：锁内重读 status/
// pay_transfer_id 为权威值，先锁后写同订单口径）。只能在事务内调用；行不存在
// 返回 (nil, nil)。
func (r *Repository) LockDemoWithdraw(ctx context.Context, tenantID, id int64) (*model.DemoWithdraw, error) {
	var row model.DemoWithdraw
	err := r.db.WithContext(ctx).Raw(
		"SELECT * FROM `pay_demo_withdraw` WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL FOR UPDATE",
		id, tenantID,
	).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

// FindDemoWithdrawByTransferIDGlobal 按转账单 ID 查示例提现单（显式
// tenant.Skip）：demo 转账回调联动的定位键（payload.DataID = pay_transfer.id），
// 不存在返回 nil（非示例提现的转账回调——幂等应答不联动）。
func (r *Repository) FindDemoWithdrawByTransferIDGlobal(ctx context.Context, payTransferID int64) (*model.DemoWithdraw, error) {
	var row model.DemoWithdraw
	err := tenant.Skip(r.db.WithContext(ctx)).Where("pay_transfer_id = ?", payTransferID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ApplyDemoWithdrawApproved 回写审批发起的转账单 ID（审批事务内：转账单创建
// 成功后回写；status 保持 0 等待——转账成功回调联动才置 10，spec §4.8）。
func (r *Repository) ApplyDemoWithdrawApproved(ctx context.Context, id, payTransferID int64) error {
	return r.db.WithContext(ctx).Model(&model.DemoWithdraw{}).Where("id = ?", id).
		Updates(map[string]any{"pay_transfer_id": payTransferID, "updated_at": time.Now()}).Error
}

// ClaimDemoWithdrawSuccess 示例提现成功认领（type=3 回调联动的幂等门槛，
// spec §4.8）：status 0→10 并回写转账渠道/时间——0 行 = 已被并发方联动或
// 状态已推进（重复回调/已关闭），调用方按语义处置。
func (r *Repository) ClaimDemoWithdrawSuccess(ctx context.Context, tenantID, id int64, channelCode string, transferTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_demo_withdraw` SET `status` = 10, `transfer_channel_code` = ?, `transfer_time` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `deleted_at` IS NULL",
		channelCode, transferTime, time.Now(), database.AuditUser(ctx), id, tenantID,
	)
	return res.RowsAffected, res.Error
}

// MarkDemoWithdrawTransferError 转账失败信息回写（type=3 失败报文的防御性
// 联动：v1 转账同步成功、任务只在成功时创建，本原语为未来异步渠道的失败
// 回调保留——失败时提现单保持 0 等待，运营可关闭或重试）。
func (r *Repository) MarkDemoWithdrawTransferError(ctx context.Context, tenantID, id int64, errorMsg string) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `pay_demo_withdraw` SET `transfer_error_msg` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `deleted_at` IS NULL",
		errorMsg, time.Now(), database.AuditUser(ctx), id, tenantID,
	).Error
}

// ClaimDemoWithdrawClosed 示例提现关闭认领（管理端关闭/拒绝，spec §4.8）：
// status 0→20 条件更新——0 行 = 已审批在途（pay_transfer_id != 0）或已终态，
// 幂等门槛与锁内重读双保险（防「转账在途被关闭」的资金事实矛盾）。
func (r *Repository) ClaimDemoWithdrawClosed(ctx context.Context, tenantID, id int64) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_demo_withdraw` SET `status` = 20, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `pay_transfer_id` = 0 AND `deleted_at` IS NULL",
		time.Now(), database.AuditUser(ctx), id, tenantID,
	)
	return res.RowsAffected, res.Error
}

// ListDemoWithdraws 按筛选分页查询示例提现单（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListDemoWithdraws(ctx context.Context, f DemoWithdrawFilter, offset, limit int) ([]model.DemoWithdraw, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.DemoWithdraw{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.DemoWithdraw
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
