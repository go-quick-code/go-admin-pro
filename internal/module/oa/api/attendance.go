// attendance.go 承载考勤基础的 HTTP 入口（issue #60，docs/oa-mvp-spec.md
// §3.9/§4.10/§5）：班次管理挂 oa:attendance:shift:manage、考勤组管理挂
// oa:attendance:group:manage（菜单种子 527/530）；员工打卡以登录身份收口
// （531 我的考勤，无权限码门槛，与 bpm 待办动作同款）。业务校验与中文错误
// 文案在 service 层，handler 只做绑定转发。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// ---------- 班次 ----------

// PageShifts 班次分页（GET /oa/attendance/shift）。
func (h *Handler) PageShifts(c *gin.Context) {
	var req dto.ShiftPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageShifts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetShift 班次详情（GET /oa/attendance/shift/:id）。
func (h *Handler) GetShift(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetShift(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// CreateShift 新建班次（POST /oa/attendance/shift）。
func (h *Handler) CreateShift(c *gin.Context) {
	var req dto.ShiftCreateReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.CreateShift(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// UpdateShift 更新班次（PUT /oa/attendance/shift/:id）。
func (h *Handler) UpdateShift(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.ShiftUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateShift(c.Request.Context(), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// SetShiftStatus 启停班次（PUT /oa/attendance/shift/:id/status）。
func (h *Handler) SetShiftStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.StatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.SetShiftStatus(c.Request.Context(), id, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// DeleteShift 删除班次（DELETE /oa/attendance/shift/:id；被组绑定 409）。
func (h *Handler) DeleteShift(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteShift(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 考勤组 ----------

// PageGroups 考勤组分页（GET /oa/attendance/group）。
func (h *Handler) PageGroups(c *gin.Context) {
	var req dto.GroupPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageGroups(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetGroup 考勤组详情含组员（GET /oa/attendance/group/:id）。
func (h *Handler) GetGroup(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetGroup(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// CreateGroup 新建考勤组（POST /oa/attendance/group）。
func (h *Handler) CreateGroup(c *gin.Context) {
	var req dto.GroupCreateReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.CreateGroup(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// UpdateGroup 更新考勤组（PUT /oa/attendance/group/:id）。
func (h *Handler) UpdateGroup(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.GroupUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateGroup(c.Request.Context(), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// SetGroupStatus 启停考勤组（PUT /oa/attendance/group/:id/status）。
func (h *Handler) SetGroupStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.StatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.SetGroupStatus(c.Request.Context(), id, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// DeleteGroup 删除考勤组（DELETE /oa/attendance/group/:id；有组员 409）。
func (h *Handler) DeleteGroup(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteGroup(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// AddGroupMembers 按用户批量添加组员（POST /oa/attendance/group/:id/members）。
func (h *Handler) AddGroupMembers(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.AddMembersReq
	if !bindJSON(c, &req) {
		return
	}
	added, err := h.svc.AddGroupMembers(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dto.AddMembersResp{Added: added})
}

// AddGroupMembersByDept 按部门批量添加组员
// （POST /oa/attendance/group/:id/members/by-dept）。
func (h *Handler) AddGroupMembersByDept(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.AddMembersByDeptReq
	if !bindJSON(c, &req) {
		return
	}
	added, err := h.svc.AddGroupMembersByDept(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dto.AddMembersResp{Added: added})
}

// RemoveGroupMember 移出组员（DELETE /oa/attendance/group/:id/members/:userId）。
func (h *Handler) RemoveGroupMember(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	userID, ok := web.ParseID(c, "userId")
	if !ok {
		response.BadRequest(c, "非法的用户 ID")
		return
	}
	if err := h.svc.RemoveGroupMember(c.Request.Context(), id, userID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- Web 打卡（登录即可用，spec §4.10）----------

// Punch 统一打卡入口（POST /oa/attendance/punch，spec §5）：请求体 type
// 0 上班 / 1 下班；与 /punch/in、/punch/out 等价。
func (h *Handler) Punch(c *gin.Context) {
	var req dto.PunchReq
	if !bindJSON(c, &req) {
		return
	}
	if req.Type != model.PunchTypeIn && req.Type != model.PunchTypeOut {
		response.BadRequest(c, "打卡类型应为 上班 / 下班")
		return
	}
	h.doPunch(c, req.Type)
}

// PunchIn 上班打卡（POST /oa/attendance/punch/in）。
func (h *Handler) PunchIn(c *gin.Context) {
	h.doPunch(c, model.PunchTypeIn)
}

// PunchOut 下班打卡（POST /oa/attendance/punch/out）。
func (h *Handler) PunchOut(c *gin.Context) {
	h.doPunch(c, model.PunchTypeOut)
}

// doPunch 统一打卡入口：记录时间/IP/User-Agent；无启用考勤组 400。
func (h *Handler) doPunch(c *gin.Context, punchType int8) {
	resp, err := h.svc.Punch(c.Request.Context(), punchType, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// GetMyPunchToday 我的打卡状态（GET /oa/attendance/punch/today）。
func (h *Handler) GetMyPunchToday(c *gin.Context) {
	resp, err := h.svc.GetMyPunchToday(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// PageMyPunches 我的打卡记录分页（GET /oa/attendance/punch/page）。
func (h *Handler) PageMyPunches(c *gin.Context) {
	var req dto.PunchPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageMyPunches(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
