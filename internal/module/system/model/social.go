package model

import "time"

// SocialClient 是社交登录的 OAuth2 客户端配置（平台级资源，不参与租户过滤）。
// client_secret 按密钥约定（spec §4.11.1）DB 明文存储，API 查询由 service 层
// 脱敏为 ******；为空的自定义端点在 #77 用 provider 默认值（spec §4.4.8）。
type SocialClient struct {
	BaseEntity
	Code         string `gorm:"size:64;not null" json:"code"`
	Name         string `gorm:"size:100;not null" json:"name"`
	Provider     string `gorm:"size:32;not null" json:"provider"`
	ClientID     string `gorm:"size:200;not null" json:"clientId"`
	ClientSecret string `gorm:"size:500;not null" json:"clientSecret"`
	AuthorizeURL string `gorm:"size:500" json:"authorizeUrl"`
	TokenURL     string `gorm:"size:500" json:"tokenUrl"`
	UserinfoURL  string `gorm:"size:500" json:"userinfoUrl"`
	Scopes       string `gorm:"size:200" json:"scopes"`
	RedirectURI  string `gorm:"size:500;not null" json:"redirectUri"`
	Status       int8   `gorm:"not null;default:0" json:"status"`
	Remark       string `gorm:"size:500" json:"remark"`
}

func (SocialClient) TableName() string { return "system_social_client" }

// SocialUser 是本地用户与三方身份的绑定关系（租户级，登记 TenantTables）。
// 唯一键 (tenant_id, provider, openid, deleted_at)：不同租户可绑定同一
// openid 的不同本地用户。RawInfo 是三方原始信息（截断 4096 字节），不回显；
// 三方 access token 不落库（spec §4.4.9）。
type SocialUser struct {
	BaseEntity
	TenantID    int64      `gorm:"index:idx_system_social_user_tenant_user;not null;default:1" json:"tenantId"`
	UserID      int64      `gorm:"index:idx_system_social_user_tenant_user;not null" json:"userId"`
	Provider    string     `gorm:"size:32;not null" json:"provider"`
	OpenID      string     `gorm:"column:openid;size:128;not null" json:"openid"`
	UnionID     string     `gorm:"column:unionid;size:128" json:"unionid"`
	Nickname    string     `gorm:"size:100" json:"nickname"`
	Avatar      string     `gorm:"size:500" json:"avatar"`
	RawInfo     string     `gorm:"type:text" json:"-"`
	Status      int8       `gorm:"not null;default:0" json:"status"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}

func (SocialUser) TableName() string { return "system_social_user" }
