package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// member_point.go 是积分域的数据访问原语与查询（issue #175，
// docs/mall-mvp-spec.md §3.1/§4）。
//
// 入账三原语（LockMemberPoint → UpdateMemberPointBalance → InsertPointRecord）
// 只供 service 层唯一入账入口（service/point.go ApplyPointChange）在事务内按
// 序调用，任何其它路径不得调用，更不得直改 member_user.point_balance
//（spec §3.4「积分不变量」硬约束，照 pay wallet 四原语先例）。原语全部走
// 显式租户参数的裸 SQL（不经租户插件，同 pay UpdateWalletBalance /
// erp_stock / NextSequenceValue 口径），保证「锁行 → 读写」与租户条件在
// 同一语句口径上，不被插件改写。
//
// 积分配置的惰性单行原语（FindPointConfig/EnsurePointConfig/
// UpdatePointConfigFields）同样走显式租户参数裸 SQL：UNIQUE(tenant_id) 兜底
// 并发首触，冲突后锁定读重读（照 pay EnsureWallet 口径）。
//
// 流水分页查询走 gorm 模型查询：租户插件自动追加 tenant_id 条件。

// MemberPointCurrent 是锁定读返回的会员积分当前值（行锁已持有，先读后写无并发窗）。
type MemberPointCurrent struct {
	ID           int64
	PointBalance int
}

// LockMemberPoint 取会员行锁并返回当前积分余额（spec §3.1：事务内
// SELECT ... FOR UPDATE，先锁后写）。会员行不存在/跨租户返回 gorm.ErrRecordNotFound
// （service 层翻译 404）。只能在事务内调用（唯一入账入口的第 1 步）。
func (r *Repository) LockMemberPoint(ctx context.Context, tenantID, userID int64) (*MemberPointCurrent, error) {
	var cur MemberPointCurrent
	err := r.db.WithContext(ctx).Raw(
		"SELECT `id`, `point_balance` FROM `member_user` "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL FOR UPDATE",
		userID, tenantID,
	).Scan(&cur).Error
	if err != nil {
		return nil, err
	}
	if cur.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &cur, nil
}

// UpdateMemberPointBalance 入账方向的余额更新（行锁已持有，先读后写无并发窗）：
// point_balance = point_balance + point（point 正入负出）。
//
// outflow=true（出向）时 SQL 带 `AND point_balance + ? >= 0` 条件——非负禁令
// 的 SQL 侧双保险（对齐 pay UpdateWalletBalance / ERP 负库存禁令，spec §3.4），
// 影响行数为 0 表示余额不足，409 由 service 层翻译。只能在事务内调用。
func (r *Repository) UpdateMemberPointBalance(ctx context.Context, tenantID, userID, point int64, outflow bool) (int64, error) {
	query := "UPDATE `member_user` SET `point_balance` = `point_balance` + ?, `updated_at` = ?, `updater` = ? " +
		"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL"
	args := []any{point, time.Now(), database.AuditUser(ctx), userID, tenantID}
	if outflow {
		query += " AND `point_balance` + ? >= 0"
		args = append(args, point)
	}
	res := r.db.WithContext(ctx).Exec(query, args...)
	return res.RowsAffected, res.Error
}

// InsertPointRecord 插入积分流水（append-only：只插入，不更新不删除，照
// pay_wallet_transaction 口径）。只能在事务内调用（与余额更新同事务，
// 保证 balance_after 快照与余额一致）。
func (r *Repository) InsertPointRecord(ctx context.Context, row *model.MemberPointRecord) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// ---------- 积分配置（租户级单行，惰性 upsert） ----------

// PointConfigCurrent 是配置行的裸读值（ID=0 表示行缺失，service 层按默认值兜底）。
type PointConfigCurrent struct {
	ID           int64
	PointPerYuan int
	Remark       string
}

