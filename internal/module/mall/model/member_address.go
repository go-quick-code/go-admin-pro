package model

// member_address.go 是会员收货地址实体（issue #174，docs/mall-mvp-spec.md §3.1）。
//
// 省市区存文本（不联动 system_area id，spec §7.8 边界记录）。
// T1 管理端只有「按会员查地址」只读入口；写入随 C 端自助（T2/T5）。

// MemberAddress 是会员收货地址（租户级，按 user_id 归属）。
type MemberAddress struct {
	BaseEntity
	TenantID       int64  `gorm:"not null;default:1" json:"tenantId"`
	UserID         int64  `gorm:"not null" json:"userId"`
	ReceiverName   string `gorm:"size:64;not null" json:"receiverName"`
	ReceiverMobile string `gorm:"size:20;not null" json:"receiverMobile"`
	Province       string `gorm:"size:64;not null" json:"province"`
	City           string `gorm:"size:64;not null" json:"city"`
	District       string `gorm:"size:64;not null" json:"district"`
	Detail         string `gorm:"size:255;not null" json:"detail"`
	IsDefault      int8   `gorm:"not null;default:0" json:"isDefault"`
}

func (MemberAddress) TableName() string { return "member_address" }
