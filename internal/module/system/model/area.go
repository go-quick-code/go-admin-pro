package model

import "time"

// Area 是行政区划参考数据（平台级，issue #82，spec §3.9）。
//
// 参考数据不设软删与 creator/updater（spec §3.9，迁移 00028 建表同理），
// 因此不复用 BaseEntity 底座，用精简结构；也不登记 TenantTables——
// 所有登录用户可读，写入仅平台租户（service 层校验）。
type Area struct {
	ID         int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt  time.Time `json:"createTime"`
	UpdatedAt  time.Time `json:"updateTime"`
	Code       string    `gorm:"size:12;not null" json:"code"`                  // 行政区划代码，唯一键 uk_system_area_code
	ParentCode string    `gorm:"size:12;not null;default:''" json:"parentCode"` // 父级区划代码，省级为空串；索引 idx_system_area_parent_code
	Name       string    `gorm:"size:100;not null" json:"name"`
	Level      int8      `gorm:"not null;default:1" json:"level"` // 1 省 / 2 市 / 3 区县
	Sort       int       `gorm:"not null;default:0" json:"sort"`
	Status     int8      `gorm:"not null;default:0" json:"status"` // 0 启用 / 1 停用
}

func (Area) TableName() string { return "system_area" }
