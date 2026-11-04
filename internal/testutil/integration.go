// Package testutil 是「真实 router + 真实 MySQL」的集成测试基座（issue #3）。
//
// 最小用法：
//
//	env := testutil.NewIntegrationEnv(t)
//	token := env.Login(t, "admin", "123456")
//	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", token, nil)
//
// 环境构成：
//   - DSN 取 GAP_TEST_DATABASE_DSN（缺省 DefaultTestDSN），且必须指向独立测试库：
//     库名为 go_admin_pro_test，或其下划线后缀变体（如 go_admin_pro_test_t6，
//     供不同工单的工作树并行跑测试），其他库名直接 Fatal；
//     刻意不复用应用的 GAP_DATABASE_DSN，避免测试专用 DSN 通过 config.Load
//     泄漏进其他包的测试；
//   - 目标库不存在时自动 CREATE DATABASE，随后执行 goose 迁移并清空全部业务表，
//     再由 internal/app.New 完成生产装配（租户插件、seed、JWT/权限/租户中间件、真实路由）；
//   - httptest.Server 挂在装配出的 gin.Engine 上，测试通过 HTTP 契约访问 /admin-api。
//
// 限制：用例间依赖清表保证互不污染，不支持 t.Parallel()；
// 运行需要可达的 MySQL 8（本机 docker compose 或 CI service），
// 因此所有调用方测试文件必须带 //go:build integration 标签，
// 默认 go test ./... 不会编译运行它们。
package testutil

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/app"
	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"gorm.io/gorm"
)

// IntegrationEnv 持有一个完整的集成测试环境。
type IntegrationEnv struct {
	Config *config.Config
	App    *app.App
	DB     *gorm.DB
	Server *httptest.Server

	closed bool
}

