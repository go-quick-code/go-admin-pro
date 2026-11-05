package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/config"
	systemdto "github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTxEnv 建立内存 sqlite + 真实 repository/service 装配，供事务语义单测使用。
// 故意不装租户插件：这里只验证事务边界，租户过滤由插件自身的单测覆盖。
func newTxEnv(t *testing.T) (*service.Service, *gorm.DB) {
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

	if err := db.AutoMigrate(
		&model.Tenant{}, &model.Dept{}, &model.Role{}, &model.RoleMenu{}, &model.RoleDept{},
		&model.User{}, &model.UserRole{}, &model.UserPost{},
	); err != nil {
		t.Fatalf("建表: %v", err)
	}

	mgr := jwt.NewManager("test-secret", time.Hour, "go-admin-pro")
	svc := service.New(repository.New(db), mgr, &config.Config{Tenant: config.TenantConfig{DefaultID: 1}})
	return svc, db
}

func txCtx() context.Context {
	return tenant.WithTenantID(context.Background(), 1)
}

func count(t *testing.T, db *gorm.DB, model any, query string, args ...any) int64 {
	t.Helper()
	var total int64
	if err := db.Model(model).Where(query, args...).Count(&total).Error; err != nil {
		t.Fatalf("计数: %v", err)
	}
	return total
}

// 创建用户时角色绑定失败（重复 roleId 触发联合主键冲突），用户行不能残留。
func TestCreateUserRollsBackWhenRoleBindingFails(t *testing.T) {
	svc, db := newTxEnv(t)

	err := svc.CreateUser(txCtx(), &systemdto.UserCreateReq{
		Username: "txuser",
		Password: "123456",
		Nickname: "事务用户",
		RoleIDs:  []int64{7, 7}, // 重复 roleId → system_user_role 主键冲突
		PostIDs:  []int64{1},
	})
	if err == nil {
		t.Fatal("重复 roleId 应导致创建失败")
	}
	if got := count(t, db, &model.User{}, "username = ?", "txuser"); got != 0 {
		t.Fatalf("角色绑定失败后不应残留用户行，实际 %d 行", got)
	}
	if got := count(t, db, &model.UserRole{}, "user_id > 0"); got != 0 {
		t.Fatalf("不应残留用户-角色绑定，实际 %d 行", got)
	}
	if got := count(t, db, &model.UserPost{}, "user_id > 0"); got != 0 {
		t.Fatalf("不应残留用户-岗位绑定，实际 %d 行", got)
	}
}

// 创建用户成功路径：用户、角色、岗位一次落库。
func TestCreateUserCommitsAllSteps(t *testing.T) {
	svc, db := newTxEnv(t)

	if err := svc.CreateUser(txCtx(), &systemdto.UserCreateReq{
		Username: "okuser",
		Password: "123456",
		Nickname: "成功用户",
		RoleIDs:  []int64{7, 8},
		PostIDs:  []int64{3},
	}); err != nil {
		t.Fatalf("创建用户: %v", err)
	}
	if got := count(t, db, &model.User{}, "username = ?", "okuser"); got != 1 {
		t.Fatalf("用户应落库，实际 %d 行", got)
	}
	if got := count(t, db, &model.UserRole{}, "role_id IN ?", []int64{7, 8}); got != 2 {
		t.Fatalf("两条角色绑定应落库，实际 %d 行", got)
	}
	if got := count(t, db, &model.UserPost{}, "post_id = ?", 3); got != 1 {
		t.Fatalf("岗位绑定应落库，实际 %d 行", got)
	}
}

// 创建租户时初始化的最后一步（管理员账号）失败：租户、部门、角色都不能残留。
func TestCreateTenantRollsBackWhenBootstrapFails(t *testing.T) {
	svc, db := newTxEnv(t)

	// 注入：本次环境中 system_user 的插入一律失败（引导流程的最后一个写入步骤）。
	if err := db.Callback().Create().Before("gorm:create").Register("test:fail_user", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_user" {
			_ = tx.AddError(errors.New("注入：租户管理员创建失败"))
		}
	}); err != nil {
		t.Fatalf("注册注入回调: %v", err)
	}

	err := svc.CreateTenant(txCtx(), &systemdto.TenantCreateReq{
		Name:        "注入租户",
		ContactName: "联系人",
		Status:      model.StatusEnable,
	})
	if err == nil {
		t.Fatal("注入失败后创建租户应报错")
	}

	if got := count(t, db, &model.Tenant{}, "name = ?", "注入租户"); got != 0 {
		t.Fatalf("引导失败后不应残留租户（含软删除补偿），实际 %d 行", got)
	}
	if got := count(t, db, &model.Dept{}, "name = ?", "总公司"); got != 0 {
		t.Fatalf("引导失败后不应残留默认部门，实际 %d 行", got)
	}
	if got := count(t, db, &model.Role{}, "code = ?", "super_admin"); got != 0 {
		t.Fatalf("引导失败后不应残留内置角色，实际 %d 行", got)
	}
	if got := count(t, db, &model.User{}, "username = ?", "admin"); got != 0 {
		t.Fatalf("引导失败后不应残留管理员账号，实际 %d 行", got)
	}
}

