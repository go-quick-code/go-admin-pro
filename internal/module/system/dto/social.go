package dto

import "time"

// ---------- 社交登录（issue #76，spec §5.3） ----------

// SocialClientCreateReq 创建社交客户端（仅平台租户）。
type SocialClientCreateReq struct {
	Code         string `json:"code" binding:"required,max=64"`
	Name         string `json:"name" binding:"required,max=100"`
	Provider     string `json:"provider" binding:"required,max=32"`
	ClientID     string `json:"clientId" binding:"required,max=200"`
	ClientSecret string `json:"clientSecret" binding:"required,max=500"`
	AuthorizeURL string `json:"authorizeUrl" binding:"max=500"`
	TokenURL     string `json:"tokenUrl" binding:"max=500"`
	UserinfoURL  string `json:"userinfoUrl" binding:"max=500"`
	Scopes       string `json:"scopes" binding:"max=200"`
	RedirectURI  string `json:"redirectUri" binding:"required,max=500"`
	Status       int8   `json:"status"`
	Remark       string `json:"remark" binding:"max=500"`
}

// SocialClientUpdateReq 修改社交客户端；clientSecret 提交 ****** 表示保留
// 原值（spec §4.11.1，与消息渠道一致）。
type SocialClientUpdateReq struct {
	ID           int64  `json:"id" binding:"required"`
	Code         string `json:"code" binding:"required,max=64"`
	Name         string `json:"name" binding:"required,max=100"`
	Provider     string `json:"provider" binding:"required,max=32"`
	ClientID     string `json:"clientId" binding:"required,max=200"`
	ClientSecret string `json:"clientSecret" binding:"max=500"`
	AuthorizeURL string `json:"authorizeUrl" binding:"max=500"`
	TokenURL     string `json:"tokenUrl" binding:"max=500"`
	UserinfoURL  string `json:"userinfoUrl" binding:"max=500"`
	Scopes       string `json:"scopes" binding:"max=200"`
	RedirectURI  string `json:"redirectUri" binding:"required,max=500"`
	Status       int8   `json:"status"`
	Remark       string `json:"remark" binding:"max=500"`
}

// SocialClientPageReq 社交客户端分页（平台级）。
type SocialClientPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Code     string `form:"code"`
	Name     string `form:"name"`
	Provider string `form:"provider"`
	Status   *int8  `form:"status"`
}

// SocialAuthorizeReq 生成授权跳转地址（公开端点）：登录模式。
type SocialAuthorizeReq struct {
	Provider string `form:"provider" binding:"required"`
	TenantID int64  `form:"tenantId"` // 缺省 1（spec §4.4.2）
}

// SocialAuthorizeResp 返回三方授权地址与本次流程的 state；
// 绑定模式（POST /system/social/bind）复用同一结构。
type SocialAuthorizeResp struct {
	URL   string `json:"url"`
	State string `json:"state"`
}

// SocialCallbackReq 社交回调（公开端点）：三方把浏览器重定向回
// redirect_uri 后，前端把 code/state 交回本端点换取登录态或完成绑定。
type SocialCallbackReq struct {
	Provider string `form:"provider" binding:"required"`
	Code     string `form:"code" binding:"required"`
	State    string `form:"state" binding:"required"`
}

// SocialCallbackResp 社交回调结果：
//   - mode=login：登录签发，携带 accessToken 等字段（与账号密码登录同构，
//     前端可复用登录存储逻辑）；
//   - mode=bind：完成绑定，携带 provider/openid。
type SocialCallbackResp struct {
	Mode        string `json:"mode"` // login：登录签发；bind：完成绑定
	AccessToken string `json:"accessToken,omitempty"`
	ExpiresIn   int64  `json:"expiresIn,omitempty"`
	UserID      int64  `json:"userId,omitempty"`
	Username    string `json:"username,omitempty"`
	TenantID    int64  `json:"tenantId,omitempty"`
	Provider    string `json:"provider,omitempty"`
	OpenID      string `json:"openid,omitempty"`
}

// SocialBindReq 发起绑定（受保护端点，登录即可）：为当前用户生成绑定模式的
// 授权地址，state 携带 user_id（spec §4.4.6）。
type SocialBindReq struct {
	Provider string `json:"provider" binding:"required"`
}

// SocialUserPageReq 绑定用户分页（当前租户）。
type SocialUserPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Provider string `form:"provider"`
	UserID   int64  `form:"userId"`
}

// SocialUserResp 绑定用户行：回填本地用户名便于管理页展示。
type SocialUserResp struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"userId"`
	Username    string     `json:"username"`
	Provider    string     `json:"provider"`
	OpenID      string     `json:"openid"`
	UnionID     string     `json:"unionid"`
	Nickname    string     `json:"nickname"`
	Avatar      string     `json:"avatar"`
	Status      int8       `json:"status"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
	CreateTime  *time.Time `json:"createTime"`
}
