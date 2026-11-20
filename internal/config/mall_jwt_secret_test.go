package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/config"
)

// mall 会员令牌签名密钥与有效期（issue #178）：dev/test 兜底 dev 默认值
//（与 jwt.secret 的 dev 便利同型）；非 dev/test 环境使用已知默认值拒绝
// 启动（validateMallJWTSecret，与 validatePayNotifyHMACKey 同型）——空值
// 由 app.New 装配首检 fail-closed 拒启（internal/app，Load 对空值放行，
// 见 normalize 注释）。token-ttl 缺省 7 天，可显式覆盖。

// mallJWTConfig 生成被测配置 YAML（jwt.secret/pay.notify-hmac-key 用强随机
// 值隔离既有校验，被测变量只留 mall 段）。
func mallJWTConfig(env string, withMall bool) string {
	out := `app:
  env: "` + env + `"
  timezone: Asia/Shanghai
jwt:
  secret: mall-jwt-test-strong-jwt-secret
pay:
  notify-hmac-key: mall-jwt-test-strong-pay-key
`
	if withMall {
		out += `mall:
  jwt-secret: go-admin-pro-mall-jwt-dev-key
`
	}
	return out
}

func TestLoadMallJWTSecretDevFallback(t *testing.T) {
	path := writeConfig(t, mallJWTConfig("dev", false))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("dev 环境未配置 mall 段应兜底默认值，实际报错: %v", err)
	}
	if cfg.Mall.JWTSecret != config.DefaultDevMallJWTSecret {
		t.Fatalf("dev 兜底密钥 = %q, want %q", cfg.Mall.JWTSecret, config.DefaultDevMallJWTSecret)
	}
	if cfg.Mall.TokenTTL != 7*24*time.Hour {
		t.Fatalf("token-ttl 缺省 = %v, want 168h（7 天）", cfg.Mall.TokenTTL)
	}
}

func TestLoadMallJWTSecretRejectsKnownDefaultOutsideDev(t *testing.T) {
	path := writeConfig(t, mallJWTConfig("production", true))
	if _, err := config.Load(path); err == nil {
		t.Fatalf("production 使用已知默认 mall.jwt-secret 应拒绝启动")
	} else if !strings.Contains(err.Error(), "mall.jwt-secret") {
		t.Fatalf("错误信息应指明 mall.jwt-secret，实际 %q", err.Error())
	}
}

func TestLoadMallJWTSecretStrongValueOutsideDev(t *testing.T) {
	path := writeConfig(t, mallJWTConfig("production", false)+`mall:
  jwt-secret: mall-jwt-test-strong-mall-secret
  token-ttl: 48h
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("production 强随机密钥应可启动: %v", err)
	}
	if cfg.Mall.JWTSecret != "mall-jwt-test-strong-mall-secret" {
		t.Fatalf("mall.jwt-secret = %q, want 显式配置值", cfg.Mall.JWTSecret)
	}
	if cfg.Mall.TokenTTL != 48*time.Hour {
		t.Fatalf("token-ttl = %v, want 48h", cfg.Mall.TokenTTL)
	}
}

func TestLoadMallJWTSecretFromEnv(t *testing.T) {
	t.Setenv("GAP_MALL_JWT_SECRET", "mall-jwt-test-env-secret")
	path := writeConfig(t, mallJWTConfig("dev", false))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Mall.JWTSecret != "mall-jwt-test-env-secret" {
		t.Fatalf("GAP_MALL_JWT_SECRET 应覆盖配置，实际 %q", cfg.Mall.JWTSecret)
	}
}
