package dto

import "time"

// member_app.go 是会员域 C 端自助身份的请求/响应 DTO（issue #178，T5；
// docs/mall-mvp-spec.md §4「注册/登录/我的信息」）。与管理端 member.go
// 分立：C 端请求字段更窄（注册只收 mobile/password/nickname，等级/状态/
// 积分全部服务端定——照 pay_money 服务端定口径先例），响应档案不含
// remark/tenantId 等管理面字段。

// AppRegisterReq C 端注册（POST /mall/app/auth/register，公开端点）：
// mobile/password/nickname。密码 6–32 位（binding 先拦格式，规则见 service
// ValidateMemberAppPassword）；nickname 选填缺省取手机号；等级/状态/积分
// 由服务端定（默认等级 level=1 查询、status 启用、point_balance 0）。
type AppRegisterReq struct {
	Mobile   string `json:"mobile" binding:"required,max=20"`
	Password string `json:"password" binding:"required,min=6,max=32"`
	Nickname string `json:"nickname" binding:"max=64"`
}

// AppLoginReq C 端登录（POST /mall/app/auth/login，公开端点）：mobile/
// password → 会员令牌。失败统一「账号或密码错误」（不泄露存在性）。
type AppLoginReq struct {
	Mobile   string `json:"mobile" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// AppMemberProfile 是 C 端会员档案（GET /mall/app/auth/me 与注册/登录响应
// 复用）：基本信息 + 等级名 + point_balance（spec §4「我的信息」）。password
// 哈希/remark/租户 ID 等管理面字段不出网（与管理端 MemberDetail 分立的
// 原因）。
type AppMemberProfile struct {
	ID           int64     `json:"id"`
	Mobile       string    `json:"mobile"`
	Nickname     string    `json:"nickname"`
	AvatarURL    string    `json:"avatarUrl"`
	LevelID      int64     `json:"levelId"`
	LevelName    string    `json:"levelName"`
	PointBalance int       `json:"pointBalance"`
	Status       int8      `json:"status"`
	CreatedAt    time.Time `json:"createTime"`
}

// AppAuthResp 是 C 端注册/登录响应：会员令牌 + 会员档案（注册自动登录，
// 省一次登录往返）。expiresIn 是令牌有效期秒数（缺省 604800 = 7 天）。
type AppAuthResp struct {
	AccessToken string           `json:"accessToken"`
	TokenType   string           `json:"tokenType"`
	ExpiresIn   int64            `json:"expiresIn"`
	Member      AppMemberProfile `json:"member"`
}
