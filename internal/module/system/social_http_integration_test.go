//go:build integration

// 社交登录骨架集成测试（issue #76，spec §4.4 / ADR 0011）：
// 用假 provider（回放固定三方身份）经真实 HTTP 端点覆盖 state 流转、登录
// 签发（login_type=3）、绑定幂等与解绑、客户端 CRUD 与权限收口。
package system_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// socialOpenID 是假 provider 回放的三方身份。
const socialOpenID = "openid-76-abc"

// fakeSocialProvider 是 provider 替身：AuthorizeURL 把 state 与 PKCE
// challenge 拼进 query 供断言；ExchangeIdentity 校验 code 后回放固定身份。
type fakeSocialProvider struct {
	openID string
	code   string
}

func newFakeSocialProvider() *fakeSocialProvider {
	return &fakeSocialProvider{openID: socialOpenID, code: "mock"}
}

func (f *fakeSocialProvider) Code() string { return f.code }

func (f *fakeSocialProvider) AuthorizeURL(_ context.Context, _ *model.SocialClient, state, codeChallenge string) (string, error) {
	return fmt.Sprintf("https://mock.example.com/oauth/authorize?state=%s&code_challenge=%s&code_challenge_method=S256",
		url.QueryEscape(state), url.QueryEscape(codeChallenge)), nil
}

func (f *fakeSocialProvider) ExchangeIdentity(_ context.Context, _ *model.SocialClient, code, codeVerifier string) (*systemservice.SocialIdentity, error) {
	if code != "good-code" || codeVerifier == "" {
		return nil, fmt.Errorf("mock exchange failed")
	}
	return &systemservice.SocialIdentity{
		OpenID:   f.openID,
		UnionID:  "unionid-76",
		Nickname: "三方昵称",
		Avatar:   "https://mock.example.com/a.png",
		RawInfo:  `{"openid":"` + f.openID + `"}`,
	}, nil
}

// createSocialClientFor 用平台管理员建一个启用客户端（provider 可指定）。
func createSocialClientFor(t *testing.T, env *testutil.IntegrationEnv, token, code, provider string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/social/client", token, map[string]any{
		"code": code, "name": "客户端" + code, "provider": provider,
		"clientId": "cid-" + code, "clientSecret": "secret-" + code,
		"redirectUri": "https://app.example.com/social/callback", "status": 0,
	})
	mustOK(t, "创建社交客户端 "+code, status, api)
	return 0
}

