// attendance_result.go 承载考勤结果与校正的 HTTP 入口（issue #61，
// docs/oa-mvp-spec.md §4.10/§5）：
//   - 管理端结果查询挂 oa:attendance:result:query（菜单 533）；
//   - 校正/撤销校正/手工重算挂 oa:attendance:result:correct（按钮 5331）；
//   - 员工「我的考勤」结果列表以登录身份收口（强制本人，无权限码门槛）。
//
// 业务校验与中文错误文案在 service 层，handler 只做绑定转发。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// PageAttendanceResults 管理端考勤结果分页（GET /oa/attendance/result/page）。
func (h *Handler) PageAttendanceResults(c *gin.Context) {
	var req dto.AttendanceResultPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageAttendanceResults(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// PageMyAttendanceResults 我的考勤结果分页（GET /oa/attendance/result/mine）：
// 强制本人，与打卡页同款登录即可用。
func (h *Handler) PageMyAttendanceResults(c *gin.Context) {
	var req dto.MyAttendanceResultPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageMyAttendanceResults(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// RecomputeAttendanceResults 手工重算（POST /oa/attendance/result/recompute）：
// 按用户 + 日期范围，幂等覆盖结果行。
func (h *Handler) RecomputeAttendanceResults(c *gin.Context) {
	var req dto.AttendanceRecomputeReq
	if !bindJSON(c, &req) {
		return
	}
	days, err := h.svc.RecomputeUserRange(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dto.AttendanceRecomputeResp{Days: days})
}

// CorrectAttendanceResult 管理员校正（PUT /oa/attendance/result/:id/correct）：
// 只写 override 状态与备注，不覆盖原始打卡；校正优先于派生结果。
func (h *Handler) CorrectAttendanceResult(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.AttendanceCorrectReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CorrectAttendanceResult(c.Request.Context(), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ClearAttendanceResultCorrection 撤销校正
// （DELETE /oa/attendance/result/:id/correct）：结果回落到派生状态。
func (h *Handler) ClearAttendanceResultCorrection(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.ClearAttendanceResultCorrection(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}
