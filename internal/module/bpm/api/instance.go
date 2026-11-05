// instance.go 承载流程运行的 HTTP 入口（issue #46，spec #42 §5 bpm 节）：
//
//	GET  /bpm/task/page            bpm:task:query   tab=todo/done/initiated
//	GET  /bpm/instance/page        bpm:instance:query
//	GET  /bpm/instance/:id         发起人/办理人/持有 bpm:instance:query（service 判定）
//	POST /bpm/task/:id/approve     当前办理人（service 判定，无权限码门槛）
//	POST /bpm/task/:id/reject      当前办理人，原因必填
//	POST /bpm/task/:id/transfer    当前办理人，目标用户
//	POST /bpm/instance/:id/withdraw 发起人，首个审批前
//
// 动作类路由按 spec §5 以「当前办理人/发起人」身份收口，不挂权限码
// （普通员工办理自己的待办不应依赖管理员授码）；bpm:task:handle 供前端
// 按钮显隐（菜单种子 5121）。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// PageMyTasks 通用待办页分页（tab=todo/done/initiated）。
func (h *Handler) PageMyTasks(c *gin.Context) {
	var req dto.TaskPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, total, err := h.svc.PageMyTasks(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, data, total)
}

// PageInstances 实例管理分页（bpm:instance:query）。
func (h *Handler) PageInstances(c *gin.Context) {
	var req dto.InstancePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.PageInstances(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetInstanceDetail 实例详情（时间线 + 节点任务，访问身份在 service 判定）。
func (h *Handler) GetInstanceDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetInstanceDetail(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// ApproveTask 通过当前节点。
func (h *Handler) ApproveTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.TaskActionReq
	if !bindJSON(c, &req) {
		return
	}
	inst, err := h.svc.ApproveTask(c.Request.Context(), id, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, inst)
}

// RejectTask 驳回（原因必填）。
func (h *Handler) RejectTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.TaskActionReq
	if !bindJSON(c, &req) {
		return
	}
	inst, err := h.svc.RejectTask(c.Request.Context(), id, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, inst)
}

// TransferTask 转办给目标用户。
func (h *Handler) TransferTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.TaskTransferReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.TransferTask(c.Request.Context(), id, req.ToUserID, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// WithdrawInstance 发起人撤回。
func (h *Handler) WithdrawInstance(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.TaskActionReq
	if !bindJSON(c, &req) {
		return
	}
	inst, err := h.svc.WithdrawInstance(c.Request.Context(), id, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, inst)
}
