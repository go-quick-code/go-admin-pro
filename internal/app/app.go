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
	erpapi "github.com/go-quick-code/go-admin-pro/internal/module/erp/api"
	erprepo "github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	erpservice "github.com/go-quick-code/go-admin-pro/internal/module/erp/service"
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
	"github.com/go-quick-code/go-admin-pro/internal/pkg/iploc"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
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
	// Social 是 system 模块的社交登录 Service（issue #76）：集成测试经该字段
	// 注册 provider 替身并调整 state TTL；业务侧只经 HTTP 端点消费。
	Social *systemservice.SocialService
	// PushHub 是实时推送 hub（issue #78）：push.enabled=false 时为 nil。
	// 集成测试经 Subscribers 观察断开清理；业务侧只经 HTTP 端点消费。
	PushHub *push.Hub
	// Notify 是 notify 模块的 Service（issue #78）：集成测试经该字段调用
	// Create/CreateInTx——与生产装配的推送发布器是同一实例，保证「创建通知
	// → 在线用户 1 秒内收到」走的是真实链路。
	Notify *notifyservice.Service
	// APILog 是访问日志异步写（issue #80）：集成测试在断言前经 FlushAPILogs
	// 排干队列；Close 时随应用一起排干退出。
	APILog *infraservice.APILogWriter
}

