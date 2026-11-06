package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// 钉钉 OAuth2 扫码/授权登录 provider（issue #77，spec §4.4.8，ADR 0011）：
// 端点为钉钉开放平台新版 OAuth2（v1.0）现行地址：
//   - 授权：https://login.dingtalk.com/oauth2/auth（PC 浏览器打开即扫码页）
//   - token：POST https://api.dingtalk.com/v1.0/oauth2/userAccessToken
//     （JSON 请求体 clientId/clientSecret/grantType=authorization_code/code）
//   - 用户信息：GET https://api.dingtalk.com/v1.0/contact/users/me
//     （请求头 x-acs-dingtalk-access-token 携带用户 token）
//
// 说明：
//   - 新版 OAuth2 支持 PKCE，AuthorizeURL 拼 code_challenge（S256），
//     token 交换由钉钉侧完成校验；
//   - 身份键取 openId；部分应用未开通 openId 权限点时回退 unionId；
//   - 钉钉 v1.0 的错误信封是字符串 code/message（HTTP 非 200），与微信的
//     errcode/errmsg（HTTP 200）不同，socialErrcode 已兼容两种形态；
//   - 三方 access token 只在内存中使用，不落库、不进 raw_info（spec §4.4.9）。
const (
	dingtalkAuthorizeDefault = "https://login.dingtalk.com/oauth2/auth"
	dingtalkTokenDefault     = "https://api.dingtalk.com/v1.0/oauth2/userAccessToken"
	dingtalkUserinfoDefault  = "https://api.dingtalk.com/v1.0/contact/users/me"
	// dingtalkScopeDefault 是扫码登录的缺省 scope（openid 即可取用户基本信息）。
	dingtalkScopeDefault = "openid"
)

// DingTalkProvider 是 dingtalk 平台的 SocialProvider 实现。
type DingTalkProvider struct {
	http *socialHTTP
}

// NewDingTalkProvider 创建钉钉 provider（10s 请求超时，spec §4.4.8）。
func NewDingTalkProvider() *DingTalkProvider {
	return &DingTalkProvider{http: newSocialHTTP()}
}

// Code 返回平台编码（system_social_client.provider）。
func (p *DingTalkProvider) Code() string { return "dingtalk" }

// dingtalkTokenResp 是 userAccessToken 端点响应。
type dingtalkTokenResp struct {
	socialErrcode
	ExpireIn     int    `json:"expireIn"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

// dingtalkUserResp 是 users/me 端点响应。
type dingtalkUserResp struct {
	Nick      string `json:"nick"`
	AvatarURL string `json:"avatarUrl"`
	OpenID    string `json:"openId"`
	UnionID   string `json:"unionId"`
	socialErrcode
}

// AuthorizeURL 构造钉钉授权地址（PKCE S256），自定义 authorize_url 覆盖默认端点。
func (p *DingTalkProvider) AuthorizeURL(_ context.Context, client *model.SocialClient, state, codeChallenge string) (string, error) {
	scopes := client.Scopes
	if scopes == "" {
		scopes = dingtalkScopeDefault
	}
	base := firstNonEmpty(client.AuthorizeURL, dingtalkAuthorizeDefault)
	q := url.Values{}
	q.Set("redirect_uri", client.RedirectURI)
	q.Set("response_type", "code")
	q.Set("client_id", client.ClientID)
	q.Set("scope", scopes)
	q.Set("state", state)
	if codeChallenge != "" {
		// 钉钉 OAuth2 支持 PKCE：授权时携带 S256 挑战。
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	return base + "?" + q.Encode(), nil
}

// ExchangeIdentity 用 authCode 换 userAccessToken 并拉取三方用户信息。
func (p *DingTalkProvider) ExchangeIdentity(ctx context.Context, client *model.SocialClient, code, _ string) (*SocialIdentity, error) {
	tokenURL := firstNonEmpty(client.TokenURL, dingtalkTokenDefault)
	tokenBody, err := p.http.postJSON(ctx, tokenURL, map[string]string{
		"clientId":     client.ClientID,
		"clientSecret": client.ClientSecret,
		"grantType":    "authorization_code",
		"code":         code,
	})
	if err != nil {
		return nil, fmt.Errorf("钉钉 token 交换失败: %w", err)
	}
	var token dingtalkTokenResp
	if err := json.Unmarshal(tokenBody, &token); err != nil {
		return nil, fmt.Errorf("解析钉钉 token 响应: %w（响应=%s）", err, truncateRunes(string(tokenBody), 200))
	}
	if token.failed() {
		return nil, fmt.Errorf("钉钉 token 交换失败: %w", token.err())
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("钉钉 token 响应缺少 accessToken（响应=%s）", truncateRunes(string(tokenBody), 200))
	}

	userinfoURL := firstNonEmpty(client.UserinfoURL, dingtalkUserinfoDefault)
	// 钉钉 v1.0 用户接口经请求头携带用户 token（而非 query 参数）。
	header := map[string]string{"x-acs-dingtalk-access-token": token.AccessToken}
	userBody, err := p.http.do(ctx, "GET", userinfoURL, nil, header)
	if err != nil {
		return nil, fmt.Errorf("钉钉拉取用户信息失败: %w", err)
	}
	var user dingtalkUserResp
	if err := json.Unmarshal(userBody, &user); err != nil {
		return nil, fmt.Errorf("解析钉钉用户信息: %w（响应=%s）", err, truncateRunes(string(userBody), 200))
	}
	if user.failed() {
		return nil, fmt.Errorf("钉钉拉取用户信息失败: %w", user.err())
	}
	// 身份键：openId 优先，缺失回退 unionId（见文件头注释）。
	openid := firstNonEmpty(user.OpenID, user.UnionID)
	if openid == "" {
		return nil, fmt.Errorf("钉钉用户信息缺少 openId/unionId")
	}

	raw := truncateRunes(string(userBody), socialRawInfoMaxBytes)
	return &SocialIdentity{
		OpenID:   openid,
		UnionID:  user.UnionID,
		Nickname: user.Nick,
		Avatar:   user.AvatarURL,
		RawInfo:  raw,
	}, nil
}
