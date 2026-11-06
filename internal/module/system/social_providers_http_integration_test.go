//go:build integration

// 社交登录 provider 集成测试（issue #77，spec §4.4.8）：
//   - 真实 provider（微信开放平台/企微）走本地 httptest 模拟的三方端点，
//     不依赖外网，覆盖授权地址 → 回调 → 登录/绑定全链路与端点覆盖配置；
//   - 登录页可用平台列表（公开端点，按启用客户端 + 已注册 provider 过滤）；
//   - 个人中心「我的社交绑定」列表与解绑（登录即可，只能解绑本人）。
package system_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// wecomOpenID 是企微 mock 端点回放的 userid。
const wecomOpenID = "zhangsan"

// httptestServer 是本地模拟三方端点的服务器包装。
type httptestServer struct {
	BaseURL string
}

func newHTTPTestServer(t *testing.T, h http.Handler) *httptestServer {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &httptestServer{BaseURL: srv.URL}
}

func writeJSONBody(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// createSocialClientRaw 直接经 HTTP 客户端 CRUD 建客户端（带自定义端点）。
func createSocialClientFull(t *testing.T, env *testutil.IntegrationEnv, token string, fields map[string]any) {
	t.Helper()
	fields["redirectUri"] = "https://app.example.com/social/callback"
	fields["status"] = 0
	status, api := env.Request(t, http.MethodPost, "/system/social/client", token, fields)
	mustOK(t, "创建社交客户端", status, api)
}

// socialAuthorizeSimple 调公开 authorize-url，返回授权地址与 state。
// 不沿用 authorize() 的 PKCE 断言：各平台对 PKCE 支持不同（钉钉拼、
// 微信/企微不拼），参数正确性由 provider 单测覆盖。
func socialAuthorizeSimple(t *testing.T, env *testutil.IntegrationEnv, provider string) (string, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/social/authorize-url?provider="+url.QueryEscape(provider), "", nil)
	mustOK(t, "生成授权地址", status, api)
	var data struct {
		URL   string `json:"url"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.URL == "" || data.State == "" {
		t.Fatalf("授权地址响应不完整: %v（data=%s）", err, string(api.Data))
	}
	return data.URL, data.State
}

// socialCallbackRaw 调公开回调，返回 (HTTP 状态, 业务码, data)。
func socialCallbackRaw(t *testing.T, env *testutil.IntegrationEnv, provider, code, state string) (int, int, map[string]any) {
	t.Helper()
	return socialCallback(t, env, provider, code, state)
}

// newWeChatOpenMockServer 模拟微信开放平台 token/userinfo 端点（本地，不联网）。
func newWeChatOpenMockServer(t *testing.T) *httptestServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/sns/oauth2/access_token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "good-code" {
			writeJSONBody(t, w, http.StatusOK, `{"errcode":40029,"errmsg":"invalid code"}`)
			return
		}
		writeJSONBody(t, w, http.StatusOK,
			`{"access_token":"wx-it-token","expires_in":7200,"openid":"wx-it-openid","unionid":"wx-it-unionid"}`)
	})
	mux.HandleFunc("/sns/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != "wx-it-token" {
			writeJSONBody(t, w, http.StatusOK, `{"errcode":40001,"errmsg":"invalid credential"}`)
			return
		}
		writeJSONBody(t, w, http.StatusOK,
			`{"openid":"wx-it-openid","nickname":"微信集成用户","headimgurl":"https://wx.example.com/a.png","unionid":"wx-it-unionid"}`)
	})
	return newHTTPTestServer(t, mux)
}

// newWeComMockServer 模拟企微 gettoken/getuserinfo/user/get 端点（本地，不联网）。
func newWeComMockServer(t *testing.T) *httptestServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cgi-bin/gettoken", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("corpsecret") != "wecom-it-secret" {
			writeJSONBody(t, w, http.StatusOK, `{"errcode":40013,"errmsg":"invalid corpid"}`)
			return
		}
		writeJSONBody(t, w, http.StatusOK, `{"errcode":0,"errmsg":"ok","access_token":"wecom-it-token","expires_in":7200}`)
	})
	mux.HandleFunc("/cgi-bin/auth/getuserinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "good-code" {
			writeJSONBody(t, w, http.StatusOK, `{"errcode":40029,"errmsg":"invalid code"}`)
			return
		}
		writeJSONBody(t, w, http.StatusOK, `{"errcode":0,"errmsg":"ok","userid":"`+wecomOpenID+`"}`)
	})
	mux.HandleFunc("/cgi-bin/user/get", func(w http.ResponseWriter, r *http.Request) {
		writeJSONBody(t, w, http.StatusOK,
			`{"errcode":0,"errmsg":"ok","userid":"`+wecomOpenID+`","name":"张三","avatar":"https://wecom.example.com/a.png"}`)
	})
	return newHTTPTestServer(t, mux)
}

// TestIntegrationSocialProvidersEndpoint 登录页可用平台列表（公开端点）：
// 仅返回「启用且已注册 provider」的客户端，同平台去重；停用/未注册的不出现。
func TestIntegrationSocialProvidersEndpoint(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	env.App.Social.RegisterSocialProvider(newFakeSocialProvider()) // mock 平台
	platformAdmin := env.Login(t, "admin", "123456")

	createSocialClientFor(t, env, platformAdmin, "c-mock-a", "mock")
	// 同平台第二个客户端：去重，只出现一次。
	createSocialClientFor(t, env, platformAdmin, "c-mock-b", "mock")
	// 停用客户端不出现。
	status, api := env.Request(t, http.MethodPost, "/system/social/client", platformAdmin, map[string]any{
		"code": "c-mock-off", "name": "停用客户端", "provider": "mock2",
		"clientId": "cid-off", "clientSecret": "s", "redirectUri": "https://x.example.com/cb", "status": 1,
	})
	mustOK(t, "创建停用客户端", status, api)

	// 公开端点：无需 token。
	resp, err := http.Get(env.APIPrefixURL("/system/social/providers"))
	if err != nil {
		t.Fatalf("调用 providers 列表: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("providers 列表应公开可访问：HTTP %d", resp.StatusCode)
	}
	var body struct {
		Code int `json:"code"`
		Data []struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析 providers 响应: %v", err)
	}
	if body.Code != 0 || len(body.Data) != 1 || body.Data[0].Provider != "mock" {
		t.Fatalf("应只返回启用的 mock 平台一条：code=%d data=%v", body.Code, body.Data)
	}
}

// TestIntegrationSocialMyBindings 个人中心「我的社交绑定」：
// 列表（登录即可）、解绑本人（登录即可）、解绑他人 404、匿名 401。
func TestIntegrationSocialMyBindings(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	env.App.Social.RegisterSocialProvider(newFakeSocialProvider())
	platformAdmin := env.Login(t, "admin", "123456")
	createSocialClientFor(t, env, platformAdmin, "c-mock", "mock")

	// admin 绑定 mock openid。
	state := bindInitiate(t, env, platformAdmin, "mock")
	status, code, _ := socialCallbackRaw(t, env, "mock", "good-code", state)
	if status != http.StatusOK || code != 0 {
		t.Fatalf("绑定应成功：HTTP %d code %d", status, code)
	}
	var bindingID int64
	if err := tenant.Skip(env.DB).Raw("SELECT id FROM system_social_user WHERE provider='mock' AND openid=?", socialOpenID).
		Row().Scan(&bindingID); err != nil || bindingID == 0 {
		t.Fatalf("应写入绑定行：id=%d err=%v", bindingID, err)
	}

	// 我的绑定列表（登录即可，无需 system:social-user:query 权限）。
	status, api := env.Request(t, http.MethodGet, "/system/social/my", platformAdmin, nil)
	mustOK(t, "我的绑定列表", status, api)
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Provider string `json:"provider"`
			OpenID   string `json:"openid"`
			Nickname string `json:"nickname"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析我的绑定列表: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 || page.List[0].Provider != "mock" || page.List[0].OpenID != socialOpenID {
		t.Fatalf("我的绑定列表应为本人一条 mock 绑定: %v", page)
	}

	// 匿名 → 401。
	if s, _ := env.Request(t, http.MethodGet, "/system/social/my", "", nil); s != http.StatusUnauthorized {
		t.Fatalf("匿名查我的绑定应 401：HTTP %d", s)
	}

	// 其他用户解绑他人绑定 → 404（spec §4.4.7：只能解绑本人）。
	status, api = env.Request(t, http.MethodPost, "/system/user", platformAdmin, map[string]any{
		"username": "socialmy2", "password": "123456", "nickname": "我的绑定用户2", "status": 0,
	})
	mustOK(t, "创建第二个用户", status, api)
	user2Token := env.Login(t, "socialmy2", "123456")
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/my/%d", bindingID), user2Token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("解绑他人绑定应 404：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 本人解绑 → 200，软删。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/my/%d", bindingID), platformAdmin, nil)
	mustOK(t, "解绑本人绑定", status, api)
	var deletedAt any
	if err := tenant.Skip(env.DB).Raw("SELECT deleted_at IS NOT NULL FROM system_social_user WHERE id=?", bindingID).
		Row().Scan(&deletedAt); err != nil || deletedAt != int64(1) {
		t.Fatalf("解绑应软删: err=%v deleted=%v", err, deletedAt)
	}

	// 重复解绑同一 ID → 404。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/social/my/%d", bindingID), platformAdmin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("重复解绑应 404：HTTP %d", status)
	}
}

// TestIntegrationSocialWeChatOpenLoginFlow 微信开放平台真实 provider 全链路
// （三方端点用本地 httptest 模拟，不依赖外网）：预置绑定 → authorize-url →
// callback → JWT 签发；openid/unionid 正确落绑定表。
func TestIntegrationSocialWeChatOpenLoginFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	mock := newWeChatOpenMockServer(t)
	createSocialClientFull(t, env, platformAdmin, map[string]any{
		"code": "c-wx-it", "name": "微信集成客户端", "provider": "wechat_open",
		"clientId": "wx-it-appid", "clientSecret": "wx-it-secret",
		"tokenUrl":    mock.BaseURL + "/sns/oauth2/access_token",
		"userinfoUrl": mock.BaseURL + "/sns/userinfo",
	})

	// 预置绑定：admin ↔ 微信 openid（tenant 1）。
	binding := &model.SocialUser{
		TenantID: 1, UserID: 1, Provider: "wechat_open", OpenID: "wx-it-openid", UnionID: "wx-it-unionid",
		Status: model.StatusEnable,
	}
	if err := tenant.Skip(env.DB).Create(binding).Error; err != nil {
		t.Fatalf("预置绑定: %v", err)
	}

	// 授权地址：默认 qrconnect 端点。
	authorizeURL, state := socialAuthorizeSimple(t, env, "wechat_open")
	if !strings.Contains(authorizeURL, "open.weixin.qq.com/connect/qrconnect") {
		t.Fatalf("微信授权地址应为默认 qrconnect 端点: %s", authorizeURL)
	}

	// 错误 code → 统一 400（不泄露内部错误，spec §4.11.3）。
	s, c, _ := socialCallbackRaw(t, env, "wechat_open", "bad-code", state)
	if s != http.StatusBadRequest || c != 400 {
		t.Fatalf("无效 code 应 400：HTTP %d code %d", s, c)
	}

	// 正确 code → 登录成功签发 JWT（验收标准）。
	_, state = socialAuthorizeSimple(t, env, "wechat_open")
	s, c, data := socialCallbackRaw(t, env, "wechat_open", "good-code", state)
	if s != http.StatusOK || c != 0 || data["mode"] != "login" {
		t.Fatalf("微信登录回调应成功：HTTP %d code %d data=%v", s, c, data)
	}
	if data["accessToken"] == "" || data["username"] != "admin" {
		t.Fatalf("应签发 token：data=%v", data)
	}

	// 绑定行的昵称/最近登录时间已被回填。
	var row model.SocialUser
	if err := tenant.Skip(env.DB).First(&row, binding.ID).Error; err != nil {
		t.Fatalf("查询绑定行: %v", err)
	}
	if row.Nickname != "微信集成用户" || row.LastLoginAt == nil {
		t.Fatalf("绑定行应回填三方资料与 last_login_at: %+v", row)
	}
}