// 创建租户成功路径：租户 + 默认部门 + 内置角色 + 管理员账号完整落库。
func TestCreateTenantCommitsBootstrap(t *testing.T) {
	svc, db := newTxEnv(t)

	if err := svc.CreateTenant(txCtx(), &systemdto.TenantCreateReq{
		Name:        "完整租户",
		ContactName: "联系人",
		Status:      model.StatusEnable,
	}); err != nil {
		t.Fatalf("创建租户: %v", err)
	}

	var created model.Tenant
	if err := db.Where("name = ?", "完整租户").First(&created).Error; err != nil {
		t.Fatalf("租户应落库: %v", err)
	}
	if got := count(t, db, &model.Dept{}, "name = ? AND tenant_id = ?", "总公司", created.ID); got != 1 {
		t.Fatalf("新租户应有默认部门，实际 %d 行", got)
	}
	if got := count(t, db, &model.Role{}, "code = ? AND tenant_id = ?", "super_admin", created.ID); got != 1 {
		t.Fatalf("新租户应有内置角色，实际 %d 行", got)
	}
	if got := count(t, db, &model.User{}, "username = ? AND tenant_id = ?", "admin", created.ID); got != 1 {
		t.Fatalf("新租户应有管理员账号，实际 %d 行", got)
	}
}

// 角色菜单分配失败（重复 menuId 触发联合主键冲突）：角色本身的修改也要回滚。
func TestUpdateRoleRollsBackWhenMenuAssignmentFails(t *testing.T) {
	svc, db := newTxEnv(t)

	role := &model.Role{TenantID: 1, Name: "原名称", Code: "ops", Status: model.StatusEnable}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("写入角色: %v", err)
	}
	if err := db.Create(&model.RoleMenu{RoleID: role.ID, MenuID: 5}).Error; err != nil {
		t.Fatalf("写入角色菜单: %v", err)
	}

	err := svc.UpdateRole(txCtx(), &systemdto.RoleUpdateReq{
		ID:        role.ID,
		Name:      "改名后",
		Code:      "ops",
		DataScope: model.DataScopeAll, // 非法数据范围会被 400 拒绝（issue #5），这里取合法值
		MenuIDs:   []int64{6, 6},      // 重复 menuId → system_role_menu 主键冲突
	})
	if err == nil {
		t.Fatal("重复 menuId 应导致更新失败")
	}

	var after model.Role
	if err := db.First(&after, role.ID).Error; err != nil {
		t.Fatalf("回读角色: %v", err)
	}
	if after.Name != "原名称" {
		t.Fatalf("菜单分配失败后角色名应回滚，实际 %q", after.Name)
	}
	if got := count(t, db, &model.RoleMenu{}, "role_id = ?", role.ID); got != 1 {
		t.Fatalf("菜单绑定应保持原状 1 行，实际 %d 行", got)
	}
}

// 角色菜单分配成功路径：旧绑定被替换为新绑定。
func TestUpdateRoleReplacesMenus(t *testing.T) {
	svc, db := newTxEnv(t)

	role := &model.Role{TenantID: 1, Name: "菜单角色", Code: "menu-op", Status: model.StatusEnable}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("写入角色: %v", err)
	}
	if err := db.Create(&model.RoleMenu{RoleID: role.ID, MenuID: 5}).Error; err != nil {
		t.Fatalf("写入角色菜单: %v", err)
	}

	if err := svc.UpdateRole(txCtx(), &systemdto.RoleUpdateReq{
		ID:        role.ID,
		Name:      "菜单角色",
		Code:      "menu-op",
		DataScope: model.DataScopeAll,
		MenuIDs:   []int64{6, 7},
	}); err != nil {
		t.Fatalf("更新角色: %v", err)
	}

	var menuIDs []int64
	if err := db.Model(&model.RoleMenu{}).Where("role_id = ?", role.ID).Order("menu_id").Pluck("menu_id", &menuIDs).Error; err != nil {
		t.Fatalf("回读菜单绑定: %v", err)
	}
	if len(menuIDs) != 2 || menuIDs[0] != 6 || menuIDs[1] != 7 {
		t.Fatalf("菜单绑定应替换为 [6 7]，实际 %v", menuIDs)
	}
}
