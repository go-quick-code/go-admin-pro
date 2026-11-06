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

	// 工作日历（issue #50，菜单种子 520 段）：管理端按月查看与维护；
	// 权限码 oa:workday:*（520 查询、5201 单日、5202 批量、5203 恢复默认）。
	workday := oa.Group("/workday")
	workday.GET("/month", h.requirePermission("oa:workday:query"), h.GetWorkdayMonth)
	workday.PUT("", h.requirePermission("oa:workday:set"), h.SetWorkday)
	workday.POST("/batch", h.requirePermission("oa:workday:batch"), h.BatchWorkday)
	workday.DELETE("/:date", h.requirePermission("oa:workday:delete"), h.ClearWorkday)

	// 考勤管理（issue #60，菜单种子 527/530 段，spec §5/§7.2）：班次与考勤组
	// 分别挂 oa:attendance:shift:manage / oa:attendance:group:manage（码在对应
	// 菜单上，授予菜单即授予该域全部维护动作）。
	shift := oa.Group("/attendance/shift", h.requirePermission("oa:attendance:shift:manage"))
	shift.GET("", h.PageShifts)
	shift.GET("/:id", h.GetShift)
	shift.POST("", h.CreateShift)
	shift.PUT("/:id", h.UpdateShift)
	shift.PUT("/:id/status", h.SetShiftStatus)
	shift.DELETE("/:id", h.DeleteShift)

	group := oa.Group("/attendance/group", h.requirePermission("oa:attendance:group:manage"))
	group.GET("", h.PageGroups)
	group.GET("/:id", h.GetGroup)
	group.POST("", h.CreateGroup)
	group.PUT("/:id", h.UpdateGroup)
	group.PUT("/:id/status", h.SetGroupStatus)
	group.DELETE("/:id", h.DeleteGroup)
	// 组员：按用户批量添加、按部门批量导入（取当前成员物化，后续不自动同步）、
	// 移出单个组员；详情接口已携带组员列表。
	group.POST("/:id/members", h.AddGroupMembers)
	group.POST("/:id/members/by-dept", h.AddGroupMembersByDept)
	group.DELETE("/:id/members/:userId", h.RemoveGroupMember)

	// 员工打卡（issue #60，菜单种子 531 我的考勤）：以登录身份收口（无权限码
	// 门槛，spec §4.10）；无启用考勤组打卡 400；同日多次打卡均保留。
	// 统一入口 POST /attendance/punch（body.type 0/1，spec §5）与 /punch/in、
	// /punch/out 两个动作路径等价。
	punch := oa.Group("/attendance/punch")
	punch.POST("", h.Punch)
	punch.POST("/in", h.PunchIn)
	punch.POST("/out", h.PunchOut)
	punch.GET("/today", h.GetMyPunchToday)
	punch.GET("/page", h.PageMyPunches)
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
