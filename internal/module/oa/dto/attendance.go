package dto

import "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"

// ---------- 考勤：班次（issue #60，docs/oa-mvp-spec.md §3.9/§4.10）----------

// ShiftCreateReq 新建班次。startTime/endTime 为 time-of-day HH:MM（不支持
// 跨天，end > start）；graceMinutes 为迟到/早退宽限分钟（0–1440）。
type ShiftCreateReq struct {
	Name         string `json:"name" binding:"required"`
	StartTime    string `json:"startTime" binding:"required"`
	EndTime      string `json:"endTime" binding:"required"`
	GraceMinutes int    `json:"graceMinutes"`
	Sort         int    `json:"sort"`
}

// ShiftUpdateReq 更新班次（全字段替换）。
type ShiftUpdateReq struct {
	Name         string `json:"name" binding:"required"`
	StartTime    string `json:"startTime" binding:"required"`
	EndTime      string `json:"endTime" binding:"required"`
	GraceMinutes int    `json:"graceMinutes"`
	Sort         int    `json:"sort"`
}

// ShiftPageReq 班次分页筛选：name 模糊、status 精确（nil 不过滤）。
type ShiftPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// StatusReq 通用启停请求（班次/考勤组共用）：0 启用 / 1 停用。
type StatusReq struct {
	Status int8 `json:"status"`
}

// AttendanceShiftResp 是班次响应行。
type AttendanceShiftResp struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	GraceMinutes int    `json:"graceMinutes"`
	Status       int8   `json:"status"`
	Sort         int    `json:"sort"`
}

// NewShiftResp 由模型行构造班次响应。
func NewShiftResp(row *model.AttendanceShift) AttendanceShiftResp {
	return AttendanceShiftResp{
		ID:           row.ID,
		Name:         row.Name,
		StartTime:    row.StartTime,
		EndTime:      row.EndTime,
		GraceMinutes: row.GraceMinutes,
		Status:       row.Status,
		Sort:         row.Sort,
	}
}

// ---------- 考勤：考勤组与组员（issue #60）----------

// GroupCreateReq 新建考勤组（绑定班次必须存在）。
type GroupCreateReq struct {
	Name    string `json:"name" binding:"required"`
	ShiftID int64  `json:"shiftId" binding:"required"`
}

// GroupUpdateReq 更新考勤组（名称/绑定班次）。
type GroupUpdateReq struct {
	Name    string `json:"name" binding:"required"`
	ShiftID int64  `json:"shiftId" binding:"required"`
}

// GroupPageReq 考勤组分页筛选：name 模糊、status 精确（nil 不过滤）。
type GroupPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// GroupMemberItem 是组员条目（昵称经 system 网关回填，缺失为空串）。
type GroupMemberItem struct {
	UserID   int64  `json:"userId"`
	Nickname string `json:"nickname"`
}

// AttendanceGroupResp 是考勤组响应（详情含组员列表；分页时 Members 为空、
// 仅带 MemberCount）。
type AttendanceGroupResp struct {
	ID          int64                `json:"id"`
	Name        string               `json:"name"`
	ShiftID     int64                `json:"shiftId"`
	Shift       *AttendanceShiftResp `json:"shift,omitempty"`
	Status      int8                 `json:"status"`
	MemberCount int64                `json:"memberCount"`
	Members     []GroupMemberItem    `json:"members"`
}

// AddMembersReq 按用户批量添加组员（仅本租户启用用户生效，同批去重；
// 已在其他组的用户改绑到本组——一个用户只属于一个组）。
type AddMembersReq struct {
	UserIDs []int64 `json:"userIds"`
}

// AddMembersByDeptReq 按部门批量添加组员（取部门当前成员物化入组，
// 后续不自动同步部门变化）。
type AddMembersByDeptReq struct {
	DeptIDs []int64 `json:"deptIds"`
}

// AddMembersResp 是批量添加组员响应：added = 实际入组人数。
type AddMembersResp struct {
	Added int `json:"added"`
}

// ---------- 考勤：Web 打卡（issue #60）----------

// PunchReq 是 spec §5 的通用打卡请求（type 0 上班 / 1 下班）；与 /punch/in、
// /punch/out 两个动作路径等价，供客户端按统一入口调用。
type PunchReq struct {
	Type int8 `json:"type"`
}

// PunchResp 是打卡记录响应（打卡动作回显 / 打卡列表行）。
type PunchResp struct {
	ID         int64  `json:"id"`
	Type       int8   `json:"type"`
	TypeName   string `json:"typeName"`
	Date       string `json:"date"`
	Time       string `json:"time"`
	Source     int8   `json:"source"`
	SourceName string `json:"sourceName"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
	// GroupID/GroupName 打卡动作回显所属组（列表行不填）。
	GroupID   int64  `json:"groupId"`
	GroupName string `json:"groupName"`
}

// PunchGroupInfo 是打卡页展示的组/班次信息（未加入启用组时为 null）。
type PunchGroupInfo struct {
	ID     int64                `json:"id"`
	Name   string               `json:"name"`
	Status int8                 `json:"status"`
	Shift  *AttendanceShiftResp `json:"shift,omitempty"`
}

// MyPunchTodayResp 是员工打卡页的今日状态：所在组信息（可为 null）+
// 当天全部打卡（升序）。date 为业务时区当天（YYYY-MM-DD）。
type MyPunchTodayResp struct {
	Date    string          `json:"date"`
	Group   *PunchGroupInfo `json:"group"`
	Punches []PunchResp     `json:"punches"`
}

// PunchPageReq 我的打卡记录分页（日期区间可选，含两端）。
type PunchPageReq struct {
	PageNo    int    `form:"pageNo"`
	PageSize  int    `form:"pageSize"`
	StartDate string `form:"startDate"`
	EndDate   string `form:"endDate"`
}
