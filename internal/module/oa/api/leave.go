// leave.go 承载请假类型与额度管理的 HTTP 入口（issue #51，docs/oa-mvp-spec.md
// §3.5/§4.8/§5）：类型 CRUD/启停/删除、额度初始化/分页/本人余额/调整。
// 权限模型（本工单约定，与 #43 helper 登记一致）：
//   - 类型与额度的全部管理动作挂 oa:leave:manage（service.ManagePermission("leave")，
//     即默认审批流 oa_leave_approval 节点 2 的 HR/管理员码，spec §7.3）；
//   - 类型列表与本人余额登录即可（员工发起请假/查看余额的前置）。
package api

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// manageLeave 是请假域的管理权限码（helper 的 ManagePermission("leave")
// 产出，菜单种子 522 的权限码一致）。
var manageLeave = service.ManagePermission("leave")

// ListLeaveTypes 类型列表（GET /oa/leave/type/list，登录即可）：
// status 精确、keyword 模糊（名称/编码），sort 升序。
func (h *Handler) ListLeaveTypes(c *gin.Context) {
	var req dto.LeaveTypeListReq
	if !bindQuery(c, &req) {
		return
	}
	rows, err := h.svc.ListLeaveTypes(c.Request.Context(), req.Status, req.Keyword)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, rows)
}

// CreateLeaveType 创建类型（POST /oa/leave/type，oa:leave:manage）。
func (h *Handler) CreateLeaveType(c *gin.Context) {
	var req dto.LeaveTypeCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateLeaveType(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateLeaveType 更新类型（PUT /oa/leave/type，oa:leave:manage；code 不可变）。
func (h *Handler) UpdateLeaveType(c *gin.Context) {
	var req dto.LeaveTypeUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.UpdateLeaveType(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// SetLeaveTypeStatus 启停类型（PUT /oa/leave/type/:id/status，oa:leave:manage）。
func (h *Handler) SetLeaveTypeStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.LeaveTypeStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.SetLeaveTypeStatus(c.Request.Context(), id, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// DeleteLeaveType 删除类型（DELETE /oa/leave/type/:id，oa:leave:manage）：
// 被申请引用时 409（只能停用）。
func (h *Handler) DeleteLeaveType(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteLeaveType(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// PageBalances 额度管理分页（GET /oa/leave/balance/page，oa:leave:manage）：
// userId/leaveTypeId/year 精确过滤，附类型与员工昵称。
func (h *Handler) PageBalances(c *gin.Context) {
	var req dto.BalancePageReq
	if !bindQuery(c, &req) {
		return
	}
	items, total, err := h.svc.PageBalances(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, items, total)
}

// MyBalances 本人额度（GET /oa/leave/balance/mine，登录即可）：
// year 省略取当前自然年；覆盖全部启用中类型。
func (h *Handler) MyBalances(c *gin.Context) {
	year := 0
	if raw := c.Query("year"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			response.BadRequest(c, "年度应为自然年（如 2026）")
			return
		}
		year = v
	}
	resp, err := h.svc.MyBalances(c.Request.Context(), year)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// InitBalances 额度初始化（POST /oa/leave/balance/init，oa:leave:manage）：
// 幂等——已有 (用户, 类型, 年) 行不重复建、不覆盖。
func (h *Handler) InitBalances(c *gin.Context) {
	var req dto.BalanceInitReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.InitBalances(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// AdjustBalance 管理员调整额度（POST /oa/leave/balance/adjust，oa:leave:manage）：
// 原因必填并留痕。
func (h *Handler) AdjustBalance(c *gin.Context) {
	var req dto.BalanceAdjustReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.AdjustBalance(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}
