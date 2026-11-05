// context.go 汇聚 crm 业务层共用的请求上下文读取（认证主体来自 authn）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
)

// CurrentUserID 返回 context 中的当前用户 ID。
func CurrentUserID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.UserID
	}
	return 0
}

// currentTenantID 返回 context 中的当前租户 ID；租户中间件保证登录链路必有值。
func currentTenantID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.TenantID
	}
	return 0
}
