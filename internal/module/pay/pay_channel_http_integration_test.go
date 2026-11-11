//go:build integration

// pay 渠道域 HTTP 集成测试（issue #131，docs/pay-mvp-spec.md §8.1 主 seam）：
// 真实 router + 真实 MySQL，走 HTTP 契约验证 14 行种子、凭证三段式
// （明文落库 / 查询脱敏 / 更新占位符合并）、平台租户收口与权限码。
package pay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationPayChannelSeed 渠道 14 行种子（spec §3.1）：mock/wallet 启用、
// 其余停用且 config={}；平台渠道无删除端点（禁删）。
func TestIntegrationPayChannelSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/pay/channel/page?pageNo=1&pageSize=50", admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("渠道分页：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		Total int64 `json:"total"`
		List  []struct {
			ID     int64  `json:"id"`
			Code   string `json:"code"`
			Name   string `json:"name"`
			Status int8   `json:"status"`
			Config string `json:"config"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析渠道分页: %v", err)
	}
	if page.Total != 14 || len(page.List) != 14 {
		t.Fatalf("渠道应为 14 行种子，实际 total=%d list=%d", page.Total, len(page.List))
	}
	byCode := make(map[string]struct {
		id     int64
		status int8
		config string
	}, len(page.List))
	for _, row := range page.List {
		byCode[row.Code] = struct {
			id     int64
			status int8
			config string
		}{row.ID, row.Status, row.Config}
	}
	for _, code := range []string{"mock", "wallet"} {
		if byCode[code].status != 0 {
			t.Fatalf("渠道 %s 种子应启用，实际 %d", code, byCode[code].status)
		}
	}
	for _, code := range []string{"wx_pub", "wx_lite", "wx_app", "wx_native", "wx_wap", "wx_bar",
		"alipay_pc", "alipay_wap", "alipay_app", "alipay_qr", "alipay_bar", "alipay_lite"} {
		if byCode[code].status != 1 {
			t.Fatalf("渠道 %s 种子应停用，实际 %d", code, byCode[code].status)
		}
		if byCode[code].config != "{}" {
			t.Fatalf("渠道 %s 种子 config 应为 {}，实际 %s", code, byCode[code].config)
		}
	}

	// 禁删：DELETE /pay/channel/:id 未注册路由（渠道禁删只启停，spec §4.10）。
	walletID := byCode["wallet"].id
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/channel/%d", walletID), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("渠道删除端点不应存在：HTTP %d，期望 404", status)
	}
}

// TestIntegrationPayChannelSecretMasking 凭证三段式（spec §3.1，照 message_channel）：
// 明文落库、查询脱敏 ******、更新提交 ****** 保留原值；响应全程无明文凭证。
func TestIntegrationPayChannelSecretMasking(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	walletID := channelIDByCode(t, env, admin, "wallet")

	// 1) 写入明文凭证：响应即脱敏（密钥只进不出）。
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", walletID), admin, map[string]any{
		"config": `{"secretKey":"s3cret-value","signName":"签名甲"}`,
		"remark": "钱包测试凭证",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("更新渠道凭证：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if strings.Contains(string(api.Data), "s3cret-value") {
		t.Fatalf("更新响应泄漏明文凭证：%s", api.Data)
	}

	// 2) 查询脱敏：secretKey=******，非密钥字段明文展示；响应无明文凭证。
	status, api = env.Request(t, http.MethodGet, "/pay/channel/page?code=wallet&pageNo=1&pageSize=10", admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("渠道查询：HTTP %d code %d", status, api.Code)
	}
	if strings.Contains(string(api.Data), "s3cret-value") {
		t.Fatalf("查询响应泄漏明文凭证：%s", api.Data)
	}
	var page struct {
		List []struct {
			Config string `json:"config"`
			Remark string `json:"remark"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析渠道分页: %v", err)
	}
	if len(page.List) != 1 {
		t.Fatalf("wallet 渠道应唯一命中，实际 %d 条", len(page.List))
	}
	cfg := parseJSON(t, page.List[0].Config)
	if cfg["secretKey"] != "******" {
		t.Fatalf("secretKey 查询应脱敏为 ******，实际 %v", cfg["secretKey"])
	}
	if cfg["signName"] != "签名甲" {
		t.Fatalf("非密钥字段应保留明文，实际 %v", cfg["signName"])
	}

	// 3) 明文落库（库里有、响应没有——三段式第一段）。
	var stored string
	if err := env.DB.Table("pay_channel").Where("id = ?", int64(walletID)).Select("config").Scan(&stored).Error; err != nil {
		t.Fatalf("读取 pay_channel.config: %v", err)
	}
	if !strings.Contains(stored, "s3cret-value") {
		t.Fatalf("凭证应明文落库（三段式），实际 %s", stored)
	}

	// 4) 更新提交 ******：保留原值；其他字段按提交值覆盖。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", walletID), admin, map[string]any{
		"config": `{"secretKey":"******","signName":"签名乙"}`,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("占位符合并更新：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if err := env.DB.Table("pay_channel").Where("id = ?", int64(walletID)).Select("config").Scan(&stored).Error; err != nil {
		t.Fatalf("读取 pay_channel.config: %v", err)
	}
	merged := parseJSON(t, stored)
	if merged["secretKey"] != "s3cret-value" {
		t.Fatalf("提交 ****** 应保留原密钥值，实际 %v（config=%s）", merged["secretKey"], stored)
	}
	if merged["signName"] != "签名乙" {
		t.Fatalf("非密钥字段应按提交值覆盖，实际 %v（config=%s）", merged["signName"], stored)
	}

	// 5) 非法 config（非 JSON 对象）400。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", walletID), admin, map[string]any{
		"config": "not-json",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("非法 config 应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationPayChannelPlatformOnly 渠道是平台级资源：非平台租户即使持
// 全部权限码也 403（requirePlatform 收口）；租户侧只读清单无凭证字段。
func TestIntegrationPayChannelPlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	walletID := channelIDByCode(t, env, admin, "wallet")

	tenant2 := createTenantByAPI(t, env, admin, "支付渠道平台隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	status, api := env.Request(t, http.MethodGet, "/pay/channel/page?pageNo=1&pageSize=10", token2, nil)
	if status != http.StatusForbidden {
		t.Fatalf("租户 2 查渠道分页应 403，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", walletID), token2, map[string]any{
		"status": 0,
	})
	if status != http.StatusForbidden {
		t.Fatalf("租户 2 改渠道应 403，实际 HTTP %d", status)
	}

	// 内置普通角色（role id=2，demo 账号）未授任何 pay 权限码 → 403。
	demo := env.Login(t, "demo", "123456")
	status, _ = env.Request(t, http.MethodGet, "/pay/channel/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色查渠道分页应 403，实际 HTTP %d", status)
	}

	// 租户侧只读清单：平台管理员可读、无凭证字段；普通角色无 pay:app:query → 403。
	status, api = env.Request(t, http.MethodGet, "/pay/channel/list", admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户侧渠道清单：HTTP %d code %d", status, api.Code)
	}
	if strings.Contains(string(api.Data), "config") || strings.Contains(string(api.Data), "s3cret") {
		t.Fatalf("租户侧清单不应携带凭证字段：%s", api.Data)
	}
	var options []map[string]any
	if err := json.Unmarshal(api.Data, &options); err != nil {
		t.Fatalf("解析渠道清单: %v", err)
	}
	if len(options) != 14 {
		t.Fatalf("租户侧渠道清单应 14 行，实际 %d", len(options))
	}
	status, _ = env.Request(t, http.MethodGet, "/pay/channel/list", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色查租户侧清单应 403（pay:app:query 未授），实际 HTTP %d", status)
	}
}

// ---------- 复用助手 ----------

// channelIDByCode 查渠道 ID（平台管理员）。
func channelIDByCode(t *testing.T, env *testutil.IntegrationEnv, token, code string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/pay/channel/page?code="+code+"&pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查渠道 %s：HTTP %d code %d msg %q", code, status, api.Code, api.Msg)
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析渠道分页: %v", err)
	}
	if len(page.List) != 1 {
		t.Fatalf("渠道 %s 应唯一命中，实际 %d 条", code, len(page.List))
	}
	return page.List[0].ID
}

// parseJSON 解析 JSON 对象（测试断言用）。
func parseJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatalf("解析 JSON %s: %v", raw, err)
	}
	return obj
}
