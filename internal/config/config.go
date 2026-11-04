package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 是应用的完整配置，对应 configs/config.yaml 的层级结构。
type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Database DatabaseConfig `mapstructure:"database"`
	JWT      JWTConfig      `mapstructure:"jwt"`
	Security SecurityConfig `mapstructure:"security"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Tenant   TenantConfig   `mapstructure:"tenant"`
	Logger   LoggerConfig   `mapstructure:"logger"`
}

type AppConfig struct {
	Name      string `mapstructure:"name"`
	Env       string `mapstructure:"env"`
	Port      int    `mapstructure:"port"`
	APIPrefix string `mapstructure:"api-prefix"`
	WebRoot   string `mapstructure:"web-root"`
}

type DatabaseConfig struct {
	DSN          string `mapstructure:"dsn"`
	MaxIdleConns int    `mapstructure:"max-idle-conns"`
	MaxOpenConns int    `mapstructure:"max-open-conns"`
	Seed         bool   `mapstructure:"seed"`
}

type JWTConfig struct {
	Secret string        `mapstructure:"secret"`
	Expire time.Duration `mapstructure:"expire"`
	Issuer string        `mapstructure:"issuer"`
}

type SecurityConfig struct {
	Captcha CaptchaConfig `mapstructure:"captcha"`
	Login   LoginConfig   `mapstructure:"login"`
}

type CaptchaConfig struct {
	Enabled bool `mapstructure:"enabled"`
	Width   int  `mapstructure:"width"`
	Height  int  `mapstructure:"height"`
	Length  int  `mapstructure:"length"`
}

type LoginConfig struct {
	MaxFail      int           `mapstructure:"max-fail"`
	LockDuration time.Duration `mapstructure:"lock-duration"`
}

type StorageConfig struct {
	Local LocalStorageConfig `mapstructure:"local"`
}

type LocalStorageConfig struct {
	BasePath     string `mapstructure:"base-path"`
	PublicPrefix string `mapstructure:"public-prefix"`
}

type TenantConfig struct {
	Enabled   bool  `mapstructure:"enabled"`
	DefaultID int64 `mapstructure:"default-id"`
}

type LoggerConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Load 读取配置文件并应用环境变量覆盖。
// 支持通过 GAP_DATABASE_DSN、GAP_JWT_SECRET、GAP_APP_PORT 覆盖关键配置。
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.applyDefaults()
	cfg.applyEnv()
	cfg.normalize()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.App.Name == "" {
		c.App.Name = "go-admin-pro"
	}
	if c.App.Port == 0 {
		c.App.Port = 8080
	}
	if c.App.APIPrefix == "" {
		c.App.APIPrefix = "/admin-api"
	}
	if c.JWT.Expire == 0 {
		c.JWT.Expire = 24 * time.Hour
	}
	if c.JWT.Issuer == "" {
		c.JWT.Issuer = c.App.Name
	}
	if c.Security.Login.MaxFail == 0 {
		c.Security.Login.MaxFail = 5
	}
	if c.Security.Login.LockDuration == 0 {
		c.Security.Login.LockDuration = 10 * time.Minute
	}
	if c.Security.Captcha.Width == 0 {
		c.Security.Captcha.Width = 120
	}
	if c.Security.Captcha.Height == 0 {
		c.Security.Captcha.Height = 40
	}
	if c.Security.Captcha.Length == 0 {
		c.Security.Captcha.Length = 4
	}
	if c.Storage.Local.BasePath == "" {
		c.Storage.Local.BasePath = "./uploads"
	}
	if c.Storage.Local.PublicPrefix == "" {
		c.Storage.Local.PublicPrefix = "/files"
	}
	if c.Tenant.DefaultID == 0 {
		c.Tenant.DefaultID = 1
	}
	if c.Logger.Level == "" {
		c.Logger.Level = "info"
	}
	if c.Logger.Format == "" {
		c.Logger.Format = "text"
	}
}

func (c *Config) applyEnv() {
	if v := os.Getenv("GAP_DATABASE_DSN"); v != "" {
		c.Database.DSN = v
	}
	if v := os.Getenv("GAP_JWT_SECRET"); v != "" {
		c.JWT.Secret = v
	}
	if v := os.Getenv("GAP_APP_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &c.App.Port)
	}
	if v := os.Getenv("GAP_APP_ENV"); v != "" {
		c.App.Env = v
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GAP_SEED"))) {
	case "true":
		c.Database.Seed = true
	case "false":
		c.Database.Seed = false
	}
}

func (c *Config) normalize() {
	c.App.APIPrefix = "/" + strings.Trim(c.App.APIPrefix, "/")
	c.Storage.Local.PublicPrefix = "/" + strings.Trim(c.Storage.Local.PublicPrefix, "/")
	if c.JWT.Secret == "" {
		c.JWT.Secret = "go-admin-pro-dev-secret"
	}
}
