package config

import "testing"

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
