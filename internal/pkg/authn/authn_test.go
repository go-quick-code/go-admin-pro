package authn_test

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
)

// TestBearerToken 覆盖 BearerToken 的全部口径（与抽取前 system 中间件 /
// notify 推送入口的逐字实现一致，语义不变）：
//   - 「Bearer <token>」→ token（大小写不敏感、容忍多余空白）；
//   - 无 Bearer 前缀 → 原样返回去空白的头值；
//   - 头缺失（空串）→ 空串。
func TestBearerToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"标准Bearer", "Bearer abc.def.ghi", "abc.def.ghi"},
		{"小写scheme", "bearer jwt-token", "jwt-token"},
		{"多余空白", "Bearer   jwt-token  ", "jwt-token"},
		{"裸token", "jwt-token", "jwt-token"},
		{"非Bearer方案", "Basic dXNlcjpwYXNz", "Basic dXNlcjpwYXNz"},
		{"头缺失", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authn.BearerToken(tc.header); got != tc.want {
				t.Fatalf("BearerToken(%q) = %q，期望 %q", tc.header, got, tc.want)
			}
		})
	}
}
