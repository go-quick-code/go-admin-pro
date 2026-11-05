// Package model 是 notify 模块的持久化实体。
package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// 通知类型枚举（issue #45，docs/oa-mvp-spec.md §4.13）。
const (
	// TypeBpmTodo 新待办：审批任务到达时投递给办理人。
	TypeBpmTodo = "bpm_todo"
	// TypeBpmResult 审批结果：通过/驳回/撤回后投递给发起人。
	TypeBpmResult = "bpm_result"
	// TypeAnnouncement 公告发布：公告投递给受众用户。
	TypeAnnouncement = "announcement"
	// TypeMeetingReminder 会议提醒：会议室预约开始前投递给参与人。
	TypeMeetingReminder = "meeting_reminder"
)

// ValidType 报告通知类型枚举是否合法。
func ValidType(t string) bool {
	switch t {
	case TypeBpmTodo, TypeBpmResult, TypeAnnouncement, TypeMeetingReminder:
		return true
	}
	return false
}

// Message 是一条站内通知（GLOSSARY：通知是系统事件的投递，公告是内容）。
// 按接收人物化一行；生产者（bpm/公告/会议室）在触发事件的同一事务内经
// notify service 的批量写入 seam 落库（spec §4.13）。
//
// 跳转目标 = Route（前端路由 path）+ RouteParams（JSON 对象字符串，如
// `{"taskId":1}`）；二者皆可空——仅站内展示、不跳转。BizType/BizID 冗余
// 定位业务实体，供生产者后续按业务检索通知（如任务办理后标记对应
// bpm_todo 已读，spec §4.13，索引 idx_notify_message_tenant_biz）。
type Message struct {
	model.BaseEntity
	TenantID    int64      `gorm:"index;not null;default:1" json:"tenantId"`
	UserID      int64      `gorm:"not null" json:"userId"`
	Type        string     `gorm:"size:32;not null" json:"type"`
	Title       string     `gorm:"size:128;not null" json:"title"`
	Content     string     `gorm:"size:500" json:"content"`
	BizType     string     `gorm:"size:64" json:"bizType"`
	BizID       int64      `gorm:"not null;default:0" json:"bizId"`
	Route       string     `gorm:"size:255" json:"route"`
	RouteParams string     `gorm:"size:512" json:"routeParams"`
	ReadAt      *time.Time `json:"readAt"`
}

func (Message) TableName() string { return "notify_message" }
