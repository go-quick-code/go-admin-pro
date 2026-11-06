// calendar.go 承载日程与会议室域的持久化模型（issue #56，
// docs/oa-mvp-spec.md §3.7/§4.11）：日程、会议室、会议室预约三表。
//
// 本工单（#56）落地三张表的 schema 与日程的完整功能；会议室与预约的
// CRUD/冲突校验/提醒由 #57 承接（模型先就位，避免 #57 再改迁移）。
//
// 日程可见性（spec §4.11）：
//   - 0 私有：仅创建人；
//   - 1 部门：创建人创建时的部门快照 dept_id 及子树可见（调部门后历史
//     日程仍归属原部门）；
//   - 2 公开：全租户可见，创建需要 oa:calendar:public 权限。
//
// 取消是软取消（status=1）：列表/详情不再展示，行保留用于审计与 #57
// 预约取消的联动（预约取消同时取消其生成的日程）。
package model

import "time"

// 日程可见性（spec §3.7/§4.11）。
const (
	CalendarVisibilityPrivate = int8(0) // 私有：仅创建人
	CalendarVisibilityDept    = int8(1) // 部门：创建人部门快照及子树
	CalendarVisibilityPublic  = int8(2) // 公开：全租户
)

// 日程状态（spec §3.7）：软取消。
const (
	CalendarEventStatusActive    = int8(0) // 有效
	CalendarEventStatusCancelled = int8(1) // 已取消（不再展示）
)

// 日程来源（spec §3.7）：会议预约成功时同事务生成 source=1 的日程（#57）。
const (
	CalendarSourceManual      = int8(0) // 手动创建
	CalendarSourceRoomBooking = int8(1) // 会议室预约生成
)

// 会议室状态（spec §3.7）：被未来预约引用时只能停用不能删除（#57）。
const (
	MeetingRoomStatusEnabled  = int8(0) // 启用
	MeetingRoomStatusDisabled = int8(1) // 停用
)

// 会议室预约状态（spec §3.7/§4.14）：预约只能取消，不能删除。
const (
	RoomBookingStatusActive    = int8(0) // 有效
	RoomBookingStatusCancelled = int8(1) // 已取消
)

// CalendarEvent 是日程（spec §3.7）：
//   - creator_id 创建人；title/description 内容；
//   - start_time/end_time 起止时间（业务时区口径，end_time 必须晚于 start_time）；
//   - all_day 全天标记（展示用，不改变存储的起止时刻）；
//   - visibility 可见性（CalendarVisibility*）；dept_id 创建时部门快照；
//   - source/source_id 来源（0 手动 / 1 会议室预约，source_id 为预约 ID，#57）；
//   - status 状态（0 有效 / 1 取消；取消后列表/详情不再展示）。
type CalendarEvent struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// CreatorID 创建人（system_user，租户内）。
	CreatorID int64 `gorm:"not null" json:"creatorId"`
	// Title 日程标题（1–200 字）。
	Title string `gorm:"size:200;not null" json:"title"`
	// Description 日程描述（≤1000 字，可空）。
	Description string `gorm:"size:1000" json:"description"`
	// StartTime 开始时间。
	StartTime time.Time `gorm:"not null" json:"startTime"`
	// EndTime 结束时间（必须晚于开始时间）。
	EndTime time.Time `gorm:"not null" json:"endTime"`
	// AllDay 全天标记。
	AllDay bool `gorm:"not null;default:0" json:"allDay"`
	// Visibility 可见性（0 私有 / 1 部门 / 2 公开）。
	Visibility int8 `gorm:"not null;default:0" json:"visibility"`
	// DeptID 创建人创建时的部门快照（部门可见性判定依据）。
	DeptID int64 `gorm:"not null;default:0" json:"deptId"`
	// Source 来源（0 手动 / 1 会议室预约）。
	Source int8 `gorm:"not null;default:0" json:"source"`
	// SourceID 来源业务 ID（source=1 时为预约 ID）。
	SourceID *int64 `gorm:"column:source_id" json:"sourceId"`
	// Status 状态（0 有效 / 1 取消）。
	Status int8 `gorm:"not null;default:0" json:"status"`
}

func (CalendarEvent) TableName() string { return "oa_calendar_event" }

// MeetingRoom 是会议室（spec §3.7）。CRUD 与预约冲突校验属 #57；
// 本工单只建 schema + 模型并登记租户插件。
type MeetingRoom struct {
	BaseEntity
	TenantID int64  `gorm:"not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:100;not null" json:"name"`
	Location string `gorm:"size:200" json:"location"`
	Capacity int    `gorm:"not null;default:0" json:"capacity"`
	Status   int8   `gorm:"not null;default:0" json:"status"`
	Sort     int    `gorm:"not null;default:0" json:"sort"`
}

func (MeetingRoom) TableName() string { return "oa_meeting_room" }

// RoomBooking 是会议室预约（spec §3.7）。写入、冲突校验、提醒与日程联动
// 属 #57；本工单只建 schema + 模型并登记租户插件。
type RoomBooking struct {
	BaseEntity
	TenantID        int64      `gorm:"not null;default:1" json:"tenantId"`
	RoomID          int64      `gorm:"not null" json:"roomId"`
	UserID          int64      `gorm:"not null" json:"userId"`
	Title           string     `gorm:"size:200;not null" json:"title"`
	StartTime       time.Time  `gorm:"not null" json:"startTime"`
	EndTime         time.Time  `gorm:"not null" json:"endTime"`
	Status          int8       `gorm:"not null;default:0" json:"status"`
	CalendarEventID *int64     `gorm:"column:calendar_event_id" json:"calendarEventId"`
	RemindedAt      *time.Time `gorm:"column:reminded_at" json:"remindedAt"`
}

func (RoomBooking) TableName() string { return "oa_room_booking" }
