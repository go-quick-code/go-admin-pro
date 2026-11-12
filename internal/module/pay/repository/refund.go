package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// refund.go 是退款单的数据访问原语与查询（issue #135，
// docs/pay-mvp-spec.md §3.2/§4.4/§4.9）。
//
// 分层口径同 order.go：
//   - 条件更新/认领（ClaimRefundSuccess、UpdateRefundResult、ApplyOrderRefund）
//     走显式租户参数的裸 SQL——「状态认领」是幂等与并发的最终裁决，条件必须
//     与租户条件同语句，不被插件改写（同 UpdateWalletBalance 口径）；
//   - 插入与分页查询走 gorm 模型：租户插件自动回填/追加 tenant_id，跨租户
//     业务 ID 一律不可见（404）。
//
// 退款单无行锁原语：并发退款由订单行锁（LockOrder）串行化——同一订单的
// 累计判定在锁内裁决（spec §4.4 部分退款累计），退款单自身以状态认领
//（ClaimRefundSuccess WHERE status=0）做幂等裁决。

// RefundFilter 是退款单分页查询条件（管理端，spec §5）。
type RefundFilter struct {
	No               string
	MerchantRefundID string
	MerchantOrderID  string
	AppID            int64
	OrderID          int64
	ChannelCode      string
	Status           *int8
}

// FindRefundByID 按 ID 查退款单（租户级），不存在/跨租户返回 nil。
func (r *Repository) FindRefundByID(ctx context.Context, id int64) (*model.Refund, error) {
	var row model.Refund
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindRefundByIDGlobal 按 ID 查退款单（显式 tenant.Skip）：demo 退款回调联动的
// 租户派生用（公开端点上下文无租户；DataID 全局唯一，签名校验先于本查询）。
func (r *Repository) FindRefundByIDGlobal(ctx context.Context, id int64) (*model.Refund, error) {
	var row model.Refund
	if err := tenant.Skip(r.db.WithContext(ctx)).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindRefundByMerchantID 按 (appID, merchantRefundID) 查退款单（幂等键回查），
// 不存在返回 nil。
func (r *Repository) FindRefundByMerchantID(ctx context.Context, appID int64, merchantRefundID string) (*model.Refund, error) {
	var row model.Refund
	err := r.db.WithContext(ctx).Where("app_id = ? AND merchant_refund_id = ?", appID, merchantRefundID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// InsertRefund 插入退款单（事务内：发号与落库同事务，spec §4.9）。
// 撞幂等键 uk_pay_refund_merchant 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) InsertRefund(ctx context.Context, row *model.Refund) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// ClaimRefundSuccess 退款单状态认领（退款成功入账函数的最终裁决，spec §4.4）：
// UPDATE ... WHERE status=0 条件更新，0 行 = 已被并发方处理（幂等返回，
// 不重复累计/不重复建任务）。回写渠道退款回执与末次回调原文（同语句落定）。
// 显式租户参数裸 SQL（同 ClaimOrderSuccess 口径）。只能在事务内调用。
func (r *Repository) ClaimRefundSuccess(ctx context.Context, tenantID, refundID int64, channelRefundNo, notifyData string, successTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_refund` SET `status` = ?, `success_time` = ?, `channel_refund_no` = ?, "+
			"`channel_notify_data` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `deleted_at` IS NULL",
		model.RefundStatusSuccess, successTime, channelRefundNo, notifyData,
		time.Now(), database.AuditUser(ctx), refundID, tenantID,
	)
	return res.RowsAffected, res.Error
}

// UpdateRefundResult 回写退款单结果（失败分支：20 失败 + 错误码/信息 + 末次
// 回调原文；订单不累计、无回调任务）。显式租户参数裸 SQL。只能在事务内调用。
func (r *Repository) UpdateRefundResult(ctx context.Context, tenantID, id int64, status int8, errorCode, errorMsg, notifyData string) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `pay_refund` SET `status` = ?, `channel_error_code` = ?, `channel_error_msg` = ?, "+
			"`channel_notify_data` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL",
		status, errorCode, errorMsg, notifyData, time.Now(), database.AuditUser(ctx), id, tenantID,
	).Error
}

// ClaimRefundFail 退款单失败认领（#136 充值退款成功回调发现余额不足以扣回，
// spec §4.7 先校验后退款）：UPDATE ... WHERE status=0 条件更新置 20 失败——
// 失败终态的幂等门槛与 ClaimRefundSuccess 同构（0 行 = 已被并发方处理，
// 幂等返回）。只能在事务内调用。
func (r *Repository) ClaimRefundFail(ctx context.Context, tenantID, refundID int64, errorCode, errorMsg, notifyData string) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_refund` SET `status` = ?, `channel_error_code` = ?, `channel_error_msg` = ?, "+
			"`channel_notify_data` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `deleted_at` IS NULL",
		model.RefundStatusFail, errorCode, errorMsg, notifyData,
		time.Now(), database.AuditUser(ctx), refundID, tenantID,
	)
	return res.RowsAffected, res.Error
}

// CountRefundsByAppID 统计应用的退款单引用（应用删除守卫，spec §4.10）。
func (r *Repository) CountRefundsByAppID(ctx context.Context, appID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Refund{}).Where("app_id = ?", appID).Count(&total).Error
	return total, err
}

// refundQuery 组装退款单筛选查询（分页与导出共用同一谓词，租户插件自动隔离）。
func (r *Repository) refundQuery(ctx context.Context, f RefundFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Refund{})
	if f.No != "" {
		q = q.Where("no = ?", f.No)
	}
	if f.MerchantRefundID != "" {
		q = q.Where("merchant_refund_id = ?", f.MerchantRefundID)
	}
	if f.MerchantOrderID != "" {
		q = q.Where("merchant_order_id = ?", f.MerchantOrderID)
	}
	if f.AppID > 0 {
		q = q.Where("app_id = ?", f.AppID)
	}
	if f.OrderID > 0 {
		q = q.Where("order_id = ?", f.OrderID)
	}
	if f.ChannelCode != "" {
		q = q.Where("channel_code = ?", f.ChannelCode)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListRefunds 按筛选分页查询退款单（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListRefunds(ctx context.Context, f RefundFilter, offset, limit int) ([]model.Refund, int64, error) {
	q := r.refundQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Refund
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllRefunds 按筛选查询全部退款单（Excel 导出用，不分页；排序与列表一致
// id DESC，#138 spec §4.12）。行数无上限：与 CRM/ERP ListAll 同口径。
func (r *Repository) ListAllRefunds(ctx context.Context, f RefundFilter) ([]model.Refund, error) {
	var list []model.Refund
	if err := r.refundQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
