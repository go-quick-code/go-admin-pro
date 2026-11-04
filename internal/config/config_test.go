package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/config"
)

// writeConfig 把配置内容写入临时文件，返回路径。
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadTimezoneDefault(t *testing.T) {
	path := writeConfig(t, `app:
  name: go-admin-pro
database:
  dsn: root:123456@tcp(127.0.0.1:3306)/go_admin_pro
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.App.Timezone != "Asia/Shanghai" {
		t.Errorf("App.Timezone = %q, want %q", cfg.App.Timezone, "Asia/Shanghai")
	}
	loc := cfg.Location()
	if loc == nil {
		t.Fatal("Location() = nil, want Asia/Shanghai")
	}
	if loc.String() != "Asia/Shanghai" {
		t.Errorf("Location() = %q, want Asia/Shanghai", loc.String())
	}
}

func TestLoadTimezoneInvalid(t *testing.T) {
	path := writeConfig(t, `app:
  timezone: Not/AZone
`)

	cfg, err := config.Load(path)
	if err == nil {
		t.Fatalf("load config with invalid timezone: want error, got cfg %+v", cfg)
	}
	if !strings.Contains(err.Error(), "Not/AZone") {
		t.Errorf("error %q should mention the invalid value Not/AZone", err.Error())
	}
}

func TestLoadTimezoneExplicitValid(t *testing.T) {
	path := writeConfig(t, `app:
  timezone: UTC
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Location().String(); got != "UTC" {
		t.Errorf("Location() = %q, want UTC", got)
	}
}

func TestLoadDSNLocFollowsTimezone(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantDSN  string
	}{
		{
			name: "默认时区覆盖 DSN 里的 loc=Local",
			yaml: `app:
database:
  dsn: root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=Local
`,
			wantDSN: "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai",
		},
		{
			name: "显式时区写入没有 loc 的 DSN",
			yaml: `app:
  timezone: UTC
database:
  dsn: root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True
`,
			wantDSN: "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(writeConfig(t, tt.yaml))
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if cfg.Database.DSN != tt.wantDSN {
				t.Errorf("Database.DSN =\n  %s\nwant:\n  %s", cfg.Database.DSN, tt.wantDSN)
			}
		})
	}
}

func TestApplyDSNLoc(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		timezone string
		want     string
	}{
		{
			name:     "覆盖已有 loc",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=Local",
			timezone: "Asia/Shanghai",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai",
		},
		{
			name:     "没有 loc 时追加",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True",
			timezone: "Asia/Shanghai",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai",
		},
		{
			name:     "完全没有参数时以 ? 开头追加",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro",
			timezone: "Asia/Shanghai",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?loc=Asia%2FShanghai",
		},
		{
			name:     "时区名 URL 编码",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?parseTime=True",
			timezone: "Asia/Shanghai",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?parseTime=True&loc=Asia%2FShanghai",
		},
		{
			name:     "无需编码的时区名保持原样",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro",
			timezone: "UTC",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?loc=UTC",
		},
		{
			name:     "密码包含斜杠不影响定位参数段",
			dsn:      "root:p/ss@tcp(127.0.0.1:3306)/go_admin_pro?loc=UTC",
			timezone: "Asia/Shanghai",
			want:     "root:p/ss@tcp(127.0.0.1:3306)/go_admin_pro?loc=Asia%2FShanghai",
		},
		{
			name:     "重复 loc 只保留一份",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?loc=UTC&loc=Local",
			timezone: "Asia/Shanghai",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?loc=Asia%2FShanghai",
		},
		{
			name:     "其余参数原样保留",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&interpolateParams=True",
			timezone: "UTC",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&interpolateParams=True&loc=UTC",
		},
		{
			name:     "空时区不改写 DSN",
			dsn:      "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?loc=Local",
			timezone: "",
			want:     "root:123456@tcp(127.0.0.1:3306)/go_admin_pro?loc=Local",
		},
		{
			name:     "空 DSN 原样返回",
			dsn:      "",
			timezone: "Asia/Shanghai",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.ApplyDSNLoc(tt.dsn, tt.timezone)
			if got != tt.want {
				t.Errorf("ApplyDSNLoc(%q, %q) =\n  %s\nwant:\n  %s", tt.dsn, tt.timezone, got, tt.want)
			}
		})
	}
}
