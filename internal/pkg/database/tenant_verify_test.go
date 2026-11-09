package database

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func requireRegistryError(t *testing.T, err error) *TenantTableRegistryError {
	t.Helper()
	if err == nil {
		t.Fatal("期望租户表登记错误，实际为 nil")
	}
	var target *TenantTableRegistryError
	if !errors.As(err, &target) {
		t.Fatalf("期望 *TenantTableRegistryError，实际 %T: %v", err, err)
	}
	return target
}

// 分类正确时不应产生任何差异。
func TestClassifyTenantTablesConsistent(t *testing.T) {
	err := classifyTenantTables(
		[]string{"system_user", "system_menu"},
		[]string{"system_user"},
		[]string{"system_user"},
		map[string]string{"system_menu": "全局菜单目录"},
	)
	if err != nil {
		t.Fatalf("分类一致时应通过，实际 %v", err)
	}
}

// 一次收集全部差异，避免启动时改一处、重启再撞下一处。
func TestClassifyTenantTablesReportsAllMismatches(t *testing.T) {
	err := classifyTenantTables(
		[]string{"system_user", "crm_new", "stray", "system_menu", "tenant_no_column"},
		[]string{"system_user", "crm_new", "system_menu"},
		[]string{"system_user", "system_user", "listed_but_missing", "tenant_no_column"},
		map[string]string{"system_menu": "全局菜单目录"},
	)
	got := requireRegistryError(t, err)

	if want := []string{"crm_new"}; !slices.Equal(got.UnregisteredTenantTables, want) {
		t.Fatalf("有 tenant_id 但未登记 = %v, want %v", got.UnregisteredTenantTables, want)
	}
	if want := []string{"stray"}; !slices.Equal(got.UnclassifiedTables, want) {
		t.Fatalf("未分类 = %v, want %v", got.UnclassifiedTables, want)
	}
	if want := []string{"listed_but_missing"}; !slices.Equal(got.MissingTables, want) {
		t.Fatalf("登记但库中不存在 = %v, want %v", got.MissingTables, want)
	}
	if want := []string{"tenant_no_column"}; !slices.Equal(got.MissingTenantColumns, want) {
		t.Fatalf("登记为租户表但缺列 = %v, want %v", got.MissingTenantColumns, want)
	}
	if want := []string{"system_menu"}; !slices.Equal(got.TenantColumnOnPlatform, want) {
		t.Fatalf("平台表带 tenant_id = %v, want %v", got.TenantColumnOnPlatform, want)
	}
	// system_user 在 TenantTables 重复两次。
	if want := []string{"system_user（TenantTables 重复）"}; !slices.Equal(got.DuplicateEntries, want) {
		t.Fatalf("重复登记 = %v, want %v", got.DuplicateEntries, want)
	}
}

// 租户表缺 tenant_id 列：插件会生成 tenant_id 谓词但列不存在，必须启动失败。
func TestClassifyTenantTablesMissingTenantColumn(t *testing.T) {
	err := classifyTenantTables(
		[]string{"system_user"},
		nil,
		[]string{"system_user"},
		nil,
	)
	got := requireRegistryError(t, err)
	if want := []string{"system_user"}; !slices.Equal(got.MissingTenantColumns, want) {
		t.Fatalf("缺 tenant_id 列 = %v, want %v", got.MissingTenantColumns, want)
	}
}

// 同一张表同时出现在两张清单：分类冲突，必须只保留一种归属。
func TestClassifyTenantTablesRejectsOverlap(t *testing.T) {
	err := classifyTenantTables(
		[]string{"system_user"},
		[]string{"system_user"},
		[]string{"system_user"},
		map[string]string{"system_user": "不应同时登记"},
	)
	got := requireRegistryError(t, err)
	if want := []string{"system_user（TenantTables 与 PlatformTables 同时登记）"}; !slices.Equal(got.DuplicateEntries, want) {
		t.Fatalf("重叠登记 = %v, want %v", got.DuplicateEntries, want)
	}
}

// 视图等非 BASE TABLE 对象即使有 tenant_id 列也不参与分类。
func TestClassifyTenantTablesIgnoresObjectsOutsideBaseTables(t *testing.T) {
	err := classifyTenantTables(
		[]string{"system_user"},
		[]string{"system_user", "some_view"},
		[]string{"system_user"},
		nil,
	)
	if err != nil {
		t.Fatalf("视图不应参与分类，实际 %v", err)
	}
}

// 错误信息必须点名差异表并给出修复入口。
func TestTenantTableRegistryErrorFormat(t *testing.T) {
	err := classifyTenantTables(
		[]string{"crm_new"},
		[]string{"crm_new"},
		nil,
		nil,
	)
	got := requireRegistryError(t, err)
	message := got.Error()
	for _, want := range []string{"crm_new", "TenantTables", "PlatformTables", "ADR 0019"} {
		if !strings.Contains(message, want) {
			t.Fatalf("错误信息缺少 %q：\n%s", want, message)
		}
	}
}
