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
	// mall（issue #174）：会员域管理端。依赖指向平台层与 pay service（mall 是
	// pay 的业务消费方，T4 支付联动接线；docs/mall-mvp-spec.md §2）。
	mallapi "github.com/go-quick-code/go-admin-pro/internal/module/mall/api"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	payapi "github.com/go-quick-code/go-admin-pro/internal/module/pay/api"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	systemapi "github.com/go-quick-code/go-admin-pro/internal/module/system/api"
	systemrepo "github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/iploc"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
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
	// Pay 是 pay 模块的 Service（issue #131）：集成测试经该字段发放序列单号
	//（pay_sequence 并发发放用例）；业务侧只经 HTTP 端点消费。
	Pay *payservice.Service
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
	// mall 会员令牌签名密钥 fail-closed（issue #178，spec §7 裁决 2）：密钥
	// 缺失时拒绝启动——C 端 /mall/app/* 的注册/登录签发与令牌自校验全部
	// 依赖该密钥，缺密钥等于 C 端身份体系不可用，静默降级只会把问题推迟到
	// 首个会员请求。放在 database.Open 之前：零依赖配置事实，与「分类正确
	// 性是 schema 事实」的 VerifyTenantTables 同一收口思路。错误信息指明
	// mall.jwt_secret（配置键 mall.jwt-secret / 环境变量 GAP_MALL_JWT_SECRET）。
	if strings.TrimSpace(cfg.Mall.JWTSecret) == "" {
		return nil, fmt.Errorf("mall.jwt_secret 不能为空：C 端会员令牌签名密钥必须在配置文件（mall.jwt-secret）或环境变量 GAP_MALL_JWT_SECRET 中设置唯一强随机值")
	}
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
	// 租户表登记校验（ADR 0019）：每张表必须且只能属于 TenantTables 或
	// PlatformTables；分类缺失/冲突直接拒绝启动。无条件执行——分类正确性
	// 是 schema 事实，与本次进程是否启用租户无关；两次元数据查询成本可忽略。
	if err := database.VerifyTenantTables(verifyCtx, sqlDB); err != nil {
		return nil, fmt.Errorf("verify tenant table registry: %w", err)
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
		// ERP 演示数据的业务时间走 app.timezone（ADR 0006）：seed 包不读配置，
		// 同样显式注入业务时区，避免依赖进程本地时区。
		seed.SetDemoLocation(cfg.Location())
		// pay 演示应用回调地址指向本机 demo 回调端点（issue #139，spec §7 演示
		// 数据）：seed 包不读配置，注入 http://127.0.0.1:port + api-prefix——
		// 回调 job 对 demo 回调端点自调用（HMAC 同源密钥，商户回调链路真实走通）。
		seed.SetDemoPayNotifyBase(fmt.Sprintf("http://127.0.0.1:%d%s", cfg.App.Port, cfg.App.APIPrefix))
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

	// Excel 导入的日期解析统一使用应用业务时区（ADR 0006：只经 cfg.Location() 获取）。
	crmAPI := crmapi.New(crmSvc, sysAPI.RequirePermission, cfg.Location())

	// ERP 模块（issue #88，docs/erp-mvp-spec.md §2）：repo→service→api 顺序装配；
	// 数据范围解析复用 system Service（datascope 引擎宿主，ADR 0004），依赖只指向
	// 平台层，不依赖 crm/oa/bpm（ADR 0015）。产品删除引用校验扩展点（product_refs.go）
	// 在下方装配处与库存/采购/销售实现组合注册（后续域继续以 RefCheckerChain 追加）。
	erpRepo := erprepo.New(db)
	erpSvc := erpservice.New(erpRepo, sysSvc)
	// 库存核心（issue #90，spec §4.9）：仓库被 erp_stock 非零记录/流水引用禁删、
	// 产品被其它出入库单明细引用禁删——仓库校验由 erp Service 自身实现并在此注册；
	// 产品校验在下方与 #91 采购实现以 RefCheckerChain 组合（后注册一次生效）。
	erpSvc.SetWarehouseRefChecker(erpSvc)
	// 销售三单（issue #92，spec §4.9）：ERP 客户被销售订单/出库/退货引用禁删——
	// CustomerRefChecker 由 erp Service 自身实现并在此注册；#91 供应商同构追加。
	erpSvc.SetCustomerRefChecker(erpSvc)
	// 采购单据引用（issue #91，spec §4.9）：产品被采购订单/入库/退货明细引用禁删
	//（与 #90 库存实现经 RefCheckerChain 组合——后注册覆盖先注册）；供应商被采购
	// 单据引用禁删（#89 扩展点首次落地）。#92+ 的单据域引用类型继续以 Chain 追加。
	// 构造内联进 Set 调用（无中间变量），与 codegen fresh-module 检查的
	// erpSvc.Set 装配行剪除适配兼容（延续 #88/#90 既有模式）。
	erpSvc.SetProductRefChecker(erpservice.RefCheckerChain{erpSvc, erpservice.NewPurchaseDocRefChecker(erpSvc)})
	erpSvc.SetSupplierRefChecker(erpservice.NewPurchaseDocRefChecker(erpSvc))
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

	infraAPI := infraapi.New(infraSvc, cfg, sysAPI.RequirePermission, apiLogWriter)
	codegenAPI := codegen.NewHandler(codegen.New(db), sysAPI.RequirePermission)

	// pay 模块（issue #131，docs/pay-mvp-spec.md §2）：repo→service→api 顺序
	// 装配；业务时区（ADR 0006）是单号日期键的唯一口径。渠道 SPI 注册表
	//（#133 定形）：mock/wallet 两实现注册进 Registry，未注册渠道在绑定与
	// submit 处 409「渠道未实现」；应用删除引用检查以 RefCheckerChain 注册
	//（#133 订单引用 + #135 退款引用，#136 追加转账）。
	payRepo := payrepo.New(db)
	paySvc := payservice.New(payRepo)
	paySvc.SetLocation(cfg.Location())
	payRegistry, payRegistryErr := payChannelRegistry(db, paySvc, paySvc, paySvc)
	if payRegistryErr != nil {
		return nil, payRegistryErr
	}
	paySvc.SetRegistry(payRegistry)
	// 应用删除引用检查链（spec §4.10）：订单引用（#133）+ 退款引用（#135）+
	// 转账引用（#137）依次检查，首个命中即拒绝。示例订单/示例提现不另设检查器
	//（决策见 payservice.DemoRefCheckerDecision：消费行对应用的引用全部经
	// pay_order/pay_transfer 传递，先行命中）。
	paySvc.SetAppRefChecker(payservice.RefCheckerChain{
		payservice.OrderRefChecker{Repo: payRepo},
		payservice.RefundRefChecker{Repo: payRepo},
		payservice.TransferRefChecker{Repo: payRepo},
	})
	// 回调链路装配（issue #134，spec §4.5）：HMAC 密钥（pay.notify-hmac-key，
	// 空值 job 拒发 fail-closed）与启用租户清单（job 跨租户逐租户，照公海
	// 回收模式——system Service 直接满足 payservice.TenantLister 窄接口）。
	paySvc.SetNotifyHMACKey(cfg.Pay.NotifyHMACKey)
	// 出站回调 SSRF 放行清单（PR #140 安全审查 P2）：dev 缺省放行本机环回
	// （configs pay.notify-ssrf-private-allow），生产留空即全拦。
	paySvc.SetNotifySSRFPrivateAllow(cfg.Pay.NotifySSRFPrivateAllow)
	paySvc.SetTenantLister(sysSvc)
	// 充值挂单应用（issue #136，spec §4.7）：发起充值同事务挂的 pay_order
	// 归属应用，按 pay.recharge-app-key 解析（缺省 demo，见 service 层决策）。
	paySvc.SetRechargeAppKey(cfg.Pay.RechargeAppKey)
	// 回调报文详情：type=1 支付 / type=2 退款（#135）/ type=3 转账（#137）
	// 均在 service.New 内注册。支付成功业务联动：充值入账（#136，service.New
	// 内注册于 OrderSuccessHandler 扩展点）；后续票经 SetOrderSuccessHandler
	// 追加。demo 状态联动（#137，spec §4.5/§4.8）：demo 回调三端点验签后经
	// HandleDemoNotify 回写 pay_demo_order/pay_demo_withdraw 状态（严格
	// kind↔type 校验、DataID 定位消费行派生租户、非 demo 行幂等应答）。
	paySvc.SetDemoNotifyHandler(paySvc.HandleDemoNotify)
	payAPI := payapi.New(paySvc, sysAPI.RequirePermission, cfg.Location())

	// mall 装配（issue #174）：repo → service → api.New，形状对照 pay/crm
	//（svc + requirePermission + location）。T1 会员域管理端；积分入账随 T2
	// 的唯一入账入口接线。会员令牌签名材料（issue #178，T5）：密钥与有效期
	// 注入 service（登录签发 + handler 自校验共用同一实现，Set* 形状照
	// paySvc.SetNotifyHMACKey）。业务时区（issue #179，T6）：SetLocation
	// 注入 service——SO 单号日期键的唯一口径（照 paySvc.SetLocation 先例）。
	mallRepo := mallrepo.New(db)
	mallSvc := mallservice.New(mallRepo)
	mallSvc.SetLocation(cfg.Location())
	mallSvc.SetMemberTokenAuth(cfg.Mall.JWTSecret, cfg.Mall.TokenTTL)
	// 支付联动（issue #181，T8；spec §2 mall→pay 单向 + §7 裁决 4）：
	//   - SetPaySvc 注入 pay Service——C 端发起支付的建单（merchant_order_id
	//     = SO 号幂等）与 SubmitOrder 转发起（委托主体，见 service.AppPayOrder）；
	//   - paySvc.SetOrderSuccessHandler 注册 mall 联动——pay OrderSuccessHandler
	//     扩展点第二消费方（充值是第一，payservice.New 内置）：pay 订单认领后
	//     同事务置 mall 订单已支付 + 积分按实付入账，任一失败整体回滚。
	//     pay 侧零改动（既有扩展点），pay 对 mall 保持零依赖（只持函数值）。
	mallSvc.SetPaySvc(paySvc)
	paySvc.SetOrderSuccessHandler(mallSvc.HandlePaySuccess)
	mallAPI := mallapi.New(mallSvc, sysAPI.RequirePermission, cfg.Location())

	// 定时任务绑定表（ADR 0024 补遗）：键 = 处理器名常量（与 seed.JobSpecs
	// 引用同一常量，处理器名没有第二处字面量），值 = 执行闭包——业务服务在
	// 装配期已就绪，闭包体自原装配点逐字迁移；demoJob 闭包自 JobScheduler
	// 构造函数移入（构造函数只管调度机制，不再自注册处理器）。
	jobBindings := map[string]infraservice.HandlerFunc{
		// 演示任务：非系统级，任何租户可建任务行（租户可见性零变化）。
		infraservice.DemoJobHandlerName: func(ctx context.Context, param string) error {
			slog.Info("demo job executed", "tenant", tenant.ID(ctx), "param", param)
			return nil
		},
		// 访问日志清理（spec §4.6.7）：种子默认行每日 03:00 启用（保留期
		// access-log.retention-days，默认 30，handler_param 可填天数覆盖）。
		// 清理跨租户执行（tenant.Skip，见 infra repository），属系统级处理器：
		// 只有平台租户管理员可操作。
		infraservice.CleanAccessLogsJobHandlerName: func(ctx context.Context, param string) error {
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
		},
		// 公海自动回收（issue #14）：种子默认行已按暂停状态写入（启用前请
		// 确认各租户公海配置，见 internal/seed/job.go 规格注释）。回收是
		// 系统级动作：handler 忽略 job 所属租户上下文，由 crm service 以
		// tenant.Skip 列出启用租户后逐租户执行。
		//
		// 系统级处理器（PR #39 审查 P0）：普通租户管理员即使持 super_admin（"*"）
		// 也不能创建/运行该 handler，否则可经 infra_job 触发跨租户公海回收。
		crmservice.RecycleJobHandlerName: func(ctx context.Context, param string) error {
			result, err := crmSvc.RunPoolRecycle(ctx)
			if err != nil {
				return err
			}
			slog.Info("pool auto-recycle job finished", "tenants", result.Tenants, "recycled", result.Recycled)
			return nil
		},
		// 考勤日结果任务（issue #61，spec §4.10）：系统级 handler（跨租户迭代，
		// tenant.Skip 列启用租户后逐租户派生），infra_job 种子默认行每天 01:00。
		// handler_param 可填 YYYY-MM-DD 回填指定日期；留空取业务时区前一日。
		oaservice.AttendanceDailyResultHandlerName: func(ctx context.Context, param string) error {
			summary, err := oaSvc.RunDailyAttendanceResult(ctx, param)
			if err != nil {
				return err
			}
			slog.Info("attendance daily result job finished",
				"date", summary.Date, "tenants", summary.Tenants, "users", summary.Users)
			return nil
		},
		// 会议提醒任务（issue #57，spec §4.11）：系统级 handler（跨租户迭代），
		// infra_job 种子默认行每 5 分钟一次。扫描 (now, now+15m] 内开始的有效
		// 未提醒预约，条件更新 reminded_at 认领后向预约人写 meeting_reminder
		// 通知（只发一次）。handler_param 可填 YYYY-MM-DD HH:mm:ss 固定「现在」
		// （测试/回放）；留空取业务时区当前时间。
		oaservice.RoomBookingReminderHandlerName: func(ctx context.Context, param string) error {
			summary, err := oaSvc.RunRoomBookingReminders(ctx, param)
			if err != nil {
				return err
			}
			slog.Info("room booking reminder job finished",
				"now", summary.Now, "tenants", summary.Tenants, "sent", summary.Sent)
			return nil
		},
		// 商户回调重试任务（issue #134，spec §4.5/§7）：系统级 handler（跨租户
		// 逐租户，照公海回收模式），infra_job 种子默认行每分钟一次。扫描到期任务
		//（status IN (0,21,22) 且 next_notify_time 到期）POST 商户回调地址，
		// 报文带 Pay-Signature（HMAC-SHA256(rawBody, pay.notify-hmac-key)）；
		// 条件更新认领防重发（同任务双触发只执行一次）；密钥缺失拒发（fail-closed）。
		payservice.NotifyRetryJobHandlerName: func(ctx context.Context, param string) error {
			result, err := paySvc.RunNotifyRetry(ctx)
			if err != nil {
				return err
			}
			slog.Info("pay notify retry job finished",
				"tenants", result.Tenants, "executed", result.Executed, "success", result.Success, "failClosed", result.FailClosed)
			return nil
		},
		// 订单超时关闭任务（issue #134，spec §7）：系统级 handler（跨租户迭代），
		// infra_job 种子默认行每 5 分钟一次。扫描过期待支付订单（status=0 且
		// expire_time < now）条件更新置 30 已关闭；已支付/已关闭不受影响。
		payservice.OrderTimeoutCloseJobHandlerName: func(ctx context.Context, param string) error {
			result, err := paySvc.RunOrderTimeoutClose(ctx)
			if err != nil {
				return err
			}
			slog.Info("pay order timeout close job finished",
				"tenants", result.Tenants, "closed", result.Closed)
			return nil
		},
	}

	// 按规格注册执行闭包（ADR 0024 补遗）：规格与绑定以同一常量为键——
	// 先 fail-closed 校验绑定齐全（键缺失或值为 nil 都算缺失：nil 执行体
	// 会在 cron 首触发时 panic，须在装配期拦截；缺绑定即拒启，纯函数检查
	// 由 app 包单测覆盖），再按规格 System 标志分派普通/系统级注册。
	// System 标志逐条对应重构前现状（demoJob=false、其余 6 条=true）：租户
	// 可见性过滤与调度二次防线读同一张系统级注册表，行为不变。孤儿绑定
	//（绑定无规格）与规格外直注（如 cleanLogs 的「注册-不种子」）都不校验——
	// 后者是合法状态（管理员按需建任务行）。
	if missing := missingJobBindings(seed.JobSpecs, jobBindings); len(missing) > 0 {
		return nil, fmt.Errorf("定时任务规格缺少执行绑定：%s（装配处 jobBindings 补齐，ADR 0024 补遗）",
			strings.Join(missing, ", "))
	}
	for _, spec := range seed.JobSpecs {
		fn := jobBindings[spec.HandlerName]
		if spec.System {
			scheduler.RegisterSystem(spec.HandlerName, fn)
		} else {
			scheduler.Register(spec.HandlerName, fn)
		}
	}

	// 模块注册（ADR 0024）：Backbone 三个受保护组中间件由装配处填充——Auth 与
	// OperateLog 归 system、AccessLog 归 infra；次序（Auth → AccessLog →
	// OperateLog）在 router.New 内固定，这里只提供函数。
	backbone := router.Backbone{
		Auth:       sysAPI.Auth(),
		AccessLog:  infraAPI.AccessLog(),
		OperateLog: sysAPI.OperateLog(),
	}
	// 注册器列表：次序即受保护组的模块注册次序（零漂移约束）；公开组与换票流组
	// 由 router 对可选能力（RegisterPublic/RegisterStream）按同一列表过滤得出，
	// 相对次序一致（公开 = system, notify, pay；换票流 = notify）。接新模块 =
	// 这里 append 一行（清单见 docs/agents/new-module.md）。
	registrars := []router.Registrar{
		sysAPI,
		infraAPI,
		codegenAPI,
		crmAPI,
		oaAPI,
		// bpm（issue #44）：流程定义管理（实例/任务接口由 #46 追加注册）。
		bpmAPI,
		// notify：公开组挂凭票据的推送流（issue #78，spec §4.5.3/§5.4——票据即
		// 凭证，浏览器 EventSource 不能带请求头，公开路由是设计使然，ADR 0013；
		// 推送关闭时注册为 no-op）；换票流组挂 POST /notify/push/ticket。
		notifyAPI,
		// message（issue #72）：渠道/模板（仅平台租户，service 层收口）、
		// 短信/邮件发送与租户级发送日志。
		messageAPI,
		// erp（issue #88）：统一前缀 /erp，主数据（分类/单位/产品）起步；
		// 单据/库存/财务路由由后续票追加注册。
		erpAPI,
		// pay（issue #131）：统一前缀 /pay，应用与渠道域起步（渠道的平台级约束
		// 在 service 层 requirePlatform 收口）；交易/回调/钱包路由由后续票追加，
		// 公开端点（demo 回调，issue #134，PR #140 补渠道回调）挂公开组——
		// 无登录、挂 IP 限速、不记访问日志（公开组在 protected 组外，结构使然）。
		payAPI,
		// mall（issue #174）：统一前缀 /mall，会员域管理端起步（商品/交易路由
		// 随 T2/T3 追加注册，C 端公开组 /mall/app/* 随 T5）。列表序在 pay 之后
		//（spec §2：mall 是 pay 的业务消费方，注册器相对次序一致）。
		mallAPI,
	}

	engine := router.New(cfg, backbone, registrars...)

	scheduler.Start(context.Background())

	return &App{Config: cfg, DB: db, Engine: engine, Scheduler: scheduler, BPM: bpmSvc, CRM: crmSvc, Message: messageSvc, Social: socialSvc, PushHub: pushHub, Notify: notifySvc, Pay: paySvc, APILog: apiLogWriter}, nil
}

