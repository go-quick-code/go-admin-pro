package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// 企微（企业微信）扫码登录 provider（issue #77，spec §4.4.8，ADR 0011）：
//   - 授权：https://login.work.weixin.qq.com/wwlogin/sso/login
//     （新版 Web 登录组件，login_type=CorpApp；PC 浏览器打开即扫码页）
//   - 应用 access_token：GET https://qyapi.weixin.qq.com/cgi-bin/gettoken
//     （corpid + 应用 secret；对应 client.token_url 覆盖位）
//   - code 换身份：GET https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo
//     （对应 client.userinfo_url 覆盖位）
//   - 用户资料补齐：GET https://qyapi.weixin.qq.com/cgi-bin/user/get
//     （无独立配置列，端点取 userinfo_url 覆盖地址的 scheme+host，缺省官方域名）
//
// 说明：
//   - 企微扫码无 scope 概念、wwlogin 不支持 PKCE：codeChallenge 丢弃，
//     code 防劫持依赖服务端一次性 state 与回调域名配置；
//   - 授权地址里的 agentid（旧版 qrConnect 必填、新组件 CorpApp 场景建议
//     填写）经自定义 authorize_url 的 query 携带，provider 合并标准参数时
//     保留既有 query（见 AuthorizeURL）；
//   - openid 取企微 userid（企业内唯一；企微无 unionid，字段留空）；
//   - 应用 access_token（gettoken 结果）只在本次交换的内存中使用，不落库、
//     不进 raw_info（spec §4.4.9）；raw_info 取身份/资料报文。
const (
	wecomAuthorizeDefault = "https://login.work.weixin.qq.com/wwlogin/sso/login"
	wecomGetTokenDefault  = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	wecomUserinfoDefault  = "https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo"
	wecomUserGetPath      = "/cgi-bin/user/get"
)

// WeComProvider 是 wecom 平台的 SocialProvider 实现。
type WeComProvider struct {
	http *socialHTTP
}

// NewWeComProvider 创建企微 provider（10s 请求超时，spec §4.4.8）。
func NewWeComProvider() *WeComProvider {
	return &WeComProvider{http: newSocialHTTP()}
}

// Code 返回平台编码（system_social_client.provider）。
func (p *WeComProvider) Code() string { return "wecom" }

// wecomTokenResp 是 gettoken 端点响应（应用 access_token 只在内存中使用）。
type wecomTokenResp struct {
	socialErrcode
	AccessToken string `json:"access_token"`
}

// wecomUseridResp 是 auth/getuserinfo 端点响应。
type wecomUseridResp struct {
	socialErrcode
	UserID string `json:"userid"`
}

// wecomUserResp 是 user/get 端点响应。
type wecomUserResp struct {
	socialErrcode
	UserID string `json:"userid"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

// AuthorizeURL 构造企微扫码登录地址；自定义 authorize_url 覆盖默认端点时，
// 覆盖地址 query 中已携带的参数（如 agentid）保留，标准参数以本方法为准。
func (p *WeComProvider) AuthorizeURL(_ context.Context, client *model.SocialClient, state, _ string) (string, error) {
	// 企微 wwlogin 无 PKCE：codeChallenge 丢弃，见文件头注释。
	base := firstNonEmpty(client.AuthorizeURL, wecomAuthorizeDefault)
	// 保留覆盖地址自带的 query（agentid 等），再用标准参数覆盖同名键。
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("企微授权地址配置不合法: %w", err)
	}
	q := baseURL.Query()
	q.Set("login_type", "CorpApp")
	q.Set("appid", client.ClientID)
	q.Set("redirect_uri", client.RedirectURI)
	q.Set("state", state)
	baseURL.RawQuery = q.Encode()
	return baseURL.String(), nil
}

// ExchangeIdentity 用 code 换用户身份：应用 access_token → auth/getuserinfo →
// user/get 资料补齐（best-effort，失败不阻断登录）。
func (p *WeComProvider) ExchangeIdentity(ctx context.Context, client *model.SocialClient, code, _ string) (*SocialIdentity, error) {
	tokenURL := firstNonEmpty(client.TokenURL, wecomGetTokenDefault)
	tokenBody, err := p.http.get(ctx, tokenURL, url.Values{
		"corpid":     {client.ClientID},
		"corpsecret": {client.ClientSecret},
	})
	if err != nil {
		return nil, fmt.Errorf("企微获取应用凭证失败: %w", err)
	}
	var token wecomTokenResp
	if err := json.Unmarshal(tokenBody, &token); err != nil {
		return nil, fmt.Errorf("解析企微应用凭证响应: %w（响应=%s）", err, truncateRunes(string(tokenBody), 200))
	}
	if token.failed() {
		return nil, fmt.Errorf("企微获取应用凭证失败: %w", token.err())
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("企微应用凭证响应缺少 access_token（响应=%s）", truncateRunes(string(tokenBody), 200))
	}

	userinfoURL := firstNonEmpty(client.UserinfoURL, wecomUserinfoDefault)
	infoBody, err := p.http.get(ctx, userinfoURL, url.Values{
		"access_token": {token.AccessToken},
		"code":         {code},
	})
	if err != nil {
		return nil, fmt.Errorf("企微换取用户身份失败: %w", err)
	}
	var info wecomUseridResp
	if err := json.Unmarshal(infoBody, &info); err != nil {
		return nil, fmt.Errorf("解析企微用户身份响应: %w（响应=%s）", err, truncateRunes(string(infoBody), 200))
	}
	if info.failed() {
		return nil, fmt.Errorf("企微换取用户身份失败: %w", info.err())
	}
	if info.UserID == "" {
		return nil, fmt.Errorf("企微用户身份响应缺少 userid（响应=%s）", truncateRunes(string(infoBody), 200))
	}

	// 资料补齐（姓名/头像）：best-effort。user/get 端点取 userinfo_url 覆盖
	// 地址的 scheme+host（自定义代理场景同源可达），失败仅缺资料不阻断登录。
	identity := &SocialIdentity{
		OpenID:  info.UserID,
		RawInfo: truncateRunes(string(infoBody), socialRawInfoMaxBytes),
	}
	if userBody, err := p.http.get(ctx, wecomUserGetBase(userinfoURL), url.Values{
		"access_token": {token.AccessToken},
		"userid":       {info.UserID},
	}); err == nil {
		var user wecomUserResp
		if json.Unmarshal(userBody, &user) == nil && !user.failed() {
			identity.Nickname = user.Name
			identity.Avatar = user.Avatar
			identity.RawInfo = truncateRunes(string(userBody), socialRawInfoMaxBytes)
		}
	}
	return identity, nil
}

// wecomUserGetBase 由 userinfo_url 推导 user/get 端点：取其 scheme+host 拼
// 固定路径；推导失败（测试里的相对地址等）时回退官方默认域名。
func wecomUserGetBase(userinfoURL string) string {
	base := wecomUserinfoDefault
	if userinfoURL != "" {
		base = userinfoURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		// 默认 userinfo_url 的 origin。
		if d, derr := url.Parse(wecomUserinfoDefault); derr == nil {
			u = d
		}
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	u.Path = wecomUserGetPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
