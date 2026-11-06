//go:build integration

// 租户配额（issue #71，spec §4.2）：
//
//   - 用户数达到 max_users 时创建用户 409「租户用户数已达套餐上限」；
//     停用用户也计入用量；平台租户（ID=1）不受限；
//   - 配额 API：GET /system/tenant/quota 当前租户登录即可；
//     GET /system/tenant/:id/quota 平台可查任意租户，非平台租户查别租户 404；
//   - 返回 limit/used/unlimited，数值与库内一致。
package system_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// tenantQuotaRow 直查库返回租户未删除用户数。
func tenantUserCount(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) int64 {
	t.Helper()
	var total int64
	if err := env.DB.Raw(
		"SELECT COUNT(*) FROM system_user WHERE tenant_id = ? AND deleted_at IS NULL", tenantID,
	).Scan(&total).Error; err != nil {
		t.Fatalf("统计租户 %d 用户数: %v", tenantID, err)
	}
	return total
}

// createUserForQuotaTest 以指定 token 创建用户，返回 HTTP 状态与信封。
func createUserForQuotaTest(t *testing.T, env *testutil.IntegrationEnv, token, username string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": username, "password": "123456", "nickname": "配额用户-" + username,
	})
}

// createPackageTenantForQuota 创建「套餐 + 绑定该套餐的租户」，返回租户 ID。
// 套餐菜单由 menuIDs 指定（展开按钮），maxUsers 为用户数上限。
func createPackageTenantForQuota(t *testing.T, env *testutil.IntegrationEnv, name string, menuIDs []int64, maxUsers int64) int64 {
	t.Helper()

	admin := env.Login(t, "admin", "123456")
	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": name + "-套餐", "status": 0, "menuIds": menuIDs, "maxUsers": maxUsers,
	})
	mustOK(t, "创建用户数套餐", status, api)

	var pkg struct {
		ID int64 `json:"id"`
	}
	if err := env.DB.Raw("SELECT id FROM system_tenant_package WHERE name = ?", name+"-套餐").Scan(&pkg).Error; err != nil || pkg.ID == 0 {
		t.Fatalf("查询套餐 ID: %v（id=%d）", err, pkg.ID)
	}

	status, api = env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": name, "contactName": "配额联系人", "status": 0, "packageId": pkg.ID,
	})
	mustOK(t, "创建配额租户", status, api)

	var tenant struct {
		ID int64 `json:"id"`
	}
	if err := env.DB.Raw("SELECT id FROM system_tenant WHERE name = ?", name).Scan(&tenant).Error; err != nil || tenant.ID == 0 {
		t.Fatalf("查询租户 ID: %v（id=%d）", err, tenant.ID)
	}
	return tenant.ID
}

