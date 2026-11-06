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
	App       AppConfig       `mapstructure:"app"`
	Database  DatabaseConfig  `mapstructure:"database"`
	JWT       JWTConfig       `mapstructure:"jwt"`
	Security  SecurityConfig  `mapstructure:"security"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Tenant    TenantConfig    `mapstructure:"tenant"`
	Logger    LoggerConfig    `mapstructure:"logger"`
	Push      PushConfig      `mapstructure:"push"`
	AccessLog AccessLogConfig `mapstructure:"access-log"`

	// loc 缓存 App.Timezone 对应的 *time.Location，由 Load 校验后写入。
	loc *time.Location
}

// PushConfig 是实时推送（issue #78，spec §4.5 / ADR 0013）的配置。
// hub 与票据都在进程内（单实例限制见 ADR 0013）。
type PushConfig struct {
	// Enabled 总开关：关闭时不注册推送路由，前端换票失败自动降级 60 秒轮询。
	// 代码默认 false（fail-safe），configs/config.yaml 示例显式开启。
	Enabled bool `mapstructure:"enabled"`
	// TicketTTL 一次性票据有效期，默认 30 秒（spec §4.5.2）。
	TicketTTL time.Duration `mapstructure:"ticket-ttl"`
	// Heartbeat SSE 心跳间隔（注释行），默认 25 秒（spec §4.5.3）。
	Heartbeat time.Duration `mapstructure:"heartbeat"`
}

// AccessLogConfig 是 API 访问日志（issue #80，spec §4.6，ADR 0014）的配置。
type AccessLogConfig struct {
	// RetentionDays 是访问日志保留天数（默认 30）：cleanAccessLogs 系统级
	// 定时任务每日 03:00 清理超过保留期的记录。
	RetentionDays int `mapstructure:"retention-days"`
}

// DefaultTimezone 是 app.timezone 为空时的默认业务时区。
const DefaultTimezone = "Asia/Shanghai"

// DefaultDevJWTSecret 是 dev/test 环境的 JWT 缺省密钥（仅为本地开发与集成测试
// 提供便利）。app.env 非 dev/test 时，空密钥或该值（以及仓库/文档中出现过的
// 其他可预测默认值）会直接拒绝启动，见 validateJWTSecret（PR #39 审查 P2）。
const DefaultDevJWTSecret = "go-admin-pro-dev-secret"

// insecureJWTSecrets 是已知可预测的 JWT 密钥：非 dev/test 环境一律拒绝，
// 避免生产部署照抄示例配置后被伪造 token。
var insecureJWTSecrets = map[string]struct{}{
	DefaultDevJWTSecret:                        {},
	"change-me-in-production-9f8a7b6c5d4e3f2a": {},
	"please-change-me":                         {},
}

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
	// SeedDemo 控制是否写入 CRM + OA 演示数据（客户/商机/合同/回款、公告/
	// 请假/报销/考勤/日程/会议室/文档等）。
	// 指针是为了区分「未配置」与显式 false：未配置视为关闭（演示数据只在
	// 显式开启时写入，避免生产环境被演示账号/示例数据污染，PR #39 审查 P2）；
	// 集成测试基座显式关闭，保证用例数据可控。
	SeedDemo *bool `mapstructure:"seed-demo"`
}

// SeedDemoEnabled 报告是否写入 CRM + OA 演示数据：配置缺省（nil）视为关闭，
// 只有显式 seed-demo: true 或 GAP_SEED_DEMO=true 才开启。
func (c *Config) SeedDemoEnabled() bool {
	return c.Database.SeedDemo != nil && *c.Database.SeedDemo
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

// LocalStorageConfig 只保留存储根目录：上传文件不再有公开访问前缀，
// 读取统一走鉴权下载端点（PR #39 残余风险 A）。
type LocalStorageConfig struct {
	BasePath string `mapstructure:"base-path"`
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

// validate 校验跨字段约束：app.timezone 必须是可加载的 IANA 时区名，
// 非 dev/test 环境必须覆盖可预测的 JWT 默认密钥。
// 在 Load 里做而不是等到第一次使用，是为了让非法值在启动阶段就失败，
// 而不是等到第一次导入 Excel 或签发 token 才暴露。
func (c *Config) validate() error {
	loc, err := time.LoadLocation(c.App.Timezone)
	if err != nil {
		return fmt.Errorf("app.timezone %q 不是合法的 IANA 时区名（期望形如 Asia/Shanghai 或 UTC）: %w", c.App.Timezone, err)
	}
	c.loc = loc
	c.Database.DSN = ApplyDSNLoc(c.Database.DSN, c.App.Timezone)
	return c.validateJWTSecret()
}

// validateJWTSecret 拒绝非 dev/test 环境使用空密钥或已知默认密钥：
// 本地开发/测试保留默认值，生产必须显式配置唯一强密钥（配置或 GAP_JWT_SECRET）。
func (c *Config) validateJWTSecret() error {
	if isLocalEnv(c.App.Env) {
		return nil
	}
	secret := strings.TrimSpace(c.JWT.Secret)
	if secret == "" {
		return fmt.Errorf("jwt.secret 不能为空：app.env=%q 时必须在配置文件或 GAP_JWT_SECRET 中设置唯一强密钥", c.App.Env)
	}
	if _, insecure := insecureJWTSecrets[secret]; insecure {
		return fmt.Errorf("jwt.secret 使用了已知默认值：app.env=%q 时必须在配置文件或 GAP_JWT_SECRET 中覆盖为唯一强密钥", c.App.Env)
	}
	return nil
}

// isLocalEnv 判断是否为本地开发/测试环境（允许 JWT 默认密钥）。
func isLocalEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "dev", "test":
		return true
	}
	return false
}

func (c *Config) applyDefaults() {
	if c.App.Name == "" {
		c.App.Name = "go-admin-pro"
	}
	if c.App.Env == "" {
		// 未配置视为 dev：与 configs/config.yaml 的默认值一致；
		// 非 dev/test 才会触发 JWT 密钥强校验（见 validateJWTSecret）。
		c.App.Env = "dev"
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
	if c.Tenant.DefaultID == 0 {
		c.Tenant.DefaultID = 1
	}
	if c.Logger.Level == "" {
		c.Logger.Level = "info"
	}
	if c.Logger.Format == "" {
		c.Logger.Format = "text"
	}
	// 实时推送（issue #78）：TTL 30s / 心跳 25s（spec §4.5.2/§4.5.3）。
	if c.Push.TicketTTL <= 0 {
		c.Push.TicketTTL = 30 * time.Second
	}
	if c.Push.Heartbeat <= 0 {
		c.Push.Heartbeat = 25 * time.Second
	}
	// API 访问日志（issue #80）：保留期默认 30 天（spec §4.6.7）。
	if c.AccessLog.RetentionDays <= 0 {
		c.AccessLog.RetentionDays = 30
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
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GAP_PUSH_ENABLED"))) {
	case "true", "1":
		c.Push.Enabled = true
	case "false", "0":
		c.Push.Enabled = false
	}
	if v := os.Getenv("GAP_PUSH_TICKET_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.Push.TicketTTL = d
		}
	}
	if v := os.Getenv("GAP_PUSH_HEARTBEAT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.Push.Heartbeat = d
		}
	}
}

func (c *Config) normalize() {
	c.App.APIPrefix = "/" + strings.Trim(c.App.APIPrefix, "/")
	if c.JWT.Secret == "" {
		c.JWT.Secret = DefaultDevJWTSecret
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
