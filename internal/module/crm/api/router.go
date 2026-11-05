package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// Handler 是 crm 模块的 HTTP 入口。
// requirePermission 复用 system 模块的权限中间件（codegen 同款装配方式），
// 权限码与菜单种子保持一致（crm:customer:*）。
// location 是应用业务时区（ADR 0006），CRM Excel 导入的日期解析统一使用它。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
	location          *time.Location
}

// New 创建 crm Handler。location 为 nil 时由 excel helper 回退到 time.Local
// （生产装配固定传 config.Location()）。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc, location *time.Location) *Handler {
	if location == nil {
		location = time.Local
	}
	return &Handler{svc: svc, requirePermission: requirePermission, location: location}
}

// RegisterProtected 注册需要登录的 CRM 接口，rg 上应已挂载 Auth 中间件。
// 统一前缀 /crm（docs/crm-mvp-spec.md §5：/admin-api/crm）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	customer := rg.Group("/crm/customer")
	customer.GET("/page", h.requirePermission("crm:customer:query"), h.PageCustomers)
	customer.GET("/pool/page", h.requirePermission("crm:customer:pool:view"), h.PagePoolCustomers)
	// Excel（issue #20）：导入/模板挂 crm:customer:import，导出挂 crm:customer:export；
	// 静态路由必须注册在 /:id 之前，否则 "import-template" 会被当成客户 ID。
	customer.GET("/import-template", h.requirePermission("crm:customer:import"), h.CustomerImportTemplate)
	customer.POST("/import", h.requirePermission("crm:customer:import"), h.ImportCustomers)
	customer.GET("/export-excel", h.requirePermission("crm:customer:export"), h.ExportCustomers)
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

	contact := rg.Group("/crm/contact")
	contact.GET("/page", h.requirePermission("crm:contact:query"), h.PageContacts)
	// Excel 导入（issue #20）：导入/模板挂 crm:contact:import，静态路由先于 /:id。
	contact.GET("/import-template", h.requirePermission("crm:contact:import"), h.ContactImportTemplate)
	contact.POST("/import", h.requirePermission("crm:contact:import"), h.ImportContacts)
	contact.GET("/:id", h.requirePermission("crm:contact:query"), h.GetContact)
	contact.POST("", h.requirePermission("crm:contact:create"), h.CreateContact)
	contact.PUT("", h.requirePermission("crm:contact:update"), h.UpdateContact)
	contact.DELETE("/:id", h.requirePermission("crm:contact:delete"), h.DeleteContact)

	// 跟进（issue #12）：查询/新增挂按钮码；修改/删除在 service 内做
	// 「创建人本人 或 权限码」组合校验（同释放/转移的装配方式），路由不挂中间件。
	followUp := rg.Group("/crm/follow-up")
	followUp.GET("/page", h.requirePermission("crm:follow-up:query"), h.PageFollowUps)
	followUp.GET("/:id", h.requirePermission("crm:follow-up:query"), h.GetFollowUp)
	followUp.POST("", h.requirePermission("crm:follow-up:create"), h.CreateFollowUp)
	followUp.PUT("", h.UpdateFollowUp)
	followUp.DELETE("/:id", h.DeleteFollowUp)

	// 产品分类与产品（issue #11）：租户级共享资源，普通 CRUD + 权限码，
	// 无 owner 列与数据范围谓词（codegen ownerScoped=false 变体）。
	h.RegisterProductCategoryRoutes(rg)
	h.RegisterProductRoutes(rg)

	// 商机（issue #15）：owner-scoped 正体，CRUD + 阶段推进。创建时可指定
	// 1–4；此后阶段变更只走 PUT /stage（挂独立按钮码 crm:opportunity:stage），
	// 进入 5/6 终态后 service 层拒绝变更。
	opportunity := rg.Group("/crm/opportunity")
	opportunity.GET("/page", h.requirePermission("crm:opportunity:query"), h.PageOpportunities)
	opportunity.GET("/:id", h.requirePermission("crm:opportunity:query"), h.GetOpportunity)
	opportunity.POST("", h.requirePermission("crm:opportunity:create"), h.CreateOpportunity)
	opportunity.PUT("", h.requirePermission("crm:opportunity:update"), h.UpdateOpportunity)
	opportunity.PUT("/stage", h.requirePermission("crm:opportunity:stage"), h.AdvanceOpportunityStage)
	opportunity.DELETE("/:id", h.requirePermission("crm:opportunity:delete"), h.DeleteOpportunity)

	// 合同（issue #16）：owner-scoped 正体，CRUD + 状态流转（唯一状态入口，
	// 路由挂 crm:contract:update——审批通过与驳回在 ApprovalService 内追加
	// 校验 crm:contract:approve，无码 403；作废走 VoidContractGuard 守卫）。
	contract := rg.Group("/crm/contract")
	contract.GET("/page", h.requirePermission("crm:contract:query"), h.PageContracts)
	// Excel 导出（issue #20）：挂 crm:contract:export，静态路由先于 /:id。
	contract.GET("/export-excel", h.requirePermission("crm:contract:export"), h.ExportContracts)
	contract.GET("/:id", h.requirePermission("crm:contract:query"), h.GetContract)
	contract.POST("", h.requirePermission("crm:contract:create"), h.CreateContract)
	contract.PUT("", h.requirePermission("crm:contract:update"), h.UpdateContract)
	contract.PUT("/status", h.requirePermission("crm:contract:update"), h.ChangeContractStatus)
	contract.DELETE("/:id", h.requirePermission("crm:contract:delete"), h.DeleteContract)

	// 合同附件（issue #17）：复用 infra_file（biz_type=crm_contract），
	// 权限跟随合同——查看挂 crm:contract:query，上传/删除挂 crm:contract:update，
	// 不要求 infra:file:* 权限码。
	contract.GET("/:id/files", h.requirePermission("crm:contract:query"), h.ListContractFiles)
	contract.POST("/:id/files", h.requirePermission("crm:contract:update"), h.UploadContractFile)
	contract.DELETE("/:id/files/:fileId", h.requirePermission("crm:contract:update"), h.DeleteContractFile)

	// 公海租户配置（issue #14）：读复用公海可见性权限码（能看公海即可读），
	// 写挂 crm:pool-config:update（菜单种子 4050）。
	poolConfig := rg.Group("/crm/pool-config")
	poolConfig.GET("", h.requirePermission("crm:customer:pool:view"), h.GetPoolConfig)
	poolConfig.PUT("", h.requirePermission("crm:pool-config:update"), h.UpdatePoolConfig)

	// 回款计划 + 回款记录（issue #18）：可见性 join 合同套用合同 owner 谓词；
	// 金额不变量（计划 ≤ 合同、记录 ≤ 计划）、派生状态与删除约束在 service 层；
	// 记录编号 HK+yyyyMMdd+4 位序号（crm_sequence 同款）。
	receivable := rg.Group("/crm/receivable")
	receivable.GET("/plan/page", h.requirePermission("crm:receivable:query"), h.PageReceivablePlans)
	// Excel 导出（issue #20）：挂 crm:receivable:export。
	receivable.GET("/plan/export-excel", h.requirePermission("crm:receivable:export"), h.ExportReceivablePlans)
	receivable.POST("/plan", h.requirePermission("crm:receivable:create"), h.CreateReceivablePlan)
	receivable.PUT("/plan", h.requirePermission("crm:receivable:update"), h.UpdateReceivablePlan)
	receivable.DELETE("/plan/:id", h.requirePermission("crm:receivable:delete"), h.DeleteReceivablePlan)
	receivable.GET("/record/page", h.requirePermission("crm:receivable:query"), h.PageReceivableRecords)
	receivable.POST("/record", h.requirePermission("crm:receivable:create"), h.CreateReceivableRecord)
	receivable.DELETE("/record/:id", h.requirePermission("crm:receivable:delete"), h.DeleteReceivableRecord)

	// 仪表盘（issue #19）：只读聚合，一次返回 4 卡片 + 阶段漏斗 + 近 12 个月趋势。
	// 权限码 crm:dashboard:view 与菜单种子 4140/4141 一致（spec §5/§6）。
	dashboard := rg.Group("/crm/dashboard")
	dashboard.GET("/summary", h.requirePermission("crm:dashboard:view"), h.DashboardSummary)
}
