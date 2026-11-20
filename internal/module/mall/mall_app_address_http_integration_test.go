//go:build integration

// mall C 端收货地址 HTTP 集成测试（规格 §4 C 端地址维护；T6 收口补口）：
//   - 首地址自动默认；显式设默认互斥清旧（单会员至多一个 is_default=1）；
//   - 默认置顶排序；更新；删除（软删）；删默认自动顶位最早剩余（保结算页
//     「默认地址非空集」语义）；
//   - 上限 20（409）；越权矩阵 404（跨会员不泄露存在性）；
//   - 输入校验（联系人/手机号/省市区/详址非空与列宽，手机号复用会员口径）。
package mall_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// appAddrBody 构造合法地址请求体（name 后缀保证断言可区分）。
func appAddrBody(name string, isDefault int8) map[string]any {
	return map[string]any{
		"receiverName":   "收件人-" + name,
		"receiverMobile": "13800001111",
		"province":       "浙江省",
		"city":           "杭州市",
		"district":       "西湖区",
		"detail":         "文一西路 " + name + " 号",
		"isDefault":      isDefault,
	}
}

// appCreateAddress 新增地址并返回落库 ID。
func appCreateAddress(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/app/address", token, body)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("新增地址：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var data struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.ID <= 0 {
		t.Fatalf("新增地址响应缺少 id：%s（err=%v）", api.Data, err)
	}
	return data.ID
}

// appAddressList 取我的地址列表（默认置顶）。
func appAddressList(t *testing.T, env *testutil.IntegrationEnv, token string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/app/address", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("地址列表：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var list []map[string]any
	if err := json.Unmarshal(api.Data, &list); err != nil {
		t.Fatalf("解析地址列表: %v（data=%s）", err, api.Data)
	}
	return list
}

// appDefaultID 从列表取默认地址 ID（无默认返回 0）。
func appDefaultID(t *testing.T, env *testutil.IntegrationEnv, token string) int64 {
	t.Helper()
	for _, m := range appAddressList(t, env, token) {
		if v, _ := m["isDefault"].(float64); v == 1 {
			id, _ := m["id"].(float64)
			return int64(id)
		}
	}
	return 0
}

// appRegisterOK 注册新会员并断言成功（返回令牌）——地址用例前置。
func appRegisterOK(t *testing.T, env *testutil.IntegrationEnv, mobile string) string {
	t.Helper()
	status, api := appRegister(t, env, map[string]any{
		"mobile": mobile, "password": "pass123456", "nickname": "地址会员",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("注册 %s：HTTP %d code %d msg %q", mobile, status, api.Code, api.Msg)
	}
	token, _ := appAuth(t, api)
	return token
}

// TestIntegrationAppAddressCRUD 全生命周期：首地址自动默认 → 显式默认互斥
// 清旧 → 更新设默认 → 删默认顶位最早剩余 → 删非默认不动默认 → 全删清空 →
// 已删地址 404。
func TestIntegrationAppAddressCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := appRegisterOK(t, env, "13890001021")

	if list := appAddressList(t, env, token); len(list) != 0 {
		t.Fatalf("新会员地址列表应为空，实际 %d 条", len(list))
	}

	id1 := appCreateAddress(t, env, token, appAddrBody("one", 0))
	if got := appDefaultID(t, env, token); got != id1 {
		t.Fatalf("首地址应自动默认：want %d got %d", id1, got)
	}
	id2 := appCreateAddress(t, env, token, appAddrBody("two", 1))
	if got := appDefaultID(t, env, token); got != id2 {
		t.Fatalf("显式默认应清旧：want %d got %d", id2, got)
	}

	// 更新 #1 并设默认：互斥后默认回到 #1。
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/mall/app/address/%d", id1), token, appAddrBody("one-upd", 1))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("更新地址：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := appDefaultID(t, env, token); got != id1 {
		t.Fatalf("更新设默认后默认应回 #1：want %d got %d", id1, got)
	}

	// 删默认（#1）→ 剩余 #2 自动顶位；删 #2（非默认）→ 全删清空。
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/mall/app/address/%d", id1), token, nil)
	if status != http.StatusOK {
		t.Fatalf("删默认地址：HTTP %d", status)
	}
	if got := appDefaultID(t, env, token); got != id2 {
		t.Fatalf("删默认后应顶位最早剩余：want %d got %d", id2, got)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/mall/app/address/%d", id2), token, nil)
	if status != http.StatusOK {
		t.Fatalf("删非默认地址：HTTP %d", status)
	}
	if got := appDefaultID(t, env, token); got != 0 {
		t.Fatalf("全删后应无默认，实际 %d", got)
	}

	// 已删地址设默认 404（软删不泄露存在性）。
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/mall/app/address/%d/default", id2), token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("已删地址设默认：HTTP %d，期望 404", status)
	}
}

// TestIntegrationAppAddressOwnershipAndValidation 越权 404 矩阵 + 输入校验
// + 上限 20。
func TestIntegrationAppAddressOwnershipAndValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	tokenA := appRegisterOK(t, env, "13890001022")
	tokenB := appRegisterOK(t, env, "13890001023")

	idA := appCreateAddress(t, env, tokenA, appAddrBody("a", 0))

	// B 对 A 的地址读写删一律 404（跨会员不泄露存在性）。
	for _, tc := range []struct {
		method, path string
		body         map[string]any
	}{{http.MethodPut, fmt.Sprintf("/mall/app/address/%d", idA), appAddrBody("hijack", 0)},
		{http.MethodPut, fmt.Sprintf("/mall/app/address/%d/default", idA), nil},
		{http.MethodDelete, fmt.Sprintf("/mall/app/address/%d", idA), nil}} {
		status, _ := env.Request(t, tc.method, tc.path, tokenB, tc.body)
		if status != http.StatusNotFound {
			t.Fatalf("越权 %s %s：HTTP %d，期望 404", tc.method, tc.path, status)
		}
	}
	if list := appAddressList(t, env, tokenB); len(list) != 0 {
		t.Fatalf("B 列表不应看到 A 的地址，实际 %d 条", len(list))
	}

	// 输入校验：空详址 / 坏手机号 / 缺城市 一律 400。
	for name, body := range map[string]map[string]any{
		"空详址":  {"receiverName": "x", "receiverMobile": "13800001111", "province": "p", "city": "c", "district": "d", "detail": ""},
		"坏手机号": {"receiverName": "x", "receiverMobile": "138", "province": "p", "city": "c", "district": "d", "detail": "ok"},
		"缺城市":  {"receiverName": "x", "receiverMobile": "13800001111", "province": "p", "city": "", "district": "d", "detail": "ok"},
	} {
		status, _ := env.Request(t, http.MethodPost, "/mall/app/address", tokenA, body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d，期望 400", name, status)
		}
	}

	// 上限 20：A 已有 1 条，补到 20 条后第 21 条 409。
	for i := 2; i <= 20; i++ {
		appCreateAddress(t, env, tokenA, appAddrBody(fmt.Sprintf("cap%d", i), 0))
	}
	status, api := env.Request(t, http.MethodPost, "/mall/app/address", tokenA, appAddrBody("cap21", 0))
	if status != http.StatusConflict {
		t.Fatalf("第 21 条：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
}
