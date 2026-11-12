package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// transfer.go 是转账单的数据访问原语与查询（issue #137，
// docs/pay-mvp-spec.md §3.2/§4.8/§4.9）。
//
// 分层口径同 order.go/refund.go：
//   - 条件更新/认领（ClaimTransferSuccess、UpdateTransferResult）走显式租户
//     参数的裸 SQL——「状态认领」是转账成功的幂等裁决，条件必须与租户条件同
//     语句，不被插件改写（同 UpdateWalletBalance 口径）；
//   - 插入与分页查询走 gorm 模型：租户插件自动回填/追加 tenant_id，跨租户
//     业务 ID 一律不可见（404）；
//   - FindTransferByIDGlobal 显式 tenant.Skip：demo 回调端点是公开路由（无
//     登录上下文），联动前按 data_id 定位转账单派生租户（签名先行校验，
//     见 service/demo.go 头注释）。
//
// 转账单无行锁原语：同一 merchant_transfer_id 的并发发起由幂等键 UNIQUE 兜底
//（撞键回读原单，同订单/退款口径），转账单自身以状态认领
//（ClaimTransferSuccess WHERE status=0）做幂等裁决。

// TransferFilter 是转账单分页查询条件（管理端，spec §5）。
type TransferFilter struct {
	No                 string
	MerchantTransferID string
	AppID              int64
	ChannelCode        string
	Status             *int8
}

// FindTransferByID 按 ID 查转账单（租户级），不存在/跨租户返回 nil。
func (r *Repository) FindTransferByID(ctx context.Context, id int64) (*model.Transfer, error) {
	var row model.Transfer
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindTransferByIDGlobal 按 ID 查转账单（显式 tenant.Skip）：demo 回调联动的
// 租户派生用（公开端点上下文无租户；DataID 全局唯一，签名校验先于本查询）。
func (r *Repository) FindTransferByIDGlobal(ctx context.Context, id int64) (*model.Transfer, error) {
	var row model.Transfer
	if err := tenant.Skip(r.db.WithContext(ctx)).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindTransferByMerchantID 按 (appID, merchantTransferID) 查转账单（幂等键
// 回查），不存在返回 nil。
func (r *Repository) FindTransferByMerchantID(ctx context.Context, appID int64, merchantTransferID string) (*model.Transfer, error) {
	var row model.Transfer
	err := r.db.WithContext(ctx).Where("app_id = ? AND merchant_transfer_id = ?", appID, merchantTransferID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// InsertTransfer 插入转账单（事务内：发号与落库同事务，spec §4.9）。撞幂等键
// uk_pay_transfer_merchant 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) InsertTransfer(ctx context.Context, row *model.Transfer) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// ClaimTransferSuccess 转账单状态认领（转账成功入账函数的最终裁决，spec §4.8）：
// UPDATE ... WHERE status=0 条件更新，0 行 = 已被并发方处理（幂等返回，不重复
// 建任务）。回写渠道转账回执与末次响应原文（同语句落定）。显式租户参数裸 SQL
// （同 ClaimOrderSuccess 口径）。只能在事务内调用。
func (r *Repository) ClaimTransferSuccess(ctx context.Context, tenantID, transferID int64, channelTransferNo, notifyData string, successTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_transfer` SET `status` = ?, `success_time` = ?, `channel_transfer_no` = ?, "+
			"`channel_notify_data` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `deleted_at` IS NULL",
		model.TransferStatusSuccess, successTime, channelTransferNo, notifyData,
		time.Now(), database.AuditUser(ctx), transferID, tenantID,
	)
	return res.RowsAffected, res.Error
}

// UpdateTransferResult 回写转账单结果（失败/关闭分支：status + 错误码/信息 +
// 末次回调原文；v1 两渠道同步成功，本原语为失败分支防御性保留——与退款
// UpdateRefundResult 同构）。显式租户参数裸 SQL。只能在事务内调用。
func (r *Repository) UpdateTransferResult(ctx context.Context, tenantID, id int64, status int8, errorCode, errorMsg, notifyData string) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `pay_transfer` SET `status` = ?, `channel_error_code` = ?, `channel_error_msg` = ?, "+
			"`channel_notify_data` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL",
		status, errorCode, errorMsg, notifyData, time.Now(), database.AuditUser(ctx), id, tenantID,
	).Error
}

// CountTransfersByAppID 统计应用的转账单引用（应用删除守卫，spec §4.10）。
func (r *Repository) CountTransfersByAppID(ctx context.Context, appID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Transfer{}).Where("app_id = ?", appID).Count(&total).Error
	return total, err
}

// transferQuery 组装转账单筛选查询（分页与导出共用同一谓词，租户插件自动隔离）。
func (r *Repository) transferQuery(ctx context.Context, f TransferFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Transfer{})
	if f.No != "" {
		q = q.Where("no = ?", f.No)
	}
	if f.MerchantTransferID != "" {
		q = q.Where("merchant_transfer_id = ?", f.MerchantTransferID)
	}
	if f.AppID > 0 {
		q = q.Where("app_id = ?", f.AppID)
	}
	if f.ChannelCode != "" {
		q = q.Where("channel_code = ?", f.ChannelCode)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListTransfers 按筛选分页查询转账单（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListTransfers(ctx context.Context, f TransferFilter, offset, limit int) ([]model.Transfer, int64, error) {
	q := r.transferQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Transfer
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllTransfers 按筛选查询全部转账单（Excel 导出用，不分页；排序与列表一致
// id DESC，#138 spec §4.12）。行数无上限：与 CRM/ERP ListAll 同口径。
func (r *Repository) ListAllTransfers(ctx context.Context, f TransferFilter) ([]model.Transfer, error) {
	var list []model.Transfer
	if err := r.transferQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
