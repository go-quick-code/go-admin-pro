package service

import (
	"context"
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

// ---------- 用例辅助 ----------

// wecomTestClient 构造一个企微客户端；自定义端点为空时走 provider 内置默认值。
// client_id = 企业 CorpID，client_secret = 应用 Secret；authorize_url 覆盖里
// 可携带 agentid 等额外 query 参数（见 provider 实现注释）。
func wecomTestClient(authorizeURL, tokenURL, userinfoURL string) *model.SocialClient {
	return &model.SocialClient{
		Code:         "c-wecom",
		Name:         "企业微信",
		Provider:     "wecom",
		ClientID:     "ww-corpid",
		ClientSecret: "wecom-secret",
		AuthorizeURL: authorizeURL,
		TokenURL:     tokenURL,
		UserinfoURL:  userinfoURL,
		RedirectURI:  "https://app.example.com/social/callback",
	}
}

// wecomMock 模拟企微 gettoken / auth/getuserinfo / user/get 三个端点。
type wecomMock struct {
	srv      *httptest.Server
	getHits  *[]url.Values // gettoken
	infoHits *[]url.Values // getuserinfo
	userHits *[]url.Values // user/get
}

func newWeComMock(t *testing.T, tokenResp, infoResp, userResp string) wecomMock {
	t.Helper()
	var getHits, infoHits, userHits []url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/cgi-bin/gettoken", func(w http.ResponseWriter, r *http.Request) {
		getHits = append(getHits, r.URL.Query())
		writeWecomJSON(t, w, tokenResp)
	})
	mux.HandleFunc("/cgi-bin/auth/getuserinfo", func(w http.ResponseWriter, r *http.Request) {
		infoHits = append(infoHits, r.URL.Query())
		writeWecomJSON(t, w, infoResp)
	})
	mux.HandleFunc("/cgi-bin/user/get", func(w http.ResponseWriter, r *http.Request) {
		userHits = append(userHits, r.URL.Query())
		writeWecomJSON(t, w, userResp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return wecomMock{srv: srv, getHits: &getHits, infoHits: &infoHits, userHits: &userHits}
}

func writeWecomJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// ---------- 授权地址 ----------

// TestWeComAuthorizeURLDefault 默认端点：wwlogin 扫码登录链接，携带
// login_type/appid/redirect_uri/state。
func TestWeComAuthorizeURLDefault(t *testing.T) {
	p := NewWeComProvider()
	u := mustParseURL(t, mustAuthorizeURL(t, p, wecomTestClient("", "", ""), "state-wx", "challenge-wx"))

	if u.Scheme != "https" || u.Host != "login.work.weixin.qq.com" || u.Path != "/wwlogin/sso/login" {
		t.Fatalf("默认授权端点应为 login.work.weixin.qq.com/wwlogin/sso/login: %s", u)
	}
	q := u.Query()
	if q.Get("login_type") != "CorpApp" {
		t.Fatalf("login_type 应为 CorpApp: %v", q)
	}
	if q.Get("appid") != "ww-corpid" {
		t.Fatalf("appid 应取 client_id（CorpID）: %v", q)
	}
	if q.Get("redirect_uri") != "https://app.example.com/social/callback" {
		t.Fatalf("redirect_uri 应取客户端配置: %v", q)
	}
	if q.Get("state") != "state-wx" {
		t.Fatalf("state 应原样拼入: %v", q)
	}
	if q.Get("code_challenge") != "" {
		t.Fatalf("企微 wwlogin 不支持 PKCE，不应携带 code_challenge: %v", q)
	}
}

// TestWeComAuthorizeURLWithAgentid 企微授权地址支持经自定义 authorize_url 的
// query 携带 agentid（旧版 qrConnect 必填；新组件 CorpApp 场景建议填写），
// provider 保留覆盖地址已有参数再追加标准参数。
func TestWeComAuthorizeURLWithAgentid(t *testing.T) {
	p := NewWeComProvider()
	client := wecomTestClient("https://login.work.weixin.qq.com/wwlogin/sso/login?agentid=1000002", "", "")
	u := mustParseURL(t, mustAuthorizeURL(t, p, client, "s", "c"))
	q := u.Query()
	if q.Get("agentid") != "1000002" {
		t.Fatalf("覆盖地址携带的 agentid 应保留: %v", q)
	}
	if q.Get("appid") != "ww-corpid" || q.Get("login_type") != "CorpApp" || q.Get("state") != "s" {
		t.Fatalf("标准参数应合并进覆盖地址: %v", q)
	}
}

// ---------- token 交换与用户信息 ----------

// TestWeComExchangeIdentitySuccess 成功链路：corpid+secret 换应用 access_token →
// code 换 userid → user/get 补齐姓名头像；userid 作为 openid。
func TestWeComExchangeIdentitySuccess(t *testing.T) {
	mock := newWeComMock(t,
		`{"errcode":0,"errmsg":"ok","access_token":"wecom-token","expires_in":7200}`,
		`{"errcode":0,"errmsg":"ok","userid":"zhangsan"}`,
		`{"errcode":0,"errmsg":"ok","userid":"zhangsan","name":"张三","avatar":"https://wecom.example.com/a.png"}`)
	p := NewWeComProvider()
	client := wecomTestClient("",
		mock.srv.URL+"/cgi-bin/gettoken",
		mock.srv.URL+"/cgi-bin/auth/getuserinfo")

	identity, err := p.ExchangeIdentity(context.Background(), client, "good-code", "verifier")
	if err != nil {
		t.Fatalf("换身份应成功: %v", err)
	}
	if identity.OpenID != "zhangsan" {
		t.Fatalf("openid 应为企微 userid: %+v", identity)
	}
	if identity.Nickname != "张三" || identity.Avatar != "https://wecom.example.com/a.png" {
		t.Fatalf("姓名/头像应从 user/get 补齐: %+v", identity)
	}
	// gettoken 参数。
	gq := (*mock.getHits)[0]
	if gq.Get("corpid") != "ww-corpid" || gq.Get("corpsecret") != "wecom-secret" {
		t.Fatalf("gettoken 参数错误: %v", gq)
	}
	// getuserinfo 参数。
	iq := (*mock.infoHits)[0]
	if iq.Get("access_token") != "wecom-token" || iq.Get("code") != "good-code" {
		t.Fatalf("getuserinfo 参数错误: %v", iq)
	}
	// user/get 参数。
	uq := (*mock.userHits)[0]
	if uq.Get("access_token") != "wecom-token" || uq.Get("userid") != "zhangsan" {
		t.Fatalf("user/get 参数错误: %v", uq)
	}
	// raw_info 不含三方 access token。
	if strings.Contains(identity.RawInfo, "wecom-token") {
		t.Fatalf("raw_info 不得包含三方 access token: %s", identity.RawInfo)
	}
}

// TestWeComExchangeIdentityUserGetOptional user/get 补齐失败不影响登录
// （身份已由 getuserinfo 确立）。
func TestWeComExchangeIdentityUserGetOptional(t *testing.T) {
	// user/get 返回错误码：仍应成功，仅缺姓名头像。
	mock := newWeComMock(t,
		`{"errcode":0,"errmsg":"ok","access_token":"t"}`,
		`{"errcode":0,"errmsg":"ok","userid":"lisi"}`,
		`{"errcode":60011,"errmsg":"no privilege"}`)
	p := NewWeComProvider()
	client := wecomTestClient("", mock.srv.URL+"/cgi-bin/gettoken", mock.srv.URL+"/cgi-bin/auth/getuserinfo")
	identity, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err != nil {
		t.Fatalf("user/get 失败不应阻断登录: %v", err)
	}
	if identity.OpenID != "lisi" || identity.Nickname != "" {
		t.Fatalf("身份应只取 userid: %+v", identity)
	}
}

// TestWeComExchangeIdentityErrorCodes 企微错误码（errcode/errmsg）上抛。
func TestWeComExchangeIdentityErrorCodes(t *testing.T) {
	cases := []struct {
		name        string
		tokenResp   string
		infoResp    string
		wantcontain string
	}{
		{"gettoken 错误码", `{"errcode":40013,"errmsg":"invalid corpid"}`, "", "40013"},
		{"getuserinfo 错误码", `{"errcode":0,"errmsg":"ok","access_token":"t"}`, `{"errcode":40029,"errmsg":"invalid code"}`, "40029"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWeComMock(t, tc.tokenResp, tc.infoResp, `{"errcode":0,"errmsg":"ok"}`)
			p := NewWeComProvider()
			client := wecomTestClient("", mock.srv.URL+"/cgi-bin/gettoken", mock.srv.URL+"/cgi-bin/auth/getuserinfo")
			_, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
			if err == nil || !strings.Contains(err.Error(), tc.wantcontain) {
				t.Fatalf("错误应包含 %s: %v", tc.wantcontain, err)
			}
		})
	}
}

// TestWeComExchangeIdentityTimeout 慢端点应超时失败并标注超时。
func TestWeComExchangeIdentityTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		writeWecomJSON(t, w, `{"errcode":0,"errmsg":"ok","access_token":"t"}`)
	}))
	t.Cleanup(srv.Close)

	p := NewWeComProvider()
	p.http.client.Timeout = 50 * time.Millisecond
	client := wecomTestClient("", srv.URL+"/cgi-bin/gettoken", srv.URL+"/cgi-bin/auth/getuserinfo")
	_, err := p.ExchangeIdentity(context.Background(), client, "code", "verifier")
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("慢端点应超时报错并标注超时: %v", err)
	}
}

// TestWeComExchangeIdentityRawInfoTruncated raw_info 截断到 4096 字节且保持
// 合法 UTF-8（spec §4.4.9）。
func TestWeComExchangeIdentityRawInfoTruncated(t *testing.T) {
	bigName := strings.Repeat("企", 3000)
	mock := newWeComMock(t,
		`{"errcode":0,"errmsg":"ok","access_token":"t"}`,
		`{"errcode":0,"errmsg":"ok","userid":"wangwu"}`,
		`{"errcode":0,"errmsg":"ok","userid":"wangwu","name":"`+bigName+`"}`)
	p := NewWeComProvider()
	client := wecomTestClient("", mock.srv.URL+"/cgi-bin/gettoken", mock.srv.URL+"/cgi-bin/auth/getuserinfo")
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
