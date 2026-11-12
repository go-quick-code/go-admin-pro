//go:build integration

// pay 应用与渠道绑定 HTTP 集成测试（issue #131，spec §8.2）：CRUD、绑定
// 全量替换校验（未知码 409 / 停用渠道 409）、删除守卫（绑定须先解绑）、
// 租户隔离（跨租户业务 ID 一律 404）、普通角色无 pay 权限。
package pay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationPayAppCrud 应用 CRUD：app_key 租户内唯一（撞键 400）、
// 更新、软删后 404。
func TestIntegrationPayAppCrud(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 创建。
	status, api := env.Request(t, http.MethodPost, "/pay/app", admin, map[string]any{
		"appKey": "demo-app", "name": "演示应用", "status": 0,
		"orderNotifyUrl": "http://127.0.0.1:18080/pay/demo/notify/order",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建应用：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var created struct {
		ID     int64  `json:"id"`
		AppKey string `json:"appKey"`
		Status int8   `json:"status"`
	}
	if err := json.Unmarshal(api.Data, &created); err != nil {
		t.Fatalf("解析创建应用响应: %v（data=%s）", err, api.Data)
	}
	if created.ID == 0 || created.AppKey != "demo-app" || created.Status != 0 {
		t.Fatalf("创建应用返回异常：%s", api.Data)
	}

	// 撞唯一键 400（app_key 租户内唯一）。
	status, api = env.Request(t, http.MethodPost, "/pay/app", admin, map[string]any{
		"appKey": "demo-app", "name": "重复标识应用",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("重复 app_key 应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 详情：绑定码为空数组。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/app/%d", created.ID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("应用详情：HTTP %d code %d", status, api.Code)
	}
	var detail struct {
		ID           int64    `json:"id"`
		ChannelCodes []string `json:"channelCodes"`
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析应用详情: %v", err)
	}
	if len(detail.ChannelCodes) != 0 {
		t.Fatalf("新应用绑定码应为空，实际 %v", detail.ChannelCodes)
	}

	// 更新（id 在 body）。
	status, api = env.Request(t, http.MethodPut, "/pay/app", admin, map[string]any{
		"id": created.ID, "appKey": "demo-app", "name": "演示应用改名", "status": 1,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("修改应用：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/app/%d", created.ID), admin, nil)
	var updated struct {
		Name   string `json:"name"`
		Status int8   `json:"status"`
	}
	if err := json.Unmarshal(api.Data, &updated); err != nil {
		t.Fatalf("解析应用详情: %v", err)
	}
	if updated.Name != "演示应用改名" || updated.Status != 1 {
		t.Fatalf("应用修改未生效：name=%q status=%d", updated.Name, updated.Status)
	}

	// 删除（无绑定）：软删后详情 404。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", created.ID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除应用：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/app/%d", created.ID), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
}

// TestIntegrationPayAppBinding 应用渠道绑定：未知码 409、停用渠道 409、
// 启用渠道可绑、全量替换、重复提交去重；有绑定禁删（spec §4.10）。
func TestIntegrationPayAppBinding(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appID := createPayApp(t, env, admin, "bind-app", "绑定演示应用")

	// 未知渠道码 → 409。
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), admin, map[string]any{
		"channelCodes": []string{"wx_pub_not_real"},
	})
	if status != http.StatusConflict {
		t.Fatalf("未知渠道码应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 停用渠道（wx_pub 种子停用）→ 409。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), admin, map[string]any{
		"channelCodes": []string{"mock", "wx_pub"},
	})
	if status != http.StatusConflict {
		t.Fatalf("停用渠道应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 启用渠道（mock/wallet）→ 绑定成功。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), admin, map[string]any{
		"channelCodes": []string{"mock", "wallet"},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("绑定启用渠道：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	assertAppChannels(t, env, admin, appID, []string{"mock", "wallet"})

	// 重复提交去重：["mock","mock"] 等价 ["mock"]。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), admin, map[string]any{
		"channelCodes": []string{"mock", "mock"},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重复码绑定：HTTP %d code %d", status, api.Code)
	}
	assertAppChannels(t, env, admin, appID, []string{"mock"})

	// 有绑定禁删 → 409（须先解绑）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", appID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("有绑定删除应用应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 全量替换为空数组 = 解绑全部 → 删除放行。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), admin, map[string]any{
		"channelCodes": []string{},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("解绑全部：HTTP %d code %d", status, api.Code)
	}
	assertAppChannels(t, env, admin, appID, []string{})
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", appID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("解绑后删除应用：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationPayAppTenantIsolation 租户隔离：租户 2 看不到租户 1 的应用，
// 跨租户详情/更新/删除/绑定一律 404（不泄露存在性）。
func TestIntegrationPayAppTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appID := createPayApp(t, env, admin, "iso-app", "隔离演示应用")
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), admin, map[string]any{
		"channelCodes": []string{"mock"},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("绑定渠道：HTTP %d code %d", status, api.Code)
	}

	tenant2 := createTenantByAPI(t, env, admin, "支付应用隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 列表互不可见。
	status, api = env.Request(t, http.MethodGet, "/pay/app/page?pageNo=1&pageSize=10", token2, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 2 应用分页：HTTP %d code %d", status, api.Code)
	}
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析应用分页: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("租户 2 应用分页 total 应 0，实际 %d", page.Total)
	}

	// 跨租户详情/更新/删除/绑定/绑定查询一律 404。
	for _, tc := range []struct {
		method string
		path   string
		body   map[string]any
	}{
		{http.MethodGet, fmt.Sprintf("/pay/app/%d", appID), nil},
		{http.MethodGet, fmt.Sprintf("/pay/app/%d/channels", appID), nil},
		{http.MethodPut, "/pay/app", map[string]any{"id": appID, "appKey": "hacked", "name": "越权"}},
		{http.MethodDelete, fmt.Sprintf("/pay/app/%d", appID), nil},
		{http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), map[string]any{"channelCodes": []string{"mock"}}},
	} {
		status, _ := env.Request(t, tc.method, tc.path, token2, tc.body)
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 %s %s：HTTP %d，期望 404", tc.method, tc.path, status)
		}
	}

	// 普通角色（demo）无 pay 权限码：应用分页 403。
	demo := env.Login(t, "demo", "123456")
	status, _ = env.Request(t, http.MethodGet, "/pay/app/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色查应用分页应 403，实际 HTTP %d", status)
	}
}

