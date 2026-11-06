package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// ---------- 用例辅助 ----------

// dingtalkTestClient 构造一个钉钉客户端；自定义端点为空时走 provider 内置默认值。
func dingtalkTestClient(authorizeURL, tokenURL, userinfoURL string) *model.SocialClient {
	return &model.SocialClient{
		Code:         "c-ding",
		Name:         "钉钉",
		Provider:     "dingtalk",
		ClientID:     "ding-client-id",
		ClientSecret: "ding-secret",
		AuthorizeURL: authorizeURL,
		TokenURL:     tokenURL,
		UserinfoURL:  userinfoURL,
		Scopes:       "openid",
		RedirectURI:  "https://app.example.com/social/callback",
	}
}

// dingtalkMock 模拟钉钉 token（POST JSON）与 users/me（GET，header 鉴权）端点。
type dingtalkMock struct {
	srv         *httptest.Server
	tokenBodies *[]map[string]any // token 端点收到的 JSON 请求体
	tokenHeader *http.Header
	userHeader  *http.Header
}

func newDingTalkMock(t *testing.T, tokenResp, userResp string, tokenStatus, userStatus int) dingtalkMock {
	t.Helper()
	var tokenBodies []map[string]any
	var tokenHeader, userHeader http.Header
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/userAccessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenHeader = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		tokenBodies = append(tokenBodies, m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		_, _ = io.WriteString(w, tokenResp)
	})
	mux.HandleFunc("/v1.0/contact/users/me", func(w http.ResponseWriter, r *http.Request) {
		userHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(userStatus)
		_, _ = io.WriteString(w, userResp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return dingtalkMock{srv: srv, tokenBodies: &tokenBodies, tokenHeader: &tokenHeader, userHeader: &userHeader}
}

// ---------- 授权地址 ----------

// TestDingTalkAuthorizeURLDefault 默认端点：钉钉新版 OAuth2 /oauth2/auth，
// 携带 client_id/redirect_uri/response_type/scope/state，并按钉钉支持的
// PKCE 拼 code_challenge（S256）。
func TestDingTalkAuthorizeURLDefault(t *testing.T) {
	p := NewDingTalkProvider()
	u := mustParseURL(t, mustAuthorizeURL(t, p, dingtalkTestClient("", "", ""), "state-dd", "challenge-dd"))

	if u.Scheme != "https" || u.Host != "login.dingtalk.com" || u.Path != "/oauth2/auth" {
		t.Fatalf("默认授权端点应为 login.dingtalk.com/oauth2/auth: %s", u)
	}
	q := u.Query()
	if q.Get("client_id") != "ding-client-id" {
		t.Fatalf("client_id 应取 client_id: %v", q)
	}
	if q.Get("redirect_uri") != "https://app.example.com/social/callback" {
		t.Fatalf("redirect_uri 应取客户端配置: %v", q)
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type 应为 code: %v", q)
	}
	if q.Get("scope") != "openid" {
		t.Fatalf("scope 应取客户端配置（测试客户端缺省 openid）: %v", q)
	}
	if q.Get("state") != "state-dd" {
		t.Fatalf("state 应原样拼入: %v", q)
	}
	if q.Get("code_challenge") != "challenge-dd" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("钉钉 OAuth2 支持 PKCE，应携带 code_challenge(S256): %v", q)
	}
}

// TestDingTalkAuthorizeURLCustom 自定义端点生效。
func TestDingTalkAuthorizeURLCustom(t *testing.T) {
	p := NewDingTalkProvider()
	client := dingtalkTestClient("https://mock.example.com/oauth2/auth", "", "")
	u := mustParseURL(t, mustAuthorizeURL(t, p, client, "s", "c"))
	if u.Host != "mock.example.com" || u.Path != "/oauth2/auth" {
		t.Fatalf("自定义授权端点应生效: %s", u)
	}
}

// ---------- token 交换与用户信息 ----------

// TestDingTalkExchangeIdentitySuccess 成功链路：POST JSON 换 userAccessToken →
// header 携带 access_token 调 users/me → openId/unionId/nick/avatarUrl 映射。
func TestDingTalkExchangeIdentitySuccess(t *testing.T) {
	mock := newDingTalkMock(t,
		`{"expireIn":7200,"accessToken":"ding-token","refreshToken":"r","corpId":""}`,
		`{"nick":"钉钉用户","avatarUrl":"https://ding.example.com/a.png","openId":"ding-openid","unionId":"ding-unionid"}`,
		http.StatusOK, http.StatusOK)
	p := NewDingTalkProvider()
	client := dingtalkTestClient("", mock.srv.URL+"/v1.0/oauth2/userAccessToken", mock.srv.URL+"/v1.0/contact/users/me")

	identity, err := p.ExchangeIdentity(context.Background(), client, "good-code", "verifier")
	if err != nil {
		t.Fatalf("换身份应成功: %v", err)
	}
	if identity.OpenID != "ding-openid" || identity.UnionID != "ding-unionid" {
		t.Fatalf("openid/unionid 映射错误: %+v", identity)
	}
	if identity.Nickname != "钉钉用户" || identity.Avatar != "https://ding.example.com/a.png" {
		t.Fatalf("昵称/头像映射错误: %+v", identity)
	}
	// token 端点：POST JSON 请求体字段符合钉钉 v1.0 契约。
	if len(*mock.tokenBodies) != 1 {
		t.Fatalf("应恰好调用一次 token 端点: %d", len(*mock.tokenBodies))
	}
	body := (*mock.tokenBodies)[0]
	if body["clientId"] != "ding-client-id" || body["clientSecret"] != "ding-secret" ||
		body["grantType"] != "authorization_code" || body["code"] != "good-code" {
		t.Fatalf("token 端点请求体错误: %v", body)
	}
	// users/me：钉钉 v1.0 用请求头携带用户 token。
	if got := (*mock.userHeader).Get("x-acs-dingtalk-access-token"); got != "ding-token" {
		t.Fatalf("users/me 应经 x-acs-dingtalk-access-token 头携带 token: %q", got)
	}
	// raw_info 不含三方 access token。
	if strings.Contains(identity.RawInfo, "ding-token") {
		t.Fatalf("raw_info 不得包含三方 access token: %s", identity.RawInfo)
	}
}

// TestDingTalkExchangeIdentityErrorCodes 钉钉 v1.0 错误信封（HTTP 非 200 +
// code/message，HTTP 200 + code 亦兼容）。
func TestDingTalkExchangeIdentityErrorCodes(t *testing.T) {
	cases := []struct {
		name        string
		tokenResp   string
		userResp    string
		tokenStatus int
		userStatus  int
		wantcontain string
	}{
		{"token 端点错误（HTTP 400 + code/message）", `{"code":"InvalidCode","message":"无效的授权码"}`, "", http.StatusBadRequest, http.StatusOK, "InvalidCode"},
		{"users/me 错误（HTTP 200 + code/message）", `{"expireIn":7200,"accessToken":"t"}`, `{"code":"Forbidden.AccessDenied","message":"无权限"}`, http.StatusOK, http.StatusOK, "Forbidden.AccessDenied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newDingTalkMock(t, tc.tokenResp, tc.userResp, tc.tokenStatus, tc.userStatus)
			p := NewDingTalkProvider()
			client := dingtalkTestClient("", mock.srv.URL+"/v1.0/oauth2/userAccessToken", mock.srv.URL+"/v1.0/contact/users/me")
			_, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
			if err == nil || !strings.Contains(err.Error(), tc.wantcontain) {
				t.Fatalf("错误应包含 %s: %v", tc.wantcontain, err)
			}
		})
	}
}

