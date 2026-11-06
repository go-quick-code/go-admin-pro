package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// ---------- 用例辅助（三家 provider 单测共用，见 social_provider_test_helpers.go） ----------

// mustAuthorizeURL 调用 provider 生成授权地址，失败即终止测试。
func mustAuthorizeURL(t *testing.T, p SocialProvider, client *model.SocialClient, state, codeChallenge string) string {
	t.Helper()
	u, err := p.AuthorizeURL(context.Background(), client, state, codeChallenge)
	if err != nil {
		t.Fatalf("生成授权地址失败: %v", err)
	}
	return u
}

// mustParseURL 解析并断言 URL 合法。
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("授权地址应为合法 URL: %v（%s）", err, raw)
	}
	return u
}

// ---------- 微信开放平台（wechat_open） ----------

// wechatTestClient 构造一个微信开放平台客户端；自定义端点为空时走 provider
// 内置默认值。
func wechatTestClient(authorizeURL, tokenURL, userinfoURL string) *model.SocialClient {
	return &model.SocialClient{
		Code:         "c-wx",
		Name:         "微信开放平台",
		Provider:     "wechat_open",
		ClientID:     "wx-appid",
		ClientSecret: "wx-secret",
		AuthorizeURL: authorizeURL,
		TokenURL:     tokenURL,
		UserinfoURL:  userinfoURL,
		RedirectURI:  "https://app.example.com/social/callback",
	}
}

// wechatMock 同时模拟 token 与 userinfo 端点的 httptest 服务，记录请求 query。
type wechatMock struct {
	srv       *httptest.Server
	tokenHits *[]url.Values
	userHits  *[]url.Values
}

