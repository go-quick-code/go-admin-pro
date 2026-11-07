//go:build integration

// dbschema 集成测试（issue #83，spec §4.9）：对真实 MySQL 的 information_schema
// 验证表清单与表结构读取，这是 codegen 与 infra 数据库文档共用的抽取层：
//   - 列表覆盖全部业务表（含 system_/infra_），支持前缀排除（codegen 语义）、
//     精确表名排除（goose_db_version）与关键字过滤；
//   - 详情返回列名/类型/可空/主键/自增/默认值/注释/序号，按序号排序；
//   - 非法表名与不存在的表报错（ErrTableNotFound 可被 errors.Is 识别）。
package dbschema_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/dbschema"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// setup 起一个完整集成环境（迁移 + seed），返回带租户插件的 gorm 连接。
// information_schema 查询不走业务表，租户插件不影响结果。
func setup(t *testing.T) *testutil.IntegrationEnv {
	t.Helper()
	return testutil.NewIntegrationEnv(t)
}

func containsTable(list []dbschema.TableInfo, name string) bool {
	for _, item := range list {
		if item.Name == name {
			return true
		}
	}
	return false
}

// TestListTablesCoversAllBusinessTables 无排除条件时应覆盖 system_/infra_ 业务表；
// goose_db_version 由调用方按需排除。
func TestListTablesCoversAllBusinessTables(t *testing.T) {
	env := setup(t)
	list, err := dbschema.ListTables(context.Background(), env.DB, dbschema.ListOptions{})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	for _, want := range []string{"system_user", "system_role", "infra_file", "infra_job", "goose_db_version"} {
		if !containsTable(list, want) {
			t.Fatalf("表清单应包含 %s，实际 %d 张表中没有", want, len(list))
		}
	}
}

// TestListTablesExclude 验证两类排除：前缀（codegen 排除 system_/infra_/gen_）
// 与精确表名（db-doc 排除 goose_db_version），且排除后其余表保留。
func TestListTablesExclude(t *testing.T) {
	env := setup(t)
	list, err := dbschema.ListTables(context.Background(), env.DB, dbschema.ListOptions{
		ExcludePrefixes: []string{`system\_`, `infra\_`, `gen\_`},
	})
	if err != nil {
		t.Fatalf("ListTables(前缀排除): %v", err)
	}
	for _, item := range list {
		if len(item.Name) >= 6 && (item.Name[:6] == "system" || item.Name[:5] == "infra" || item.Name[:3] == "gen") {
			t.Fatalf("前缀排除后不应出现 %s", item.Name)
		}
	}
	if !containsTable(list, "crm_customer") {
		t.Fatal("前缀排除后应保留 crm_customer")
	}

	list, err = dbschema.ListTables(context.Background(), env.DB, dbschema.ListOptions{
		ExcludeTables: []string{"goose_db_version"},
	})
	if err != nil {
		t.Fatalf("ListTables(精确排除): %v", err)
	}
	if containsTable(list, "goose_db_version") {
		t.Fatal("精确排除后不应出现 goose_db_version")
	}
	if !containsTable(list, "system_user") {
		t.Fatal("精确排除后应保留 system_user")
	}
}

// TestListTablesKeyword 关键字同时匹配表名与注释。
func TestListTablesKeyword(t *testing.T) {
	env := setup(t)
	list, err := dbschema.ListTables(context.Background(), env.DB, dbschema.ListOptions{Keyword: "user"})
	if err != nil {
		t.Fatalf("ListTables(user): %v", err)
	}
	if !containsTable(list, "system_user") {
		t.Fatal("关键字 user 应命中 system_user")
	}
	for _, item := range list {
		if item.Name == "infra_job" {
			t.Fatal("关键字 user 不应命中 infra_job")
		}
	}
}

// TestGetTableColumns 详情应返回列的完整信息并按序号排列。
func TestGetTableColumns(t *testing.T) {
	env := setup(t)
	table, err := dbschema.GetTable(context.Background(), env.DB, "system_user")
	if err != nil {
		t.Fatalf("GetTable(system_user): %v", err)
	}
	if table.Name != "system_user" {
		t.Fatalf("表名不符: %q", table.Name)
	}
	if len(table.Columns) == 0 {
		t.Fatal("system_user 应有列")
	}
	// 序号必须从 1 递增（ORDER BY ordinal_position）。
	for i, col := range table.Columns {
		if col.Ordinal != i+1 {
			t.Fatalf("第 %d 列序号为 %d，期望 %d", i, col.Ordinal, i+1)
		}
	}
	// 主键列：id 自增主键、非空。
	var idCol *dbschema.ColumnInfo
	for i := range table.Columns {
		if table.Columns[i].Name == "id" {
			idCol = &table.Columns[i]
			break
		}
	}
	if idCol == nil {
		t.Fatal("system_user 应有 id 列")
	}
	if !idCol.PrimaryKey || !idCol.AutoIncrement || idCol.Nullable {
		t.Fatalf("id 列应为主键自增非空，实际 %+v", *idCol)
	}
	// 建表迁移里 username 为 varchar 且非空。
	var username *dbschema.ColumnInfo
	for i := range table.Columns {
		if table.Columns[i].Name == "username" {
			username = &table.Columns[i]
			break
		}
	}
	if username == nil {
		t.Fatal("system_user 应有 username 列")
	}
	if username.DataType != "varchar" || username.Nullable {
		t.Fatalf("username 应为非空 varchar，实际 %+v", *username)
	}
}

// TestGetTableErrors 非法表名拒绝；不存在的表返回可识别的 ErrTableNotFound，
// 错误文案与 codegen 原实现一致（表不存在：<名>）。
func TestGetTableErrors(t *testing.T) {
	env := setup(t)
	if _, err := dbschema.GetTable(context.Background(), env.DB, "system-user;drop"); err == nil {
		t.Fatal("非法表名应报错")
	}
	_, err := dbschema.GetTable(context.Background(), env.DB, "no_such_table_xx")
	if err == nil {
		t.Fatal("不存在的表应报错")
	}
	if !errors.Is(err, dbschema.ErrTableNotFound) {
		t.Fatalf("应为 ErrTableNotFound，实际 %v", err)
	}
	if got := err.Error(); got != "表不存在：no_such_table_xx" {
		t.Fatalf("错误文案不符: %q", got)
	}
}
