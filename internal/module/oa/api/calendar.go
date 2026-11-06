// calendar.go 是日程的 HTTP 入口（issue #56，docs/oa-mvp-spec.md
// §4.11/§5）：
//
//   - 列表/详情：oa:calendar:query（菜单 524；员工侧菜单 536 同码）——
//     可见性由服务层谓词收口（私有/部门/公开），范围外与跨租户 404；
//   - 创建：oa:calendar:create（5241）；公开日程另需 oa:calendar:public
//     （5244，服务层校验，缺失返回 403）；
//   - 编辑：oa:calendar:update（5242），仅创建人、仅有效日程；
//   - 取消：oa:calendar:delete（5243），软取消（status=1），取消后不再展示。
//
// DELETE /oa/calendar/event/{id} 与 POST /oa/calendar/event/{id}/cancel
// 等价（spec §5 用 DELETE；UI 用显式「取消」动作更清晰）。
package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 日程权限码（菜单种子 524 段：524 查询、5241 创建、5242 编辑、5243 取消、
// 5244 公开日程；issue #56）。
const (
	calendarQuery = "oa:calendar:query"
)

// CreateCalendarEvent 创建日程（POST /oa/calendar/event，oa:calendar:create）。
func (h *Handler) CreateCalendarEvent(c *gin.Context) {
	var req dto.CalendarEventSaveReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.CreateCalendarEvent(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// UpdateCalendarEvent 编辑日程（PUT /oa/calendar/event，oa:calendar:update）。
func (h *Handler) UpdateCalendarEvent(c *gin.Context) {
	var req dto.CalendarEventSaveReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.UpdateCalendarEvent(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// CancelCalendarEvent 取消日程（DELETE /oa/calendar/event/:id 或
// POST /oa/calendar/event/:id/cancel，oa:calendar:delete）：软取消。
func (h *Handler) CancelCalendarEvent(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.CancelCalendarEvent(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ListCalendarEvents 日程列表（GET /oa/calendar/event/list，oa:calendar:query）：
// 月/周视图传时间窗口；按可见性返回有效日程。
func (h *Handler) ListCalendarEvents(c *gin.Context) {
	var req dto.CalendarEventListReq
	if !bindQuery(c, &req) {
		return
	}
	items, err := h.svc.ListCalendarEvents(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, items)
}

// GetCalendarEvent 日程详情（GET /oa/calendar/event/:id，oa:calendar:query）。
func (h *Handler) GetCalendarEvent(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.GetCalendarEvent(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}
