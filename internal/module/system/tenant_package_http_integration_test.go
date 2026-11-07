//go:build integration

// 租户套餐（issue #69）：平台租户专属的 CRUD、菜单校验与删除占用守卫。
package system_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

func TestIntegrationTenantPackageCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 创建：菜单只允许目录/菜单（110 租户套餐、101 用户管理）。
	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": "测试套餐", "status": 0, "menuIds": []int64{110, 101},
		"maxUsers": 5, "maxStorageMb": 100, "maxSmsMonthly": 10, "maxMailMonthly": 20,
	})
	mustOK(t, "创建套餐", status, api)

	status, api = env.Request(t, http.MethodGet, "/system/tenant-package/page?pageNo=1&pageSize=10&name=测试套餐", admin, nil)
	mustOK(t, "查询套餐分页", status, api)
	var page struct {
		List []struct {
			ID             int64   `json:"id"`
			MenuIDs        []int64 `json:"menuIds"`
			MaxUsers       int64   `json:"maxUsers"`
			MaxStorageMB   int64   `json:"maxStorageMb"`
			MaxSMSMonthly  int64   `json:"maxSmsMonthly"`
			MaxMailMonthly int64   `json:"maxMailMonthly"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析套餐分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("应查到唯一套餐，实际 total=%d list=%+v", page.Total, page.List)
	}
	pkg := page.List[0]
	if len(pkg.MenuIDs) != 2 || pkg.MenuIDs[0] != 110 || pkg.MenuIDs[1] != 101 {
		t.Fatalf("menuIds 回读不符：%+v", pkg.MenuIDs)
	}
	if pkg.MaxUsers != 5 || pkg.MaxStorageMB != 100 || pkg.MaxSMSMonthly != 10 || pkg.MaxMailMonthly != 20 {
		t.Fatalf("配额回读不符：%+v", pkg)
	}

	// 详情。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant-package/%d", pkg.ID), admin, nil)
	mustOK(t, "查询套餐详情", status, api)

	// 修改：换菜单与配额。
	status, api = env.Request(t, http.MethodPut, "/system/tenant-package", admin, map[string]any{
		"id": pkg.ID, "name": "测试套餐-改", "status": 0, "menuIds": []int64{100, 101},
		"maxUsers": 8, "maxStorageMb": 0, "maxSmsMonthly": 0, "maxMailMonthly": 0,
	})
	mustOK(t, "修改套餐", status, api)

	// 停用/启用。
	status, api = env.Request(t, http.MethodPut, "/system/tenant-package/status", admin, map[string]any{"id": pkg.ID, "status": 1})
	mustOK(t, "停用套餐", status, api)
	status, api = env.Request(t, http.MethodPut, "/system/tenant-package/status", admin, map[string]any{"id": pkg.ID, "status": 0})
	mustOK(t, "启用套餐", status, api)

	// 校验失败：不存在的菜单、按钮菜单、负配额、非法状态、重名。
	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"不存在菜单", map[string]any{"name": "坏套餐1", "menuIds": []int64{999999}}, http.StatusBadRequest},
		{"按钮菜单", map[string]any{"name": "坏套餐2", "menuIds": []int64{1101}}, http.StatusBadRequest},
		{"负配额", map[string]any{"name": "坏套餐3", "maxUsers": -1}, http.StatusBadRequest},
		{"非法状态", map[string]any{"name": "坏套餐4", "status": 9}, http.StatusBadRequest},
		{"重名", map[string]any{"name": "测试套餐-改"}, http.StatusConflict},
	} {
		status, api = env.Request(t, http.MethodPost, "/system/tenant-package", admin, tc.body)
		if status != tc.want {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 %d", tc.name, status, api.Code, api.Msg, tc.want)
		}
	}

	// 删除：先解除租户引用（本工单尚无绑定入口，直接改库模拟），再删除。
	tenantID := createTenantForPlatformTest(t, env, admin, "套餐占用租户")
	if err := env.DB.Exec("UPDATE system_tenant SET package_id = ? WHERE id = ?", pkg.ID, tenantID).Error; err != nil {
		t.Fatalf("模拟租户引用套餐: %v", err)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/tenant-package/%d", pkg.ID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("删除被引用套餐：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	if err := env.DB.Exec("UPDATE system_tenant SET package_id = 0 WHERE id = ?", tenantID).Error; err != nil {
		t.Fatalf("解除租户套餐引用: %v", err)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/tenant-package/%d", pkg.ID), admin, nil)
	mustOK(t, "删除未被引用套餐", status, api)

	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant-package/%d", pkg.ID), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("删除后查详情：HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationTenantPackageSoftDeletedNameConflict 软删同名后新建应 409
// （PR #86 审查意见）：CountTenantPackageByName 原走 gorm 软删作用域只数活行，
// 与唯一键 uk_system_tenant_package_name（含软删行）口径不一致——复用软删名
// 绕过重名校验后撞 DB 唯一键，返回未映射的 500。
func TestIntegrationTenantPackageSoftDeletedNameConflict(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": "软删重名套餐", "status": 0, "menuIds": []int64{110, 101},
	})
	mustOK(t, "创建套餐", status, api)
	pkgID := tenantPackageIDForTest(t, env, admin, "软删重名套餐")

	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/tenant-package/%d", pkgID), admin, nil)
	mustOK(t, "软删除套餐", status, api)

	// 新建同名：应 409「套餐名称已存在」（修复前 Count 只数活行放行，Create 撞唯一键 500）。
	status, api = env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": "软删重名套餐", "status": 0, "menuIds": []int64{110, 101},
	})
	if status != http.StatusConflict {
		t.Fatalf("新建软删同名套餐：HTTP %d code %d msg %q，期望 %d", status, api.Code, api.Msg, http.StatusConflict)
	}
}

func TestIntegrationTenantPackagePlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	tenantID := createTenantForPlatformTest(t, env, admin, "套餐越权租户")
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"列表", http.MethodGet, "/system/tenant-package/page?pageNo=1&pageSize=10", nil},
		{"简表", http.MethodGet, "/system/tenant-package/simple-list", nil},
		{"创建", http.MethodPost, "/system/tenant-package", map[string]any{"name": "越权套餐"}},
	} {
		status, api := env.Request(t, tc.method, tc.path, tenantAdmin, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("非平台租户管理员%s套餐：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}
}
