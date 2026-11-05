//go:build integration

package testutil_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationSmokeLoginAndUserPage 是集成基座的验收冒烟：
// 登录 → 带 token 访问用户分页，全程经过真实 router 的 JWT、权限、租户中间件。
func TestIntegrationSmokeLoginAndUserPage(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 未携带 token 访问受保护接口：JWT 中间件必须拦截为 401。
	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", "", nil)
	if status != http.StatusUnauthorized || api.Code != 401 {
		t.Fatalf("无 token 访问用户分页：HTTP %d code %d msg %q，期望 HTTP 401 code 401", status, api.Code, api.Msg)
	}

	// 错误密码：登录接口本身可用（HTTP 200），业务码非零。
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]string{
		"username": "admin",
		"password": "wrong-password",
	})
	if status != http.StatusOK || api.Code == 0 {
		t.Fatalf("错误密码登录：HTTP %d code %d msg %q，期望 HTTP 200 且业务码非零", status, api.Code, api.Msg)
	}

	// 正确登录：拿到 accessToken。
	token := env.Login(t, "admin", "123456")

	// 带 token 访问用户分页：seed 的用户必须可见。
	status, api = env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("带 token 访问用户分页：HTTP %d code %d msg %q，期望 HTTP 200 code 0", status, api.Code, api.Msg)
	}
	var page struct {
		List []struct {
			Username string `json:"username"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页响应: %v（data=%s）", err, string(api.Data))
	}
	if page.Total < 1 || len(page.List) < 1 {
		t.Fatalf("用户分页为空：total=%d list=%d，期望至少包含 seed 的 admin", page.Total, len(page.List))
	}
}

// TestIntegrationSmokeSecondEnv 再起一个全新环境并重复登录链路：
// 前一个用例的清理（清表 + 关停）不能影响下一个用例从干净基线重新迁移与 seed。
func TestIntegrationSmokeSecondEnv(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// demo 是普通角色（仅本人数据），能登录并查看用户分页说明
	// seed 完整、权限中间件对普通账号同样放行其可见范围。
	token := env.Login(t, "demo", "123456")

	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("demo 访问用户分页：HTTP %d code %d msg %q，期望 HTTP 200 code 0", status, api.Code, api.Msg)
	}
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页响应: %v（data=%s）", err, string(api.Data))
	}
	if page.Total < 1 {
		t.Fatalf("demo 用户分页为空：total=%d，期望至少包含其本人", page.Total)
	}
}
