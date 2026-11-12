package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"gorm.io/gorm"
)

// recharge.go 是充值域的数据访问原语与查询（issue #136，
// docs/pay-mvp-spec.md §3.4/§4.7/§4.10）。
//
// 事务内写入原语（InsertRecharge/UpdateRechargePayOrder/ClaimRechargePaid/
// ApplyRechargeRefundSuccess/FailRechargeRefund）与查询走两条口径，同
// order.go/wallet.go：
//   - 条件更新/认领（ClaimRechargePaid、ApplyRechargeRefundSuccess、
//     FailRechargeRefund）走显式租户参数的裸 SQL——「状态认领」是支付入账与
//     退款扣回的幂等裁决，条件必须与租户条件同语句，不被插件改写；
//   - 插入/分页查询/引用计数走 gorm 模型：租户插件自动追加 tenant_id，
//     跨租户业务 ID 一律不可见（404）。
//
// 套餐被充值单引用禁删（spec §4.10）：引用计数不带 deleted_at 过滤
//（Unscoped）——充值单不可删（资金事实 append-only），引用永久，即使充值行
// 被意外软删（无入口，防御性口径）也不释放套餐引用。

// ---------- 充值套餐 CRUD（管理端，spec §5） ----------

// RechargePackageFilter 是充值套餐分页查询条件。
type RechargePackageFilter struct {
	Name   string
	Status *int8
}

