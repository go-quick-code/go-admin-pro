package authn

import "context"

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