// ---------- 复用助手 ----------

// createPayApp 创建支付应用并返回 ID。
func createPayApp(t *testing.T, env *testutil.IntegrationEnv, token, appKey, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/app", token, map[string]any{
		"appKey": appKey, "name": name,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建应用 %s：HTTP %d code %d msg %q", appKey, status, api.Code, api.Msg)
	}
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建应用响应: %v", err)
	}
	return row.ID
}

// assertAppChannels 断言应用绑定码恰为 want（顺序无关）。
func assertAppChannels(t *testing.T, env *testutil.IntegrationEnv, token string, appID int64, want []string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/pay/app/%d", appID), token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("应用详情：HTTP %d code %d", status, api.Code)
	}
	var detail struct {
		ChannelCodes []string `json:"channelCodes"`
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析应用详情: %v", err)
	}
	got := map[string]bool{}
	for _, code := range detail.ChannelCodes {
		got[code] = true
	}
	wantSet := map[string]bool{}
	for _, code := range want {
		wantSet[code] = true
	}
	if len(got) != len(wantSet) {
		t.Fatalf("应用 %d 绑定码不符：实际 %v 期望 %v", appID, detail.ChannelCodes, want)
	}
	for code := range wantSet {
		if !got[code] {
			t.Fatalf("应用 %d 绑定码不符：实际 %v 期望 %v", appID, detail.ChannelCodes, want)
		}
	}
}
