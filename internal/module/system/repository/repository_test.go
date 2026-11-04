package repository_test

import (
	"context"
	"testing"
	"time"

	sysmodel "github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newSystemTestDB 建立装好租户插件的独立 sqlite 库（单连接，保证内存库共享）。
func newSystemTestDB(t *testing.T, models ...any) *gorm.DB {
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

// CleanLogsBefore 由 cleanLogs 定时任务调用，属于系统级清理：
// 必须显式 tenant.Skip 跨租户清理，而不是只清任务所属租户的日志。
func TestCleanLogsBeforeIsSystemLevel(t *testing.T) {
	db := newSystemTestDB(t, &sysmodel.LoginLog{}, &sysmodel.OperateLog{})

	old := time.Now().AddDate(0, 0, -60)
	recent := time.Now().AddDate(0, 0, -1)
	loginLogs := []sysmodel.LoginLog{
		{TenantID: 1, Username: "t1-old", LoginTime: old},
		{TenantID: 1, Username: "t1-recent", LoginTime: recent},
		{TenantID: 2, Username: "t2-old", LoginTime: old},
		{TenantID: 2, Username: "t2-recent", LoginTime: recent},
	}
	operateLogs := []sysmodel.OperateLog{
		{TenantID: 1, Username: "t1-old", OperateTime: old},
		{TenantID: 2, Username: "t2-old", OperateTime: old},
		{TenantID: 2, Username: "t2-recent", OperateTime: recent},
	}
	if err := tenant.Skip(db).Create(&loginLogs).Error; err != nil {
		t.Fatalf("写入登录日志: %v", err)
	}
	if err := tenant.Skip(db).Create(&operateLogs).Error; err != nil {
		t.Fatalf("写入操作日志: %v", err)
	}

	if err := repository.New(db).CleanLogsBefore(context.Background(), time.Now().AddDate(0, 0, -30)); err != nil {
		t.Fatalf("无租户上下文清理日志：定时任务路径不应被租户插件拦截: %v", err)
	}

	var loginUsernames []string
	if err := tenant.Skip(db).Model(&sysmodel.LoginLog{}).Order("username").Pluck("username", &loginUsernames).Error; err != nil {
		t.Fatalf("回读登录日志: %v", err)
	}
	if len(loginUsernames) != 2 || loginUsernames[0] != "t1-recent" || loginUsernames[1] != "t2-recent" {
		t.Fatalf("应只保留两个租户的近期登录日志，实际 %v", loginUsernames)
	}

	var operateUsernames []string
	if err := tenant.Skip(db).Model(&sysmodel.OperateLog{}).Order("username").Pluck("username", &operateUsernames).Error; err != nil {
		t.Fatalf("回读操作日志: %v", err)
	}
	if len(operateUsernames) != 1 || operateUsernames[0] != "t2-recent" {
		t.Fatalf("应只保留租户 2 的近期操作日志，实际 %v", operateUsernames)
	}
}
