package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 公告权限码（菜单种子 521 段：521 查询、5211 创建、5212 编辑、5213 删除、
// 5214 发布/撤回、5215 已读统计（#49 使用）；issue #48/#49）。
const (
	announcementQuery     = "oa:announcement:query"
	announcementCreate    = "oa:announcement:create"
	announcementUpdate    = "oa:announcement:update"
	announcementDelete    = "oa:announcement:delete"
	announcementPublish   = "oa:announcement:publish"
	announcementReadStats = "oa:announcement:read-stats"
)

// 考勤结果权限码（菜单种子 533 段；issue #61）：
// 533 查询、5331 校正/撤销校正/手工重算。可见范围的「全租户」判定复用
// #43 的 oa:attendance:manage（按钮 5332 承载，供 HR/管理员授权）。
const (
	attendanceResultQuery   = "oa:attendance:result:query"
	attendanceResultCorrect = "oa:attendance:result:correct"
)

// 考勤月度汇总权限码（issue #62，菜单种子 534）：查询挂菜单本身
// （oa:attendance:summary:query）；全租户可见复用 oa:attendance:manage（5332）。
const attendanceSummaryQuery = "oa:attendance:summary:query"

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
	// 可见范围探测（issue #43 交付的 helper 调试面，issue #64 review 保留）：
	// 返回当前用户在某类 OA 业务记录上的可见范围（spec §4.6：manage → 全租户；
	// 部门负责人 → 子树用户 ∪ 本人；否则仅本人）。任何登录用户都可探知自己的
	// 可见范围（与 system 仪表盘/部门树同级的登录即可用接口）；resource 未登记
	// 返回 400。集成测试用它在各工单里断言可见范围口径，不承担业务数据读写，
	// 因此不挂权限码、也不删除。
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

	// 考勤结果与校正（issue #61，菜单种子 533 段，spec §4.10/§5）：管理端
	// 结果查询挂 oa:attendance:result:query；校正/撤销/手工重算挂
	// oa:attendance:result:correct；员工「我的考勤」结果列表登录即可（强制本人）。
	// 可见范围由 service 经 VisibilityScopeOf("attendance") 收口：
	// oa:attendance:manage → 全租户、部门负责人 → 子树、否则本人。
	result := oa.Group("/attendance/result")
	result.GET("/page", h.requirePermission(attendanceResultQuery), h.PageAttendanceResults)
	result.GET("/mine", h.PageMyAttendanceResults)
	result.POST("/recompute", h.requirePermission(attendanceResultCorrect), h.RecomputeAttendanceResults)
	result.PUT("/:id/correct", h.requirePermission(attendanceResultCorrect), h.CorrectAttendanceResult)
	result.DELETE("/:id/correct", h.requirePermission(attendanceResultCorrect), h.ClearAttendanceResultCorrection)

	// 考勤月度汇总（issue #62，菜单种子 534 段，spec §4.10）：管理端分页/单
	// 用户查询挂 oa:attendance:summary:query；员工「我的考勤」本月汇总卡片
	// 登录即可（强制本人）。可见范围与结果列表同源（VisibilityScopeOf）。
	summary := oa.Group("/attendance/summary")
	summary.GET("/page", h.requirePermission(attendanceSummaryQuery), h.PageAttendanceSummaries)
	summary.GET("/user", h.requirePermission(attendanceSummaryQuery), h.GetAttendanceSummaryOfUser)
	summary.GET("/mine", h.GetMyAttendanceSummary)

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
	// 已读统计（issue #49）：人数 + 分页名单，挂按钮码 5215；撤回保留历史
	// 已读、重新发布不重置（service 只读 oa_announcement_read）。
	announcement.GET("/:id/read-stats", h.requirePermission(announcementReadStats), h.GetAnnouncementReadStats)
	// 附件沿用 infra_file（biz_type=oa_announcement）：查看跟随公告可见性，
	// 上传/删除挂 oa:announcement:update 且仅草稿（service 层校验状态）。
	announcement.GET("/:id/files", h.ListAnnouncementFiles)
	announcement.GET("/:id/files/:fileId/download", h.DownloadAnnouncementFile)
	announcement.POST("/:id/files", h.requirePermission(announcementUpdate), h.UploadAnnouncementFile)
	announcement.DELETE("/:id/files/:fileId", h.requirePermission(announcementUpdate), h.DeleteAnnouncementFile)

	// 请假申请（issue #52/#53，菜单种子 529 段；查询码 5297 为 issue #64
	// review 补齐）：列表/详情挂 oa:leave:query（spec §5），服务层再按 §4.6
	// 可见范围收口（manage → 全租户、部门负责人 → 子树、否则本人；范围外/
	// 跨租户 404）；附件查看登录即可。写动作逐码——创建/编辑/删除/提交/
	// 撤回/取消，附件上传与删除跟随编辑码（oa:leave:update）。审批走通用
	// 待办（/bpm/task/*），本组不提供审批接口（spec §5：审批人的动作由 bpm 收口）。
	request := leave.Group("/request")
	request.GET("/page", h.requirePermission("oa:leave:query"), h.PageLeaveRequests)
	request.GET("/:id", h.requirePermission("oa:leave:query"), h.GetLeaveRequestDetail)
	request.POST("", h.requirePermission("oa:leave:create"), h.CreateLeaveRequest)
	request.PUT("", h.requirePermission("oa:leave:update"), h.UpdateLeaveRequest)
	request.DELETE("/:id", h.requirePermission("oa:leave:delete"), h.DeleteLeaveRequest)
	request.POST("/:id/submit", h.requirePermission("oa:leave:submit"), h.SubmitLeaveRequest)
	request.POST("/:id/withdraw", h.requirePermission("oa:leave:withdraw"), h.WithdrawLeaveRequest)
	request.POST("/:id/cancel", h.requirePermission("oa:leave:cancel"), h.CancelLeaveRequest)
	request.GET("/:id/files", h.ListLeaveRequestFiles)
	request.GET("/:id/files/:fileId/download", h.DownloadLeaveRequestFile)
	request.POST("/:id/files", h.requirePermission("oa:leave:update"), h.UploadLeaveRequestFile)
	request.DELETE("/:id/files/:fileId", h.requirePermission("oa:leave:update"), h.DeleteLeaveRequestFile)

	// 报销单（issue #54/#55，菜单种子 523/535 段；查询码 5236 为 issue #64
	// review 补齐）：列表/详情挂 oa:expense:query（spec §5），服务层再按
	// §4.6 可见范围收口（manage → 全租户、部门负责人 → 子树、否则本人；
	// 范围外/跨租户 404）；附件查看登录即可。写动作逐码——创建/编辑/删除/
	// 提交，附件上传与删除跟随编辑码（oa:expense:update）。审批动作走通用
	// 待办（/bpm/task/*、/bpm/instance/:id/withdraw），本组不提供审批接口。
	claim := oa.Group("/expense/claim")
	claim.GET("/page", h.requirePermission("oa:expense:query"), h.PageExpenseClaims)
	claim.GET("/:id", h.requirePermission("oa:expense:query"), h.GetExpenseClaimDetail)
	claim.POST("", h.requirePermission("oa:expense:create"), h.CreateExpenseClaim)
	claim.PUT("", h.requirePermission("oa:expense:update"), h.UpdateExpenseClaim)
	claim.DELETE("/:id", h.requirePermission("oa:expense:delete"), h.DeleteExpenseClaim)
	claim.POST("/:id/submit", h.requirePermission("oa:expense:submit"), h.SubmitExpenseClaim)
	claim.GET("/:id/files", h.ListExpenseClaimFiles)
	claim.GET("/:id/files/:fileId/download", h.DownloadExpenseClaimFile)
	claim.POST("/:id/files", h.requirePermission("oa:expense:update"), h.UploadExpenseClaimFile)
	claim.DELETE("/:id/files/:fileId", h.requirePermission("oa:expense:update"), h.DeleteExpenseClaimFile)

	// 日程（issue #56，菜单种子 524 段，spec §4.11/§5）：列表/详情挂
	// oa:calendar:query（可见性由服务层三态谓词收口：私有仅创建人、部门按
	// 创建时部门快照及子树、公开全租户；范围外/跨租户/已取消 404）；
	// 创建/编辑/取消逐码，取消为软取消（status=1，不再展示）。公开日程的
	// 创建/编辑另需 oa:calendar:public（service 校验，缺失 403）。静态路由
	// /list 先于 /:id 注册。
	event := oa.Group("/calendar/event")
	event.GET("/list", h.requirePermission(calendarQuery), h.ListCalendarEvents)
	event.GET("/:id", h.requirePermission(calendarQuery), h.GetCalendarEvent)
	event.POST("", h.requirePermission("oa:calendar:create"), h.CreateCalendarEvent)
	event.PUT("", h.requirePermission("oa:calendar:update"), h.UpdateCalendarEvent)
	event.DELETE("/:id", h.requirePermission("oa:calendar:delete"), h.CancelCalendarEvent)
	event.POST("/:id/cancel", h.requirePermission("oa:calendar:delete"), h.CancelCalendarEvent)

	// 会议室与预约（issue #57，菜单种子 525/538/539 段，spec §4.11/§4.14/§5）：
	// 会议室查询/详情与预约列表挂 oa:room:query；会议室 CRUD/启停/删除挂
	// oa:room:manage（有未来有效预约时删除 409，只能停用）；创建预约挂
	// oa:room:booking；取消预约登录即可（服务层校验预约人本人或
	// oa:room:manage，并同步软取消生成的日程）。员工侧预约列表复用同一接口：
	// 无 oa:room:manage 的查看者强制只看本人。静态路由（/list、/booking/list）
	// 先于 /:id 注册。
	room := oa.Group("/room")
	room.GET("/list", h.requirePermission(roomQuery), h.ListEnabledMeetingRooms)
	room.GET("/page", h.requirePermission(roomQuery), h.PageMeetingRooms)
	booking := room.Group("/booking")
	booking.GET("/list", h.requirePermission(roomQuery), h.PageRoomBookings)
	booking.GET("/:id", h.requirePermission(roomQuery), h.GetRoomBooking)
	booking.POST("", h.requirePermission(roomBooking), h.CreateRoomBooking)
	booking.POST("/:id/cancel", h.CancelRoomBooking)
	room.GET("/:id", h.requirePermission(roomQuery), h.GetMeetingRoom)
	room.POST("", h.requirePermission(roomManage), h.CreateMeetingRoom)
	room.PUT("/:id", h.requirePermission(roomManage), h.UpdateMeetingRoom)
	room.PUT("/:id/status", h.requirePermission(roomManage), h.SetMeetingRoomStatus)
	room.DELETE("/:id", h.requirePermission(roomManage), h.DeleteMeetingRoom)

	// 文档目录与 ACL（issue #58，菜单种子 526 段 / 员工侧 537，spec §4.12/§5）：
	// 目录树/详情挂 oa:document:query（不可见目录不出现在树里，详情 404）；
	// 创建挂 oa:document:create（5261）。改名/删除/ACL 设置只要求「目录管理权」
	//（owner 或 oa:document:manage），因此不在路由上挂独立权限码——owner 可能
	// 没有管理按钮码；服务层区分 403（可见无管理权）/404（不可见）。
	// 文件（issue #59，spec §5）：分页/下载挂查询码，服务层再按文件所属目录
	// 的读权限收口（不可读 404，文件 ID 不能绕过 ACL）；上传/删除只要求目录
	// 管理权，与目录写动作一致，不额外挂按钮码（5263 oa:document:upload
	// 保留给前端按钮展示）。
	document := oa.Group("/document")
	folder := document.Group("/folder")
	folder.GET("/tree", h.requirePermission(documentQuery), h.ListDocumentFolderTree)
	folder.GET("/:id", h.requirePermission(documentQuery), h.GetDocumentFolder)
	folder.GET("/:id/acl", h.GetDocumentFolderACL)
	folder.POST("", h.requirePermission("oa:document:create"), h.CreateDocumentFolder)
	folder.PUT("", h.UpdateDocumentFolder)
	folder.PUT("/:id/acl", h.SetDocumentFolderACL)
	folder.DELETE("/:id", h.DeleteDocumentFolder)
	file := document.Group("/file")
	file.GET("/page", h.requirePermission(documentQuery), h.PageDocumentFiles)
	file.POST("/upload", h.UploadDocumentFile)
	file.GET("/:id/download", h.requirePermission(documentQuery), h.DownloadDocumentFile)
	file.DELETE("/:id", h.DeleteDocumentFile)
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
