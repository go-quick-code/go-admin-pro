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

	// loc 缓存 App.Timezone 对应的 *time.Location，由 Load 校验后写入。
	loc *time.Location
}

// DefaultTimezone 是 app.timezone 为空时的默认业务时区。
const DefaultTimezone = "Asia/Shanghai"

type AppConfig struct {
	Name      string     `mapstructure:"name"`
	Env       string     `mapstructure:"env"`
	Port      int        `mapstructure:"port"`
	APIPrefix string     `mapstructure:"api-prefix"`
	WebRoot   string     `mapstructure:"web-root"`
	Timezone  string     `mapstructure:"timezone"`
	CORS      CORSConfig `mapstructure:"cors"`
}

// CORSConfig 是 API 的跨域白名单。
// AllowedOrigins 为精确匹配的 Origin 列表；为空表示不放行任何跨域请求
// （不返回 CORS 头），需要跨域访问的部署必须显式配置。
type CORSConfig struct {
	AllowedOrigins []string `mapstructure:"allowed-origins"`
}

type DatabaseConfig struct {
	DSN          string `mapstructure:"dsn"`
	MaxIdleConns int    `mapstructure:"max-idle-conns"`
	MaxOpenConns int    `mapstructure:"max-open-conns"`
	Seed         bool   `mapstructure:"seed"`
	// SeedDemo 控制是否写入 CRM 演示数据（客户/商机/合同/回款等）。
	// 指针是为了区分「未配置」与显式 false：未配置时默认开启，由
	// SeedDemoEnabled 读取；集成测试基座显式关闭，保证用例数据可控。
	SeedDemo *bool `mapstructure:"seed-demo"`
}

// SeedDemoEnabled 报告是否写入 CRM 演示数据：配置缺省（nil）视为开启。
func (c *Config) SeedDemoEnabled() bool {
	return c.Database.SeedDemo == nil || *c.Database.SeedDemo
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
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate 校验跨字段约束：app.timezone 必须是可加载的 IANA 时区名。
// 在 Load 里做而不是等到第一次使用，是为了让非法值在启动阶段就失败，
// 而不是等到第一次导入 Excel 才暴露。
func (c *Config) validate() error {
	loc, err := time.LoadLocation(c.App.Timezone)
	if err != nil {
		return fmt.Errorf("app.timezone %q 不是合法的 IANA 时区名（期望形如 Asia/Shanghai 或 UTC）: %w", c.App.Timezone, err)
	}
	c.loc = loc
	c.Database.DSN = ApplyDSNLoc(c.Database.DSN, c.App.Timezone)
	return nil
}

func (c *Config) applyDefaults() {
	if c.App.Name == "" {
		c.App.Name = "go-admin-pro"
	}
	if c.App.Timezone == "" {
		c.App.Timezone = DefaultTimezone
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
	if v := os.Getenv("GAP_CORS_ALLOWED_ORIGINS"); v != "" {
		parts := strings.Split(v, ",")
		c.App.CORS.AllowedOrigins = make([]string, 0, len(parts))
		for _, origin := range parts {
			if origin = strings.TrimSpace(origin); origin != "" {
				c.App.CORS.AllowedOrigins = append(c.App.CORS.AllowedOrigins, origin)
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GAP_SEED"))) {
	case "true":
		c.Database.Seed = true
	case "false":
		c.Database.Seed = false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GAP_SEED_DEMO"))) {
	case "true":
		enabled := true
		c.Database.SeedDemo = &enabled
	case "false":
		enabled := false
		c.Database.SeedDemo = &enabled
	}
}

func (c *Config) normalize() {
	c.App.APIPrefix = "/" + strings.Trim(c.App.APIPrefix, "/")
	c.Storage.Local.PublicPrefix = "/" + strings.Trim(c.Storage.Local.PublicPrefix, "/")
	if c.JWT.Secret == "" {
		c.JWT.Secret = "go-admin-pro-dev-secret"
	}
}

// Location 返回业务时区的 *time.Location（由 app.timezone 决定）。
// 这是业务侧获取时区的单一访问点：Excel 导入等需要显式时区的调用方
// 用它传给 helper（如 ImportOptions.Location），不要再各自解析配置。
func (c *Config) Location() *time.Location {
	if c.loc != nil {
		return c.loc
	}
	name := c.App.Timezone
	if name == "" {
		name = DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// 仅防绕过 Load 的构造路径（如手工 &Config{} 的测试）：正常路径
		// 已在启动时校验 App.Timezone，不会走到这里。兜底用确定的业务
		// 默认时区 DefaultTimezone，而不是 time.Local——依赖服务器 TZ
		// 正是 spec #32 要消除的行为。
		loc, err = time.LoadLocation(DefaultTimezone)
		if err != nil {
			// 极端情况（运行环境缺少 tzdata）下 DefaultTimezone 也加载
			// 失败时退到 UTC：仍是确定值，不依赖服务器 TZ。
			loc = time.UTC
		}
	}
	c.loc = loc
	return loc
}
