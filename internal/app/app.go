package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	bpmapi "github.com/go-quick-code/go-admin-pro/internal/module/bpm/api"
	bpmrepo "github.com/go-quick-code/go-admin-pro/internal/module/bpm/repository"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
	crmapi "github.com/go-quick-code/go-admin-pro/internal/module/crm/api"
	crmrepo "github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	crmservice "github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
	infraapi "github.com/go-quick-code/go-admin-pro/internal/module/infra/api"
	infrarepo "github.com/go-quick-code/go-admin-pro/internal/module/infra/repository"
	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	messageapi "github.com/go-quick-code/go-admin-pro/internal/module/message/api"
	messagerepo "github.com/go-quick-code/go-admin-pro/internal/module/message/repository"
	messageservice "github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	notifyapi "github.com/go-quick-code/go-admin-pro/internal/module/notify/api"
	notifyrepo "github.com/go-quick-code/go-admin-pro/internal/module/notify/repository"
	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	oaapi "github.com/go-quick-code/go-admin-pro/internal/module/oa/api"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	systemapi "github.com/go-quick-code/go-admin-pro/internal/module/system/api"
	systemrepo "github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/router"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"gorm.io/gorm"
)

// App 持有应用运行期依赖。
type App struct {
	Config    *config.Config
	DB        *gorm.DB
	Engine    *gin.Engine
	Scheduler *infraservice.JobScheduler
	// BPM 是 bpm 模块的 Service（issue #44）：BusinessHandler 注册接缝——
	// spec §4.4 要求业务回调由 internal/app 装配时注册（#47 起合同审批在此
	// 接线）；集成测试经该字段注册测试替身。
	BPM *bpmservice.Service
	// CRM 是 crm 模块的 Service（issue #47）：存量合同审批 backfill 命令与
	// 集成测试经该字段调用业务服务。
	CRM *crmservice.Service
	// Message 是 message 模块的 Service（issue #72）：集成测试经该字段注册
	// provider 替身（成功/失败）；业务侧只经 HTTP 端点消费。
	Message *messageservice.Service
}

