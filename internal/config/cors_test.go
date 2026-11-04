package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCORSConfig 把配置内容写入临时文件，返回路径。
func writeCORSConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// app.cors.allowed-origins 从 YAML 读入；缺省（未配置）为空 = 不放行任何跨域。
func TestLoadCORSAllowedOrigins(t *testing.T) {
	path := writeCORSConfig(t, `app:
  name: go-admin-pro
  timezone: UTC
  cors:
    allowed-origins:
      - http://localhost:5173
      - https://admin.example.com
database:
  dsn: root:123456@tcp(127.0.0.1:3306)/go_admin_pro
jwt:
  secret: test-secret
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置: %v", err)
	}
	got := cfg.App.CORS.AllowedOrigins
	if len(got) != 2 || got[0] != "http://localhost:5173" || got[1] != "https://admin.example.com" {
		t.Fatalf("allowed-origins 读取不符：实际 %v", got)
	}

	path = writeCORSConfig(t, `app:
  name: go-admin-pro
  timezone: UTC
database:
  dsn: root:123456@tcp(127.0.0.1:3306)/go_admin_pro
jwt:
  secret: test-secret
`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("加载配置: %v", err)
	}
	if len(cfg.App.CORS.AllowedOrigins) != 0 {
		t.Fatalf("未配置 CORS 白名单应为空（拒绝所有跨域），实际 %v", cfg.App.CORS.AllowedOrigins)
	}
}

// GAP_CORS_ALLOWED_ORIGINS 以逗号分隔覆盖白名单，供部署侧不改文件调整。
func TestApplyEnvCORSAllowedOrigins(t *testing.T) {
	cfg := &Config{}
	t.Setenv("GAP_CORS_ALLOWED_ORIGINS", "http://a.example.com, http://b.example.com , ,")
	cfg.applyEnv()
	got := cfg.App.CORS.AllowedOrigins
	if len(got) != 2 || got[0] != "http://a.example.com" || got[1] != "http://b.example.com" {
		t.Fatalf("环境变量覆盖不符：实际 %v", got)
	}
}
