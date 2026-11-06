// Package repository 是 oa 模块的 DAL 层。
//
// 跨模块取数（用户/部门/权限解析）按仓库约定走 system 模块网关
// （见 oa/service.SystemGateway 与 system/service/gateway.go），
// 不在 oa 侧直查 system 表。OA 实体表的查询随建表工单进入本包：
// 工作日历（issue #50，00017_oa_workday_calendar）已登记 TenantTables，
// 本层查询经租户插件自动带 tenant_id 条件，不手工拼主表租户条件。
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// Repository 是 oa 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 oa Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 返回底层句柄（事务内即事务句柄）：公告发布需要把 tx 传给 notify 的
// CreateInTx seam（spec §4.13：通知与源操作同一事务），业务仓储操作仍走本
// 仓储方法（与 bpm repository 同款约定）。
func (r *Repository) DB() *gorm.DB { return r.db }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}
