// announcement.go 承载公告域的持久化模型（issue #48，docs/oa-mvp-spec.md
// §3.4/§4.7）：公告、受众、已读三表。受众可见性判定在纯函数
// （service/announcement_rule.go），本文件只是存储形态。
//
// 已读表是追加型表（spec §3：不设 creator/updater，只保留业务时间），
// 读取侧的幂等写入与统计由 #49 承接；本工单只建 schema 与模型。
package model

import "time"

// 公告状态机（spec §4.7：草稿 → 发布 → 撤回 → 重新发布）。
const (
	AnnouncementStatusDraft     = int8(0) // 草稿（可编辑、可删除）
	AnnouncementStatusPublished = int8(1) // 已发布（不可编辑、不可删除，只能撤回）
	AnnouncementStatusWithdrawn = int8(2) // 已撤回（不再出现在员工列表，可重新发布）
)

// 受众类型（spec §3.4）：0 全员 / 1 自定义（部门子树 ∪ 角色）。
const (
	AudienceTypeAll    = int8(0)
	AudienceTypeCustom = int8(1)
)

// 受众目标类型（spec §3.4）：1 部门（含子树）/ 2 角色。
const (
	AnnouncementTargetDept = int8(1)
	AnnouncementTargetRole = int8(2)
)

// ValidAnnouncementStatus 报告公告状态是否合法。
func ValidAnnouncementStatus(status int8) bool {
	switch status {
	case AnnouncementStatusDraft, AnnouncementStatusPublished, AnnouncementStatusWithdrawn:
		return true
	}
	return false
}

// ValidAudienceType 报告受众类型是否合法。
func ValidAudienceType(t int8) bool {
	return t == AudienceTypeAll || t == AudienceTypeCustom
}

// ValidAnnouncementTargetType 报告受众目标类型是否合法。
func ValidAnnouncementTargetType(t int8) bool {
	return t == AnnouncementTargetDept || t == AnnouncementTargetRole
}

// Announcement 是一条公告（GLOSSARY：公告是内容，通知是投递）。
// content 始终是服务端 bluemonday 清洗后的 HTML（spec §4.7）。
type Announcement struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// Title 公告标题（≤200 字；通知标题列 128 字，投递时截断）。
	Title string `gorm:"size:200;not null" json:"title"`
	// Content 公告正文（清洗后的 HTML）。
	Content string `gorm:"type:mediumtext;not null" json:"content"`
	// Status 状态机（AnnouncementStatus*）。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// AudienceType 受众类型：0 全员 / 1 自定义。
	AudienceType int8 `gorm:"not null;default:0" json:"audienceType"`
	// Pinned 是否置顶（员工列表置顶在前）。
	Pinned bool `gorm:"not null;default:false" json:"pinned"`
	// PublishedAt 最近一次发布时间（草稿为 nil，重新发布时刷新）。
	PublishedAt *time.Time `json:"publishedAt"`
	// WithdrawnAt 撤回时间（重新发布时清空）。
	WithdrawnAt *time.Time `json:"withdrawnAt"`
}

func (Announcement) TableName() string { return "oa_announcement" }

// AnnouncementTarget 是自定义受众的一行（关联表：复合主键 + tenant_id，
// 不设软删除，spec §3 表结构总则）。
type AnnouncementTarget struct {
	TenantID       int64     `gorm:"primaryKey;not null;default:1" json:"tenantId"`
	AnnouncementID int64     `gorm:"primaryKey;not null" json:"announcementId"`
	TargetType     int8      `gorm:"primaryKey;not null" json:"targetType"`
	TargetID       int64     `gorm:"primaryKey;not null" json:"targetId"`
	CreatedAt      time.Time `json:"createTime"`
}

func (AnnouncementTarget) TableName() string { return "oa_announcement_target" }

// AnnouncementRead 是公告已读记录（追加型表：无软删除与 creator/updater；
// 唯一键 (tenant_id, announcement_id, user_id) 保证打开详情的写入幂等）。
// #48 建 schema，写入（service.GetAnnouncement 幂等 upsert）与统计
// （read-stats 人数/名单）由 #49 承接。
type AnnouncementRead struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt      time.Time `json:"createTime"`
	TenantID       int64     `gorm:"not null;default:1" json:"tenantId"`
	AnnouncementID int64     `gorm:"not null" json:"announcementId"`
	UserID         int64     `gorm:"not null" json:"userId"`
	ReadAt         time.Time `gorm:"not null" json:"readAt"`
}

func (AnnouncementRead) TableName() string { return "oa_announcement_read" }