// New 初始化数据库、模块与 HTTP 引擎。
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	db, err := database.Open(cfg.Database, log)
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	verifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.VerifyMigrationVersion(verifyCtx, sqlDB); err != nil {
		return nil, fmt.Errorf("verify migration version: %w", err)
	}

	if cfg.Tenant.Enabled {
		if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
			return nil, fmt.Errorf("install tenant plugin: %w", err)
		}
	}

	if err := db.Use(database.NewAuditPlugin()); err != nil {
		return nil, fmt.Errorf("install audit plugin: %w", err)
	}

	if cfg.Database.Seed {
		// OA 演示数据的文档文件需要落到实际存储目录（默认 ./uploads）；
		// seed 包不读配置，这里显式传入本地存储根路径。
		seed.SetDemoStorageBasePath(cfg.Storage.Local.BasePath)
		if err := seed.Run(db, cfg.SeedDemoEnabled()); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}

	jwtManager := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.Expire, cfg.JWT.Issuer)

	sysRepo := systemrepo.New(db)
	sysSvc := systemservice.New(sysRepo, jwtManager, cfg)

	infraRepo := infrarepo.New(db)
	// 定时任务按应用时区解释 cron 表达式（spec §4.10：每日 01:00 应用时区）。
	scheduler := infraservice.NewJobScheduler(infraRepo, cfg.Location())
	infraSvc := infraservice.New(infraRepo, cfg, scheduler)

	// 注册日志清理任务，可在“定时任务”中把 handlerName 配置为 cleanLogs。
	// 清理跨租户执行（tenant.Skip，见 system repository），属系统级处理器：
	// 只有平台租户管理员可创建/修改/启用/运行（PR #39 审查 P0）。
	scheduler.RegisterSystem("cleanLogs", func(ctx context.Context, param string) error {
		days, err := strconv.Atoi(strings.TrimSpace(param))
		if err != nil || days <= 0 {
			days = 30
		}
		return sysSvc.CleanLogsBefore(ctx, days)
	})

	sysAPI := systemapi.New(sysSvc, cfg)

	// CRM 模块：数据范围解析复用 system Service（datascope 引擎宿主）。
	crmRepo := crmrepo.New(db)
	crmSvc := crmservice.New(crmRepo, sysSvc)
	// 业务时区（ADR 0006）：仪表盘按月归集与「今天」判定统一走 app.timezone，
	// 不依赖服务器 TZ。
	crmSvc.SetLocation(cfg.Location())
	// 产品删除引用校验（issue #11 扩展点，#15/#16 各自提供行项目侧实现）：
	// 商机与合同任一行项目引用的产品都禁删。RefCheckerChain 依次调用、
	// 首个非 nil 错误即返回（组合说明见 #15/#16 提交注释）。
	crmSvc.SetProductRefCountChecker(crmservice.RefCheckerChain{
		crmservice.NewOpportunityProductRefChecker(crmRepo),
		crmservice.NewContractProductRefChecker(crmRepo),
	})
	// 公海自动回收的合同排除（issue #14 接 #16）：有未作废合同的客户不被回收，
	// 作废合同不拦截。替换 v1 空实现（恒「无合同」，见
	// crmservice.ContractChecker 接口注释与 pool_recycle.go）。
	crmSvc.SetContractChecker(crmservice.NewContractChecker(crmRepo))
	// 作废/删除合同的回款拦截（issue #18 接入 #16 预留守卫）：
	// 合同存在未删除回款记录时禁止作废/删除（spec §4.5），替换 v1 空实现
	//（接缝见 crmservice.VoidContractGuard 接口注释）。
	crmSvc.SetVoidContractGuard(crmservice.NewReceivableVoidGuard(crmRepo))
	// 合同附件（issue #17）：crm 只依赖 ContractFileStore 接口，这里装配 infra 的
	// 通用业务文件能力（infra_file 按 biz_type/biz_id 归属，见 contract_file_store.go）。
	crmSvc.SetContractFileStore(contractFileStore{infra: infraSvc})
	// 附件 URL 走鉴权下载端点（PR #39 审查 P2）：拼装地址需要 HTTP API 前缀。
	crmSvc.SetAPIPrefix(cfg.App.APIPrefix)

	// 注册公海自动回收任务（issue #14）：handlerName 为 recyclePoolCustomers，
	// 种子默认行已按暂停状态写入（启用前请确认各租户公海配置，见 internal/seed
	// 的 seedJobs 注释）。回收是系统级动作：handler 忽略 job 所属租户上下文，
	// 由 crm service 以 tenant.Skip 列出启用租户后逐租户执行。
	//
	// 系统级注册（PR #39 审查 P0）：普通租户管理员即使持 super_admin（"*"）
	// 也不能创建/运行该 handler，否则可经 infra_job 触发跨租户公海回收。
	scheduler.RegisterSystem(crmservice.RecycleJobHandlerName, func(ctx context.Context, param string) error {
		result, err := crmSvc.RunPoolRecycle(ctx)
		if err != nil {
			return err
		}
		slog.Info("pool auto-recycle job finished", "tenants", result.Tenants, "recycled", result.Recycled)
		return nil
	})

	// Excel 导入的日期解析统一使用应用业务时区（ADR 0006：只经 cfg.Location() 获取）。
	crmAPI := crmapi.New(crmSvc, sysAPI.RequirePermission, cfg.Location())

	// notify 模块（issue #45）：平台级站内通知投递/读取基础设施（spec §3.2/§4.13）。
	// 无业务依赖、不反向依赖业务模块；bpm/公告/会议室等生产者经 notifySvc 的
	// CreateInTx seam 把通知写进各自的事务（同生共死），读取侧走 /notify/message/*。
	notifySvc := notifyservice.New(notifyrepo.New(db))
	notifyAPI := notifyapi.New(notifySvc, sysAPI.RequirePermission)

	// message 模块（issue #72，spec §4.3 / ADR 0010）：渠道/模板是平台级配置，
	// 发送日志走租户插件；provider 注册表在 New 内先注册内置 log provider，
	// smtp（#73）/aliyun_sms（#74）实现后在此追加 RegisterSender。
	messageSvc := messageservice.New(messagerepo.New(db))
	messageAPI := messageapi.New(messageSvc, sysAPI.RequirePermission)

	// OA 模块（issue #43 骨架 + #50 工作日历起引入实体表仓储）：可见范围
	// 判定复用 system 的权限/部门解析（oa/service.SystemGateway 直接受
	// *systemservice.Service 满足）；公告发布把 notifySvc 作为 NotifyGateway
	// 注入（issue #48：通知与发布同事务，spec §4.13）；附件复用 infra_file
	// （biz_type=oa_announcement，经 AnnouncementFileStore 适配器装配）。
	oaSvc := oaservice.New(sysSvc, repository.New(db), notifySvc)
	// 请假联动（issue #62）：考勤结果派生经 LeaveDaysProvider 读已通过请假
	//（整天 → 请假状态、半天 → leave_days=0.5 不改状态，spec §4.10）。
	oaSvc.SetLeaveDaysProvider(oaservice.NewLeaveDaysProvider(repository.New(db)))
	// 业务时区（ADR 0006）：考勤打卡日期（issue #60）与请假取消窗口
	// （issue #53）都按 app.timezone 取「今天」。
	oaSvc.SetLocation(cfg.Location())
	// 附件适配器（issue #64 review 去重）：公告/请假/报销共用 bizFileStore，
	// 文档文件用固定归属键（oa_document + 目录 ID）的薄封装。
	fileStore := bizFileStore{infra: infraSvc}
	oaSvc.SetAnnouncementFileStore(fileStore)
	// 文档文件（issue #59）：归属行 oa_document_file + 文件本体 infra_file
	//（biz_type=oa_document、biz_id=目录 ID）；下载经 OA 鉴权端点，不走公开直链。
	oaSvc.SetDocumentFileStore(newDocumentFileStore(infraSvc))
	// 通用 infra 文件端点（下载/删除/分页）的业务授权（PR #66 安全审计
	// finding 1）：infra 只保留注册表，OA 四类附件在此按 biz_type 接线；
	// 未注册的业务类型（如 CRM 合同附件）在通用端点上一律 404（fail-closed）。
	registerOAFileAuthorizers(infraSvc, oaSvc)
	oaAPI := oaapi.New(oaSvc, sysAPI.RequirePermission)

	// 字典项引用检查（issue #64 review，spec §4.14）：system 只提供按
	// dictType 的扩展点，OA 在此登记报销类别的引用计数（oa_expense_item
	// 明细行）。被引用的字典项删除返回 409，提示改为停用；检查用租户插件
	// 过滤当前租户，不会把别家租户的引用算进来。
	sysSvc.RegisterDictUsageChecker(oaservice.ExpenseCategoryDictType, func(ctx context.Context, code string) (int64, error) {
		return repository.New(db).CountExpenseItemsByCategory(ctx, code)
	})

	// bpm 模块（issue #44 定义管理，#46 运行引擎）：BusinessHandler 注册表在
	// 装配期建立；spec §4.4：未注册 business_type 的定义不允许发布/发起。
	// #52 在此接线请假回调（oa_leave_approval），#47（CRM 合同）/#55（报销）
	// 后续按同一接缝注册。通知与审批同事务：notifySvc 作为 NotifyGateway 注入。
	bpmSvc := bpmservice.New(bpmrepo.New(db), bpmSystemGateway{sys: sysSvc}, notifySvc)
	bpmAPI := bpmapi.New(bpmSvc, sysAPI.RequirePermission)

	// 合同审批（issue #47，spec §4.5）：CRM → bpm 单向依赖——提交在合同事务内
	// 经 StartInstanceInTx 发起 crm_contract_approval，bpm 在通过/驳回/撤回的
	// 同一事务回调 crmSvc（OnApproved/OnRejected/OnWithdrawn）。合同状态端点
	// 只保留提交/作废，审批统一在待办中心完成。
	crmSvc.SetBPM(bpmSvc)
	bpmSvc.RegisterBusinessHandler(crmservice.ContractApprovalBusinessType, crmSvc)

	// 请假申请（issue #52/#53）：OA → bpm 单向依赖——提交在业务事务内经
	// StartInstanceInTx 发起审批，bpm 在通过/驳回/撤回的同一事务回调 oaSvc
	// （OnApproved/OnRejected/OnWithdrawn）。撤回复用 bpm 撤回入口；
	// 取消是业务终态（不动 bpm 实例，事务内返还 used）。
	// 附件复用 infra_file（biz_type=oa_leave）。
	oaSvc.SetBPM(bpmSvc)
	oaSvc.SetAPIPrefix(cfg.App.APIPrefix)
	oaSvc.SetLeaveFileStore(fileStore)
	// 报销单（issue #54/#55）：草稿/明细闭环（#54）与提交/审批回调（#55）共用
	// 同一状态机。提交在业务事务内经 StartInstanceInTx 发起 oa_expense_approval；
	// 报销用独立适配器注册回调（oa.Service 已注册为请假审批处理器，一个对象
	// 不能绑定两套回调语义）。附件复用 infra_file（biz_type=oa_expense）。
	oaSvc.SetExpenseFileStore(fileStore)
	bpmSvc.RegisterBusinessHandler(oaservice.LeaveApprovalBusinessType, oaSvc)
	bpmSvc.RegisterBusinessHandler(oaservice.ExpenseApprovalBusinessType, oaservice.NewExpenseApprovalHandler(oaSvc))

	// 考勤日结果任务（issue #61，spec §4.10）：系统级 handler（跨租户迭代，
	// tenant.Skip 列启用租户后逐租户派生），infra_job 种子默认行每天 01:00。
	// handler_param 可填 YYYY-MM-DD 回填指定日期；留空取业务时区前一日。
	scheduler.RegisterSystem(oaservice.AttendanceDailyResultHandlerName, func(ctx context.Context, param string) error {
		summary, err := oaSvc.RunDailyAttendanceResult(ctx, param)
		if err != nil {
			return err
		}
		slog.Info("attendance daily result job finished",
			"date", summary.Date, "tenants", summary.Tenants, "users", summary.Users)
		return nil
	})

	// 会议提醒任务（issue #57，spec §4.11）：系统级 handler（跨租户迭代），
	// infra_job 种子默认行每 5 分钟一次。扫描 (now, now+15m] 内开始的有效
	// 未提醒预约，条件更新 reminded_at 认领后向预约人写 meeting_reminder
	// 通知（只发一次）。handler_param 可填 YYYY-MM-DD HH:mm:ss 固定「现在」
	// （测试/回放）；留空取业务时区当前时间。
	scheduler.RegisterSystem(oaservice.RoomBookingReminderHandlerName, func(ctx context.Context, param string) error {
		summary, err := oaSvc.RunRoomBookingReminders(ctx, param)
		if err != nil {
			return err
		}
		slog.Info("room booking reminder job finished",
			"now", summary.Now, "tenants", summary.Tenants, "sent", summary.Sent)
		return nil
	})

	infraAPI := infraapi.New(infraSvc, cfg, sysAPI.RequirePermission)
	codegenAPI := codegen.NewHandler(codegen.New(db), sysAPI.RequirePermission)

	engine := router.New(cfg, sysAPI, infraAPI, codegenAPI, crmAPI, oaAPI, bpmAPI, notifyAPI, messageAPI)

	scheduler.Start(context.Background())

	return &App{Config: cfg, DB: db, Engine: engine, Scheduler: scheduler, BPM: bpmSvc, CRM: crmSvc, Message: messageSvc}, nil
}

// Close 释放资源。
func (a *App) Close() error {
	a.Scheduler.Stop()
	sqlDB, err := a.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
