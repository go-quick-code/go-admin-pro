package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// Handler 是 crm 模块的 HTTP 入口。
// requirePermission 复用 system 模块的权限中间件（codegen 同款装配方式），
// 权限码与菜单种子保持一致（crm:customer:*）。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
}

// New 创建 crm Handler。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requirePermission: requirePermission}
}

// RegisterProtected 注册需要登录的 CRM 接口，rg 上应已挂载 Auth 中间件。
// 统一前缀 /crm（docs/crm-mvp-spec.md §5：/admin-api/crm）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	customer := rg.Group("/crm/customer")
	customer.GET("/page", h.requirePermission("crm:customer:query"), h.PageCustomers)
	customer.GET("/pool/page", h.requirePermission("crm:customer:pool:view"), h.PagePoolCustomers)
	customer.GET("/:id", h.requirePermission("crm:customer:query"), h.GetCustomer)
	customer.GET("/:id/owner-history", h.requirePermission("crm:customer:query"), h.PageCustomerOwnerHistory)
	customer.POST("", h.requirePermission("crm:customer:create"), h.CreateCustomer)
	customer.PUT("", h.requirePermission("crm:customer:update"), h.UpdateCustomer)
	// 释放/转移的权限在 service 内做「负责人本人 或 权限码」组合校验
	//（issue #13）：负责人不持按钮码也可操作自己的客户。
	customer.PUT("/release", h.ReleaseCustomer)
	customer.PUT("/claim", h.requirePermission("crm:customer:claim"), h.ClaimCustomer)
	customer.PUT("/transfer", h.TransferCustomer)
	customer.DELETE("/:id", h.requirePermission("crm:customer:delete"), h.DeleteCustomer)
}
