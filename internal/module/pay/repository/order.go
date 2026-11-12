package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"gorm.io/gorm"
)

// order.go 是交易与回调域的数据访问原语与查询（issue #133，
// docs/pay-mvp-spec.md §3.2/§3.3/§4.2）。
//
// 事务内写入原语（InsertOrder/InsertOrderExtension/ClaimOrderSuccess/
// UpdateExtension*/InsertNotifyTask）与查询走两条口径，同 wallet.go：
//   - 条件更新/认领（ClaimOrderSuccess、UpdateExtensionStatus）走显式租户
//     参数的裸 SQL——「状态认领」是幂等与并发的最终裁决，条件必须与租户条件
//     同语句，不被插件改写（同 UpdateWalletBalance 口径）；
//   - 插入与分页查询走 gorm 模型：租户插件自动回填/追加 tenant_id，跨租户
//     业务 ID 一律不可见（404）。
//
// 订单行锁（LockOrder）供 submit 串行化同一订单的并发发起：锁内重读状态、
// 校验、建 extension，避免并发 extension 序号错乱与同单双扣。

// OrderFilter 是订单分页查询条件（管理端，spec §5）。
type OrderFilter struct {
	No              string
	MerchantOrderID string
	AppID           int64
	ChannelCode     string
	Status          *int8
}

// FindOrderByID 按 ID 查订单（租户级），不存在/跨租户返回 nil。
func (r *Repository) FindOrderByID(ctx context.Context, id int64) (*model.Order, error) {
	var row model.Order
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindOrderByMerchantID 按 (appID, merchantOrderID) 查订单（幂等键回查），
// 不存在返回 nil。
func (r *Repository) FindOrderByMerchantID(ctx context.Context, appID int64, merchantOrderID string) (*model.Order, error) {
	var row model.Order
	err := r.db.WithContext(ctx).Where("app_id = ? AND merchant_order_id = ?", appID, merchantOrderID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// InsertOrder 插入订单（事务内：发号与落库同事务，spec §4.9）。
// 撞幂等键 uk_pay_order_merchant 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) InsertOrder(ctx context.Context, row *model.Order) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// LockOrder 取订单行锁并返回锁定读的整行（submit/refund 事务内第一步，先锁
// 后写；status/refund_price/price 为锁下权威值——并发 submit 与并发退款由行锁
// 串行化，累计判定无并发窗）。只能在事务内调用；行不存在返回 (nil, nil)
// （先经插件可见性查询确认存在的场景不会出现，防御翻译为 404）。
func (r *Repository) LockOrder(ctx context.Context, tenantID, id int64) (*model.Order, error) {
	var row model.Order
	err := r.db.WithContext(ctx).Raw(
		"SELECT * FROM `pay_order` WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL FOR UPDATE",
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

// CountOrderExtensions 统计订单已存在的发起尝试数（extension no 的发起序，
// submit 在订单行锁内调用，计数无并发窗）。
func (r *Repository) CountOrderExtensions(ctx context.Context, orderID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.OrderExtension{}).Where("order_id = ?", orderID).Count(&total).Error
	return total, err
}

// InsertOrderExtension 插入支付发起 extension（事务内）。撞 uk_pay_order_extension_no
// 时返回 IsDuplicateEntry 可识别的错误（发起序错乱的兜底，submit 行锁内不应发生）。
func (r *Repository) InsertOrderExtension(ctx context.Context, row *model.OrderExtension) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// FindLatestExtensionByID 返回订单最近一次发起的 extension（管理详情/收银台
// 展示最近尝试；不存在返回 nil）。
func (r *Repository) FindLatestExtensionByID(ctx context.Context, orderID int64) (*model.OrderExtension, error) {
	var row model.OrderExtension
	err := r.db.WithContext(ctx).Where("order_id = ?", orderID).Order("id DESC").First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindPendingExtensionByID 返回订单「处理中」的最近一次发起（mock 模拟回调
// 的失败分支对象；不存在返回 nil）。
func (r *Repository) FindPendingExtensionByID(ctx context.Context, orderID int64) (*model.OrderExtension, error) {
	var row model.OrderExtension
	err := r.db.WithContext(ctx).Where("order_id = ? AND status = ?", orderID, model.ExtensionStatusPending).
		Order("id DESC").First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ClaimOrderSuccess 订单状态认领（支付成功入账函数的最终裁决，spec §4.2）：
// UPDATE ... WHERE status=0 条件更新，0 行 = 已被并发方处理（幂等返回，
// 不重复入账/不重复建任务）。回写渠道回执与最近 extension（同语句落定）。
// 显式租户参数裸 SQL（同 UpdateWalletBalance 口径）。只能在事务内调用。
func (r *Repository) ClaimOrderSuccess(ctx context.Context, tenantID, orderID, extensionID int64, channelCode, channelOrderNo, channelUserID string, successTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_order` SET `status` = ?, `success_time` = ?, `channel_code` = ?, `channel_order_no` = ?, "+
			"`channel_user_id` = ?, `extension_id` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` = 0 AND `deleted_at` IS NULL",
		model.OrderStatusSuccess, successTime, channelCode, channelOrderNo, channelUserID,
		extensionID, time.Now(), database.AuditUser(ctx), orderID, tenantID,
	)
	return res.RowsAffected, res.Error
}

// UpdateOrderChannelAttempt 回写订单最近一次发起（channel_code/extension_id，
// mock pending 分支；wallet 成功分支由认领语句一并回写）。显式租户参数裸 SQL。
// 只能在事务内调用。
func (r *Repository) UpdateOrderChannelAttempt(ctx context.Context, tenantID, orderID int64, channelCode string, extensionID int64) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `pay_order` SET `channel_code` = ?, `extension_id` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL",
		channelCode, extensionID, time.Now(), database.AuditUser(ctx), orderID, tenantID,
	).Error
}

// ApplyOrderRefund 订单退款累计（refundSuccessTx 第 3 步，spec §4.4）：
// refund_price = refund_price + 本次退款金额，status 置调用方锁内计算的目标
// 状态（累计 = price → 20 已退款；部分退款 → 保持 10）。订单行锁由调用方
// 持有（refundSuccessTx 锁内重读后调用），先读后写无并发窗。显式租户参数
// 裸 SQL（同 ClaimOrderSuccess 口径）。只能在事务内调用。
func (r *Repository) ApplyOrderRefund(ctx context.Context, tenantID, orderID int64, refundPrice money.Money, status int8) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `pay_order` SET `refund_price` = `refund_price` + ?, `status` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL",
		refundPrice, status, time.Now(), database.AuditUser(ctx), orderID, tenantID,
	).Error
}

// UpdateExtensionResult 回写 extension 结果（成功/失败分支共用，spec §3.2）：
// 状态 + 错误码/信息（成功分支清空）+ 末次回调原文。显式租户参数裸 SQL。
// 只能在事务内调用。
func (r *Repository) UpdateExtensionResult(ctx context.Context, tenantID, id int64, status int8, errorCode, errorMsg, notifyData string) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `pay_order_extension` SET `status` = ?, `channel_error_code` = ?, `channel_error_msg` = ?, "+
			"`channel_notify_data` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL",
		status, errorCode, errorMsg, notifyData, time.Now(), database.AuditUser(ctx), id, tenantID,
	).Error
}

