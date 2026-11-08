// Package model 提供跨模块共享的实体模型底座 BaseEntity。
//
// 所有实体模型统一嵌入 BaseEntity，包含主键、时间戳、软删除与审计字段；
// 用法：type User struct { model.BaseEntity; ... }。
// 审计字段 creator/updater 由 database.AuditPlugin 在写入时自动回填，
// 业务代码一般不需要手动赋值。
package model

import (
	"time"

	"gorm.io/gorm"
)

// BaseEntity 是通用实体基类，包含主键、时间戳、软删除与审计字段。
// creator/updater 存用户 ID（字符串形式），与芋道惯例一致；
// 取不到登录上下文时落系统账号 "0"。
type BaseEntity struct {
	ID        int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `json:"createTime"`
	UpdatedAt time.Time      `json:"updateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	Creator   string         `gorm:"size:64;not null;default:'0'" json:"-"`
	Updater   string         `gorm:"size:64;not null;default:'0'" json:"-"`
}

// GetID 返回主键；供单据内核的 Header 约束读取新建单据的 ID。
func (b *BaseEntity) GetID() int64 { return b.ID }
