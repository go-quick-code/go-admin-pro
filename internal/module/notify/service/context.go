// context.go 汇聚 notify 业务层的请求上下文读取（认证主体来自 authn）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// requireUser 返回当前登录用户 ID；通知是个人数据，读取/已读操作一律以
// 登录主体收口（fail-closed：取不到主体直接拒绝）。
func requireUser(ctx context.Context) (int64, error) {
	if p, ok := authn.From(ctx); ok && p.UserID > 0 {
		return p.UserID, nil
	}
	return 0, xerr.Unauthorized("未登录")
}
