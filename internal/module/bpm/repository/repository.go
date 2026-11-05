// Package repository 是 bpm 模块的 DAL 层（issue #44）。
// 定义/实例/任务/审批记录四张表已登记 TenantTables，本层查询经租户插件
// 自动带 tenant_id 条件，不手工拼主表租户条件；列名 key 为 MySQL 保留字，
// 原生条件里一律反引号包裹。
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// Repository 是 bpm 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 bpm Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 返回底层句柄（事务内的 *gorm.DB）：BusinessHandler 回调（spec §4.4
// 入参即事务）与 notify seam 需要原生事务句柄，业务仓储操作仍走本仓储方法。
func (r *Repository) DB() *gorm.DB { return r.db }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository。
// 发布（同 key 旧版本停用 + 新版本置发布）必须同事务完成（spec §4.1）。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}
