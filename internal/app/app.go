package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/module/codegen"
	crmapi "github.com/lautung/go-admin-pro/internal/module/crm/api"
	crmrepo "github.com/lautung/go-admin-pro/internal/module/crm/repository"
	crmservice "github.com/lautung/go-admin-pro/internal/module/crm/service"
	infraapi "github.com/lautung/go-admin-pro/internal/module/infra/api"
	infrarepo "github.com/lautung/go-admin-pro/internal/module/infra/repository"
	infraservice "github.com/lautung/go-admin-pro/internal/module/infra/service"
	systemapi "github.com/lautung/go-admin-pro/internal/module/system/api"
	systemrepo "github.com/lautung/go-admin-pro/internal/module/system/repository"
	systemservice "github.com/lautung/go-admin-pro/internal/module/system/service"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"github.com/lautung/go-admin-pro/internal/pkg/jwt"
	"github.com/lautung/go-admin-pro/internal/router"
	"github.com/lautung/go-admin-pro/internal/seed"
	"gorm.io/gorm"
)

// App 持有应用运行期依赖。
type App struct {
	Config    *config.Config
	DB        *gorm.DB
	Engine    *gin.Engine
	Scheduler *infraservice.JobScheduler
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
		if err := seed.Run(db); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}

	jwtManager := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.Expire, cfg.JWT.Issuer)

	sysRepo := systemrepo.New(db)
	sysSvc := systemservice.New(sysRepo, jwtManager, cfg)

	infraRepo := infrarepo.New(db)
	scheduler := infraservice.NewJobScheduler(infraRepo)
	infraSvc := infraservice.New(infraRepo, cfg, scheduler)

	// 注册日志清理任务，可在“定时任务”中把 handlerName 配置为 cleanLogs。
	scheduler.Register("cleanLogs", func(ctx context.Context, param string) error {
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

	// 注册公海自动回收任务（issue #14）：handlerName 为 recyclePoolCustomers，
	// 种子默认行已按暂停状态写入（启用前请确认各租户公海配置，见 internal/seed
	// 的 seedJobs 注释）。回收是系统级动作：handler 忽略 job 所属租户上下文，
	// 由 crm service 以 tenant.Skip 列出启用租户后逐租户执行。
	scheduler.Register(crmservice.RecycleJobHandlerName, func(ctx context.Context, param string) error {
		result, err := crmSvc.RunPoolRecycle(ctx)
		if err != nil {
			return err
		}
		slog.Info("pool auto-recycle job finished", "tenants", result.Tenants, "recycled", result.Recycled)
		return nil
	})

	// Excel 导入的日期解析统一使用应用业务时区（ADR 0006：只经 cfg.Location() 获取）。
	crmAPI := crmapi.New(crmSvc, sysAPI.RequirePermission, cfg.Location())

	infraAPI := infraapi.New(infraSvc, cfg, sysAPI.RequirePermission)
	codegenAPI := codegen.NewHandler(codegen.New(db), sysAPI.RequirePermission)

	engine := router.New(cfg, sysAPI, infraAPI, codegenAPI, crmAPI)

	scheduler.Start(context.Background())

	return &App{Config: cfg, DB: db, Engine: engine, Scheduler: scheduler}, nil
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
