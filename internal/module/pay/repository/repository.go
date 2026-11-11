// Package repository 是 pay 模块的 DAL 层。
//
// pay_channel 是平台级表（未登记 TenantTables）：本层渠道查询显式 tenant.Skip，
// 表示「这是系统级读取」，不依赖租户插件放行。应用/绑定是租户级表：查询/
// 写入都由租户插件自动追加 tenant_id 条件，跨租户业务 ID 一律不可见。
// 序列发放是裸 SQL（不经插件），租户 ID 为显式入参（同 erp_sequence）。
package repository

import (
	"context"
	"errors"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Repository 是 pay 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 pay Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 返回底层 gorm 句柄（跨模块/事务内传递同一事务时使用）。
func (r *Repository) DB() *gorm.DB { return r.db }

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository
// （与 erp 同构）：唯一入账入口的「锁钱包行 → 条件 UPDATE → 发号 → 插流水」
// 与后续票的单据落库都依赖同一事务。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// platform 返回平台级表（pay_channel）的查询句柄：显式 tenant.Skip。
func (r *Repository) platform(ctx context.Context) *gorm.DB {
	return tenant.Skip(r.db.WithContext(ctx))
}

// wrapDuplicateEntry 把 MySQL 1062 重复键错误规整为 gorm.ErrDuplicatedKey 链，
// service 层据此翻译成业务 409/400。
func wrapDuplicateEntry(err error) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return errors.Join(gorm.ErrDuplicatedKey, err)
	}
	return err
}

// IsDuplicateEntry 报告错误是否为唯一键冲突（wrapDuplicateEntry 规整后的形态）。
func IsDuplicateEntry(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
