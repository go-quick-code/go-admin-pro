//go:build integration

package database_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/go-quick-code/go-admin-pro/internal/app"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

const (
	// registryProbeDatabase 是失败路径专用的独立测试库：每次用例开始
	// DROP/CREATE，结束再 DROP，探针表不会污染主测试库或其他集成包
	// （app.New 的启动校验会把残留探针表变成整包失败）。
	registryProbeDatabase = testutil.TestDatabaseName + "_registry"
	unclassifiedProbe     = "tenant_registry_probe"
	unregisteredProbe     = "tenant_registry_unregistered_probe"
	platformColumnProbe   = "tenant_registry_platform_probe"
	tenantColumnProbe     = "tenant_registry_tenant_probe"
	missingTableProbe     = "tenant_registry_missing_probe"
)

// newRegistryProbeDB 建一个已迁移的独立测试库，返回连接与 DSN。
func newRegistryProbeDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	dsn := os.Getenv("GAP_TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = testutil.DefaultTestDSN
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("解析测试 DSN: %v", err)
	}
	if !testutil.IsTestDatabaseName(cfg.DBName) {
		t.Fatalf("测试 DSN 指向 %q：必须使用独立测试库（%q 或其下划线后缀）", cfg.DBName, testutil.TestDatabaseName)
	}

	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	if err != nil {
		t.Fatalf("连接 MySQL: %v", err)
	}
	if _, err := admin.Exec("DROP DATABASE IF EXISTS `" + registryProbeDatabase + "`"); err != nil {
		_ = admin.Close()
		t.Fatalf("清理探针库: %v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE `" + registryProbeDatabase + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		_ = admin.Close()
		t.Fatalf("创建探针库: %v", err)
	}
	_ = admin.Close()

	probeCfg := *cfg
	probeCfg.DBName = registryProbeDatabase
	probeDSN := probeCfg.FormatDSN()
	db, err := sql.Open("mysql", probeDSN)
	if err != nil {
		t.Fatalf("连接探针库: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		admin, err := sql.Open("mysql", adminCfg.FormatDSN())
		if err != nil {
			return
		}
		_, _ = admin.Exec("DROP DATABASE IF EXISTS `" + registryProbeDatabase + "`")
		_ = admin.Close()
	})
	if err := database.MigrateUp(context.Background(), db); err != nil {
		t.Fatalf("迁移探针库: %v", err)
	}
	return db, probeDSN
}

func createProbeTable(t *testing.T, db *sql.DB, name string, withTenantID bool) {
	t.Helper()
	columns := "`id` bigint NOT NULL AUTO_INCREMENT PRIMARY KEY"
	if withTenantID {
		columns += ", `tenant_id` bigint NOT NULL DEFAULT '1'"
	}
	if _, err := db.Exec("CREATE TABLE `" + name + "` (" + columns + ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("创建探针表 %s: %v", name, err)
	}
}

func assertRegistryListContains(t *testing.T, list []string, want, field string) {
	t.Helper()
	if !slices.Contains(list, want) {
		t.Fatalf("%s 应包含 %q，实际 %v", field, want, list)
	}
}

// TestVerifyTenantTablesAgainstMySQL 在真实 MySQL 上覆盖校验器的查询与错误聚合：
// 迁移后的 schema 必须通过；四类探针表（未分类、有列未登记、平台带列、
// 租户缺列）与一张登记但缺表的名字必须分别落到对应差异分类。
func TestVerifyTenantTablesAgainstMySQL(t *testing.T) {
	db, _ := newRegistryProbeDB(t)
	ctx := context.Background()

	if err := database.VerifyTenantTables(ctx, db); err != nil {
		t.Fatalf("迁移后的 schema 应通过租户表登记校验: %v", err)
	}

	createProbeTable(t, db, unclassifiedProbe, false)
	createProbeTable(t, db, unregisteredProbe, true)
	createProbeTable(t, db, platformColumnProbe, true)
	createProbeTable(t, db, tenantColumnProbe, false)

	database.PlatformTables[platformColumnProbe] = "测试探针：平台表带 tenant_id"
	t.Cleanup(func() { delete(database.PlatformTables, platformColumnProbe) })
	before := len(database.TenantTables)
	database.TenantTables = append(database.TenantTables, tenantColumnProbe, missingTableProbe)
	t.Cleanup(func() { database.TenantTables = database.TenantTables[:before] })

	err := database.VerifyTenantTables(ctx, db)
	var registryErr *database.TenantTableRegistryError
	if !errors.As(err, &registryErr) {
		t.Fatalf("探针表应返回 *TenantTableRegistryError，实际 %v", err)
	}
	assertRegistryListContains(t, registryErr.UnclassifiedTables, unclassifiedProbe, "UnclassifiedTables")
	assertRegistryListContains(t, registryErr.UnregisteredTenantTables, unregisteredProbe, "UnregisteredTenantTables")
	assertRegistryListContains(t, registryErr.TenantColumnOnPlatform, platformColumnProbe, "TenantColumnOnPlatform")
	assertRegistryListContains(t, registryErr.MissingTenantColumns, tenantColumnProbe, "MissingTenantColumns")
	assertRegistryListContains(t, registryErr.MissingTables, missingTableProbe, "MissingTables")
}

// TestAppNewRejectsUnclassifiedTenantTable 锁住启动接线：不是直接调校验器，
// 而是让生产装配入口 app.New 在未分类表上失败。删除/移动 app.New 里的校验
// 会让本用例变红。
func TestAppNewRejectsUnclassifiedTenantTable(t *testing.T) {
	db, probeDSN := newRegistryProbeDB(t)
	createProbeTable(t, db, unclassifiedProbe, false)

	env := testutil.NewIntegrationEnv(t)
	cfg := *env.Config
	cfg.Database.DSN = probeDSN
	cfg.Database.Seed = false

	a, err := app.New(&cfg, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	if err == nil {
		_ = a.Close()
		t.Fatal("未分类探针表应让 app.New 失败")
	}
	var registryErr *database.TenantTableRegistryError
	if !errors.As(err, &registryErr) {
		t.Fatalf("app.New 应返回 *TenantTableRegistryError，实际 %v", err)
	}
	assertRegistryListContains(t, registryErr.UnclassifiedTables, unclassifiedProbe, "UnclassifiedTables")
	if !strings.Contains(err.Error(), "verify tenant table registry") {
		t.Fatalf("错误应带启动校验上下文，实际 %v", err)
	}
}