func newWeChatMock(t *testing.T, tokenResp, userResp string, tokenStatus, userStatus int) wechatMock {
	t.Helper()
	var tokenHits, userHits []url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/sns/oauth2/access_token", func(w http.ResponseWriter, r *http.Request) {
		tokenHits = append(tokenHits, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		_, _ = io.WriteString(w, tokenResp)
	})
	mux.HandleFunc("/sns/userinfo", func(w http.ResponseWriter, r *http.Request) {
		userHits = append(userHits, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(userStatus)
		_, _ = io.WriteString(w, userResp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return wechatMock{srv: srv, tokenHits: &tokenHits, userHits: &userHits}
}

// TestWeChatOpenAuthorizeURLDefault 默认端点：qrconnect 授权地址携带
// appid/redirect_uri/response_type/state，scope 缺省 snsapi_login；
// 微信 qrconnect 不支持 PKCE，不得把 code_challenge 拼进 URL。
func TestWeChatOpenAuthorizeURLDefault(t *testing.T) {
	p := NewWeChatOpenProvider()
	u := mustParseURL(t, mustAuthorizeURL(t, p, wechatTestClient("", "", ""), "state-abc", "challenge-xyz"))

	if u.Scheme != "https" || u.Host != "open.weixin.qq.com" || u.Path != "/connect/qrconnect" {
		t.Fatalf("默认授权端点应为 open.weixin.qq.com/connect/qrconnect: %s", u)
	}
	q := u.Query()
	if q.Get("appid") != "wx-appid" {
		t.Fatalf("appid 应取 client_id: %v", q)
	}
	if q.Get("redirect_uri") != "https://app.example.com/social/callback" {
		t.Fatalf("redirect_uri 应取客户端配置: %v", q)
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type 应为 code: %v", q)
	}
	if q.Get("scope") != "snsapi_login" {
		t.Fatalf("scope 缺省应为 snsapi_login: %v", q)
	}
	if q.Get("state") != "state-abc" {
		t.Fatalf("state 应原样拼入: %v", q)
	}
	if q.Get("code_challenge") != "" || q.Get("code_challenge_method") != "" {
		t.Fatalf("微信 qrconnect 不支持 PKCE，不应携带 code_challenge: %v", q)
	}
	if u.Fragment != "wechat_redirect" {
		t.Fatalf("微信授权地址应以 #wechat_redirect 结尾: %s", u)
	}
}

// TestWeChatOpenAuthorizeURLCustom 自定义端点与 scopes 覆盖默认值
// （spec §4.4.8：配置了自定义端点时以配置为准）。
func TestWeChatOpenAuthorizeURLCustom(t *testing.T) {
	p := NewWeChatOpenProvider()
	client := wechatTestClient("https://mock.example.com/qrconnect", "", "")
	client.Scopes = "snsapi_login snsapi_userinfo"
	u := mustParseURL(t, mustAuthorizeURL(t, p, client, "s2", "c2"))
	if u.Host != "mock.example.com" || u.Path != "/qrconnect" {
		t.Fatalf("自定义授权端点应生效: %s", u)
	}
	if q := u.Query(); q.Get("scope") != "snsapi_login snsapi_userinfo" {
		t.Fatalf("scopes 应取客户端配置: %v", q)
	}
}

// TestWeChatOpenExchangeIdentitySuccess 成功链路：code 换 token（appid/secret/
// grant_type 正确）→ 拉用户信息（access_token/openid 正确）→ 身份字段映射；
// raw_info 只存 userinfo 原始报文，不含三方 access token（spec §4.4.9）。
func TestWeChatOpenExchangeIdentitySuccess(t *testing.T) {
	mock := newWeChatMock(t,
		`{"access_token":"wx-token","expires_in":7200,"openid":"wx-openid","unionid":"wx-unionid","scope":"snsapi_login"}`,
		`{"openid":"wx-openid","nickname":"微信用户","headimgurl":"https://wx.example.com/a.png","unionid":"wx-unionid","sex":1}`,
		http.StatusOK, http.StatusOK)
	p := NewWeChatOpenProvider()
	client := wechatTestClient("", mock.srv.URL+"/sns/oauth2/access_token", mock.srv.URL+"/sns/userinfo")

	identity, err := p.ExchangeIdentity(context.Background(), client, "good-code", "verifier")
	if err != nil {
		t.Fatalf("换身份应成功: %v", err)
	}
	if identity.OpenID != "wx-openid" || identity.UnionID != "wx-unionid" {
		t.Fatalf("openid/unionid 映射错误: %+v", identity)
	}
	if identity.Nickname != "微信用户" || identity.Avatar != "https://wx.example.com/a.png" {
		t.Fatalf("昵称/头像映射错误: %+v", identity)
	}
	// token 端点请求参数。
	if len(*mock.tokenHits) != 1 {
		t.Fatalf("应恰好调用一次 token 端点: %d", len(*mock.tokenHits))
	}
	tq := (*mock.tokenHits)[0]
	if tq.Get("appid") != "wx-appid" || tq.Get("secret") != "wx-secret" ||
		tq.Get("code") != "good-code" || tq.Get("grant_type") != "authorization_code" {
		t.Fatalf("token 端点参数错误: %v", tq)
	}
	// userinfo 端点请求参数。
	uq := (*mock.userHits)[0]
	if uq.Get("access_token") != "wx-token" || uq.Get("openid") != "wx-openid" {
		t.Fatalf("userinfo 端点参数错误: %v", uq)
	}
	// raw_info 是 userinfo 原始报文：不含三方 access token。
	if strings.Contains(identity.RawInfo, "wx-token") {
		t.Fatalf("raw_info 不得包含三方 access token（spec §4.4.9）: %s", identity.RawInfo)
	}
	if !strings.Contains(identity.RawInfo, "微信用户") {
		t.Fatalf("raw_info 应为 userinfo 原始报文: %s", identity.RawInfo)
	}
}

// TestWeChatOpenExchangeIdentityErrcode 微信把业务错误放在 200 里返回：
// errcode/errmsg 应转成错误上抛（验收标准：错误码覆盖）。
func TestWeChatOpenExchangeIdentityErrcode(t *testing.T) {
	cases := []struct {
		name        string
		tokenResp   string
		userResp    string
		wantcontain string
	}{
		{"token 端点错误码", `{"errcode":40029,"errmsg":"invalid code"}`, "", "40029"},
		{"userinfo 端点错误码", `{"access_token":"t","openid":"o1"}`, `{"errcode":40001,"errmsg":"invalid credential"}`, "40001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWeChatMock(t, tc.tokenResp, tc.userResp, http.StatusOK, http.StatusOK)
			p := NewWeChatOpenProvider()
			client := wechatTestClient("", mock.srv.URL+"/sns/oauth2/access_token", mock.srv.URL+"/sns/userinfo")
			_, err := p.ExchangeIdentity(context.Background(), client, "bad-code", "verifier")
			if err == nil || !strings.Contains(err.Error(), tc.wantcontain) {
				t.Fatalf("错误应包含错误码 %s: %v", tc.wantcontain, err)
			}
		})
	}
}

// TestWeChatOpenExchangeIdentityHTTPError 非 200 响应应报错且不泄露密钥。
func TestWeChatOpenExchangeIdentityHTTPError(t *testing.T) {
	mock := newWeChatMock(t, `{"errcode":40164,"errmsg":"invalid ip"}`, "", http.StatusForbidden, http.StatusOK)
	p := NewWeChatOpenProvider()
	client := wechatTestClient("", mock.srv.URL+"/sns/oauth2/access_token", mock.srv.URL+"/sns/userinfo")
	_, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err == nil {
		t.Fatalf("HTTP 403 应报错")
	}
	if strings.Contains(err.Error(), "wx-secret") {
		t.Fatalf("错误信息不得包含 client_secret: %v", err)
	}
}

// TestWeChatOpenExchangeIdentityTimeout 三方端点慢响应应在超时后失败并标注
// 超时（spec §4.4.8：三方 HTTP 调用超时 10s；测试用短超时替身验证行为）。
func TestWeChatOpenExchangeIdentityTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟慢响应：sleep 超过测试客户端的 50ms 超时。
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"t","openid":"o"}`)
	}))
	t.Cleanup(srv.Close)

	p := NewWeChatOpenProvider()
	// 测试专用短超时（生产为 10s，见 socialHTTPTimeout）。
	p.http.client.Timeout = 50 * time.Millisecond
	client := wechatTestClient("", srv.URL+"/sns/oauth2/access_token", srv.URL+"/sns/userinfo")
	_, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("慢端点应超时报错并标注超时: %v", err)
	}
}

// TestWeChatOpenExchangeIdentityRawInfoTruncated raw_info 超 4096 字节时截断
// 且不产生半个 UTF-8 字符（spec §4.4.9）。
func TestWeChatOpenExchangeIdentityRawInfoTruncated(t *testing.T) {
	bigNick := strings.Repeat("昵", 3000) // 每字 3 字节，远超 4096
	userResp, err := json.Marshal(map[string]any{"openid": "o1", "nickname": bigNick})
	if err != nil {
		t.Fatalf("构造响应: %v", err)
	}
	mock := newWeChatMock(t, `{"access_token":"t","openid":"o1"}`, string(userResp), http.StatusOK, http.StatusOK)
	p := NewWeChatOpenProvider()
	client := wechatTestClient("", mock.srv.URL+"/sns/oauth2/access_token", mock.srv.URL+"/sns/userinfo")
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

// TestWeChatOpenExchangeIdentityMissingOpenID 缺 openid 的响应应报错，
// 不允许空 openid 落库。
func TestWeChatOpenExchangeIdentityMissingOpenID(t *testing.T) {
	mock := newWeChatMock(t, `{"access_token":"t"}`, "", http.StatusOK, http.StatusOK)
	p := NewWeChatOpenProvider()
	client := wechatTestClient("", mock.srv.URL+"/sns/oauth2/access_token", mock.srv.URL+"/sns/userinfo")
	if _, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier"); err == nil {
		t.Fatalf("token 响应缺 openid 应报错")
	}
}
