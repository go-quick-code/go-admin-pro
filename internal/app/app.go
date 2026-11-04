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
	// #16 接入点：合同模块落地后在此调用 crmSvc.SetContractChecker(实现)，
	// 替换公海自动回收的空合同检查（v1 恒「无合同」，见
	// crmservice.ContractChecker 接口注释）。

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

	crmAPI := crmapi.New(crmSvc, sysAPI.RequirePermission)

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
