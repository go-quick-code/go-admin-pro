package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// 微信开放平台扫码登录 provider（issue #77，spec §4.4.8，ADR 0011）：
// 端点为微信开放平台「网站应用微信登录」（qrconnect）现行地址：
//   - 授权：https://open.weixin.qq.com/connect/qrconnect
//   - token：https://api.weixin.qq.com/sns/oauth2/access_token
//   - 用户信息：https://api.weixin.qq.com/sns/userinfo
//
// 说明：
//   - qrconnect 不支持 PKCE，AuthorizeURL 丢弃 codeChallenge；code_verifier
//     仍由服务端 state 保存，code 的防劫持依赖 state（服务端一次性、10 分钟）
//     与回调域名配置；
//   - token 端点响应携带三方 access token，只在本方法内使用换取 userinfo，
//     不落库、不进 raw_info（spec §4.4.9）；
//   - raw_info 取 userinfo 原始报文（不含 access_token），截断逻辑在 service
//     层 Callback 统一做（provider 返回前再做一次防御性截断）。
const (
	wechatOpenAuthorizeDefault = "https://open.weixin.qq.com/connect/qrconnect"
	wechatOpenTokenDefault     = "https://api.weixin.qq.com/sns/oauth2/access_token"
	wechatOpenUserinfoDefault  = "https://api.weixin.qq.com/sns/userinfo"
	// wechatOpenScopeDefault 是扫码登录的缺省 scope（网站应用固定 snsapi_login）。
	wechatOpenScopeDefault = "snsapi_login"
)

// WeChatOpenProvider 是 wechat_open 平台的 SocialProvider 实现。
type WeChatOpenProvider struct {
	http *socialHTTP
}

// NewWeChatOpenProvider 创建微信开放平台 provider（10s 请求超时，spec §4.4.8）。
func NewWeChatOpenProvider() *WeChatOpenProvider {
	return &WeChatOpenProvider{http: newSocialHTTP()}
}

// Code 返回平台编码（system_social_client.provider）。
func (p *WeChatOpenProvider) Code() string { return "wechat_open" }

// wechatOpenTokenResp 是 token 端点响应（access_token 只在内存中使用）。
type wechatOpenTokenResp struct {
	socialErrcode
	AccessToken string `json:"access_token"`
	OpenID      string `json:"openid"`
	UnionID     string `json:"unionid"`
}

// wechatOpenUserResp 是 userinfo 端点响应。
type wechatOpenUserResp struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	Nickname   string `json:"nickname"`
	HeadImgURL string `json:"headimgurl"`
	socialErrcode
}

// AuthorizeURL 构造 qrconnect 授权地址；自定义 authorize_url 覆盖默认端点，
// 覆盖地址 query 中已携带的参数（如页面语言）保持原样。
func (p *WeChatOpenProvider) AuthorizeURL(_ context.Context, client *model.SocialClient, state, _ string) (string, error) {
	// 微信 qrconnect 无 PKCE：codeChallenge 丢弃，见文件头注释。
	scopes := client.Scopes
	if scopes == "" {
		scopes = wechatOpenScopeDefault
	}
	base := firstNonEmpty(client.AuthorizeURL, wechatOpenAuthorizeDefault)
	q := url.Values{}
	q.Set("appid", client.ClientID)
	q.Set("redirect_uri", client.RedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scopes)
	q.Set("state", state)
	return base + "?" + q.Encode() + "#wechat_redirect", nil
}

// ExchangeIdentity 用 code 换 token 并拉取三方用户信息。
func (p *WeChatOpenProvider) ExchangeIdentity(ctx context.Context, client *model.SocialClient, code, _ string) (*SocialIdentity, error) {
	tokenURL := firstNonEmpty(client.TokenURL, wechatOpenTokenDefault)
	tokenBody, err := p.http.get(ctx, tokenURL, url.Values{
		"appid":      {client.ClientID},
		"secret":     {client.ClientSecret},
		"code":       {code},
		"grant_type": {"authorization_code"},
	})
	if err != nil {
		return nil, fmt.Errorf("微信 token 交换失败: %w", err)
	}
	var token wechatOpenTokenResp
	if err := json.Unmarshal(tokenBody, &token); err != nil {
		return nil, fmt.Errorf("解析微信 token 响应: %w（响应=%s）", err, truncateRunes(string(tokenBody), 200))
	}
	if token.failed() {
		return nil, fmt.Errorf("微信 token 交换失败: %w", token.err())
	}
	if token.AccessToken == "" || token.OpenID == "" {
		return nil, fmt.Errorf("微信 token 响应缺少 access_token/openid（响应=%s）", truncateRunes(string(tokenBody), 200))
	}

	userinfoURL := firstNonEmpty(client.UserinfoURL, wechatOpenUserinfoDefault)
	userBody, err := p.http.get(ctx, userinfoURL, url.Values{
		"access_token": {token.AccessToken},
		"openid":       {token.OpenID},
	})
	if err != nil {
		return nil, fmt.Errorf("微信拉取用户信息失败: %w", err)
	}
	var user wechatOpenUserResp
	if err := json.Unmarshal(userBody, &user); err != nil {
		return nil, fmt.Errorf("解析微信用户信息: %w（响应=%s）", err, truncateRunes(string(userBody), 200))
	}
	if user.failed() {
		return nil, fmt.Errorf("微信拉取用户信息失败: %w", user.err())
	}
	openid := firstNonEmpty(user.OpenID, token.OpenID)
	if openid == "" {
		return nil, fmt.Errorf("微信用户信息缺少 openid")
	}

	raw := truncateRunes(string(userBody), socialRawInfoMaxBytes)
	return &SocialIdentity{
		OpenID:   openid,
		UnionID:  firstNonEmpty(user.UnionID, token.UnionID),
		Nickname: user.Nickname,
		Avatar:   user.HeadImgURL,
		RawInfo:  raw,
	}, nil
}
