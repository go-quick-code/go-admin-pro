package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 公告权限码（菜单种子 521 段：521 查询、5211 创建、5212 编辑、5213 删除、
// 5214 发布/撤回、5215 已读统计（#49 使用）；issue #48）。
const (
	announcementQuery   = "oa:announcement:query"
	announcementCreate  = "oa:announcement:create"
	announcementUpdate  = "oa:announcement:update"
	announcementDelete  = "oa:announcement:delete"
	announcementPublish = "oa:announcement:publish"
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

	// 请假类型与额度（issue #51，菜单种子 522 段）：类型列表与本人余额
	// 登录即可（员工发起请假/查看余额的前置）；类型与额度的全部管理动作
	// 挂 oa:leave:manage（默认审批流节点 2 的 HR/管理员码，spec §7.3）。
	leave := oa.Group("/leave")
	leave.GET("/type/list", h.ListLeaveTypes)
	leave.POST("/type", h.requirePermission(manageLeave), h.CreateLeaveType)
	leave.PUT("/type", h.requirePermission(manageLeave), h.UpdateLeaveType)
	leave.PUT("/type/:id/status", h.requirePermission(manageLeave), h.SetLeaveTypeStatus)
	leave.DELETE("/type/:id", h.requirePermission(manageLeave), h.DeleteLeaveType)
	balance := leave.Group("/balance")
	balance.GET("/page", h.requirePermission(manageLeave), h.PageBalances)
	balance.GET("/mine", h.MyBalances)
	balance.POST("/init", h.requirePermission(manageLeave), h.InitBalances)
	balance.POST("/adjust", h.requirePermission(manageLeave), h.AdjustBalance)

	// 公告（issue #48，菜单种子 521 段）：管理列表/草稿预览挂
	// oa:announcement:query；创建/编辑/删除/发布/撤回逐码；员工侧「我的公告」
	// 与已发布详情登录即可，可见性由受众判定收口（spec §4.7）。
	// 静态路由（/page、/my/page）先于 /:id 注册。
	announcement := oa.Group("/announcement")
	announcement.GET("/page", h.requirePermission(announcementQuery), h.PageAnnouncements)
	announcement.GET("/my/page", h.MyAnnouncements)
	announcement.GET("/:id", h.GetAnnouncement)
	announcement.POST("", h.requirePermission(announcementCreate), h.CreateAnnouncement)
	announcement.PUT("", h.requirePermission(announcementUpdate), h.UpdateAnnouncement)
	announcement.DELETE("/:id", h.requirePermission(announcementDelete), h.DeleteAnnouncement)
	announcement.POST("/:id/publish", h.requirePermission(announcementPublish), h.PublishAnnouncement)
	announcement.POST("/:id/withdraw", h.requirePermission(announcementPublish), h.WithdrawAnnouncement)
	// 附件沿用 infra_file（biz_type=oa_announcement）：查看跟随公告可见性，
	// 上传/删除挂 oa:announcement:update 且仅草稿（service 层校验状态）。
	announcement.GET("/:id/files", h.ListAnnouncementFiles)
	announcement.GET("/:id/files/:fileId/download", h.DownloadAnnouncementFile)
	announcement.POST("/:id/files", h.requirePermission(announcementUpdate), h.UploadAnnouncementFile)
	announcement.DELETE("/:id/files/:fileId", h.requirePermission(announcementUpdate), h.DeleteAnnouncementFile)
	// 请假申请（issue #52，菜单种子 529 段）：本人列表/详情/附件查看登录即可
	//（服务层按本人收口，非本人 404）；写动作逐码——创建/编辑/删除/提交，
	// 附件上传与删除跟随编辑码（oa:leave:update）。审批走通用待办
	//（/bpm/task/*），本组不提供审批接口（spec §5：审批人的动作由 bpm 收口）。
	request := leave.Group("/request")
	request.GET("/page", h.PageMyLeaveRequests)
	request.GET("/:id", h.GetMyLeaveRequest)
	request.POST("", h.requirePermission("oa:leave:create"), h.CreateLeaveRequest)
	request.PUT("", h.requirePermission("oa:leave:update"), h.UpdateLeaveRequest)
	request.DELETE("/:id", h.requirePermission("oa:leave:delete"), h.DeleteLeaveRequest)
	request.POST("/:id/submit", h.requirePermission("oa:leave:submit"), h.SubmitLeaveRequest)
	request.GET("/:id/files", h.ListLeaveRequestFiles)
	request.GET("/:id/files/:fileId/download", h.DownloadLeaveRequestFile)
	request.POST("/:id/files", h.requirePermission("oa:leave:update"), h.UploadLeaveRequestFile)
	request.DELETE("/:id/files/:fileId", h.requirePermission("oa:leave:update"), h.DeleteLeaveRequestFile)
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
