package repository_test

import (
	"context"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取 sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			t.Fatalf("建表: %v", err)
		}
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}
	return db
}

// ListEnabledJobs 供调度器启动时使用，启动路径没有租户上下文，
// 必须显式 tenant.Skip 才能在 fail-closed 的租户插件下加载全部租户的启用任务。
func TestListEnabledJobsIsSystemLevel(t *testing.T) {
	db := newTestDB(t, &model.Job{})
	rows := []model.Job{
		{TenantID: 1, Name: "任务一", HandlerName: "demoJob", CronExpression: "0 */5 * * * *"},
		{TenantID: 2, Name: "任务二", HandlerName: "demoJob", CronExpression: "0 */5 * * * *"},
	}
	// Status 带 default:1（暂停）标签，插入时零值会被省略，这里显式改回开启。
	if err := tenant.Skip(db).Create(&rows).Error; err != nil {
		t.Fatalf("写入任务: %v", err)
	}
	if err := tenant.Skip(db).Model(&model.Job{}).Where("name IN ?", []string{"任务一", "任务二"}).Update("status", int8(model.JobStatusRunning)).Error; err != nil {
		t.Fatalf("开启任务: %v", err)
	}
	paused := model.Job{TenantID: 1, Name: "暂停任务", HandlerName: "demoJob", CronExpression: "0 */5 * * * *", Status: model.JobStatusPaused}
	if err := tenant.Skip(db).Create(&paused).Error; err != nil {
		t.Fatalf("写入暂停任务: %v", err)
	}

	jobs, err := repository.New(db).ListEnabledJobs(context.Background())
	if err != nil {
		t.Fatalf("无租户上下文加载启用任务：调度器启动路径不应被租户插件拦截: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("应加载两个租户共 2 个启用任务，实际 %d 个", len(jobs))
	}
	for _, job := range jobs {
		if job.Status != model.JobStatusRunning {
			t.Fatalf("不应返回暂停任务：%+v", job)
		}
	}
}