// TestIntegrationSocialWeComBindFlow 企微真实 provider 绑定全链路（本地 mock）：
// bind → callback 写入绑定（userid 作 openid，user/get 补齐姓名），raw_info
// 不含三方 access token；重复绑定幂等。
func TestIntegrationSocialWeComBindFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	mock := newWeComMockServer(t)
	createSocialClientFull(t, env, platformAdmin, map[string]any{
		"code": "c-wecom-it", "name": "企微集成客户端", "provider": "wecom",
		"clientId": "ww-it-corpid", "clientSecret": "wecom-it-secret",
		"tokenUrl":    mock.BaseURL + "/cgi-bin/gettoken",
		"userinfoUrl": mock.BaseURL + "/cgi-bin/auth/getuserinfo",
	})

	// 绑定模式：bind 返回的 state 携带 user_id（spec §4.4.6），回调完成写入。
	state := bindInitiate(t, env, platformAdmin, "wecom")
	s, c, data := socialCallbackRaw(t, env, "wecom", "good-code", state)
	if s != http.StatusOK || c != 0 || data["mode"] != "bind" {
		t.Fatalf("企微绑定回调应成功：HTTP %d code %d data=%v", s, c, data)
	}

	// 绑定行：openid=userid，姓名经 user/get 补齐；raw_info 不含 access_token。
	var row model.SocialUser
	if err := tenant.Skip(env.DB).Where("provider='wecom' AND openid=?", wecomOpenID).First(&row).Error; err != nil {
		t.Fatalf("应写入企微绑定行: %v", err)
	}
	if row.Nickname != "张三" || row.UnionID != "" {
		t.Fatalf("企微绑定行资料错误: %+v", row)
	}
	if strings.Contains(row.RawInfo, "wecom-it-token") {
		t.Fatalf("raw_info 不得包含三方 access token: %s", row.RawInfo)
	}

	// 重复绑定同一 openid → 幂等成功，不新增行。
	state = bindInitiate(t, env, platformAdmin, "wecom")
	s, c, _ = socialCallbackRaw(t, env, "wecom", "good-code", state)
	if s != http.StatusOK || c != 0 {
		t.Fatalf("重复绑定应幂等成功：HTTP %d code %d", s, c)
	}
	var count int64
	if err := tenant.Skip(env.DB).Raw("SELECT COUNT(*) FROM system_social_user WHERE provider='wecom' AND openid=?", wecomOpenID).
		Row().Scan(&count); err != nil || count != 1 {
		t.Fatalf("重复绑定不应新增行：count=%d err=%v", count, err)
	}
}