// missingJobBindings 返回规格清单中没有装配绑定的处理器名名单（按规格次序，
// 便于与 JobSpecs 对照）：绑定表键缺失或值为 nil 都算缺失——键存在校验拦不住
// 手写 X: nil 的 map 字面量，nil 执行体要到 cron 首触发才 panic。任一条缺失都
// 意味着对应任务行启用后 cron 触发会报「未注册的任务处理器」，属接线 bug——
// app.New 据此拒绝启动（fail-closed，ADR 0024 补遗；先例：演示种子撞序号
// init panic、ADR 0019 VerifyTenantTables 拒启）。孤儿绑定（绑定无规格）与
// 规格外直注（如 cleanLogs 的「注册-不种子」）是合法状态，不在检查范围。
func missingJobBindings(specs []seed.JobSpec, bindings map[string]infraservice.HandlerFunc) []string {
	var missing []string
	for _, spec := range specs {
		if bindings[spec.HandlerName] == nil {
			missing = append(missing, spec.HandlerName)
		}
	}
	return missing
}

// payChannelRegistry 装配渠道 SPI 注册表（issue #133 支付 / #135 退款 /
// #137 转账，docs/pay-mvp-spec.md §4.1）：v1 注册 mock/wallet 两实现；渠道
// config 取 pay_channel 种子行原文（空对象 {}，由渠道实现自行反序列化校验）。
// 注册表是「渠道未实现」的唯一判定源：未注册渠道在应用绑定与 submit 处 409
// （service 层 Known 检查）。wallet 渠道的扣减/回加/转账入账后端均为 pay
// service 层（同一实例，事务经 payrepo.WithTx 传递）。补渠道实现不改 SPI：
// 实现 Client 后在此 Register 即可。
func payChannelRegistry(db *gorm.DB, payer paychannel.WalletPayer, refunder paychannel.WalletRefunder, transferer paychannel.WalletTransferer) (*paychannel.Registry, error) {
	rows, err := payrepo.New(db).ListChannelsByCodes(
		context.Background(), []string{paychannel.CodeMock, paychannel.CodeWallet})
	if err != nil {
		return nil, fmt.Errorf("load pay channel configs: %w", err)
	}
	configByCode := make(map[string]string, len(rows))
	for _, row := range rows {
		configByCode[row.Code] = row.Config
	}
	mockClient, err := paychannel.NewMockClient(configByCode[paychannel.CodeMock])
	if err != nil {
		return nil, fmt.Errorf("init mock pay client: %w", err)
	}
	walletClient, err := paychannel.NewWalletClient(configByCode[paychannel.CodeWallet], payer, refunder, transferer)
	if err != nil {
		return nil, fmt.Errorf("init wallet pay client: %w", err)
	}
	registry := paychannel.NewRegistry()
	registry.Register(mockClient)
	registry.Register(walletClient)
	return registry, nil
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
