package tenant

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrNoTenantContext 是 fail-closed 的哨兵错误：对租户表的操作缺少租户上下文。
// 系统级操作（seed、定时任务等）应改用 tenant.Skip 显式跳过，而不是靠插件放行。
var ErrNoTenantContext = errors.New("缺少租户上下文")

// ContextKey 用于在 context 中携带租户 ID。
type contextKey struct{}

var ctxKey = contextKey{}

// DefaultID 是系统默认租户（内置管理后台）。
const DefaultID int64 = 1

// WithTenantID 把租户 ID 写入 context。
func WithTenantID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxKey, id)
}

// FromContext 从 context 读取租户 ID。
func FromContext(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	id, ok := ctx.Value(ctxKey).(int64)
	return id, ok && id > 0
}

// ID 返回 context 中的租户 ID，不存在时返回默认租户。
func ID(ctx context.Context) int64 {
	if id, ok := FromContext(ctx); ok {
		return id
	}
	return DefaultID
}

// ignoreKey 是 GORM Statement 上跳过租户过滤的标记。
const ignoreKey = "tenant:ignore"

// Skip 让本次查询/更新/删除跳过租户过滤，仅用于系统级操作。
func Skip(db *gorm.DB) *gorm.DB {
	return db.Set(ignoreKey, true)
}

// IsSkipped 判断本次操作是否跳过租户过滤。
func IsSkipped(db *gorm.DB) bool {
	if db.Statement == nil {
		return false
	}
	v, ok := db.Statement.Settings.Load(ignoreKey)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}
