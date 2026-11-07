package seed_test

import (
	"testing"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	msgmodel "github.com/go-quick-code/go-admin-pro/internal/module/message/model"
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
		&sysmodel.TenantPackage{},
		&inframodel.Config{}, &inframodel.Job{},
		&msgmodel.Channel{}, &msgmodel.Template{}, &sysmodel.Area{},
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
	// 内置普通角色只授基础菜单（spec §7.3，issue #64 review / PR #66 review）：
	//   - 待办菜单 512 与办理按钮 5121（部门负责人打开 /bpm/task 不再 403）、
	//     请假/报销查询码（5297/5236）与通知查询（5123）必须授予；
	//   - 考勤管理面 533/5331/5332 不再授予（HR 视角由 seed-demo 的 hr 角色承载）。
	assertRoleMenus(t, db, 2, []int64{512, 5121, 5297, 5236, 5123}, true, "普通角色")
	assertRoleMenus(t, db, 2, []int64{533, 5331, 5332}, false, "普通角色")
	// 字典类型：sys_user_sex / sys_normal_disable / crm_product_unit /
	// oa_expense_category（issue #54）+ ERP 三类（issue #90：审核状态、
	// 其它入库类型、其它出库类型）。
	assertCount(t, db, &sysmodel.DictType{}, 7, "字典类型")
	// 11 条基础字典项 + 报销类别 5 条（issue #54）+ ERP 12 条
	//（issue #90：审核 2 + 入库 6 + 出库 4）。
	assertCount(t, db, &sysmodel.DictData{}, 28, "字典数据")
	// 默认「标准版」套餐（issue #69）：全部启用目录/菜单，配额不限。
	assertCount(t, db, &sysmodel.TenantPackage{}, 1, "默认套餐")
	assertCount(t, db, &inframodel.Config{}, 2, "参数配置")
	// 定时任务：演示 + 公海回收 + 考勤日结果（issue #61）+ 会议提醒（#57）
	// + 访问日志清理（issue #80，每日 03:00 按保留期清理）。
	assertCount(t, db, &inframodel.Job{}, 5, "定时任务")
	assertCount(t, db, &crmmodel.PoolConfig{}, 1, "公海配置")
	// 消息默认数据（issue #72）：log provider 的短信/邮件渠道 + 两条欢迎模板。
	assertCount(t, db, &msgmodel.Channel{}, 2, "消息渠道")
	assertCount(t, db, &msgmodel.Template{}, 2, "消息模板")
	// 默认审批流程（spec §7.3）：crm_contract_approval v1（issue #47）、
	// oa_leave_approval v1（issue #63 收口）与 oa_expense_approval v1（#55）均已发布。
	assertCount(t, db, &bpmmodel.ProcessDefinition{}, 3, "默认流程定义")
	assertDefaultLeaveDefinition(t, db)
	assertDefaultExpenseDefinition(t, db)
	// 定时任务状态（issue #63 收口 GORM 零值 + default:1 导致「默认启用」被写成暂停）：
	// 演示任务与公海回收默认暂停，考勤日结果与会议提醒默认启用。
	assertJobStatus(t, db, "demoJob", inframodel.JobStatusPaused)
	assertJobStatus(t, db, "recyclePoolCustomers", inframodel.JobStatusPaused)
	assertJobStatus(t, db, "oaAttendanceDailyResult", inframodel.JobStatusRunning)
	assertJobStatus(t, db, "oaRoomBookingReminder", inframodel.JobStatusRunning)
	assertJobStatus(t, db, "cleanAccessLogs", inframodel.JobStatusRunning)
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
	assertCount(t, db, &bpmmodel.ProcessDefinition{}, 3, "默认流程定义（幂等）")
	assertCount(t, db, &sysmodel.TenantPackage{}, 1, "默认套餐（幂等）")
	assertDefaultLeaveDefinition(t, db)
	assertDefaultExpenseDefinition(t, db)
	assertJobStatus(t, db, "oaAttendanceDailyResult", inframodel.JobStatusRunning)
	assertJobStatus(t, db, "oaRoomBookingReminder", inframodel.JobStatusRunning)
	assertJobStatus(t, db, "cleanAccessLogs", inframodel.JobStatusRunning)
	assertCount(t, db, &oamodel.AttendanceShift{}, 1, "默认班次（幂等）")
	assertCount(t, db, &oamodel.AttendanceGroup{}, 1, "默认考勤组（幂等）")
	assertCount(t, db, &msgmodel.Channel{}, 2, "消息渠道（幂等）")
	assertCount(t, db, &msgmodel.Template{}, 2, "消息模板（幂等）")
	// 行政区划（issue #82，spec §4.8.2）：省市两级全量 377 行（34 省 + 343 市）。
	assertCount(t, db, &sysmodel.Area{}, 377, "地区（省市两级）")
}

