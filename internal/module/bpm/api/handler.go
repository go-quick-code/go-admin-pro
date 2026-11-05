// Package api 是 bpm 模块的 HTTP 入口（issue #44）：路由、参数绑定与统一响应。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
)

// Handler 是 bpm 模块的 HTTP 入口。
// requirePermission 复用 system 模块的权限中间件（crm 同款装配方式），
// 权限码与菜单种子保持一致（bpm:definition:*）。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
}

// New 创建 bpm Handler。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requirePermission: requirePermission}
}

// RegisterProtected 注册需要登录的 bpm 接口，rg 上应已挂载 Auth 中间件。
// 统一前缀 /bpm（spec #42 §5：/admin-api/bpm）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	h.RegisterDefinitionRoutes(rg)
	h.RegisterRuntimeRoutes(rg)
}
