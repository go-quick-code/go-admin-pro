package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_recharge.go 是充值与套餐域的 HTTP 入口（issue #136，spec §5/§4.11）：
//   - 套餐 CRUD（管理端）挂 pay:wallet-recharge-package:* 四码；自助端点
//     GET /pay/wallet-recharge-package/list 只挂登录（发起充值的套餐选择
//     数据源，同 /pay/channel/list 的租户侧只读清单先例）；
//   - 发起充值/我的充值只挂登录（自助端点，服务层强制 wallet 归属本人，
//     spec §4.11）；管理端分页挂 pay:wallet-recharge:query。充值单只读
//    （append-only 资金事实，spec §4.10）：无编辑/删除端点。

// ---------- 充值套餐（管理端 CRUD + 自助清单） ----------

// PageRechargePackages 充值套餐分页（管理端）。
func (h *Handler) PageRechargePackages(c *gin.Context) {
	var req dto.RechargePackagePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListRechargePackages(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetRechargePackage 充值套餐详情（管理端）。
func (h *Handler) GetRechargePackage(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetRechargePackage(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateRechargePackage 创建充值套餐（管理端）。
func (h *Handler) CreateRechargePackage(c *gin.Context) {
	var req dto.RechargePackageSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateRechargePackage(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateRechargePackage 修改充值套餐（管理端，id 在 body）。
func (h *Handler) UpdateRechargePackage(c *gin.Context) {
	var req dto.RechargePackageUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateRechargePackage(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// DeleteRechargePackage 删除充值套餐（管理端；被充值单引用 409，spec §4.10）。
func (h *Handler) DeleteRechargePackage(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteRechargePackage(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ListRechargePackageOptions 启用套餐清单（自助端点，登录即可）：发起充值
// 的套餐选择数据源，只含启用档位（无状态/审计列）。
func (h *Handler) ListRechargePackageOptions(c *gin.Context) {
	options, err := h.svc.ListRechargePackageOptions(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, options)
}

// ---------- 充值单（自助发起/我的充值 + 管理端分页） ----------

// CreateRecharge 发起充值（自助端点，登录）：套餐快照或自定义金额，同事务
// 挂支付订单（spec §4.7），返回充值单 + 收银台定位参数。
func (h *Handler) CreateRecharge(c *gin.Context) {
	var req dto.RechargeCreateReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.CreateRecharge(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// PageMyRecharges 我的充值分页（自助端点，登录）：服务层强制本人钱包过滤。
func (h *Handler) PageMyRecharges(c *gin.Context) {
	var req dto.RechargeMyPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListMyRecharges(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// PageRecharges 充值单分页（管理端）。
func (h *Handler) PageRecharges(c *gin.Context) {
	var req dto.RechargePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListRecharges(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