// TestIntegrationUserQuotaLimit 套餐 maxUsers=2：租户管理员 + 1 个新用户后，
// 再建用户 409；停用其中一人后仍 409（停用用户也计入）；配额 API 的 used
// 与库内一致。
func TestIntegrationUserQuotaLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 套餐菜单含 101 用户管理（按钮 1011 用户新增随父菜单展开）。
	tenantID := createPackageTenantForQuota(t, env, "用户受限租户", []int64{101}, 2)
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	// 租户内置 admin 已占 1 席，再建 1 个达到上限 2。
	status, api := createUserForQuotaTest(t, env, tenantAdmin, "user2")
	mustOK(t, "创建第 2 个用户", status, api)

	status, api = createUserForQuotaTest(t, env, tenantAdmin, "user3")
	if status != http.StatusConflict {
		t.Fatalf("超限创建用户：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	if api.Msg != "租户用户数已达套餐上限（2）" {
		t.Fatalf("超限文案不符：%q", api.Msg)
	}

	// 停用 user2 后再建：停用用户也计入 → 仍 409。
	var user2ID int64
	if err := env.DB.Raw("SELECT id FROM system_user WHERE username = 'user2'").Scan(&user2ID).Error; err != nil || user2ID == 0 {
		t.Fatalf("查询 user2 ID: %v（id=%d）", err, user2ID)
	}
	status, api = env.Request(t, http.MethodPut, "/system/user/status", tenantAdmin, map[string]any{"id": user2ID, "status": 1})
	mustOK(t, "停用 user2", status, api)

	status, api = createUserForQuotaTest(t, env, tenantAdmin, "user4")
	if status != http.StatusConflict {
		t.Fatalf("停用用户计入后创建：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 用量与库内一致：配额 API used.users == 库内未删除用户数。
	if want := tenantUserCount(t, env, tenantID); want != 2 {
		t.Fatalf("库内用户数应为 2，实际 %d", want)
	}
	status, api = env.Request(t, http.MethodGet, "/system/tenant/quota", tenantAdmin, nil)
	mustOK(t, "查询当前租户配额", status, api)
	var quota struct {
		Unlimited bool `json:"unlimited"`
		Limit     struct {
			MaxUsers     int64 `json:"maxUsers"`
			MaxStorageMB int64 `json:"maxStorageMb"`
		} `json:"limit"`
		Used struct {
			Users int64 `json:"users"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &quota); err != nil {
		t.Fatalf("解析配额响应: %v（data=%s）", err, string(api.Data))
	}
	if quota.Unlimited {
		t.Fatal("绑定套餐租户不应 unlimited")
	}
	if quota.Limit.MaxUsers != 2 {
		t.Fatalf("limit.maxUsers 应为 2，实际 %d", quota.Limit.MaxUsers)
	}
	if quota.Used.Users != 2 {
		t.Fatalf("used.users 应为 2（含停用），实际 %d", quota.Used.Users)
	}
}

// TestIntegrationUserQuotaPlatformUnlimited 平台租户（ID=1）不受套餐限制：
// 平台未绑定套餐，连续创建用户不触发 409。
func TestIntegrationUserQuotaPlatformUnlimited(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	for i := 1; i <= 3; i++ {
		status, api := createUserForQuotaTest(t, env, platformAdmin, fmt.Sprintf("platform_user_%d", i))
		mustOK(t, fmt.Sprintf("平台创建第 %d 个用户", i), status, api)
	}
	if got := tenantUserCount(t, env, 1); got < 4 {
		t.Fatalf("平台租户用户数应 >= 4（admin + 3 新建），实际 %d", got)
	}
}

// TestIntegrationTenantQuotaAPI 配额 API 权限语义与数值一致性：
//   - 当前租户登录即可查（平台与套餐租户都 200）；
//   - 平台可查任意租户，数值与库内一致；
//   - 非平台租户查别租户 404；平台查不存在租户 404。
func TestIntegrationTenantQuotaAPI(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	// 平台查自己：unlimited=true（未绑定套餐）。
	status, api := env.Request(t, http.MethodGet, "/system/tenant/quota", platformAdmin, nil)
	mustOK(t, "平台查询自身配额", status, api)
	var platformQuota struct {
		Unlimited bool `json:"unlimited"`
		Used      struct {
			Users int64 `json:"users"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &platformQuota); err != nil {
		t.Fatalf("解析平台配额: %v（data=%s）", err, string(api.Data))
	}
	if !platformQuota.Unlimited {
		t.Fatal("平台租户应 unlimited=true")
	}
	if want := tenantUserCount(t, env, 1); platformQuota.Used.Users != want {
		t.Fatalf("平台 used.users=%d 与库内 %d 不一致", platformQuota.Used.Users, want)
	}

	// 套餐租户：管理员查自己 200，limit 与套餐一致，used 与库内一致。
	// 菜单含 107 租户管理（system:tenant:query）：指定租户路由需要该权限码，
	// 非 404 的前置条件是调用方先有权限。
	tenantID := createPackageTenantForQuota(t, env, "配额查询租户", []int64{101, 107}, 5)
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant/%d/quota", tenantID), tenantAdmin, nil)
	mustOK(t, "租户管理员查本租户配额", status, api)
	var tenantQuota struct {
		Unlimited bool `json:"unlimited"`
		Limit     struct {
			MaxUsers     int64 `json:"maxUsers"`
			MaxStorageMB int64 `json:"maxStorageMb"`
		} `json:"limit"`
		Used struct {
			Users        int64 `json:"users"`
			StorageBytes int64 `json:"storageBytes"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &tenantQuota); err != nil {
		t.Fatalf("解析租户配额: %v（data=%s）", err, string(api.Data))
	}
	if tenantQuota.Unlimited || tenantQuota.Limit.MaxUsers != 5 {
		t.Fatalf("套餐租户配额不符：unlimited=%v maxUsers=%d", tenantQuota.Unlimited, tenantQuota.Limit.MaxUsers)
	}
	if want := tenantUserCount(t, env, tenantID); tenantQuota.Used.Users != want {
		t.Fatalf("租户 used.users=%d 与库内 %d 不一致", tenantQuota.Used.Users, want)
	}

	// 平台查该租户：同一份数据。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant/%d/quota", tenantID), platformAdmin, nil)
	mustOK(t, "平台查询租户配额", status, api)
	var viaPlatform struct {
		Used struct {
			Users int64 `json:"users"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &viaPlatform); err != nil {
		t.Fatalf("解析平台视角配额: %v", err)
	}
	if viaPlatform.Used.Users != tenantQuota.Used.Users {
		t.Fatalf("平台视角 used.users=%d 与租户视角 %d 不一致", viaPlatform.Used.Users, tenantQuota.Used.Users)
	}

	// 非平台租户查别租户 → 404（先造一个第二租户作为越权目标）。
	otherTenantID := createPackageTenantForQuota(t, env, "配额越权目标租户", []int64{101, 107}, 5)
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant/%d/quota", otherTenantID), tenantAdmin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("非平台租户查别租户配额：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// 平台查不存在的租户 → 404。
	status, api = env.Request(t, http.MethodGet, "/system/tenant/999999/quota", platformAdmin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("平台查不存在租户：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
}
