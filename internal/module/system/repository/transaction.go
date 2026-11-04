package repository

import (
	"context"

	"gorm.io/gorm"
)

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository。
// 需要原子落库的多步写操作（如创建用户并绑定角色/岗位、创建租户并初始化
// 部门/角色/管理员）应包在这里面：任一步失败整体回滚，不留半套数据。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}
