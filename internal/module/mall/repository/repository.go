// Package repository 是 mall 模块的 DAL 层（issue #174 起步）。
//
// mall 全部业务表（member_/product_/trade_/mall_sequence）都是租户级数据
// （TenantTables，ADR 0019）：查询/写入都由租户插件自动追加 tenant_id 条件，
// 跨租户业务 ID 一律不可见（404，不泄露存在性）。跨行不变量的写路径（积分
// 唯一入账入口、下单扣库存、取消回补、单号发放）走显式租户参数的裸 SQL，
// 照 pay wallet / erp_stock 口径（不经租户插件，保证「锁行 → 读写」与租户
// 条件在同一语句口径上，不被插件改写）。
package repository

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Repository 是 mall 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 mall Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 返回底层 gorm 句柄（跨模块/事务内传递同一事务时使用）。
func (r *Repository) DB() *gorm.DB { return r.db }

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository
// （与 pay 同构）：T2 的积分唯一入账入口（锁行 → 校验 → 条件 UPDATE → 插流水）
// 依赖同一事务；T1 的 member/level 写入是单语句，不经此入口。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}

// wrapDuplicateEntry 把 MySQL 1062 重复键错误规整为 gorm.ErrDuplicatedKey 链，
// service 层据此翻译成业务 409（issue #174：mobile/name/level 唯一冲突 409）。
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

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}
