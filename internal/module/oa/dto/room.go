// room.go 是会议室与预约的请求/响应 DTO（issue #57，
// docs/oa-mvp-spec.md §3.7/§4.11/§4.14/§5）：
//
//   - 会议室：保存请求（名称/位置/容量/排序）、启停请求、分页筛选、行视图；
//   - 预约：创建请求（房间/标题/起止时间）、分页筛选（房间/日期/状态/
//     预约人）、行视图（回填房间名称与预约人昵称）。
//
// 时间统一接受业务时区墙钟 "YYYY-MM-DD HH:mm:ss" 或 RFC3339（与日程同口径，
// ADR 0006）；响应按业务时区墙钟格式化。
package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// calendarDTOLayout 是会议室/预约时间的对外格式（业务时区墙钟），与日程
// dto 的 CalendarTimeLayout（service 层）保持同一口径。
const calendarDTOLayout = "2006-01-02 15:04:05"

// ---------- 会议室（菜单 525 段） ----------

// MeetingRoomSaveReq 是会议室创建/编辑请求（POST /oa/room 与
// PUT /oa/room/{id}）：名称 1–100 字，位置 ≤200 字，容量 0–100000。
type MeetingRoomSaveReq struct {
	Name     string `json:"name" binding:"required"`
	Location string `json:"location"`
	Capacity int    `json:"capacity"`
	Sort     int    `json:"sort"`
}

// MeetingRoomStatusReq 是会议室启停请求：0 启用 / 1 停用。
type MeetingRoomStatusReq struct {
	Status int8 `json:"status"`
}

// MeetingRoomPageReq 是会议室分页筛选：name 模糊、status 精确（nil 不过滤）。
type MeetingRoomPageReq struct {
	web.PageQuery
	Name   string `form:"name"`
	Status *int8  `form:"status"`
}

// MeetingRoomResp 是会议室行视图。
type MeetingRoomResp struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Capacity   int    `json:"capacity"`
	Status     int8   `json:"status"`
	Sort       int    `json:"sort"`
	CreateTime string `json:"createTime"`
	UpdateTime string `json:"updateTime"`
}

// NewMeetingRoomResp 由模型行构造会议室响应（时间按业务时区墙钟格式化）。
func NewMeetingRoomResp(row *model.MeetingRoom, loc *time.Location) MeetingRoomResp {
	resp := MeetingRoomResp{
		ID:       row.ID,
		Name:     row.Name,
		Location: row.Location,
		Capacity: row.Capacity,
		Status:   row.Status,
		Sort:     row.Sort,
	}
	if !row.CreatedAt.IsZero() {
		resp.CreateTime = row.CreatedAt.In(loc).Format(calendarDTOLayout)
	}
	if !row.UpdatedAt.IsZero() {
		resp.UpdateTime = row.UpdatedAt.In(loc).Format(calendarDTOLayout)
	}
	return resp
}

// ---------- 会议室预约（菜单 538/539 段） ----------

// RoomBookingCreateReq 是创建预约请求（POST /oa/room/booking）：
// 房间必填；标题 1–200 字；起止时间为业务时区墙钟，结束必须晚于开始。
type RoomBookingCreateReq struct {
	RoomID    int64  `json:"roomId" binding:"required"`
	Title     string `json:"title" binding:"required"`
	StartTime string `json:"startTime" binding:"required"`
	EndTime   string `json:"endTime" binding:"required"`
}

// RoomBookingPageReq 是预约分页筛选（GET /oa/room/booking/list）：
//   - roomId 精确；date 为业务时区日期 YYYY-MM-DD（查询当日相交的预约）；
//   - status 精确（nil 不过滤）；userId 仅对持有 oa:room:manage 的查看者生效；
//   - 无管理权限的员工侧强制只看本人预约（服务层收口）。
type RoomBookingPageReq struct {
	web.PageQuery
	RoomID int64  `form:"roomId"`
	UserID int64  `form:"userId"`
	Status *int8  `form:"status"`
	Date   string `form:"date"`
}

// RoomBookingResp 是预约行视图（列表/创建/取消返回共用）。
type RoomBookingResp struct {
	ID              int64  `json:"id"`
	RoomID          int64  `json:"roomId"`
	RoomName        string `json:"roomName"`
	RoomLocation    string `json:"roomLocation"`
	UserID          int64  `json:"userId"`
	UserName        string `json:"userName"`
	Title           string `json:"title"`
	StartTime       string `json:"startTime"`
	EndTime         string `json:"endTime"`
	Status          int8   `json:"status"`
	CalendarEventID *int64 `json:"calendarEventId"`
	RemindedAt      string `json:"remindedAt"`
	CreateTime      string `json:"createTime"`
	UpdateTime      string `json:"updateTime"`
}

// NewRoomBookingResp 由模型行构造预约响应；room/user 名称由调用方回填。
func NewRoomBookingResp(row *model.RoomBooking, loc *time.Location) RoomBookingResp {
	resp := RoomBookingResp{
		ID:              row.ID,
		RoomID:          row.RoomID,
		UserID:          row.UserID,
		Title:           row.Title,
		StartTime:       row.StartTime.In(loc).Format(calendarDTOLayout),
		EndTime:         row.EndTime.In(loc).Format(calendarDTOLayout),
		Status:          row.Status,
		CalendarEventID: row.CalendarEventID,
	}
	if row.RemindedAt != nil {
		resp.RemindedAt = row.RemindedAt.In(loc).Format(calendarDTOLayout)
	}
	if !row.CreatedAt.IsZero() {
		resp.CreateTime = row.CreatedAt.In(loc).Format(calendarDTOLayout)
	}
	if !row.UpdatedAt.IsZero() {
		resp.UpdateTime = row.UpdatedAt.In(loc).Format(calendarDTOLayout)
	}
	return resp
}
