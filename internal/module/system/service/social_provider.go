package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// SocialIdentity 是 provider 用 code 换到的三方身份。
// 三方 access token 只在 provider 实现内部使用，不落库、不上抛（spec §4.4.9）。
type SocialIdentity struct {
	OpenID   string // 三方用户唯一标识
	UnionID  string // 可空
	Nickname string
	Avatar   string
	RawInfo  string // 三方原始信息 JSON 文本，service 层截断 4096 字节
}

// SocialProvider 是第三方 OAuth2 provider 的实现接缝（ADR 0011：只做
// OAuth2 客户端）。#77 在此落微信开放平台/钉钉/企微三个实现并在 internal/app
// 装配期 RegisterSocialProvider；本工单（#76）只留接口与骨架，未注册
// provider 时授权/回调端点返回 400。
//
// 实现约定：
//   - client.AuthorizeURL/TokenURL/UserinfoURL 为空时用 provider 默认端点
//     （spec §4.4.8），配置了自定义端点时以配置为准；
//   - 对三方 HTTP 调用超时 10s（spec §4.4.8）；
//   - ExchangeIdentity 失败时返回带原因的错误，service 层只记日志、对外
//     返回统一 400，不泄露内部错误（spec §4.11.3）。
type SocialProvider interface {
	// Code 返回平台编码，对应 system_social_client.provider
	//（wechat_open / dingtalk / wecom）。
	Code() string
	// AuthorizeURL 构造授权跳转地址：拼 client_id、redirect_uri、state 与
	// PKCE code_challenge（S256）。scopes 取 client.Scopes。
	AuthorizeURL(ctx context.Context, client *model.SocialClient, state, codeChallenge string) (string, error)
	// ExchangeIdentity 用授权码换 token 并拉取三方用户信息；codeVerifier
	// 用于 PKCE 换取（spec §4.4.2）。
	ExchangeIdentity(ctx context.Context, client *model.SocialClient, code, codeVerifier string) (*SocialIdentity, error)
}

// RegisterSocialProvider 注册一个 provider 实现（装配期或集成测试使用），
// 同编码后注册者覆盖先注册者。
func (s *SocialService) RegisterSocialProvider(p SocialProvider) {
	if p == nil {
		return
	}
	s.socialMu.Lock()
	defer s.socialMu.Unlock()
	s.socialProviders[p.Code()] = p
}

func (s *SocialService) socialProvider(code string) SocialProvider {
	s.socialMu.RLock()
	defer s.socialMu.RUnlock()
	return s.socialProviders[code]
}