// FlushAPILogs 排干访问日志队列并等待落库（issue #80）：集成测试在断言前
// 调用，保证「请求完成 → 日志可见」的确定性。
func (a *App) FlushAPILogs(ctx context.Context) error {
	if a.APILog == nil {
		return nil
	}
	return a.APILog.Flush(ctx)
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

	// IP 归属地定位（issue #82，spec §4.8.4/§4.8.5）：配置 ip-location.xdb-path
	// 时启用 ip2region xdb 驱动；文件不存在/加载失败只告警一次并退回空实现
	//（定位是展示增强，不允许阻断启动）。登录日志分页经 10000 条 LRU 缓存
	// 解析归属地；未配置时为空实现，归属地为空串，分页不受影响。
	sysSvc.SetIPResolver(iploc.NewCached(iploc.New(log, cfg.IPLocation.XdbPath), iploc.LoginLogCacheSize))

	// 社交登录（issue #76/#77，spec §4.4 / ADR 0011）：与 system 共享仓储/JWT 的
	// 独立服务；#77 注册微信开放平台/钉钉/企微三个 provider 实现（端点可由
	// 客户端配置覆盖，缺省用 provider 内置默认值）。
	socialSvc := systemservice.NewSocialService(sysRepo, jwtManager, cfg)
	socialSvc.RegisterSocialProvider(systemservice.NewWeChatOpenProvider())
	socialSvc.RegisterSocialProvider(systemservice.NewDingTalkProvider())
	socialSvc.RegisterSocialProvider(systemservice.NewWeComProvider())

	// 敏感词（issue #81，spec §4.7）：租户级词库 CRUD/检测与按租户的匹配
	// 缓存；同时经 sensitiveWordChecker 适配器注入 OA 公告与 CRM 跟进
	// 的落库前检查（未注入不检查，集成测试可显式关闭）。
	sensitiveSvc := systemservice.NewSensitiveWordService(sysRepo)

	infraRepo := infrarepo.New(db)
	// 定时任务按应用时区解释 cron 表达式（spec §4.10：每日 01:00 应用时区）。
	scheduler := infraservice.NewJobScheduler(infraRepo, cfg.Location())
	infraSvc := infraservice.New(infraRepo, cfg, scheduler)

	// 租户配额装配（issue #71/#75，spec §4.2 / ADR 0012）：system 解释套餐限额
	// 与用户数，infra 提供存储用量，message 提供短信/邮件月度用量；适配器同时
	// 满足 system 的 QuotaProvider、infra 的 QuotaGateway 与 message 的
	// MessageQuotaGateway 三个窄接口，在此组合注入（见 quota_gateway.go）。
	// messageSvc 建在后面：适配器的 message 侧与 system/message 两个注入点
	// 在 message 装配处补齐。
	quotaAdapter := quotaGateway{sys: sysSvc, infra: infraSvc}
	infraSvc.SetQuotaGateway(quotaAdapter)

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

	// API 访问日志（issue #80，spec §4.6，ADR 0014）：进程内异步写（有界队列
	// 4096、批量 200 条或 1 秒落库，队列满丢弃不阻塞请求）。writer 同时供
	// infraAPI 的采集中间件、App.Flush/Close 排干（测试与优雅关闭）。
	apiLogWriter := infraservice.NewAPILogWriter(infraRepo, infraservice.APILogWriterConfig{})
	apiLogWriter.Run()

	// 注册访问日志清理任务（spec §4.6.7）：handlerName 为 cleanAccessLogs，
	// 种子默认行每日 03:00 启用（保留期 access-log.retention-days，默认 30，
	// handler_param 可填天数覆盖）。清理跨租户执行（tenant.Skip，见 infra
	// repository），属系统级处理器：只有平台租户管理员可操作。
	scheduler.RegisterSystem("cleanAccessLogs", func(ctx context.Context, param string) error {
		days := 0
		if v, err := strconv.Atoi(strings.TrimSpace(param)); err == nil {
			days = v
		}
		cleaned, err := infraSvc.CleanAccessLogsBefore(ctx, days)
		if err != nil {
			return err
		}
		slog.Info("access log cleanup job finished", "cleaned", cleaned)
		return nil
	})

	sysAPI := systemapi.New(sysSvc, socialSvc, sensitiveSvc, cfg)

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
	// 跟进内容敏感词检查（issue #81，spec §4.7.4）：创建/修改落库前检查，
	// 命中 400 不落库。
	crmSvc.SetSensitiveWordChecker(sensitiveWordChecker{svc: sensitiveSvc})
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

	// ERP 模块（issue #88，docs/erp-mvp-spec.md §2）：repo→service→api 顺序装配；
	// 数据范围解析复用 system Service（datascope 引擎宿主，ADR 0004），依赖只指向
	// 平台层，不依赖 crm/oa/bpm（ADR 0015）。产品删除引用校验扩展点（product_refs.go）
	// 本票为骨架，#90–#94 的单据域引用类型在各自装配处注册。
	erpRepo := erprepo.New(db)
	erpSvc := erpservice.New(erpRepo, sysSvc)
	// 库存核心（issue #90，spec §4.9）：仓库被 erp_stock 非零记录/流水引用禁删、
	// 产品被其它出入库单明细引用禁删——两组引用校验由 erp Service 自身实现并在此注册；
	// #91+ 的单据域引用类型（挂单/往来单位）继续以 RefCheckerChain 组合追加。
	erpSvc.SetWarehouseRefChecker(erpSvc)
	erpSvc.SetProductRefChecker(erpSvc)
	// 业务时区（ADR 0006）：单号日期键与单据业务时间解析的唯一口径。
	erpSvc.SetLocation(cfg.Location())
	erpAPI := erpapi.New(erpSvc, sysAPI.RequirePermission, cfg.Location())

	// notify 模块（issue #45）：平台级站内通知投递/读取基础设施（spec §3.2/§4.13）。
	// 无业务依赖、不反向依赖业务模块；bpm/公告/会议室等生产者经 notifySvc 的
	// CreateInTx seam 把通知写进各自的事务（同生共死），读取侧走 /notify/message/*。
	notifySvc := notifyservice.New(notifyrepo.New(db))

	// 实时推送（issue #78，spec §4.5 / ADR 0013）：hub 按 (tenant, user) 订阅、
	// 一次性票据 30s TTL、SSE 25s 心跳。hub 与票据都在进程内——单实例限制，
	// 多实例需要外部 pub/sub（本轮不实现，见 ADR 0013）。推送关闭时路由不注册，
	// 前端换票失败自动降级 60 秒轮询。
	var pushHub *push.Hub
	var pushHandler *notifyapi.PushHandler
	if cfg.Push.Enabled {
		pushHub = push.NewHub()
		pushTickets := push.NewTicketManager(cfg.Push.TicketTTL)
		// SSE 请求带 Bearer 时做第二身份比对（票据与 Bearer 必须同人，
		// 防脚本拿 A 的票据配 B 的 token 跨租户订阅）；浏览器不带该头。
		resolvePrincipal := func(token string) (int64, int64, bool) {
			claims, err := sysSvc.ParseToken(token)
			if err != nil || claims.TenantID <= 0 || claims.UserID <= 0 {
				return 0, 0, false
			}
			return claims.TenantID, claims.UserID, true
		}
		pushHandler = notifyapi.NewPushHandler(pushHub, pushTickets, cfg.Push.Heartbeat, resolvePrincipal)
		// *push.Hub 满足 notifyservice.PushPublisher（Publish 不阻塞、不报错）：
		// Create 提交后发布；生产者在事务提交后经 PublishInputs 显式发布。
		notifySvc.SetPushPublisher(pushHub)
	}
	notifyAPI := notifyapi.New(notifySvc, sysAPI.RequirePermission, pushHandler)

	// message 模块（issue #72，spec §4.3 / ADR 0010）：渠道/模板是平台级配置，
	// 发送日志走租户插件；provider 注册表在 New 内先注册内置 log provider，
	// smtp（#73）/aliyun_sms（#74）实现后在此追加 RegisterSender。
	messageSvc := messageservice.New(messagerepo.New(db))
	// 业务时区（ADR 0006）：月度配额的「当月」边界按 app.timezone 划分。
	messageSvc.SetLocation(cfg.Location())
	// 配额装配补齐（issue #75）：适配器带上 message 后再注入 system 的
	// QuotaProvider（TenantQuota 的短信/邮件月度用量）与 message 的
	// MessageQuotaGateway（发送校验的限额来源）。
	quotaAdapter.message = messageSvc
	sysSvc.SetQuotaProvider(quotaAdapter)
	messageSvc.SetQuotaGateway(quotaAdapter)
	// 监控统计装配（issue #84，spec §4.10）：适配器组合 system（租户数/
	// 用户数）与 message（发送成功/失败数），满足 infra 的
	// MonitorStatsGateway 窄接口；infra 不直接读 system/message 表
	//（ADR 模块边界，同 quotaGateway 模式）。message 装配在此完成，
	// 适配器一次注入（值类型，注入后不得再改字段）。
	monitorAdapter := monitorGateway{sys: sysSvc, message: messageSvc}
	infraSvc.SetMonitorStatsGateway(monitorAdapter)
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
	// 公告标题/正文敏感词检查（issue #81，spec §4.7.4）：创建/修改落库前
	// 检查，命中 400 不落库。
	oaSvc.SetSensitiveWordChecker(sensitiveWordChecker{svc: sensitiveSvc})
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

	infraAPI := infraapi.New(infraSvc, cfg, sysAPI.RequirePermission, apiLogWriter)
	codegenAPI := codegen.NewHandler(codegen.New(db), sysAPI.RequirePermission)

	engine := router.New(cfg, sysAPI, infraAPI, codegenAPI, crmAPI, oaAPI, bpmAPI, notifyAPI, messageAPI, erpAPI)

	scheduler.Start(context.Background())

	return &App{Config: cfg, DB: db, Engine: engine, Scheduler: scheduler, BPM: bpmSvc, CRM: crmSvc, Message: messageSvc, Social: socialSvc, PushHub: pushHub, Notify: notifySvc, APILog: apiLogWriter}, nil
}

// Close 释放资源。
func (a *App) Close() error {
	a.Scheduler.Stop()
	// 访问日志排干（spec §4.6.5：Flush 供优雅关闭）：限 5 秒，超时放弃剩余
	// 条目（ADR 0014：进程内队列允许丢失），不阻塞关库。
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.APILog.Close(closeCtx); err != nil {
		slog.Warn("flush api access log on close", "err", err)
	}
	sqlDB, err := a.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