// InsertNotifyTask 创建商户回调任务（支付成功入账函数第 4 步，type=1）。
// 重复回调被订单状态认领挡住，任务不会重复创建。
func (r *Repository) InsertNotifyTask(ctx context.Context, row *model.NotifyTask) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CountOrdersByAppID 统计应用的订单引用（应用删除守卫，spec §4.10）。
func (r *Repository) CountOrdersByAppID(ctx context.Context, appID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Order{}).Where("app_id = ?", appID).Count(&total).Error
	return total, err
}

// orderQuery 组装订单筛选查询（分页与导出共用同一谓词，租户插件自动隔离）。
func (r *Repository) orderQuery(ctx context.Context, f OrderFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Order{})
	if f.No != "" {
		q = q.Where("no = ?", f.No)
	}
	if f.MerchantOrderID != "" {
		q = q.Where("merchant_order_id = ?", f.MerchantOrderID)
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

// ListOrders 按筛选分页查询订单（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListOrders(ctx context.Context, f OrderFilter, offset, limit int) ([]model.Order, int64, error) {
	q := r.orderQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Order
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllOrders 按筛选查询全部订单（Excel 导出用，不分页；排序与列表一致
// id DESC，#138 spec §4.12）。行数无上限：与 CRM/ERP ListAll 同口径，
// 由筛选条件控制结果规模。
func (r *Repository) ListAllOrders(ctx context.Context, f OrderFilter) ([]model.Order, error) {
	var list []model.Order
	if err := r.orderQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// WithTx 的事务键与存取（channel/wallet.go 的 WalletPayer 从 ctx 取事务）。
type txContextKey struct{}

// WithTx 把事务绑定的 Repository 放入 ctx（service 层发起支付事务内调用），
// wallet 渠道扣减后端经 TxFrom 取同一事务，保证「扣减 → 认领 → 建任务」
// 同事务原子提交。
func WithTx(ctx context.Context, tx *Repository) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// TxFrom 取 ctx 绑定的事务 Repository；不存在返回 false（事务外调用 wallet
// 扣减属程序错误，service 层翻译为 500）。
func TxFrom(ctx context.Context) (*Repository, bool) {
	tx, ok := ctx.Value(txContextKey{}).(*Repository)
	return tx, ok
}
