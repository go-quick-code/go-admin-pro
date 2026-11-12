package config_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/config"
)

// PR #39 审查 P2：非 dev/test 环境必须显式覆盖可预测的 JWT 默认密钥，
// 否则拒绝启动；dev/test 保留默认值以便本地开发与集成测试。

// clearConfigEnv 清掉会影响 Load 的环境变量（测试进程可能继承外部值）。
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GAP_JWT_SECRET", "GAP_APP_ENV"} {
		t.Setenv(name, "")
	}
}

func TestLoadRejectsInsecureJWTSecretOutsideDev(t *testing.T) {
	for _, secret := range []string{
		"go-admin-pro-dev-secret",
		"change-me-in-production-9f8a7b6c5d4e3f2a",
		"please-change-me",
		"",
	} {
		name := secret
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			clearConfigEnv(t)
			path := writeConfig(t, `app:
  env: production
  timezone: Asia/Shanghai
jwt:
  secret: "`+secret+`"
pay:
  notify-hmac-key: 140sec-jwt-test-strong-key
`)
			cfg, err := config.Load(path)
			if err == nil {
				t.Fatalf("生产环境使用默认/空 JWT 密钥应启动失败，实际 cfg.JWT.Secret=%q", cfg.JWT.Secret)
			}
			if !strings.Contains(err.Error(), "jwt.secret") {
				t.Fatalf("错误信息应指明 jwt.secret，实际 %q", err.Error())
			}
		})
	}
}

func TestLoadRejectsInsecureJWTSecretFromEnvOutsideDev(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GAP_APP_ENV", "prod")
	t.Setenv("GAP_JWT_SECRET", "please-change-me")
	path := writeConfig(t, `app:
  timezone: Asia/Shanghai
jwt:
  secret: custom-and-still-replaced
pay:
  notify-hmac-key: 140sec-jwt-test-strong-key
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("环境变量覆盖为已知默认密钥时应启动失败")
	}
}

func TestLoadAcceptsCustomJWTSecretOutsideDev(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `app:
  env: production
  timezone: Asia/Shanghai
jwt:
  secret: 6c1f3b2a-strong-unique-secret
pay:
  notify-hmac-key: 140sec-jwt-test-strong-key
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("自定义强密钥应通过启动校验: %v", err)
	}
	if cfg.JWT.Secret != "6c1f3b2a-strong-unique-secret" {
		t.Fatalf("JWT.Secret = %q", cfg.JWT.Secret)
	}
}

func TestLoadAllowsDefaultJWTSecretInDevAndTest(t *testing.T) {
	for _, env := range []string{"dev", "test", ""} {
		name := env
		if name == "" {
			name = "empty-defaults-dev"
		}
		t.Run(name, func(t *testing.T) {
			clearConfigEnv(t)
			path := writeConfig(t, `app:
  env: "`+env+`"
  timezone: Asia/Shanghai
`)
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("dev/test（含未配置 env）应允许默认 JWT 密钥: %v", err)
			}
			if cfg.JWT.Secret != config.DefaultDevJWTSecret {
				t.Fatalf("dev/test 空密钥应回退 %q，实际 %q", config.DefaultDevJWTSecret, cfg.JWT.Secret)
			}
		})
	}
}
