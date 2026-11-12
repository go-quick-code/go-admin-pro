package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// wallet.go 是钱包余额账的数据访问原语与查询（issue #132，
// docs/pay-mvp-spec.md §3.4/§4.3/§4.11）。
//
// 入账四原语（EnsureWallet → LockWallet → UpdateWalletBalance →
// InsertWalletTransaction）只供 service 层唯一入账入口（service/wallet.go
// ApplyWalletChange）在事务内按序调用，任何其它路径不得调用，更不得直改
// pay_wallet.balance（spec §4.3 硬约束）。原语全部走显式租户参数的裸 SQL
//（不经租户插件，同 erp_stock / NextSequenceValue 口径），保证「锁行 → 读写」
// 与租户条件在同一语句口径上，不被插件改写。
//
// 分页/自助查询走 gorm 模型查询：租户插件自动追加 tenant_id 条件，
// 自助端点的 user_id 归属过滤在 service 层强制（spec §4.11）。

// WalletCurrent 是锁定读返回的钱包当前值（行锁已持有，先读后写无并发窗）。
type WalletCurrent struct {
	ID            int64
	Balance       money.Money
	TotalRecharge money.Money
	TotalExpense  money.Money
}

// EnsureWallet 使 (tenantID, userID, userType) 的钱包行存在（懒创建）并返回 ID。
//
// 并发安全（spec §4.3：UNIQUE 兜底并发，冲突重读）：并发首触时先到者 INSERT
// 成功、后到者被 UNIQUE(tenant_id, user_id, user_type) 拦下——INSERT 会等待
// 先到者提交/回滚，等到 1062 时先到者必然已定局，此处用 FOR UPDATE 锁定读
// 重读（RR 快照读会漏看并发提交的行，锁定读读最新已提交版本）。
// 只能在事务内调用（唯一入账入口的第 1 步）。
func (r *Repository) EnsureWallet(ctx context.Context, tenantID, userID int64, userType int8) (int64, error) {
	const byUser = "`tenant_id` = ? AND `user_id` = ? AND `user_type` = ? AND `deleted_at` IS NULL"

	var id int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT `id` FROM `pay_wallet` WHERE "+byUser, tenantID, userID, userType,
	).Scan(&id).Error; err != nil {
		return 0, err
	}
	if id > 0 {
		return id, nil
	}

	now := time.Now()
	user := database.AuditUser(ctx)
	res := r.db.WithContext(ctx).Exec(
		"INSERT INTO `pay_wallet` (`tenant_id`, `user_id`, `user_type`, `balance`, `total_recharge`, `total_expense`, "+
			"`created_at`, `updated_at`, `creator`, `updater`) "+
			"VALUES (?, ?, ?, 0.00, 0.00, 0.00, ?, ?, ?, ?)",
		tenantID, userID, userType, now, now, user, user,
	)
	if res.Error == nil {
		// 同事务同连接回读自增主键（gorm.DB 不透出 LastInsertId）。
		if err := r.db.WithContext(ctx).Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error; err != nil {
			return 0, err
		}
		return id, nil
	}
	if !IsDuplicateEntry(wrapDuplicateEntry(res.Error)) {
		return 0, res.Error
	}

	// 冲突重读：并发首触的赢家已提交，锁定读既拿到最新版本又顺带持有行锁，
	// 交给后续 LockWallet 前的窗口内行不会被改写。
	if err := r.db.WithContext(ctx).Raw(
		"SELECT `id` FROM `pay_wallet` WHERE "+byUser+" FOR UPDATE", tenantID, userID, userType,
	).Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return id, nil
}

// LockWallet 取钱包行锁并返回当前余额/累计（spec §4.3：事务内 SELECT ... FOR
// UPDATE，先锁后写）。行刚由 EnsureWallet 物化，必然存在；缺失视为数据异常
// 由 service 层翻译。只能在事务内调用。
func (r *Repository) LockWallet(ctx context.Context, tenantID, id int64) (*WalletCurrent, error) {
	var cur WalletCurrent
	err := r.db.WithContext(ctx).Raw(
		"SELECT `id`, `balance`, `total_recharge`, `total_expense` FROM `pay_wallet` "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL FOR UPDATE",
		id, tenantID,
	).Scan(&cur).Error
	if err != nil {
		return nil, err
	}
	if cur.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &cur, nil
}

