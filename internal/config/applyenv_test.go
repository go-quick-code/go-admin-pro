package config

import "testing"

func TestApplyEnvSeedDemo(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		configured bool
		initial    bool
		want       bool
	}{
		{name: "未配置默认关闭", env: "", configured: false, want: false},
		{name: "显式开启保持开启", env: "", configured: true, initial: true, want: true},
		{name: "显式关闭保持关闭", env: "", configured: true, initial: false, want: false},
		{name: "环境变量开启", env: "true", configured: false, want: true},
		{name: "环境变量大写开启", env: "TRUE", configured: true, initial: false, want: true},
		{name: "环境变量关闭", env: "false", configured: false, want: false},
		{name: "环境变量关闭覆盖显式开启", env: "false", configured: true, initial: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GAP_SEED_DEMO", tt.env)
			cfg := &Config{}
			if tt.configured {
				initial := tt.initial
				cfg.Database.SeedDemo = &initial
			}
			cfg.applyEnv()
			if got := cfg.SeedDemoEnabled(); got != tt.want {
				t.Fatalf("GAP_SEED_DEMO=%q（configured=%v initial=%v）: demo = %v, want %v",
					tt.env, tt.configured, tt.initial, got, tt.want)
			}
		})
	}
}

// TestSeedDemoEnabledDefaultsOff 验证未配置 seed-demo 时默认关闭演示数据
// （指针 nil，PR #39 审查 P2：演示数据必须显式开启，避免污染生产库），
// 显式 true 才开启。
func TestSeedDemoEnabledDefaultsOff(t *testing.T) {
	cfg := &Config{}
	if cfg.SeedDemoEnabled() {
		t.Fatal("未配置 database.seed-demo 时应默认关闭")
	}
	enabled := true
	cfg.Database.SeedDemo = &enabled
	if !cfg.SeedDemoEnabled() {
		t.Fatal("显式 seed-demo=true 时应开启")
	}
	disabled := false
	cfg.Database.SeedDemo = &disabled
	if cfg.SeedDemoEnabled() {
		t.Fatal("显式 seed-demo=false 时应关闭")
	}
}

func TestApplyEnvSeed(t *testing.T) {
	tests := []struct {
		env  string
		seed bool
		want bool
	}{
		{env: "", seed: true, want: true},
		{env: "true", seed: false, want: true},
		{env: "TRUE", seed: false, want: true},
		{env: "false", seed: true, want: false},
		{env: "false", seed: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("GAP_SEED", tt.env)
			cfg := &Config{Database: DatabaseConfig{Seed: tt.seed}}
			cfg.applyEnv()
			if cfg.Database.Seed != tt.want {
				t.Fatalf("GAP_SEED=%q: seed = %v, want %v", tt.env, cfg.Database.Seed, tt.want)
			}
		})
	}
}
