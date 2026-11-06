// calendar.go 是日程的请求/响应 DTO（issue #56，docs/oa-mvp-spec.md
// §3.7/§4.11/§5）：
//
//   - 保存请求：标题/描述/起止时间/全天/可见性；创建与编辑共用；
//   - 列表：可选时间窗口（月/周视图按窗口拉取）与关键词；
//   - 行视图：起止时间按业务时区墙钟格式化（YYYY-MM-DD HH:mm:ss）。
//
// 可见性不在请求里做权限判断（服务层校验公开权限）；部门快照由服务端在
// 创建时写入，请求不携带 deptId（防止越权指定可见部门）。
package dto

// CalendarEventSaveReq 是日程创建/编辑请求（POST /oa/calendar/event 与
// PUT /oa/calendar/event）：
//   - 标题 1–200 字、描述 ≤1000 字；
//   - startTime/endTime 接受业务时区墙钟 "YYYY-MM-DD HH:mm:ss" 或 RFC3339；
//   - visibility 0 私有 / 1 部门 / 2 公开（公开需 oa:calendar:public）。
type CalendarEventSaveReq struct {
	// ID 编辑时必填；创建忽略。
	ID int64 `json:"id"`
	// Title 日程标题（1–200 字）。
	Title string `json:"title" binding:"required"`
	// Description 日程描述（≤1000 字，可空）。
	Description string `json:"description"`
	// StartTime 开始时间（业务时区）。
	StartTime string `json:"startTime" binding:"required"`
	// EndTime 结束时间（业务时区，必须晚于开始时间）。
	EndTime string `json:"endTime" binding:"required"`
	// AllDay 全天标记（展示用）。
	AllDay bool `json:"allDay"`
	// Visibility 可见性（0 私有 / 1 部门 / 2 公开）。
	Visibility int8 `json:"visibility"`
}

// CalendarEventListReq 是日程列表查询（GET /oa/calendar/event/list）：
// 月/周视图传入窗口；两端口径为「日程与窗口相交」。关键词匹配标题。
type CalendarEventListReq struct {
	// StartTime 窗口开始（可选，须与 EndTime 同时提供）。
	StartTime string `form:"startTime"`
	// EndTime 窗口结束（可选，须与 StartTime 同时提供）。
	EndTime string `form:"endTime"`
	// Keyword 标题关键词（可选）。
	Keyword string `form:"keyword"`
}

// CalendarEventItem 是日程行视图（列表/详情/保存返回共用）。
type CalendarEventItem struct {
	ID          int64  `json:"id"`
	CreatorID   int64  `json:"creatorId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	AllDay      bool   `json:"allDay"`
	Visibility  int8   `json:"visibility"`
	DeptID      int64  `json:"deptId"`
	Source      int8   `json:"source"`
	SourceID    *int64 `json:"sourceId"`
	Status      int8   `json:"status"`
	CreateTime  string `json:"createTime"`
	UpdateTime  string `json:"updateTime"`
}