// InsertRechargePackage 插入充值套餐。
func (r *Repository) InsertRechargePackage(ctx context.Context, row *model.WalletRechargePackage) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// FindRechargePackageByID 按 ID 查充值套餐，不存在/跨租户返回 nil。
func (r *Repository) FindRechargePackageByID(ctx context.Context, id int64) (*model.WalletRechargePackage, error) {
	var row model.WalletRechargePackage
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// UpdateRechargePackageFields 按 map 更新套餐字段（id 在 body，与 CRM/ProTable 一致）。
func (r *Repository) UpdateRechargePackageFields(ctx context.Context, id int64, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.WalletRechargePackage{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteRechargePackage 软删除充值套餐（删除前引用校验在 service 层，spec §4.10）。
func (r *Repository) DeleteRechargePackage(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.WalletRechargePackage{}, id).Error
}

// CountRechargesByPackageID 统计引用套餐的充值单行数（引用计数 Unscoped：
// 不带 deleted_at 过滤——充值单不可删，引用永久；软删行同样计数，防御性口径，
// spec §4.10 充值套餐被充值单引用禁删）。
func (r *Repository) CountRechargesByPackageID(ctx context.Context, packageID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Unscoped().Model(&model.WalletRecharge{}).
		Where("package_id = ?", packageID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// ListRechargePackages 分页查询充值套餐（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListRechargePackages(ctx context.Context, f RechargePackageFilter, offset, limit int) ([]model.WalletRechargePackage, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.WalletRechargePackage{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.WalletRechargePackage
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabledRechargePackages 返回启用套餐清单（自助端点「发起充值」的套餐
// 选择数据源，登录即可，无凭证列；id ASC 保证展示顺序稳定——与分页查询的
// id DESC 不同：清单是选择器数据源，按创建序展示）。
func (r *Repository) ListEnabledRechargePackages(ctx context.Context) ([]model.WalletRechargePackage, error) {
	var list []model.WalletRechargePackage
	err := r.db.WithContext(ctx).Where("status = ?", model.StatusEnable).
		Order("id ASC").Find(&list).Error
	return list, err
}

// ---------- 充值单（挂支付订单，spec §4.7） ----------

// RechargeFilter 是充值单分页查询条件（管理端/自助端共用）。
type RechargeFilter struct {
	WalletID  int64
	PackageID int64
	PayStatus *int8
}

// InsertRecharge 插入充值单（事务内：与挂单同事务，spec §4.7）。
func (r *Repository) InsertRecharge(ctx context.Context, row *model.WalletRecharge) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// UpdateRechargePayOrder 回写充值单的支付订单 ID（挂单同事务内的第二步：
// merchant_order_id 取充值单 ID，先落充值单拿到 ID 再建订单）。
func (r *Repository) UpdateRechargePayOrder(ctx context.Context, id, payOrderID int64) error {
	return r.db.WithContext(ctx).Model(&model.WalletRecharge{}).Where("id = ?", id).
		Updates(map[string]any{"pay_order_id": payOrderID, "updated_at": time.Now()}).Error
}

// FindRechargeByID 按 ID 查充值单，不存在/跨租户返回 nil。
func (r *Repository) FindRechargeByID(ctx context.Context, id int64) (*model.WalletRecharge, error) {
	var row model.WalletRecharge
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindRechargeByOrderID 按支付订单 ID 反查充值单（submit 禁钱包渠道校验与
// 支付成功/退款成功联动的定位键，idx_pay_wallet_recharge_pay_order 支撑），
// 不存在返回 nil（非充值订单）。
func (r *Repository) FindRechargeByOrderID(ctx context.Context, orderID int64) (*model.WalletRecharge, error) {
	var row model.WalletRecharge
	err := r.db.WithContext(ctx).Where("pay_order_id = ?", orderID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ClaimRechargePaid 充值支付认领（支付成功联动的幂等门槛，spec §4.7）：
// pay_status 0→1 并回写支付渠道/时间——0 行 = 已被并发方入账（重复回调），
// 调用方幂等跳过。只能在事务内调用（与钱包入账同事务：认领失败/入账失败
// 整体回滚，spec §4.3）。
func (r *Repository) ClaimRechargePaid(ctx context.Context, tenantID, id int64, channelCode string, payTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_wallet_recharge` SET `pay_status` = 1, `pay_channel_code` = ?, `pay_time` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `pay_status` = 0 AND `deleted_at` IS NULL",
		channelCode, payTime, time.Now(), database.AuditUser(ctx), id, tenantID,
	)
	return res.RowsAffected, res.Error
}

// ApplyRechargeRefundSuccess 退款成功列回写（spec §4.7）：refund_status 10 +
// pay_refund_id + refund_* 三列（pay + bonus 全额）+ refund_time——条件
// `refund_status <> 10`：失败（20）后重试成功可覆盖，成功（10）后重复回调
// 0 行幂等跳过（重复扣回由退款单认领先行挡住，此处兜底）。
// 只能在事务内调用（与钱包扣回同事务）。
func (r *Repository) ApplyRechargeRefundSuccess(ctx context.Context, tenantID, id, payRefundID int64, totalPrice, payPrice, bonusPrice money.Money, refundTime time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_wallet_recharge` SET `refund_status` = 10, `pay_refund_id` = ?, "+
			"`refund_total_price` = ?, `refund_pay_price` = ?, `refund_bonus_price` = ?, "+
			"`refund_time` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `refund_status` <> 10 AND `deleted_at` IS NULL",
		payRefundID, totalPrice, payPrice, bonusPrice, refundTime, time.Now(), database.AuditUser(ctx), id, tenantID,
	)
	return res.RowsAffected, res.Error
}

// FailRechargeRefund 充值退款失败标记（spec §4.7 先校验后退款：退款成功回调
// 发现余额不足以扣回时，退款单置 20 失败的同事务里给充值单记失败态）：
// refund_status 0→20 条件更新，金额列不动（未扣回）、可重新发起。
// 只能在事务内调用。
func (r *Repository) FailRechargeRefund(ctx context.Context, tenantID, id int64) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_wallet_recharge` SET `refund_status` = 20, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `refund_status` = 0 AND `deleted_at` IS NULL",
		time.Now(), database.AuditUser(ctx), id, tenantID,
	)
	return res.RowsAffected, res.Error
}

// rechargeQuery 组装充值单筛选查询（分页与导出共用同一谓词，租户插件自动隔离）。
func (r *Repository) rechargeQuery(ctx context.Context, f RechargeFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.WalletRecharge{})
	if f.WalletID > 0 {
		q = q.Where("wallet_id = ?", f.WalletID)
	}
	if f.PackageID > 0 {
		q = q.Where("package_id = ?", f.PackageID)
	}
	if f.PayStatus != nil {
		q = q.Where("pay_status = ?", *f.PayStatus)
	}
	return q
}

// ListRecharges 分页查询充值单（管理端固定 id DESC；自助端传 walletID 过滤
// 本人充值单，归属过滤在 service 层强制，spec §4.11）。
func (r *Repository) ListRecharges(ctx context.Context, f RechargeFilter, offset, limit int) ([]model.WalletRecharge, int64, error) {
	q := r.rechargeQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.WalletRecharge
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllRecharges 按筛选查询全部充值单（Excel 导出用，不分页；排序与列表一致
// id DESC，#138 spec §4.12）。行数无上限：与 CRM/ERP ListAll 同口径。
func (r *Repository) ListAllRecharges(ctx context.Context, f RechargeFilter) ([]model.WalletRecharge, error) {
	var list []model.WalletRecharge
	if err := r.rechargeQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FindRechargePackagesByIDs 按 ID 批量查充值套餐（导出套餐名称列解析），
// 不存在的 ID 不在返回里。
func (r *Repository) FindRechargePackagesByIDs(ctx context.Context, ids []int64) ([]model.WalletRechargePackage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []model.WalletRechargePackage
	if err := r.db.WithContext(ctx).Find(&rows, ids).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ---------- 充值单视图装配（appKey/orderNo 回填，我的充值页发起退款与收银台跳转） ----------

// FindOrdersByIDs 按 ID 批量查支付订单（充值单视图回填 order_no），不存在
// 的 ID 不在返回里。
func (r *Repository) FindOrdersByIDs(ctx context.Context, ids []int64) ([]model.Order, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []model.Order
	if err := r.db.WithContext(ctx).Find(&rows, ids).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// FindAppsByIDs 按 ID 批量查应用（充值单视图回填 app_key），不存在的 ID 不在
// 返回里。
func (r *Repository) FindAppsByIDs(ctx context.Context, ids []int64) ([]model.App, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []model.App
	if err := r.db.WithContext(ctx).Find(&rows, ids).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
