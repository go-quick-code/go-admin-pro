// Package repository 是 erp 模块的 DAL 层。
// 所有查询/写入通过租户插件自动带 tenant_id 条件（erp_* 六张主数据表已登记
// TenantTables，internal/pkg/database/tenant_tables.go），本层不手工拼主表的
// 租户条件；后续票的单据表落地迁移时同样登记。
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// Repository 是 erp 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 erp Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 返回底层 gorm 句柄（跨模块/事务内传递同一事务时使用）。
func (r *Repository) DB() *gorm.DB { return r.db }

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository
// （与 crm 同构；后续票的编号发放与落库同事务依赖它）。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}

// notFound 把 gorm.ErrRecordNotFound 规整为 nil（service 层统一翻译 404）。
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}
