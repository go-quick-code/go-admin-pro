package seed_test

import (
	"testing"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
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
		&crmmodel.PoolConfig{},
		&bpmmodel.ProcessDefinition{},
		&oamodel.AttendanceShift{}, &oamodel.AttendanceGroup{},
	); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}

	if err := seed.Run(db, false); err != nil {
		t.Fatalf("fail-closed 租户插件下 seed 失败: %v", err)
	}

	assertCount(t, db, &sysmodel.Tenant{}, 1, "租户")
	assertCount(t, db, &sysmodel.User{}, 2, "用户")
	assertCount(t, db, &sysmodel.Dept{}, 4, "部门")
	assertCount(t, db, &sysmodel.Role{}, 2, "角色")
	assertCount(t, db, &sysmodel.Post{}, 2, "岗位")
	assertCount(t, db, &sysmodel.DictType{}, 4, "字典类型")
	// 11 条基础字典项 + 报销类别 5 条（issue #54，oa_expense_category）。
	assertCount(t, db, &sysmodel.DictData{}, 16, "字典数据")
	assertCount(t, db, &inframodel.Config{}, 2, "参数配置")
	// 定时任务：演示 + 公海回收 + 考勤日结果（issue #61）。
	assertCount(t, db, &inframodel.Job{}, 3, "定时任务")
	assertCount(t, db, &crmmodel.PoolConfig{}, 1, "公海配置")
	// 默认审批流程（spec §7.3）：crm_contract_approval v1（issue #47）与
	// oa_expense_approval v1（issue #55）均已发布。
	assertCount(t, db, &bpmmodel.ProcessDefinition{}, 2, "默认流程定义")
	assertDefaultExpenseDefinition(t, db)
	// 考勤默认配置（issue #60，spec §7.3）：默认班次 09:00–18:00 宽限 5 分钟 +
	// 默认考勤组（绑定默认班次）。
	assertCount(t, db, &oamodel.AttendanceShift{}, 1, "默认班次")
	assertCount(t, db, &oamodel.AttendanceGroup{}, 1, "默认考勤组")
	var defaultShift oamodel.AttendanceShift
	if err := tenant.Skip(db).First(&defaultShift, 1).Error; err != nil {
		t.Fatalf("读取默认班次: %v", err)
	}
	if defaultShift.StartTime != "09:00" || defaultShift.EndTime != "18:00" || defaultShift.GraceMinutes != 5 {
		t.Fatalf("默认班次应为 09:00–18:00/宽限 5，实际 %+v", defaultShift)
	}

	var admin sysmodel.User
	if err := tenant.Skip(db).First(&admin, 1).Error; err != nil {
		t.Fatalf("读取内置管理员: %v", err)
	}
	if admin.TenantID != 1 {
		t.Fatalf("内置管理员应归属租户 1，实际 tenant_id=%d", admin.TenantID)
	}

	// 重复执行必须幂等：守卫计数不因租户过滤而漏判。
	if err := seed.Run(db, false); err != nil {
		t.Fatalf("重复 seed 失败: %v", err)
	}
	assertCount(t, db, &sysmodel.User{}, 2, "用户（幂等）")
	assertCount(t, db, &bpmmodel.ProcessDefinition{}, 2, "默认流程定义（幂等）")
	assertDefaultExpenseDefinition(t, db)
	assertCount(t, db, &oamodel.AttendanceShift{}, 1, "默认班次（幂等）")
	assertCount(t, db, &oamodel.AttendanceGroup{}, 1, "默认考勤组（幂等）")
}

// assertDefaultExpenseDefinition 断言报销默认流程（issue #55，spec §4.9）：
// oa_expense_approval v1 已发布，节点 1 为发起人部门负责人、节点 2 为
// oa:expense:manage 权限码候选池。
func assertDefaultExpenseDefinition(t *testing.T, db *gorm.DB) {
	t.Helper()
	var def bpmmodel.ProcessDefinition
	if err := tenant.Skip(db).
		Where("tenant_id = ? AND `key` = ?", 1, "oa_expense_approval").
		First(&def).Error; err != nil {
		t.Fatalf("读取报销默认流程定义: %v", err)
	}
	if def.Version != 1 || def.Status != bpmmodel.DefinitionStatusPublished || def.PublishedAt == nil {
		t.Fatalf("报销默认流程应为已发布 v1：%+v", def)
	}
	if def.BusinessType != "oa_expense_approval" {
		t.Fatalf("报销默认流程 business_type 不符：%q", def.BusinessType)
	}
	if len(def.Nodes) != 2 {
		t.Fatalf("报销默认流程应有两个节点，实际 %+v", def.Nodes)
	}
	if def.Nodes[0].AssigneeType != bpmmodel.AssigneeInitiatorDeptLeader {
		t.Fatalf("节点 1 应为发起人部门负责人：%+v", def.Nodes[0])
	}
	if def.Nodes[1].AssigneeType != bpmmodel.AssigneePermission || def.Nodes[1].AssigneeValue != "oa:expense:manage" {
		t.Fatalf("节点 2 应为 oa:expense:manage 权限码：%+v", def.Nodes[1])
	}
	if def.Nodes[1].Name == "" {
		t.Fatalf("节点 2 缺少名称：%+v", def.Nodes[1])
	}
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
