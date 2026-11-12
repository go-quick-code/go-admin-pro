package config_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/config"
)

// PR #140 安全审查 P3-3：pay.notify-hmac-key 随仓库带已知默认值
// （go-admin-pro-pay-notify-dev-key），生产照抄示例配置时 demo 回调三端点
// 的签名认证对任何读过仓库的人失效。按 validateJWTSecret（PR #39 P2）同型
// 收口：非 dev/test 环境空值或已知默认值一律拒绝启动；dev/test 保留默认值。

// payNotifyKeyConfig 生成被测配置 YAML（jwt.secret 用强随机值隔离 JWT 校验，
// 被测变量只留 pay.notify-hmac-key）。
func payNotifyKeyConfig(env, key string) string {
	return `app:
  env: "` + env + `"
  timezone: Asia/Shanghai
jwt:
  secret: 140sec-test-jwt-strong-secret
pay:
  notify-hmac-key: "` + key + `"
`
}

// TestLoadRejectsInsecurePayNotifyKeyOutsideDev 非 dev/test 环境：空值与已知
// 默认密钥拒绝启动（错误指明 pay.notify-hmac-key），自定义强密钥通过。
func TestLoadRejectsInsecurePayNotifyKeyOutsideDev(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		key     string
		wantErr bool
	}{
		{"production 空密钥拒绝", "production", "", true},
		{"production 已知默认值拒绝", "production", config.DefaultDevPayNotifyHMACKey, true},
		{"prod 已知默认值拒绝", "prod", config.DefaultDevPayNotifyHMACKey, true},
		{"production 自定义强密钥通过", "production", "140sec-pay-notify-strong-key", false},
		{"dev 默认值放行", "dev", config.DefaultDevPayNotifyHMACKey, false},
		{"test 默认值放行", "test", config.DefaultDevPayNotifyHMACKey, false},
		{"未配置 env 视为 dev 放行", "", config.DefaultDevPayNotifyHMACKey, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			path := writeConfig(t, payNotifyKeyConfig(tt.env, tt.key))
			cfg, err := config.Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("env=%q 使用空/默认 pay.notify-hmac-key 应启动失败，实际通过", tt.env)
				}
				if !strings.Contains(err.Error(), "pay.notify-hmac-key") {
					t.Fatalf("错误信息应指明 pay.notify-hmac-key，实际 %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("env=%q 应放行（dev/test 便利）: %v", tt.env, err)
			}
			if cfg.Pay.NotifyHMACKey != tt.key {
				t.Fatalf("Pay.NotifyHMACKey = %q，期望 %q", cfg.Pay.NotifyHMACKey, tt.key)
			}
		})
	}
}

// TestLoadRejectsInsecurePayNotifyKeyFromEnvOutsideDev 环境变量覆盖为已知
// 默认密钥同样拒绝（GAP_PAY_NOTIFY_HMAC_KEY 在生产必须设为唯一强密钥）。
func TestLoadRejectsInsecurePayNotifyKeyFromEnvOutsideDev(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GAP_APP_ENV", "production")
	t.Setenv("GAP_PAY_NOTIFY_HMAC_KEY", config.DefaultDevPayNotifyHMACKey)
	path := writeConfig(t, payNotifyKeyConfig("production", "140sec-config-file-strong-key"))
	if _, err := config.Load(path); err == nil {
		t.Fatal("GAP_PAY_NOTIFY_HMAC_KEY 覆盖为已知默认密钥时应启动失败")
	}
}
