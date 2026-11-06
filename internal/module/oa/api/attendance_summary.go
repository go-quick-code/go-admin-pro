// attendance_summary.go 承载考勤月度汇总的 HTTP 入口（issue #62，
// docs/oa-mvp-spec.md §4.10/§5）：
//   - 管理端汇总分页与单用户查询挂 oa:attendance:summary:query（菜单 534）；
//   - 员工「我的考勤」本月汇总卡片以登录身份收口（强制本人，无权限码门槛）。
//
// 可见范围（oa:attendance:manage → 全租户、部门负责人 → 子树、否则本人）
// 在 service 层经 VisibilityScopeOf("attendance") 收口，handler 只做绑定转发。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// PageAttendanceSummaries 管理端月度汇总分页
// （GET /oa/attendance/summary/page?month=YYYY-MM）。
func (h *Handler) PageAttendanceSummaries(c *gin.Context) {
	var req dto.AttendanceSummaryPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageAttendanceSummaries(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetAttendanceSummaryOfUser 单用户月度汇总
// （GET /oa/attendance/summary/user?month=YYYY-MM&userId=）。范围外/跨租户 404。
func (h *Handler) GetAttendanceSummaryOfUser(c *gin.Context) {
	var req dto.AttendanceSummaryOfUserReq
	if !bindQuery(c, &req) {
		return
	}
	summary, err := h.svc.GetAttendanceSummaryOfUser(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, summary)
}

// GetMyAttendanceSummary 我的月度汇总
// （GET /oa/attendance/summary/mine?month=YYYY-MM；month 缺省当月）：
// 强制本人，与「我的考勤」页同款登录即可用。
func (h *Handler) GetMyAttendanceSummary(c *gin.Context) {
	var req dto.MyAttendanceSummaryReq
	if !bindQuery(c, &req) {
		return
	}
	summary, err := h.svc.GetMyAttendanceSummary(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, summary)
}
