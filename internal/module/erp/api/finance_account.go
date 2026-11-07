package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 结算账户（issue #94，docs/erp-mvp-spec.md §3.6/§4.7/§5）----------
// 无余额入参：列表/详情由服务端返回「已审核收付款净额」实时余额列；默认账户
// 租户内唯一互斥（置默认取消原默认）；被收付款单引用禁删。

// PageFinanceAccounts 结算账户分页（行内带实时余额）。
func (h *Handler) PageFinanceAccounts(c *gin.Context) {
	var req dto.FinanceAccountPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListFinanceAccounts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetFinanceAccount 结算账户详情（含实时余额）。
func (h *Handler) GetFinanceAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetFinanceAccount(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateFinanceAccount 新建结算账户。
func (h *Handler) CreateFinanceAccount(c *gin.Context) {
	var req dto.FinanceAccountCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateFinanceAccount(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateFinanceAccount 修改结算账户（PUT 契约 id 在 body）。
func (h *Handler) UpdateFinanceAccount(c *gin.Context) {
	var req dto.FinanceAccountUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateFinanceAccount(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteFinanceAccount 删除结算账户（被收付款单引用 409）。
func (h *Handler) DeleteFinanceAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteFinanceAccount(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// RegisterFinanceAccountRoutes 注册结算账户路由（静态路由先于 /:id）。
func (h *Handler) RegisterFinanceAccountRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/finance-account")
	doc.GET("/page", h.requirePermission("erp:finance-account:query"), h.PageFinanceAccounts)
	doc.GET("/:id", h.requirePermission("erp:finance-account:query"), h.GetFinanceAccount)
	doc.POST("", h.requirePermission("erp:finance-account:create"), h.CreateFinanceAccount)
	doc.PUT("", h.requirePermission("erp:finance-account:update"), h.UpdateFinanceAccount)
	doc.DELETE("/:id", h.requirePermission("erp:finance-account:delete"), h.DeleteFinanceAccount)
}
