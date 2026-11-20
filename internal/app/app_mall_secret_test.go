package app

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/config"
)

// mall 会员令牌签名密钥 fail-closed（issue #178）：app.New 装配首检——密钥
// 为空（含纯空白）时拒绝启动，错误信息指明 mall.jwt_secret 与补救途径。
// 本检查放在 database.Open 之前，单测无需真实 MySQL 即可断言拒绝行为。
func TestNewRejectsEmptyMallJWTSecret(t *testing.T) {
	for _, secret := range []string{"", "   "} {
		cfg := &config.Config{}
		cfg.Mall.JWTSecret = secret
		_, err := New(cfg, slog.New(slog.DiscardHandler))
		if err == nil {
			t.Fatalf("mall.jwt_secret=%q 应拒绝启动（fail-closed），实际未报错", secret)
		}
		if !strings.Contains(err.Error(), "mall.jwt_secret") {
			t.Fatalf("错误信息应指明 mall.jwt_secret，实际 %q", err.Error())
		}
		if !strings.Contains(err.Error(), "GAP_MALL_JWT_SECRET") {
			t.Fatalf("错误信息应指明环境变量 GAP_MALL_JWT_SECRET，实际 %q", err.Error())
		}
	}
}