// UpdateWalletBalance 入账方向的余额更新（行锁已持有，先读后写无并发窗）：
// balance = balance + price（price 正入负出），并联动非负累计列
// total_recharge/total_expense（充值侧/支出侧，由 service 层按 biz_type 给出
// 增量并在锁内校验非负）。
//
// outflow=true（出向）时 SQL 带 `AND balance + ? >= 0` 条件——负余额禁令的
// SQL 侧双保险（对齐 ERP 负库存禁令，spec §4.3），影响行数为 0 表示余额不足，
// 409 由 service 层翻译（提示当前余额/变更量）。只能在事务内调用。
func (r *Repository) UpdateWalletBalance(ctx context.Context, tenantID, id int64, price, rechargeDelta, expenseDelta money.Money, outflow bool) (int64, error) {
	query := "UPDATE `pay_wallet` SET `balance` = `balance` + ?, `total_recharge` = `total_recharge` + ?, " +
		"`total_expense` = `total_expense` + ?, `updated_at` = ?, `updater` = ? " +
		"WHERE `id` = ? AND `tenant_id` = ? AND `deleted_at` IS NULL"
	args := []any{price, rechargeDelta, expenseDelta, time.Now(), database.AuditUser(ctx), id, tenantID}
	if outflow {
		query += " AND `balance` + ? >= 0"
		args = append(args, price)
	}
	res := r.db.WithContext(ctx).Exec(query, args...)
	return res.RowsAffected, res.Error
}

// InsertWalletTransaction 插入钱包流水（append-only：只插入，不更新不删除）。
// 只能在事务内调用（与余额更新同事务，保证快照一致）。
func (r *Repository) InsertWalletTransaction(ctx context.Context, row *model.WalletTransaction) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// ---------- 查询（分页/归属查找；走租户插件，租户条件自动注入） ----------

// WalletFilter 是钱包分页查询条件（管理端，spec §5）。
type WalletFilter struct {
	UserID   int64
	UserType *int8
}

// WalletTransactionFilter 是钱包流水分页查询条件。
type WalletTransactionFilter struct {
	WalletID int64
	BizType  *int8
	No       string
}

// FindWalletByUser 按归属查找钱包（自助端点「我的钱包」）：租户条件由插件注入。
// 未找到返回 (nil, nil)。
func (r *Repository) FindWalletByUser(ctx context.Context, userID int64, userType int8) (*model.Wallet, error) {
	var row model.Wallet
	err := r.db.WithContext(ctx).Where("user_id = ? AND user_type = ?", userID, userType).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// FindWalletByID 按 ID 查钱包（存在性校验/详情）：未找到返回 (nil, nil)。
func (r *Repository) FindWalletByID(ctx context.Context, id int64) (*model.Wallet, error) {
	var row model.Wallet
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// walletQuery 组装钱包筛选查询（分页共用谓词，租户插件自动隔离）。
func (r *Repository) walletQuery(ctx context.Context, f WalletFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Wallet{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.UserType != nil {
		q = q.Where("user_type = ?", *f.UserType)
	}
	return q
}

// walletTransactionQuery 组装钱包流水筛选查询（分页与导出共用同一谓词）。
func (r *Repository) walletTransactionQuery(ctx context.Context, f WalletTransactionFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.WalletTransaction{})
	if f.WalletID > 0 {
		q = q.Where("wallet_id = ?", f.WalletID)
	}
	if f.BizType != nil {
		q = q.Where("biz_type = ?", *f.BizType)
	}
	if f.No != "" {
		q = q.Where("no = ?", f.No)
	}
	return q
}

// ListWallets 分页查询钱包（固定 id DESC，spec §5）。
func (r *Repository) ListWallets(ctx context.Context, f WalletFilter, offset, limit int) ([]model.Wallet, int64, error) {
	q := r.walletQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Wallet
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListWalletTransactions 分页查询钱包流水（固定 id DESC，spec §5）。
func (r *Repository) ListWalletTransactions(ctx context.Context, f WalletTransactionFilter, offset, limit int) ([]model.WalletTransaction, int64, error) {
	q := r.walletTransactionQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.WalletTransaction
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllWalletTransactions 按筛选查询全部钱包流水（Excel 导出用，不分页；排序
// 与列表一致 id DESC，#138 spec §4.12）。行数无上限：与 CRM/ERP ListAll 同口径。
func (r *Repository) ListAllWalletTransactions(ctx context.Context, f WalletTransactionFilter) ([]model.WalletTransaction, error) {
	var list []model.WalletTransaction
	if err := r.walletTransactionQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
