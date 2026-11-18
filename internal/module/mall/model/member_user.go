package model

// member_user.go 是会员用户实体（issue #174，docs/mall-mvp-spec.md §3.1）。
//
// mobile 租户内唯一（UNIQUE(tenant_id, mobile)）：C 端注册/登录身份；唯一键
// 不含 deleted_at——会员业务身份不允许「软删后同号重建」顶替旧号历史，
// 管理端无删除端点（停启用覆盖生命周期，spec §4）。
// password_hash 为 bcrypt 哈希（照 system_user），明文不落库不回传。
// point_balance 是积分余额（int，不动 money）：只经 T2 唯一入账入口变动，
// 任何代码不得直改（照 pay wallet balance 先例）；T1 无写入路径恒 0。

// MemberUser 是会员用户（租户级）。
type MemberUser struct {
	BaseEntity
	TenantID     int64  `gorm:"not null;default:1" json:"tenantId"`
	Mobile       string `gorm:"size:20;not null" json:"mobile"`
	PasswordHash string `gorm:"size:100;not null;default:''" json:"-"`
	Nickname     string `gorm:"size:64;not null;default:''" json:"nickname"`
	AvatarURL    string `gorm:"size:500" json:"avatarUrl"`
	// Status 不带 default 标签（GORM 零值省略带 default 的字段，0 启用会被
	// DB 默认值覆盖的坑，照 pay model.App 注释）。
	Status  int8  `gorm:"not null" json:"status"`
	LevelID int64 `gorm:"not null;default:0" json:"levelId"`
	// PointBalance 积分余额：只经唯一入账入口变动（T2），不直改。
	PointBalance int    `gorm:"not null;default:0" json:"pointBalance"`
	Remark       string `gorm:"size:500" json:"remark"`
}

func (MemberUser) TableName() string { return "member_user" }
