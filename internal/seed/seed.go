package seed

import (
	"context"
	"log/slog"
	"time"

	inframodel "github.com/lautung/go-admin-pro/internal/module/infra/model"
	sysmodel "github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/service"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// Run 写入初始化数据。表结构由 goose 迁移预先建好，这里只负责数据。
func Run(db *gorm.DB) error {
	ctx := tenant.WithTenantID(context.Background(), 1)
	if err := seedTenant(db, ctx); err != nil {
		return err
	}
	if err := seedMenus(db); err != nil {
		return err
	}
	if err := seedDepts(db, ctx); err != nil {
		return err
	}
	if err := seedRoles(db, ctx); err != nil {
		return err
	}
	if err := seedPosts(db, ctx); err != nil {
		return err
	}
	if err := seedUsers(db, ctx); err != nil {
		return err
	}
	if err := seedDicts(db, ctx); err != nil {
		return err
	}
	if err := seedConfigs(db, ctx); err != nil {
		return err
	}
	if err := seedJobs(db, ctx); err != nil {
		return err
	}
	return nil
}

func seedTenant(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&sysmodel.Tenant{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	expire := time.Now().AddDate(10, 0, 0)
	t := &sysmodel.Tenant{
		BaseEntity:    sysmodel.BaseEntity{ID: 1},
		Name:          "go-admin-pro",
		ContactName:   "管理员",
		ContactMobile: "13800000000",
		Status:        sysmodel.StatusEnable,
		Domain:        "localhost",
		ExpireTime:    &expire,
		AccountCount:  999,
		Remark:        "内置租户",
	}
	if err := db.WithContext(ctx).Create(t).Error; err != nil {
		return err
	}
	slog.Info("seed tenant created", "tenantId", 1)
	return nil
}

func seedMenus(db *gorm.DB) error {
	var count int64
	if err := db.Model(&sysmodel.Menu{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	btn := func(id, parent int64, name, perm string, sort int) sysmodel.Menu {
		return sysmodel.Menu{
			BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name, Permission: perm,
			Type: sysmodel.MenuTypeButton, ParentID: parent, Sort: sort,
			Visible: true, Status: sysmodel.StatusEnable,
		}
	}
	menu := func(id, parent int64, name, path, component, perm, icon string, sort int) sysmodel.Menu {
		return sysmodel.Menu{
			BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name, Permission: perm,
			Type: sysmodel.MenuTypeMenu, ParentID: parent, Path: path, Component: component,
			ComponentName: name, Icon: icon, Sort: sort, Visible: true,
			Status: sysmodel.StatusEnable, KeepAlive: true,
		}
	}
	dir := func(id int64, name, path, icon string, sort int) sysmodel.Menu {
		return sysmodel.Menu{
			BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name,
			Type: sysmodel.MenuTypeDir, Path: path, Icon: icon, Sort: sort,
			Visible: true, Status: sysmodel.StatusEnable,
		}
	}

	menus := []sysmodel.Menu{
		menu(1, 0, "仪表盘", "/dashboard", "dashboard/index", "", "Odometer", 0),

		dir(100, "系统管理", "/system", "Setting", 10),
		menu(101, 100, "用户管理", "user", "system/user/index", "system:user:query", "User", 1),
		menu(102, 100, "角色管理", "role", "system/role/index", "system:role:query", "UserFilled", 2),
		menu(103, 100, "菜单管理", "menu", "system/menu/index", "system:menu:query", "Menu", 3),
		menu(104, 100, "部门管理", "dept", "system/dept/index", "system:dept:query", "OfficeBuilding", 4),
		menu(105, 100, "岗位管理", "post", "system/post/index", "system:post:query", "Postcard", 5),
		menu(106, 100, "字典管理", "dict", "system/dict/index", "system:dict:query", "Collection", 6),
		menu(107, 100, "租户管理", "tenant", "system/tenant/index", "system:tenant:query", "Shop", 7),
		menu(108, 100, "登录日志", "login-log", "system/login-log/index", "system:login-log:query", "Document", 8),
		menu(109, 100, "操作日志", "operate-log", "system/operate-log/index", "system:operate-log:query", "Tickets", 9),

		btn(1011, 101, "用户新增", "system:user:create", 1),
		btn(1012, 101, "用户修改", "system:user:update", 2),
		btn(1013, 101, "用户删除", "system:user:delete", 3),
		btn(1021, 102, "角色新增", "system:role:create", 1),
		btn(1022, 102, "角色修改", "system:role:update", 2),
		btn(1023, 102, "角色删除", "system:role:delete", 3),
		btn(1031, 103, "菜单新增", "system:menu:create", 1),
		btn(1032, 103, "菜单修改", "system:menu:update", 2),
		btn(1033, 103, "菜单删除", "system:menu:delete", 3),
		btn(1041, 104, "部门新增", "system:dept:create", 1),
		btn(1042, 104, "部门修改", "system:dept:update", 2),
		btn(1043, 104, "部门删除", "system:dept:delete", 3),
		btn(1051, 105, "岗位新增", "system:post:create", 1),
		btn(1052, 105, "岗位修改", "system:post:update", 2),
		btn(1053, 105, "岗位删除", "system:post:delete", 3),
		btn(1061, 106, "字典新增", "system:dict:create", 1),
		btn(1062, 106, "字典修改", "system:dict:update", 2),
		btn(1063, 106, "字典删除", "system:dict:delete", 3),
		btn(1071, 107, "租户新增", "system:tenant:create", 1),
		btn(1072, 107, "租户修改", "system:tenant:update", 2),
		btn(1073, 107, "租户删除", "system:tenant:delete", 3),
		btn(1081, 108, "日志清空", "system:login-log:delete", 1),
		btn(1091, 109, "日志清空", "system:operate-log:delete", 1),

		dir(200, "基础设施", "/infra", "Tools", 20),
		menu(201, 200, "文件管理", "file", "infra/file/index", "infra:file:query", "FolderOpened", 1),
		menu(202, 200, "参数配置", "config", "infra/config/index", "infra:config:query", "SetUp", 2),
		menu(203, 200, "定时任务", "job", "infra/job/index", "infra:job:query", "Timer", 3),

		btn(2011, 201, "文件上传", "infra:file:upload", 1),
		btn(2012, 201, "文件删除", "infra:file:delete", 2),
		btn(2021, 202, "参数新增", "infra:config:create", 1),
		btn(2022, 202, "参数修改", "infra:config:update", 2),
		btn(2023, 202, "参数删除", "infra:config:delete", 3),
		btn(2031, 203, "任务新增", "infra:job:create", 1),
		btn(2032, 203, "任务修改", "infra:job:update", 2),
		btn(2033, 203, "任务删除", "infra:job:delete", 3),

		menu(300, 0, "代码生成", "/codegen", "infra/codegen/index", "infra:codegen:query", "MagicStick", 30),
		btn(3001, 300, "生成代码", "infra:codegen:generate", 1),
	}
	if err := db.Create(&menus).Error; err != nil {
		return err
	}
	slog.Info("seed menus created", "count", len(menus))
	return nil
}

func seedDepts(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&sysmodel.Dept{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	depts := []sysmodel.Dept{
		{BaseEntity: sysmodel.BaseEntity{ID: 100}, Name: "总公司", ParentID: 0, Sort: 0, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 101}, Name: "研发部", ParentID: 100, Sort: 1, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 102}, Name: "市场部", ParentID: 100, Sort: 2, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 103}, Name: "财务部", ParentID: 100, Sort: 3, Status: sysmodel.StatusEnable},
	}
	return db.WithContext(ctx).Create(&depts).Error
}

func seedRoles(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&sysmodel.Role{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	roles := []sysmodel.Role{
		{
			BaseEntity: sysmodel.BaseEntity{ID: 1}, Name: "超级管理员", Code: "super_admin",
			Sort: 0, DataScope: sysmodel.DataScopeAll, Status: sysmodel.StatusEnable, Type: 1,
			Remark: "内置角色，拥有全部权限",
		},
		{
			BaseEntity: sysmodel.BaseEntity{ID: 2}, Name: "普通角色", Code: "common",
			Sort: 1, DataScope: sysmodel.DataScopeSelf, Status: sysmodel.StatusEnable, Type: 2,
			Remark: "示例角色：仅本人数据，可查看用户列表",
		},
	}
	if err := db.WithContext(ctx).Create(&roles).Error; err != nil {
		return err
	}
	// 普通角色：仪表盘 + 系统管理目录 + 用户管理页面（无增删改按钮）。
	roleMenus := []sysmodel.RoleMenu{
		{RoleID: 2, MenuID: 1},
		{RoleID: 2, MenuID: 100},
		{RoleID: 2, MenuID: 101},
		{RoleID: 2, MenuID: 104},
	}
	return db.WithContext(ctx).Create(&roleMenus).Error
}

func seedPosts(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&sysmodel.Post{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	posts := []sysmodel.Post{
		{BaseEntity: sysmodel.BaseEntity{ID: 1}, Name: "董事长", Code: "chairman", Sort: 0, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 2}, Name: "项目经理", Code: "project_manager", Sort: 1, Status: sysmodel.StatusEnable},
	}
	return db.WithContext(ctx).Create(&posts).Error
}

func seedUsers(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&sysmodel.User{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := service.HashPassword("123456")
	if err != nil {
		return err
	}
	users := []sysmodel.User{
		{
			BaseEntity: sysmodel.BaseEntity{ID: 1}, TenantID: 1, Username: "admin", Password: hash,
			Nickname: "超级管理员", DeptID: 100, Email: "admin@example.com", Mobile: "13800000001",
			Status: sysmodel.StatusEnable, Remark: "内置超级管理员",
		},
		{
			BaseEntity: sysmodel.BaseEntity{ID: 2}, TenantID: 1, Username: "demo", Password: hash,
			Nickname: "演示用户", DeptID: 101, Email: "demo@example.com", Mobile: "13800000002",
			Status: sysmodel.StatusEnable, Remark: "普通角色演示账号",
		},
	}
	if err := db.WithContext(ctx).Create(&users).Error; err != nil {
		return err
	}
	userRoles := []sysmodel.UserRole{
		{UserID: 1, RoleID: 1},
		{UserID: 2, RoleID: 2},
	}
	userPosts := []sysmodel.UserPost{{UserID: 2, PostID: 2}}
	if err := db.WithContext(ctx).Create(&userRoles).Error; err != nil {
		return err
	}
	slog.Info("seed users created", "admin", "admin/123456", "demo", "demo/123456")
	return db.WithContext(ctx).Create(&userPosts).Error
}

func seedDicts(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&sysmodel.DictType{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	types := []sysmodel.DictType{
		{BaseEntity: sysmodel.BaseEntity{ID: 1}, Name: "用户性别", Type: "sys_user_sex", Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 2}, Name: "通用状态", Type: "sys_normal_disable", Status: sysmodel.StatusEnable},
	}
	if err := db.WithContext(ctx).Create(&types).Error; err != nil {
		return err
	}
	data := []sysmodel.DictData{
		{DictType: "sys_user_sex", Label: "男", Value: "1", Sort: 1, Status: sysmodel.StatusEnable, ColorType: "primary"},
		{DictType: "sys_user_sex", Label: "女", Value: "2", Sort: 2, Status: sysmodel.StatusEnable, ColorType: "danger"},
		{DictType: "sys_user_sex", Label: "未知", Value: "0", Sort: 3, Status: sysmodel.StatusEnable, ColorType: "info"},
		{DictType: "sys_normal_disable", Label: "开启", Value: "0", Sort: 1, Status: sysmodel.StatusEnable, ColorType: "success"},
		{DictType: "sys_normal_disable", Label: "停用", Value: "1", Sort: 2, Status: sysmodel.StatusEnable, ColorType: "danger"},
	}
	return db.WithContext(ctx).Create(&data).Error
}

func seedConfigs(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&inframodel.Config{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	configs := []inframodel.Config{
		{Category: "system", Name: "系统名称", Key: "system.name", Value: "go-admin-pro", Type: "string", Visible: true, Remark: "显示在登录页与管理后台"},
		{Category: "system", Name: "用户初始密码", Key: "user.init.password", Value: "123456", Type: "string", Visible: false, Remark: "新增用户默认密码"},
	}
	return db.WithContext(ctx).Create(&configs).Error
}

func seedJobs(db *gorm.DB, ctx context.Context) error {
	var count int64
	if err := db.WithContext(ctx).Model(&inframodel.Job{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	job := &inframodel.Job{
		Name:           "演示任务",
		HandlerName:    "demoJob",
		HandlerParam:   "hello go-admin-pro",
		CronExpression: "0 */5 * * * *",
		RetryCount:     0,
		Status:         inframodel.JobStatusPaused,
		Remark:         "每 5 分钟执行一次的演示任务，默认暂停",
	}
	return db.WithContext(ctx).Create(job).Error
}