// FindPointConfig 读租户配置行（裸 SQL 显式租户参数，可在事务内外调用）。
// 行缺失返回 (nil, nil)——不产生数据，调用方决定是否惰性建行。
func (r *Repository) FindPointConfig(ctx context.Context, tenantID int64) (*PointConfigCurrent, error) {
	var cur PointConfigCurrent
	err := r.db.WithContext(ctx).Raw(
		"SELECT `id`, `point_per_yuan`, `remark` FROM `member_point_config` "+
			"WHERE `tenant_id` = ? AND `deleted_at` IS NULL LIMIT 1",
		tenantID,
	).Scan(&cur).Error
	if err != nil {
		return nil, err
	}
	if cur.ID == 0 {
		return nil, nil
	}
	return &cur, nil
}

// EnsurePointConfig 使租户配置行存在（惰性建行，默认 point_per_yuan=1）并返回当前行。
//
// 并发安全（UNIQUE(tenant_id) 兜底并发首触，冲突重读，照 pay EnsureWallet
// 口径）：并发首触时先到者 INSERT 成功、后到者等先到者定局后收 1062，锁定读
// 重读最新已提交版本（RR 快照读会漏看并发提交的行）。只能在事务内调用
// （唯一入账入口同款的「先物化后读写」形状）。
func (r *Repository) EnsurePointConfig(ctx context.Context, tenantID int64) (*PointConfigCurrent, error) {
	if row, err := r.FindPointConfig(ctx, tenantID); err != nil || row != nil {
		return row, err
	}
	now := time.Now()
	user := database.AuditUser(ctx)
	res := r.db.WithContext(ctx).Exec(
		"INSERT INTO `member_point_config` (`tenant_id`, `point_per_yuan`, `remark`, "+
			"`created_at`, `updated_at`, `creator`, `updater`) "+
			"VALUES (?, 1, '', ?, ?, ?, ?)",
		tenantID, now, now, user, user,
	)
	if res.Error == nil {
		return r.FindPointConfig(ctx, tenantID)
	}
	if !IsDuplicateEntry(wrapDuplicateEntry(res.Error)) {
		return nil, res.Error
	}
	// 冲突重读：并发首触的赢家已提交，锁定读既拿到最新版本又顺带持有行锁。
	var cur PointConfigCurrent
	err := r.db.WithContext(ctx).Raw(
		"SELECT `id`, `point_per_yuan`, `remark` FROM `member_point_config` "+
			"WHERE `tenant_id` = ? AND `deleted_at` IS NULL LIMIT 1 FOR UPDATE",
		tenantID,
	).Scan(&cur).Error
	if err != nil {
		return nil, err
	}
	if cur.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &cur, nil
}

// UpdatePointConfigFields 更新租户配置行（行已由 EnsurePointConfig 物化）。
// 只能在事务内调用。软删行不复活（deleted_at IS NULL），影响 0 行由 service
// 层翻译（并发删除在 MVP 无入口，理论分支兜底）。
func (r *Repository) UpdatePointConfigFields(ctx context.Context, tenantID int64, pointPerYuan int, remark string) error {
	return r.db.WithContext(ctx).Exec(
		"UPDATE `member_point_config` SET `point_per_yuan` = ?, `remark` = ?, "+
			"`updated_at` = ?, `updater` = ? "+
			"WHERE `tenant_id` = ? AND `deleted_at` IS NULL",
		pointPerYuan, remark, time.Now(), database.AuditUser(ctx), tenantID,
	).Error
}

// ---------- 积分流水查询（走租户插件，租户条件自动注入） ----------

// PointRecordFilter 是积分流水分页查询条件（管理端，spec §4）。
type PointRecordFilter struct {
	UserID  int64
	BizType string
}

// pointRecordQuery 组装流水筛选查询（分页共用谓词，租户插件自动隔离）。
// biz_type 过滤值合法性由 service 层校验（400），此处只拼条件。
func (r *Repository) pointRecordQuery(ctx context.Context, f PointRecordFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.MemberPointRecord{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.BizType != "" {
		q = q.Where("biz_type = ?", f.BizType)
	}
	return q
}

// ListPointRecords 分页查询积分流水（固定 id ASC：流水重放按时间正序累计，
// 与钱包 id DESC 展示序不同——积分页首要验证「可重放」，正序即重放序）。
func (r *Repository) ListPointRecords(ctx context.Context, f PointRecordFilter, page web.PageQuery) ([]model.MemberPointRecord, int64, error) {
	q := r.pointRecordQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.MemberPointRecord
	if err := q.Order("id ASC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
