package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/pressly/goose/v3"
)

// TenantTableRegistryError 汇总租户表登记与数据库 schema 的全部差异。
type TenantTableRegistryError struct {
	// UnregisteredTenantTables 有 tenant_id 列，但未登记进 TenantTables。
	UnregisteredTenantTables []string
	// UnclassifiedTables 既不在 TenantTables 也不在 PlatformTables。
	UnclassifiedTables []string
	// MissingTables 已登记，但数据库里不存在。
	MissingTables []string
	// MissingTenantColumns 登记为租户表，但缺 tenant_id 列。
	MissingTenantColumns []string
	// TenantColumnOnPlatform 登记为平台表，却带 tenant_id 列。
	TenantColumnOnPlatform []string
	// DuplicateEntries 重复登记，或同时出现在两张清单。
	DuplicateEntries []string
}

func (e *TenantTableRegistryError) Error() string {
	var b strings.Builder
	b.WriteString("租户表登记与数据库 schema 不一致：\n")
	writeRegistrySection(&b, "有 tenant_id 列但未登记（疑似漏登记，读写会跨租户）", e.UnregisteredTenantTables)
	writeRegistrySection(&b, "既不是租户表也不是平台表（未分类）", e.UnclassifiedTables)
	writeRegistrySection(&b, "已登记但数据库里不存在（迁移缺失或表名写错）", e.MissingTables)
	writeRegistrySection(&b, "登记为租户表但缺 tenant_id 列", e.MissingTenantColumns)
	writeRegistrySection(&b, "登记为平台表却带 tenant_id 列（分类冲突）", e.TenantColumnOnPlatform)
	writeRegistrySection(&b, "重复登记", e.DuplicateEntries)
	b.WriteString("修复：在 internal/pkg/database/tenant_tables.go 的 TenantTables 或 PlatformTables 登记缺失表；平台级表必须写明理由（ADR 0019）。")
	return b.String()
}

func writeRegistrySection(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "  - %s：%s\n", label, strings.Join(items, ", "))
}

// VerifyTenantTables 校验数据库 schema 与租户表登记是否精确一致（ADR 0019）：
// 每张 BASE TABLE 必须且只能属于 TenantTables 或 PlatformTables；租户表必须有
// tenant_id 列，平台表必须没有。只读 information_schema，不修改任何数据。
//
// 应用启动时（安装租户插件之前）调用；不一致即返回 *TenantTableRegistryError，
// 由调用方拒绝启动。
func VerifyTenantTables(ctx context.Context, db *sql.DB) error {
	tables, err := queryBaseTableNames(ctx, db)
	if err != nil {
		return fmt.Errorf("读取数据库表清单: %w", err)
	}
	columns, err := queryTenantColumnTables(ctx, db)
	if err != nil {
		return fmt.Errorf("读取 tenant_id 列清单: %w", err)
	}
	return classifyTenantTables(tables, columns, TenantTables, PlatformTables)
}

// classifyTenantTables 是纯函数：把差异一次性收集成 *TenantTableRegistryError。
func classifyTenantTables(allTables, tenantColumnTables, tenantTables []string, platformTables map[string]string) error {
	all := make(map[string]struct{}, len(allTables))
	for _, table := range allTables {
		all[table] = struct{}{}
	}
	columns := make(map[string]struct{}, len(tenantColumnTables))
	for _, table := range tenantColumnTables {
		// 只统计真实业务表；视图等其他对象不参与分类。
		if _, ok := all[table]; ok {
			columns[table] = struct{}{}
		}
	}

	fail := &TenantTableRegistryError{}
	tenantCount := make(map[string]int, len(tenantTables))
	for _, table := range tenantTables {
		tenantCount[table]++
		if tenantCount[table] > 1 {
			fail.DuplicateEntries = append(fail.DuplicateEntries, table+"（TenantTables 重复）")
		}
	}
	for table := range platformTables {
		if _, ok := tenantCount[table]; ok {
			fail.DuplicateEntries = append(fail.DuplicateEntries, table+"（TenantTables 与 PlatformTables 同时登记）")
		}
	}

	for table := range all {
		_, isTenant := tenantCount[table]
		_, isPlatform := platformTables[table]
		_, hasColumn := columns[table]
		switch {
		case isTenant && isPlatform:
			// 已在 DuplicateEntries 记录，分类冲突由修复重复登记解决。
		case isTenant:
			if !hasColumn {
				fail.MissingTenantColumns = append(fail.MissingTenantColumns, table)
			}
		case isPlatform:
			if hasColumn {
				fail.TenantColumnOnPlatform = append(fail.TenantColumnOnPlatform, table)
			}
		case hasColumn:
			fail.UnregisteredTenantTables = append(fail.UnregisteredTenantTables, table)
		default:
			fail.UnclassifiedTables = append(fail.UnclassifiedTables, table)
		}
	}

	for table := range tenantCount {
		if _, ok := all[table]; !ok {
			fail.MissingTables = append(fail.MissingTables, table)
		}
	}
	for table := range platformTables {
		if _, ok := all[table]; !ok {
			fail.MissingTables = append(fail.MissingTables, table)
		}
	}

	fail.DuplicateEntries = uniqueSorted(fail.DuplicateEntries)
	fail.MissingTables = uniqueSorted(fail.MissingTables)
	fail.MissingTenantColumns = uniqueSorted(fail.MissingTenantColumns)
	fail.TenantColumnOnPlatform = uniqueSorted(fail.TenantColumnOnPlatform)
	fail.UnregisteredTenantTables = uniqueSorted(fail.UnregisteredTenantTables)
	fail.UnclassifiedTables = uniqueSorted(fail.UnclassifiedTables)

	if len(fail.DuplicateEntries) == 0 &&
		len(fail.MissingTables) == 0 &&
		len(fail.MissingTenantColumns) == 0 &&
		len(fail.TenantColumnOnPlatform) == 0 &&
		len(fail.UnregisteredTenantTables) == 0 &&
		len(fail.UnclassifiedTables) == 0 {
		return nil
	}
	return fail
}

func queryBaseTableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	const query = "SELECT table_name FROM information_schema.tables " +
		"WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND table_name <> ? " +
		"ORDER BY table_name"
	rows, err := db.QueryContext(ctx, query, goose.TableName())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func queryTenantColumnTables(ctx context.Context, db *sql.DB) ([]string, error) {
	const query = "SELECT DISTINCT table_name FROM information_schema.columns " +
		"WHERE table_schema = DATABASE() AND column_name = 'tenant_id' AND table_name <> ? " +
		"ORDER BY table_name"
	rows, err := db.QueryContext(ctx, query, goose.TableName())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func uniqueSorted(items []string) []string {
	if len(items) < 2 {
		return items
	}
	sort.Strings(items)
	out := items[:1]
	for _, item := range items[1:] {
		if item != out[len(out)-1] {
			out = append(out, item)
		}
	}
	return out
}
