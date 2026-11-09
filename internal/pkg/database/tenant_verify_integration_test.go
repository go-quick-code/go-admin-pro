//go:build integration

package database_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

const tenantRegistryProbe = "tenant_registry_probe"

// dropTenantRegistryProbe 在起集成环境前清掉上次中断留下的探针表：
// 否则 app.New 的启动校验会先把环境拦住，而不是被本用例断言。
func dropTenantRegistryProbe(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("GAP_TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = testutil.DefaultTestDSN
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return // 库尚未创建时交给 NewIntegrationEnv
	}
	defer db.Close()
	_, _ = db.Exec("DROP TABLE IF EXISTS `" + tenantRegistryProbe + "`")
}

// TestVerifyTenantTablesAgainstMySQL 在真实 MySQL 上覆盖校验器的查询与错误聚合：
// 迁移后的 schema 必须通过；凭空多出一张未分类表时必须点名该表。
func TestVerifyTenantTablesAgainstMySQL(t *testing.T) {
	dropTenantRegistryProbe(t)
	env := testutil.NewIntegrationEnv(t)
	ctx := context.Background()

	sqlDB, err := env.DB.DB()
	if err != nil {
		t.Fatalf("取 sql.DB: %v", err)
	}

	if err := database.VerifyTenantTables(ctx, sqlDB); err != nil {
		t.Fatalf("迁移后的 schema 应通过租户表登记校验: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx,
		"CREATE TABLE `"+tenantRegistryProbe+"` (`id` bigint NOT NULL AUTO_INCREMENT PRIMARY KEY) "+
			"ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("创建探针表: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("DROP TABLE IF EXISTS `" + tenantRegistryProbe + "`")
	})

	err = database.VerifyTenantTables(ctx, sqlDB)
	var registryErr *database.TenantTableRegistryError
	if !errors.As(err, &registryErr) {
		t.Fatalf("未分类表应返回 *TenantTableRegistryError，实际 %v", err)
	}
	if !slices.Contains(registryErr.UnclassifiedTables, tenantRegistryProbe) {
		t.Fatalf("错误应点名未分类探针表，实际 %+v", registryErr)
	}
}
