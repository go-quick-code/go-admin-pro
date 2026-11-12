package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_demo.go 是示例业务的 HTTP 入口（issue #137，spec §5/§6/§4.11）：
//   - 示例订单：创建/字典/退款只挂登录（「demo 创建」自助端点，本人校验在
//     service 层强制，spec §4.11）；分页挂 pay:demo-order:query（管理台页面，
//     不设 owner 行级数据权限）。
//   - 示例提现：申请只挂登录（自助端点，user_account 服务端取登录主体）；
//     分页挂 pay:demo-withdraw:query；审批/关闭挂独立权限码
//     pay:demo-withdraw:approve|close（spec §6 操作按钮走独立权限码——close
//     为 spec §5 未列的页面实际授予端点，决策记录见 service/demo.go 头注释）。

// ListDemoSPUs 演示商品字典清单（登录即可）：创建表单的商品选择数据源。
func (h *Handler) ListDemoSPUs(c *gin.Context) {
	options, err := h.svc.ListDemoSPUs(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, options)
}

// CreateDemoOrder 创建示例订单（自助端点，登录）：spuId 取字典，同事务挂支付
// 订单，返回挂单定位（发起支付跳收银台）。
func (h *Handler) CreateDemoOrder(c *gin.Context) {
	var req dto.DemoOrderCreateReq
	if !bindJSON(c, &req) {
		return
	}
	view, err := h.svc.CreateDemoOrder(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}

// PageDemoOrders 示例订单分页（管理端）。
func (h *Handler) PageDemoOrders(c *gin.Context) {
	var req dto.DemoOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListDemoOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// RefundDemoOrder 示例订单发起退款（登录 + 本人订单）：全额退未退余额，
// 成功联动走 #134 回调链路（type=2 任务 → job → demo/refund 端点）。
func (h *Handler) RefundDemoOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.DemoOrderRefundReq
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if !bindJSON(c, &req) {
			return
		}
	}
	refund, err := h.svc.RefundDemoOrder(c.Request.Context(), id, &req, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, refund)
}

// CreateDemoWithdraw 申请提现（自助端点，登录）：user_account/user_name 服务
// 端取登录主体。
func (h *Handler) CreateDemoWithdraw(c *gin.Context) {
	var req dto.DemoWithdrawCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateDemoWithdraw(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// PageDemoWithdraws 示例提现分页（管理端）。
func (h *Handler) PageDemoWithdraws(c *gin.Context) {
	var req dto.DemoWithdrawPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListDemoWithdraws(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// ApproveDemoWithdraw 审批提现（管理端，pay:demo-withdraw:approve）：创建
// 转账单（wallet 渠道入账申请人钱包），转账成功回调联动置 10。
func (h *Handler) ApproveDemoWithdraw(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.ApproveDemoWithdraw(c.Request.Context(), id, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CloseDemoWithdraw 关闭提现（管理端，pay:demo-withdraw:close）：仅等待状态
// 且未发起转账可关，置 20 不建转账单。
func (h *Handler) CloseDemoWithdraw(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.CloseDemoWithdraw(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}