// assertDefaultLeaveDefinition 断言请假默认流程（issue #63 收口，spec §4.8）：
// oa_leave_approval v1 已发布，节点 1 为发起人部门负责人、节点 2 为
// oa:leave:manage 权限码候选池。
func assertDefaultLeaveDefinition(t *testing.T, db *gorm.DB) {
	t.Helper()
	var def bpmmodel.ProcessDefinition
	if err := tenant.Skip(db).
		Where("tenant_id = ? AND `key` = ?", 1, "oa_leave_approval").
		First(&def).Error; err != nil {
		t.Fatalf("读取请假默认流程定义: %v", err)
	}
	if def.Version != 1 || def.Status != bpmmodel.DefinitionStatusPublished || def.PublishedAt == nil {
		t.Fatalf("请假默认流程应为已发布 v1：%+v", def)
	}
	if def.BusinessType != "oa_leave_approval" {
		t.Fatalf("请假默认流程 business_type 不符：%q", def.BusinessType)
	}
	if len(def.Nodes) != 2 {
		t.Fatalf("请假默认流程应有两个节点，实际 %+v", def.Nodes)
	}
	if def.Nodes[0].AssigneeType != bpmmodel.AssigneeInitiatorDeptLeader {
		t.Fatalf("节点 1 应为发起人部门负责人：%+v", def.Nodes[0])
	}
	if def.Nodes[1].AssigneeType != bpmmodel.AssigneePermission || def.Nodes[1].AssigneeValue != "oa:leave:manage" {
		t.Fatalf("节点 2 应为 oa:leave:manage 权限码：%+v", def.Nodes[1])
	}
}

// assertJobStatus 断言种子任务的启用状态（GORM 零值 + default:1 的回归守卫）。
func assertJobStatus(t *testing.T, db *gorm.DB, handlerName string, want int8) {
	t.Helper()
	var job inframodel.Job
	if err := tenant.Skip(db).Where("`handler_name` = ?", handlerName).First(&job).Error; err != nil {
		t.Fatalf("读取定时任务 %s: %v", handlerName, err)
	}
	if job.Status != want {
		t.Fatalf("定时任务 %s 状态应为 %d，实际 %d（0 启用 / 1 暂停）", handlerName, want, job.Status)
	}
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

// assertRoleMenus 断言角色对指定菜单的授予/未授予状态（issue #64 review：
// 普通角色权限收敛的回归守卫）。
func assertRoleMenus(t *testing.T, db *gorm.DB, roleID int64, menuIDs []int64, want bool, label string) {
	t.Helper()
	for _, menuID := range menuIDs {
		var total int64
		if err := tenant.Skip(db).Model(&sysmodel.RoleMenu{}).
			Where("role_id = ? AND menu_id = ?", roleID, menuID).
			Count(&total).Error; err != nil {
			t.Fatalf("统计%s菜单 %d: %v", label, menuID, err)
		}
		got := total > 0
		if got != want {
			t.Fatalf("%s菜单 %d 授予状态不符：期望 %v，实际 %v", label, menuID, want, got)
		}
	}
}
