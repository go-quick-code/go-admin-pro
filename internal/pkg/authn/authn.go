// Package authn 提供登录主体（Principal）的上下文传递与 HTTP 凭证提取：
// 认证中间件用 With 把登录主体写入请求上下文，业务代码用 From 读取；
// BearerToken 从 Authorization 头值提取 Bearer 凭证，system 中间件与
// notify 推送入口共用同一实现（PR #86 审查意见：消除两处逐字重复）。
package authn

import (
	"context"
	"strings"
)

// Principal 是当前登录主体。
type Principal struct {
	UserID   int64
	TenantID int64
	Username string
}

type contextKey struct{}

var ctxKey = contextKey{}

func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey, p)
}

func From(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(ctxKey).(Principal)
	return p, ok
}

// BearerToken 从 Authorization 头值解析 Bearer token（语义与抽取前的
// system/api 中间件、notify/api 推送入口的逐字实现一致）：
//   - 「Bearer <token>」（scheme 大小写不敏感、容忍多余空白）→ 返回 token；
//   - 其余格式（裸 token、无 Bearer 前缀）→ 返回去空白后的整个头值；
//   - 空串 → 空串。
//
// 入参是原始头值而非 gin.Context：internal/pkg 保持 HTTP 无关
// （CODING_STANDARDS），gin 侧由调用方传 c.GetHeader("Authorization")；
// 是否校验与失败如何响应由调用方决定，本函数只做提取。
func BearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(header)
}