// TestDingTalkExchangeIdentityTimeout 慢端点应超时失败并标注超时。
func TestDingTalkExchangeIdentityTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, `{"expireIn":7200,"accessToken":"t"}`)
	}))
	t.Cleanup(srv.Close)

	p := NewDingTalkProvider()
	p.http.client.Timeout = 50 * time.Millisecond
	client := dingtalkTestClient("", srv.URL+"/v1.0/oauth2/userAccessToken", srv.URL+"/v1.0/contact/users/me")
	_, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("慢端点应超时报错并标注超时: %v", err)
	}
}

// TestDingTalkExchangeIdentityRawInfoTruncated raw_info 截断到 4096 字节且
// 保持合法 UTF-8（spec §4.4.9）。
func TestDingTalkExchangeIdentityRawInfoTruncated(t *testing.T) {
	bigNick := strings.Repeat("钉", 3000)
	mock := newDingTalkMock(t,
		`{"expireIn":7200,"accessToken":"t"}`,
		`{"nick":"`+bigNick+`","openId":"ding-openid"}`,
		http.StatusOK, http.StatusOK)
	p := NewDingTalkProvider()
	client := dingtalkTestClient("", mock.srv.URL+"/v1.0/oauth2/userAccessToken", mock.srv.URL+"/v1.0/contact/users/me")
	identity, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err != nil {
		t.Fatalf("换身份应成功: %v", err)
	}
	if len(identity.RawInfo) > socialRawInfoMaxBytes {
		t.Fatalf("raw_info 应截断到 %d 字节内: %d", socialRawInfoMaxBytes, len(identity.RawInfo))
	}
	if !utf8.ValidString(identity.RawInfo) {
		t.Fatalf("截断后应为合法 UTF-8")
	}
}

// TestDingTalkExchangeIdentityMissingToken token 端点未返回 accessToken 应报错。
func TestDingTalkExchangeIdentityMissingToken(t *testing.T) {
	mock := newDingTalkMock(t, `{"expireIn":7200}`, "", http.StatusOK, http.StatusOK)
	p := NewDingTalkProvider()
	client := dingtalkTestClient("", mock.srv.URL+"/v1.0/oauth2/userAccessToken", mock.srv.URL+"/v1.0/contact/users/me")
	if _, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier"); err == nil {
		t.Fatalf("缺 accessToken 应报错")
	}
}

// TestDingTalkExchangeIdentityFallbackUnionID openId 缺失时回退 unionId
// 作为身份键（部分应用未开通 openId 权限点）。
func TestDingTalkExchangeIdentityFallbackUnionID(t *testing.T) {
	mock := newDingTalkMock(t,
		`{"expireIn":7200,"accessToken":"t"}`,
		`{"nick":"u","unionId":"ding-unionid-only"}`,
		http.StatusOK, http.StatusOK)
	p := NewDingTalkProvider()
	client := dingtalkTestClient("", mock.srv.URL+"/v1.0/oauth2/userAccessToken", mock.srv.URL+"/v1.0/contact/users/me")
	identity, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err != nil {
		t.Fatalf("换身份应成功: %v", err)
	}
	if identity.OpenID != "ding-unionid-only" || identity.UnionID != "ding-unionid-only" {
		t.Fatalf("openId 缺失应回退 unionId: %+v", identity)
	}
}
