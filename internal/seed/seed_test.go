package seed_test

import (
	"testing"

	inframodel "github.com/lautung/go-admin-pro/internal/module/infra/model"
	sysmodel "github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"github.com/lautung/go-admin-pro/internal/seed"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Run 是系统级写入路径：在 fail-closed 的租户插件下必须能完成 seed，
// 且写入的业务行都要归属默认租户 1（不依赖插件回填）。
func TestSeedRunUnderFailClosedTenantPlugin(t *testing.T) {
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

	if err := db.AutoMigrate(
		&sysmodel.Tenant{}, &sysmodel.Menu{}, &sysmodel.Dept{}, &sysmodel.Role{},
		&sysmodel.RoleMenu{}, &sysmodel.Post{}, &sysmodel.User{}, &sysmodel.UserRole{},
		&sysmodel.UserPost{}, &sysmodel.DictType{}, &sysmodel.DictData{},
		&inframodel.Config{}, &inframodel.Job{},
	); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}

	if err := seed.Run(db); err != nil {
		t.Fatalf("fail-closed 租户插件下 seed 失败: %v", err)
	}

	assertCount(t, db, &sysmodel.Tenant{}, 1, "租户")
	assertCount(t, db, &sysmodel.User{}, 2, "用户")
	assertCount(t, db, &sysmodel.Dept{}, 4, "部门")
	assertCount(t, db, &sysmodel.Role{}, 2, "角色")
	assertCount(t, db, &sysmodel.Post{}, 2, "岗位")
	assertCount(t, db, &sysmodel.DictType{}, 2, "字典类型")
	assertCount(t, db, &sysmodel.DictData{}, 5, "字典数据")
	assertCount(t, db, &inframodel.Config{}, 2, "参数配置")
	assertCount(t, db, &inframodel.Job{}, 1, "定时任务")

	var admin sysmodel.User
	if err := tenant.Skip(db).First(&admin, 1).Error; err != nil {
		t.Fatalf("读取内置管理员: %v", err)
	}
	if admin.TenantID != 1 {
		t.Fatalf("内置管理员应归属租户 1，实际 tenant_id=%d", admin.TenantID)
	}

	// 重复执行必须幂等：守卫计数不因租户过滤而漏判。
	if err := seed.Run(db); err != nil {
		t.Fatalf("重复 seed 失败: %v", err)
	}
	assertCount(t, db, &sysmodel.User{}, 2, "用户（幂等）")
}

func assertCount(t *testing.T, db *gorm.DB, model any, want int64, label string) {
	t.Helper()
	var total int64
	if err := tenant.Skip(db).Model(model).Count(&total).Error; err != nil {
		t.Fatalf("统计%s: %v", label, err)
	}
	if total != want {
		t.Fatalf("%s 数量不符：期望 %d，实际 %d", label, want, total)
	}
}