// authorize 走公开 authorize-url 拿 (url, state)，断言 state 与 PKCE challenge 进了 URL。
func authorize(t *testing.T, env *testutil.IntegrationEnv, provider string, tenantID int64) (string, string) {
	t.Helper()
	path := "/system/social/authorize-url?provider=" + url.QueryEscape(provider)
	if tenantID > 0 {
		path = fmt.Sprintf("%s&tenantId=%d", path, tenantID)
	}
	status, api := env.Request(t, http.MethodGet, path, "", nil)
	mustOK(t, "生成授权地址", status, api)
	var data struct {
		URL   string `json:"url"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析授权地址响应: %v（data=%s）", err, string(api.Data))
	}
	if data.URL == "" || data.State == "" {
		t.Fatalf("授权地址响应不完整: %s", string(api.Data))
	}
	for _, sub := range []string{"state=" + data.State, "code_challenge", "S256"} {
		if !strings.Contains(data.URL, sub) {
			t.Fatalf("授权地址应携带 %s: %s", sub, data.URL)
		}
	}
	return data.URL, data.State
}

// socialCallback 公开回调；返回 HTTP 状态、业务码与原始 data。
func socialCallback(t *testing.T, env *testutil.IntegrationEnv, provider, code, state string) (int, int, map[string]any) {
	t.Helper()
	resp, err := http.Get(env.APIPrefixURL(fmt.Sprintf("/system/social/callback?provider=%s&code=%s&state=%s",
		url.QueryEscape(provider), url.QueryEscape(code), url.QueryEscape(state))))
	if err != nil {
		t.Fatalf("调用回调: %v", err)
	}
	defer resp.Body.Close()
	var api struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析回调响应: %v", err)
	}
	return resp.StatusCode, api.Code, api.Data
}

// bindInitiate 受保护端点发起绑定，返回 state。
func bindInitiate(t *testing.T, env *testutil.IntegrationEnv, token, provider string) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/social/bind", token, map[string]any{"provider": provider})
	mustOK(t, "发起绑定 "+provider, status, api)
	var data struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.State == "" {
		t.Fatalf("绑定发起响应缺少 state: %v（data=%s）", err, string(api.Data))
	}
	return data.State
}

func countRows(t *testing.T, env *testutil.IntegrationEnv, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := tenant.Skip(env.DB).Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("统计行数 %s: %v", query, err)
	}
	return n
}

// TestIntegrationSocialLoginFlow 已绑定用户走完整登录链路：
// 预置绑定 → authorize-url → callback → 签发 JWT + 登录日志 login_type=3；
// 未绑定 → 409 业务码 1901（不自动建号）；state 重放 → 400。
func TestIntegrationSocialLoginFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	env.App.Social.RegisterSocialProvider(newFakeSocialProvider())
	platformAdmin := env.Login(t, "admin", "123456")
	createSocialClientFor(t, env, platformAdmin, "c-mock", "mock")

	// 预置绑定：admin（tenant 1）↔ mock openid。
	binding := &model.SocialUser{
		TenantID: 1, UserID: 1, Provider: "mock", OpenID: socialOpenID,
		Status: model.StatusEnable,
	}
	if err := tenant.Skip(env.DB).Create(binding).Error; err != nil {
		t.Fatalf("预置绑定: %v", err)
	}

	// 未带 tenantId → 缺省租户 1（spec §4.4.2）。
	_, state := authorize(t, env, "mock", 0)
	status, code, data := socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusOK || code != 0 {
		t.Fatalf("已绑定登录应成功：HTTP %d code %d", status, code)
	}
	if data["mode"] != "login" || data["accessToken"] == "" || data["username"] != "admin" {
		t.Fatalf("登录回调应签发 token：data=%v", data)
	}
	// 授权码不回流（#85 安全审计 C7 硬化）：回调成功响应不回显 code/state，
	// 前端拿到的载荷可安全跳转（配合回调页从地址栏清掉 code/state）。
	if _, ok := data["code"]; ok {
		t.Fatalf("回调响应不应回显 code：data=%v", data)
	}
	if _, ok := data["state"]; ok {
		t.Fatalf("回调响应不应回显 state：data=%v", data)
	}

	// 登录日志 login_type=3（验收标准）。
	var logType int64
	if err := tenant.Skip(env.DB).Raw("SELECT login_type FROM system_login_log WHERE user_id = 1 AND login_type = 3 ORDER BY id DESC LIMIT 1").
		Row().Scan(&logType); err != nil || logType != 3 {
		t.Fatalf("应写入 login_type=3 的登录日志: type=%d err=%v", logType, err)
	}

	// 绑定表 last_login_at 已回填。
	var row model.SocialUser
	if err := tenant.Skip(env.DB).First(&row, binding.ID).Error; err != nil {
		t.Fatalf("查询绑定行: %v", err)
	}
	if row.LastLoginAt == nil {
		t.Fatalf("last_login_at 应回填")
	}

	// 未绑定 → HTTP 409 + 业务码 1901（验收标准，不自动建号）。
	createSocialClientFor(t, env, platformAdmin, "c-mock2", "mock2")
	env.App.Social.RegisterSocialProvider(&fakeSocialProvider{openID: "openid-76-unbound", code: "mock2"})
	_, state = authorize(t, env, "mock2", 0)
	status, code, _ = socialCallback(t, env, "mock2", "good-code", state)
	if status != http.StatusConflict || code != 1901 {
		t.Fatalf("未绑定应 409/1901：HTTP %d code %d", status, code)
	}
	if n := countRows(t, env, "SELECT COUNT(*) FROM system_user WHERE username LIKE 'openid-76%'"); n != 0 {
		t.Fatalf("未绑定不得自动建号：count=%d", n)
	}

	// state 重放 → 400（验收标准）。
	status, _, _ = socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusBadRequest {
		t.Fatalf("state 重放应 400：HTTP %d", status)
	}
}

// TestIntegrationSocialStateGuards state 流转守卫：
// 伪造 state / provider 不匹配 / 过期 state → 400；未注册 provider → 400。
func TestIntegrationSocialStateGuards(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	env.App.Social.RegisterSocialProvider(newFakeSocialProvider())
	platformAdmin := env.Login(t, "admin", "123456")
	createSocialClientFor(t, env, platformAdmin, "c-mock", "mock")

	// 伪造/未知 state → 400。
	status, _, _ := socialCallback(t, env, "mock", "good-code", "forged-state")
	if status != http.StatusBadRequest {
		t.Fatalf("伪造 state 应 400：HTTP %d", status)
	}

	// provider 与 state 签发时不匹配 → 400（验收标准）。
	_, state := authorize(t, env, "mock", 0)
	status, _, _ = socialCallback(t, env, "other", "good-code", state)
	if status != http.StatusBadRequest {
		t.Fatalf("provider 不匹配应 400：HTTP %d", status)
	}

	// 过期 state → 400（把 TTL 调短模拟 10 分钟过期，spec §4.4.2）。
	env.App.Social.SetStateTTL(time.Millisecond)
	_, state = authorize(t, env, "mock", 0)
	time.Sleep(20 * time.Millisecond)
	status, _, _ = socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusBadRequest {
		t.Fatalf("过期 state 应 400：HTTP %d", status)
	}
	env.App.Social.SetStateTTL(10 * time.Minute)

	// 未注册 provider 的平台 → authorize-url 400。
	status, api := env.Request(t, http.MethodGet, "/system/social/authorize-url?provider=nobody", "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("未注册 provider 应 400：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationSocialBindFlow 绑定链路（spec §4.4.6）：
// bind → callback 写入绑定；重复绑定幂等；同一 openid 绑定其他用户 409；
// 解绑软删、重复解绑 404、跨租户 404；解绑后可重新绑定。
func TestIntegrationSocialBindFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	env.App.Social.RegisterSocialProvider(newFakeSocialProvider())
	platformAdmin := env.Login(t, "admin", "123456")
	createSocialClientFor(t, env, platformAdmin, "c-mock", "mock")

	// 发起绑定（受保护，登录即可）→ 回调完成绑定，写入绑定行。
	state := bindInitiate(t, env, platformAdmin, "mock")
	status, code, data := socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusOK || code != 0 || data["mode"] != "bind" {
		t.Fatalf("绑定回调应成功：HTTP %d code %d data=%v", status, code, data)
	}
	var bindingID int64
	if err := tenant.Skip(env.DB).Raw("SELECT id FROM system_social_user WHERE provider='mock' AND openid=?", socialOpenID).
		Row().Scan(&bindingID); err != nil || bindingID == 0 {
		t.Fatalf("应写入绑定行：id=%d err=%v", bindingID, err)
	}
	if n := countRows(t, env, "SELECT COUNT(*) FROM system_social_user WHERE provider='mock' AND openid=?", socialOpenID); n != 1 {
		t.Fatalf("绑定应恰好一行：count=%d", n)
	}

	// 重复绑定同一 openid → 幂等成功，不新增行（验收标准）。
	state = bindInitiate(t, env, platformAdmin, "mock")
	status, code, _ = socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusOK || code != 0 {
		t.Fatalf("重复绑定应幂等成功：HTTP %d code %d", status, code)
	}
	if n := countRows(t, env, "SELECT COUNT(*) FROM system_social_user WHERE provider='mock' AND openid=?", socialOpenID); n != 1 {
		t.Fatalf("重复绑定不应新增行：count=%d", n)
	}

	// 同一 openid 绑定其他用户 → 409（spec §4.4.6）。
	status, api := env.Request(t, http.MethodPost, "/system/user", platformAdmin, map[string]any{
		"username": "socialuser2", "password": "123456", "nickname": "社交用户2", "status": 0,
	})
	mustOK(t, "创建第二个用户", status, api)
	user2Token := env.Login(t, "socialuser2", "123456")
	state = bindInitiate(t, env, user2Token, "mock")
	status, _, _ = socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusConflict {
		t.Fatalf("同一 openid 绑定其他用户应 409：HTTP %d", status)
	}

	// 解绑（system:social-user:delete）：软删。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/user/%d", bindingID), platformAdmin, nil)
	mustOK(t, "解绑", status, api)
	var deletedAt sql.NullTime
	if err := tenant.Skip(env.DB).Raw("SELECT deleted_at FROM system_social_user WHERE id=?", bindingID).
		Row().Scan(&deletedAt); err != nil || !deletedAt.Valid {
		t.Fatalf("解绑应软删: err=%v valid=%v", err, deletedAt.Valid)
	}

	// 再次解绑同一 ID → 404（spec §4.4.7：不存在 404）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/user/%d", bindingID), platformAdmin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("重复解绑应 404：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 跨租户绑定 ID → 404（租户插件隔离，验收标准）。
	tenantID := createTenantForPlatformTest(t, env, platformAdmin, "社交隔离租户")
	cross := &model.SocialUser{TenantID: tenantID, UserID: 1, Provider: "mock", OpenID: "openid-t2", Status: model.StatusEnable}
	if err := tenant.Skip(env.DB).Create(cross).Error; err != nil {
		t.Fatalf("预置租户2绑定: %v", err)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/user/%d", cross.ID), platformAdmin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户解绑应 404：HTTP %d", status)
	}

	// 解绑后重新绑定 → 成功（唯一键含 deleted_at，绑定/解绑幂等语义）。
	state = bindInitiate(t, env, platformAdmin, "mock")
	status, code, _ = socialCallback(t, env, "mock", "good-code", state)
	if status != http.StatusOK || code != 0 {
		t.Fatalf("解绑后重新绑定应成功：HTTP %d code %d", status, code)
	}
}

// TestIntegrationSocialClientManagement 客户端 CRUD（仅平台租户）+ 密钥脱敏。
func TestIntegrationSocialClientManagement(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	// 创建 + 分页：client_secret 脱敏为 ******（spec §4.11.1）。
	createSocialClientFor(t, env, platformAdmin, "c-mock", "mock")
	status, api := env.Request(t, http.MethodGet, "/system/social/client/page?pageNo=1&pageSize=10", platformAdmin, nil)
	mustOK(t, "客户端分页", status, api)
	var page struct {
		List []struct {
			ID           int64  `json:"id"`
			ClientSecret string `json:"clientSecret"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析客户端分页: %v", err)
	}
	if len(page.List) != 1 || page.List[0].ClientSecret != "******" {
		t.Fatalf("client_secret 应脱敏为 ******: %+v", page.List)
	}
	clientID := page.List[0].ID

	// 编码重复 → 409。
	status, api = env.Request(t, http.MethodPost, "/system/social/client", platformAdmin, map[string]any{
		"code": "c-mock", "name": "重复编码", "provider": "mock",
		"clientId": "cid-x", "clientSecret": "s", "redirectUri": "https://x.example.com/cb", "status": 0,
	})
	if status != http.StatusConflict {
		t.Fatalf("重复编码应 409：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 修改：提交 ****** 保留原密钥（spec §4.11.1）。
	status, api = env.Request(t, http.MethodPut, "/system/social/client", platformAdmin, map[string]any{
		"id": clientID, "code": "c-mock", "name": "改名客户端", "provider": "mock",
		"clientId": "cid-c-mock", "clientSecret": "******",
		"redirectUri": "https://app.example.com/social/callback", "status": 0,
	})
	mustOK(t, "修改客户端", status, api)
	var secret string
	if err := tenant.Skip(env.DB).Raw("SELECT client_secret FROM system_social_client WHERE id=?", clientID).
		Row().Scan(&secret); err != nil || secret != "secret-c-mock" {
		t.Fatalf("提交 ****** 应保留原密钥：got %q err=%v", secret, err)
	}

	// 非平台租户管理员 → 客户端管理 403（平台级资源收口）。
	tenantID := createTenantForPlatformTest(t, env, platformAdmin, "社交客户端越权租户")
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)
	status, api = env.Request(t, http.MethodGet, "/system/social/client/page?pageNo=1&pageSize=10", tenantAdmin, nil)
	if status != http.StatusForbidden {
		t.Fatalf("非平台租户查客户端应 403：HTTP %d", status)
	}
	status, api = env.Request(t, http.MethodPost, "/system/social/client", tenantAdmin, map[string]any{
		"code": "t2", "name": "越权", "provider": "mock", "clientId": "c", "clientSecret": "s", "redirectUri": "https://x/cb", "status": 0,
	})
	if status != http.StatusForbidden {
		t.Fatalf("非平台租户建客户端应 403：HTTP %d", status)
	}

	// 删除。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/client/%d", clientID), platformAdmin, nil)
	mustOK(t, "删除客户端", status, api)
	// 软删：活跃行计数归零（Raw 统计需显式排除 deleted_at）。
	if n := countRows(t, env, "SELECT COUNT(*) FROM system_social_client WHERE id=? AND deleted_at IS NULL", clientID); n != 0 {
		t.Fatalf("客户端删除后不应再有活跃行：count=%d", n)
	}
}

// TestIntegrationSocialEndpointAuth 端点鉴权：受保护端点匿名 401；
// bind 登录即可（未配置 provider 时 400 而非 401/403）。
func TestIntegrationSocialEndpointAuth(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	status, _ := env.Request(t, http.MethodGet, "/system/social/user/page?pageNo=1&pageSize=10", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("匿名查绑定用户应 401：HTTP %d", status)
	}
	status, api := env.Request(t, http.MethodPost, "/system/social/bind", platformAdmin, map[string]any{"provider": "mock"})
	if status != http.StatusBadRequest {
		t.Fatalf("登录后 bind（未配置 provider）应 400：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationSocialAuthorizeURLRateLimit 公开端点按 IP 限速（#85 安全审计
// NV1）：authorize-url 每分钟 60 次，第 61 次返回 429；窗口内正常请求不受影响。
// 每用例独立 env（独立 Handler/限速器），不与其他用例的 authorize 调用串窗。
func TestIntegrationSocialAuthorizeURLRateLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	createSocialClientFor(t, env, admin, "rl-client", "wechat_open")

	const limit = 60 // 与 api.publicIPLimit 对齐
	path := "/system/social/authorize-url?provider=wechat_open"
	last := 0
	for i := 1; i <= limit+1; i++ {
		status, api := env.Request(t, http.MethodGet, path, "", nil)
		last = status
		if status == http.StatusTooManyRequests {
			if i <= limit {
				t.Fatalf("第 %d 次即 429，早于上限", i)
			}
			if api.Code != http.StatusTooManyRequests {
				t.Fatalf("429 业务码不符：%d", api.Code)
			}
			break
		}
		if status != http.StatusOK {
			t.Fatalf("第 %d 次应 200：HTTP %d code %d msg %q", i, status, api.Code, api.Msg)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("%d 次调用未触发 429：最后一次 HTTP %d", limit+1, last)
	}
}
