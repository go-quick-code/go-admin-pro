package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// Handler 是 oa 模块的 HTTP 入口。
// requirePermission 复用 system 模块的权限中间件（与 crm 同款装配方式）：
// 本工单（issue #43）的可见范围探测接口是登录即可用的「我的可见范围」查询，
// 不挂权限码；后续业务路由（公告/请假/报销/考勤等）统一经它挂
// oa:<resource>:<action> 按钮码（菜单种子 500 段）。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
}

// New 创建 oa Handler。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, requirePermission: requirePermission}
}

// RegisterProtected 注册需要登录的 OA 接口，rg 上应已挂载 Auth 中间件。
// 统一前缀 /oa（docs/oa-mvp-spec.md §5）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	oa := rg.Group("/oa")
	// 可见范围探测：返回当前用户在某类 OA 业务记录上的可见范围
	//（spec §4.6：manage → 全租户；部门负责人 → 子树用户 ∪ 本人；否则仅本人）。
	// 任何登录用户都可探知自己的可见范围（与 system 仪表盘/部门树同级的
	// 登录即可用接口）；resource 未登记返回 400。
	oa.GET("/visibility-scope", h.GetVisibilityScope)
}

// GetVisibilityScope 返回当前用户的可见范围。
func (h *Handler) GetVisibilityScope(c *gin.Context) {
	resource := c.Query("resource")
	scope, err := h.svc.VisibilityScopeOf(c.Request.Context(), resource)
	if err != nil {
		response.Fail(c, err)
		return
	}
	// UserIDs 统一输出空数组（manage 路径不携带集合，避免 JSON null）。
	userIDs := scope.UserIDs
	if userIDs == nil {
		userIDs = []int64{}
	}
	response.OK(c, dto.VisibilityScopeResp{Resource: resource, All: scope.All, UserIDs: userIDs})
}
