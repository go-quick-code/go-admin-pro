//go:build integration

// PR #86 审查意见：UpdateTenant 的套餐停用校验原先无条件执行且早于
// packageChanged 计算——套餐停用后，绑定该套餐的租户编辑时原样回传
// packageId 也会被 400 拒绝，编辑入口被锁死。修复后校验收敛到套餐
// 变更分支：未改套餐的编辑不做停用校验，改到停用套餐仍 400。
package system_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// tenantPackageIDForTest 按名称查询套餐分页，返回唯一匹配的套餐 ID。
func tenantPackageIDForTest(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/tenant-package/page?pageNo=1&pageSize=50&name="+name, token, nil)
	mustOK(t, "查询套餐 "+name, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析套餐分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 || page.List[0].ID <= 0 {
		t.Fatalf("应查到唯一套餐 %s，实际 %+v", name, page.List)
	}
	return page.List[0].ID
}

// TestIntegrationTenantEditKeepsDisabledPackage 套餐停用后的租户编辑守卫：
//   - 未改套餐（原样回传已停用套餐 packageId）→ 编辑成功；
//   - 改绑到另一个已停用套餐 → 400「套餐已停用，不能绑定」。
func TestIntegrationTenantEditKeepsDisabledPackage(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 创建套餐 A 并在创建租户时绑定，随后停用套餐 A。
	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": "停用守卫套餐A", "status": 0, "menuIds": []int64{110, 101},
	})
	mustOK(t, "创建套餐A", status, api)
	pkgA := tenantPackageIDForTest(t, env, admin, "停用守卫套餐A")

	status, api = env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": "停用守卫租户", "contactName": "联系人", "status": 0, "packageId": pkgA,
	})
	mustOK(t, "创建绑定套餐A的租户", status, api)
	tenantID := int64(0)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=50&name=停用守卫租户", admin, nil)
	mustOK(t, "查询停用守卫租户", status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析租户分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 || page.List[0].ID <= 1 {
		t.Fatalf("应查到唯一新建租户，实际 %+v", page.List)
	}
	tenantID = page.List[0].ID

	status, api = env.Request(t, http.MethodPut, "/system/tenant-package/status", admin, map[string]any{"id": pkgA, "status": 1})
	mustOK(t, "停用套餐A", status, api)

	// 未改套餐的编辑：原样回传已停用套餐 packageId，应成功（修复前 400）。
	status, api = env.Request(t, http.MethodPut, "/system/tenant", admin, map[string]any{
		"id": tenantID, "name": "停用守卫租户改", "status": 0, "packageId": pkgA,
	})
	mustOK(t, "编辑未改套餐的租户（套餐已停用）", status, api)

	// 创建并停用套餐 B：改绑到停用套餐仍要 400。
	status, api = env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": "停用守卫套餐B", "status": 0, "menuIds": []int64{110, 101},
	})
	mustOK(t, "创建套餐B", status, api)
	pkgB := tenantPackageIDForTest(t, env, admin, "停用守卫套餐B")
	status, api = env.Request(t, http.MethodPut, "/system/tenant-package/status", admin, map[string]any{"id": pkgB, "status": 1})
	mustOK(t, "停用套餐B", status, api)

	status, api = env.Request(t, http.MethodPut, "/system/tenant", admin, map[string]any{
		"id": tenantID, "name": "停用守卫租户改", "status": 0, "packageId": pkgB,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("改绑停用套餐：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
}
