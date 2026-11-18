package model

// member_level.go 是会员等级实体（issue #174，docs/mall-mvp-spec.md §3.1）。
//
// 等级纯手工维护（MVP 明确不做经验值体系）：experience 是纯维护的阈值列。
// name 与 level 双唯一（UNIQUE(tenant_id, name) / UNIQUE(tenant_id, level)），
// 唯一键不含 deleted_at（照 crm_product code 先例：软删行保持占位）；
// 管理端删除守卫「被会员引用禁删」（service 层，409）。

// MemberLevel 是会员等级（租户级）。
type MemberLevel struct {
	BaseEntity
	TenantID int64  `gorm:"not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	// Level 是排序值（租户内唯一，小值低级）。
	Level int `gorm:"not null" json:"level"`
	// Experience 是经验阈值（纯维护列，MVP 无经验值体系）。
	Experience int    `gorm:"not null;default:0" json:"experience"`
	Status     int8   `gorm:"not null" json:"status"`
	Remark     string `gorm:"size:500" json:"remark"`
}

func (MemberLevel) TableName() string { return "member_level" }
