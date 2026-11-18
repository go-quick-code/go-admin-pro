// Package repository 是 mall 模块的 DAL 层（issue #174）。
//
// mall 五张 member_* 表全部是租户级数据（TenantTables，ADR 0019）：查询/
// 写入都由租户插件自动追加 tenant_id 条件，跨租户业务 ID 一律不可见（404，
// 不泄露存在性）。T1 只落会员域三表的读写原语与查询；积分两表（point_config/
// point_record）无读写方——表结构随迁移一次建全，入账原语随 T2 落地
// （唯一入账入口走显式租户参数的裸 SQL，照 pay wallet 口径）。
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
