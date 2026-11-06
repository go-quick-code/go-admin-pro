// room.go 是会议室与预约的 HTTP 入口（issue #57，
// docs/oa-mvp-spec.md §4.11/§4.14/§5）：
//
//   - 会议室查询/详情/预约列表：oa:room:query（菜单 525/538/539）；
//   - 会议室 CRUD/启停/删除：oa:room:manage（按钮 5251）；
//   - 创建预约：oa:room:booking（按钮 5252）；
//   - 取消预约：登录即可，服务层按「预约人本人或 oa:room:manage」收口
//     （spec §5：预约人/oa:room:manage）。
//
// 员工侧预约列表复用同一 GET /oa/room/booking/list：无 oa:room:manage 的
// 查看者由服务层强制只看本人，不能通过 userId 参数越权。
package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 会议室权限码（spec §5 OA 日程与会议室；菜单 525 段 + 员工入口 538/539）。
const (
	roomQuery   = "oa:room:query"
	roomManage  = "oa:room:manage"
	roomBooking = "oa:room:booking"
)

// ---------- 会议室 ----------

// CreateMeetingRoom 创建会议室（POST /oa/room，oa:room:manage）。
func (h *Handler) CreateMeetingRoom(c *gin.Context) {
	var req dto.MeetingRoomSaveReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.CreateMeetingRoom(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// UpdateMeetingRoom 编辑会议室（PUT /oa/room/:id，oa:room:manage）。
func (h *Handler) UpdateMeetingRoom(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.MeetingRoomSaveReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.UpdateMeetingRoom(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// SetMeetingRoomStatus 启停会议室（PUT /oa/room/:id/status，oa:room:manage）。
func (h *Handler) SetMeetingRoomStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.MeetingRoomStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.SetMeetingRoomStatus(c.Request.Context(), id, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// DeleteMeetingRoom 删除会议室（DELETE /oa/room/:id，oa:room:manage）：
// 有未来有效预约返回 409。
func (h *Handler) DeleteMeetingRoom(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteMeetingRoom(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// GetMeetingRoom 会议室详情（GET /oa/room/:id，oa:room:query）。
func (h *Handler) GetMeetingRoom(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.GetMeetingRoom(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// PageMeetingRooms 会议室分页（GET /oa/room/page，oa:room:query）。
func (h *Handler) PageMeetingRooms(c *gin.Context) {
	var req dto.MeetingRoomPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageMeetingRooms(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// ListEnabledMeetingRooms 启用会议室列表（GET /oa/room/list，oa:room:query）：
// 员工预约下拉，spec §5 的路径。
func (h *Handler) ListEnabledMeetingRooms(c *gin.Context) {
	list, err := h.svc.ListEnabledMeetingRooms(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ---------- 预约 ----------

// CreateRoomBooking 创建预约（POST /oa/room/booking，oa:room:booking）：
// 同事务生成部门可见日程并回填 calendarEventId；冲突 400。
func (h *Handler) CreateRoomBooking(c *gin.Context) {
	var req dto.RoomBookingCreateReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.CreateRoomBooking(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// CancelRoomBooking 取消预约（POST /oa/room/booking/:id/cancel）：登录即可，
// 服务层校验预约人本人或 oa:room:manage；同步取消生成的日程。
func (h *Handler) CancelRoomBooking(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.CancelRoomBooking(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// GetRoomBooking 预约详情（GET /oa/room/booking/:id，oa:room:query）：
// 非本人且无管理权 404。
func (h *Handler) GetRoomBooking(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.GetRoomBooking(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// PageRoomBookings 预约分页（GET /oa/room/booking/list，oa:room:query）：
// 管理端按房间/日期查看全部；员工侧强制本人。
func (h *Handler) PageRoomBookings(c *gin.Context) {
	var req dto.RoomBookingPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageRoomBookings(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