// NewIntegrationEnv 搭建集成测试环境并注册清理：
// 迁移 → 清空业务表 → seed（app.New 内）→ httptest server。
// 清理会重新清空业务表并关停 server 与连接池，用例无需手动收尾。
func NewIntegrationEnv(tb testing.TB) *IntegrationEnv {
	tb.Helper()

	dsn := os.Getenv("GAP_TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = DefaultTestDSN
	}
	name, err := ParseDSNDatabase(dsn)
	if err != nil {
		tb.Fatalf("解析测试 DSN（GAP_DATABASE_DSN）: %v", err)
	}
	if !IsTestDatabaseName(name) {
		tb.Fatalf("集成测试拒绝连接数据库 %q：必须使用独立测试库（名称为 %q 或其下划线后缀变体，如 %q）；用例间会清空数据，防止误伤开发库",
			name, TestDatabaseName, TestDatabaseName+"_t6")
	}

	ensureTestDatabase(tb, dsn)

	cfg := loadConfig(tb)
	cfg.Database.DSN = config.ApplyDSNLoc(dsn, cfg.App.Timezone)
	cfg.Database.Seed = true
	// gin 在非 dev 环境走 ReleaseMode；上传目录指向临时目录，避免污染仓库工作区。
	cfg.App.Env = "test"
	cfg.Storage.Local.BasePath = tb.TempDir()

	migrateDB, err := database.OpenSQL(cfg.Database)
	if err != nil {
		tb.Fatalf("连接测试库: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	defer migrateDB.Close()
	if err := database.MigrateUp(ctx, migrateDB); err != nil {
		tb.Fatalf("执行迁移: %v", err)
	}
	// 迁移后清表：上次运行（或被中断的用例）残留的数据不会污染本次 seed 与断言。
	if err := truncateAllTables(ctx, migrateDB); err != nil {
		tb.Fatalf("清空业务表: %v", err)
	}

	// 静默应用日志：装配链路的错误通过返回值进 tb.Fatalf，不打测试输出。
	a, err := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		tb.Fatalf("装配应用: %v", err)
	}

	env := &IntegrationEnv{
		Config: cfg,
		App:    a,
		DB:     a.DB,
		Server: httptest.NewServer(a.Engine),
	}
	tb.Cleanup(env.Close)
	return env
}

// Close 清空业务表并关停 server 与连接池。由 tb.Cleanup 自动调用，一般无需手动调用。
func (e *IntegrationEnv) Close() {
	if e.closed {
		return
	}
	e.closed = true
	if e.Server != nil {
		e.Server.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 清理失败不掩盖用例本身的结论；下个环境起建时会再清一次。
	if sqlDB, err := e.DB.DB(); err == nil {
		_ = truncateAllTables(ctx, sqlDB)
	}
	_ = e.App.Close()
}

// APIPrefixURL 返回拼接了 admin-api 前缀的完整 URL，path 形如 "/system/auth/login"。
func (e *IntegrationEnv) APIPrefixURL(path string) string {
	return e.Server.URL + e.Config.App.APIPrefix + path
}

// APIResponse 是统一响应信封 { code, data, msg }，data 保留原始 JSON 供调用方按需解码。
type APIResponse struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

// Request 向真实 server 发起一次带 token 的请求并解码统一响应信封。
// body 非 nil 时按 JSON 编码发送；token 为空时不带 Authorization 头。
func (e *IntegrationEnv) Request(tb testing.TB, method, path, token string, body any) (int, APIResponse) {
	tb.Helper()

	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			tb.Fatalf("编码请求体: %v", err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.APIPrefixURL(path), payload)
	if err != nil {
		tb.Fatalf("构造请求 %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.Server.Client().Do(req)
	if err != nil {
		tb.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var api APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		tb.Fatalf("解析 %s %s 响应: %v", method, path, err)
	}
	return resp.StatusCode, api
}

// Login 走真实登录接口换取 accessToken，登录失败直接 Fatal。
func (e *IntegrationEnv) Login(tb testing.TB, username, password string) string {
	tb.Helper()

	status, api := e.Request(tb, http.MethodPost, "/system/auth/login", "", map[string]string{
		"username": username,
		"password": password,
	})
	if status != http.StatusOK || api.Code != 0 {
		tb.Fatalf("登录 %s: HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		tb.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	if data.AccessToken == "" {
		tb.Fatalf("登录响应缺少 accessToken: %s", string(api.Data))
	}
	return data.AccessToken
}

// loadConfig 读取真实的 configs/config.yaml（含环境变量覆盖），
// 保证集成测试与应用生产装配共用同一份配置来源。
func loadConfig(tb testing.TB) *config.Config {
	tb.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("无法定位 testutil 源文件，找不到仓库根目录")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	path := filepath.Join(root, "configs", "config.yaml")
	cfg, err := config.Load(path)
	if err != nil {
		tb.Fatalf("加载配置 %s: %v", path, err)
	}
	return cfg
}

// ensureTestDatabase 在目标库不存在时自动创建（幂等）。
// 目标库取 DSN 里的库名（守卫已确认是测试库命名），不是基准名常量：
// 后缀变体（如 go_admin_pro_test_t6）要建各自的库。
func ensureTestDatabase(tb testing.TB, dsn string) {
	tb.Helper()

	name, err := ParseDSNDatabase(dsn)
	if err != nil {
		tb.Fatalf("解析测试 DSN: %v", err)
	}
	if !safeTableName.MatchString(name) {
		tb.Fatalf("测试库名 %q 含非法字符", name)
	}
	serverDSN, err := ServerDSN(dsn)
	if err != nil {
		tb.Fatalf("解析测试 DSN: %v", err)
	}
	db, err := database.OpenSQL(config.DatabaseConfig{DSN: serverDSN})
	if err != nil {
		tb.Fatalf("连接 MySQL 服务器失败（需要可达的 MySQL 8，见 README）: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stmt := "CREATE DATABASE IF NOT EXISTS `" + name + "` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		tb.Fatalf("创建测试库 %s: %v", name, err)
	}
}

// safeTableName 限制可操作的表名字符，防止 information_schema 返回异常值拼进 SQL。
var safeTableName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// truncateAllTables 清空当前库除 goose_db_version 外的全部业务表。
// 基线迁移不含外键，仍用 FOREIGN_KEY_CHECKS=0 兜底未来引入的外键；
// SET 会话级变量必须在同一连接上执行，故使用独占连接。
func truncateAllTables(ctx context.Context, sqlDB *sql.DB) error {
	const q = "SELECT table_name FROM information_schema.tables" +
		" WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND table_name <> 'goose_db_version'"
	rows, err := sqlDB.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("查询业务表清单: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("扫描表名: %w", err)
		}
		if safeTableName.MatchString(name) {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表名: %w", err)
	}

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("取独占连接: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return fmt.Errorf("关闭外键检查: %w", err)
	}
	for _, name := range tables {
		if _, err := conn.ExecContext(ctx, "TRUNCATE TABLE `"+name+"`"); err != nil {
			return fmt.Errorf("truncate %s: %w", name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1"); err != nil {
		return fmt.Errorf("恢复外键检查: %w", err)
	}
	return nil
}
