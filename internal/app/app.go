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
	infraAPI := infraapi.New(infraSvc, cfg, sysAPI.RequirePermission)
	codegenAPI := codegen.NewHandler(codegen.New(db), sysAPI.RequirePermission)

	engine := router.New(cfg, sysAPI, infraAPI, codegenAPI)

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
