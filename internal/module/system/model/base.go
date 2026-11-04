package model

import (
	"time"

	"gorm.io/gorm"
)

// BaseEntity 是通用实体基类，包含主键、时间戳与软删除。
type BaseEntity struct {
	ID        int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `json:"createTime"`
	UpdatedAt time.Time      `json:"updateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// 状态常量：0 启用 / 1 停用，与芋道保持一致。
const (
	StatusEnable  = 0
	StatusDisable = 1
)

// 菜单类型。
const (
	MenuTypeDir    = 1
	MenuTypeMenu   = 2
	MenuTypeButton = 3
)

// 数据权限范围。
const (
	DataScopeAll        = 1 // 全部数据
	DataScopeDeptAndSub = 2 // 本部门及以下
	DataScopeDept       = 3 // 本部门
	DataScopeSelf       = 4 // 仅本人
)
