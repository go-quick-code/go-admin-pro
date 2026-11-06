package seed

import (
	"log/slog"
	"time"

	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// Run 写入初始化数据。表结构由 goose 迁移预先建好，这里只负责数据。
// withDemo 为 true 时在基础种子之后写入 CRM 演示数据（crm_demo.go）；
// 集成测试基座传 false，保证用例数据只来自用例自身。
//
// seed 是系统级写入路径：不携带租户上下文，所有语句显式 tenant.Skip
// （租户插件 fail-closed 后，无上下文访问租户表会被直接拦截）；
// 写入的业务行归属默认租户 1，显式赋值，不依赖插件回填。
func Run(db *gorm.DB, withDemo bool) error {
	if err := seedTenant(db); err != nil {
		return err
	}
	if err := seedMenus(db); err != nil {
		return err
	}
	if err := seedDepts(db); err != nil {
		return err
	}
	if err := seedRoles(db); err != nil {
		return err
	}
	if err := seedPosts(db); err != nil {
		return err
	}
	if err := seedUsers(db); err != nil {
		return err
	}
	if err := seedDicts(db); err != nil {
		return err
	}
	if err := seedConfigs(db); err != nil {
		return err
	}
	if err := seedPoolConfigs(db); err != nil {
		return err
	}
	if err := seedJobs(db); err != nil {
		return err
	}
	if withDemo && seedCRMDemoFunc != nil {
		if err := seedCRMDemoFunc(db); err != nil {
			return err
		}
	}
	return nil
}

// seedCRMDemoFunc 是 CRM 演示数据的写入入口，由 crm_demo.go 的 init 注册。
// 用函数变量而不是直接调用，是为了让代码生成器的编译校验（会剔换真实
// 产品实体及其消费者）可以在不携带 crm_demo.go 的副本上继续编译 seed 包；
// 正常构建里 crm_demo.go 始终存在，行为等价于直接调用。
var seedCRMDemoFunc func(*gorm.DB) error

func seedTenant(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.Tenant{}).Count(&count).Error; err != nil {
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
	if err := tenant.Skip(db).Create(t).Error; err != nil {
		return err
	}
	slog.Info("seed tenant created", "tenantId", 1)
	return nil
}

func seedMenus(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.Menu{}).Count(&count).Error; err != nil {
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
	dir := func(id, parent int64, name, path, icon string, sort int) sysmodel.Menu {
		return sysmodel.Menu{
			BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name,
			Type: sysmodel.MenuTypeDir, ParentID: parent, Path: path, Icon: icon, Sort: sort,
			Visible: true, Status: sysmodel.StatusEnable,
		}
	}

	menus := []sysmodel.Menu{
		menu(1, 0, "仪表盘", "/dashboard", "dashboard/index", "", "Odometer", 0),

		dir(100, 0, "系统管理", "/system", "Setting", 10),
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
		// 简表权限（PR #39 残余风险 C）：simple-list 不再对任意登录用户开放。
		// 用户简表同时供 CRM 负责人下拉使用，演示销售角色在 crm_demo.go 单独绑定。
		btn(1014, 101, "用户简表", "system:user:simple-list", 4),
		btn(1021, 102, "角色新增", "system:role:create", 1),
		btn(1022, 102, "角色修改", "system:role:update", 2),
		btn(1023, 102, "角色删除", "system:role:delete", 3),
		btn(1024, 102, "角色简表", "system:role:simple-list", 4),
		btn(1031, 103, "菜单新增", "system:menu:create", 1),
		btn(1032, 103, "菜单修改", "system:menu:update", 2),
		btn(1033, 103, "菜单删除", "system:menu:delete", 3),
		btn(1041, 104, "部门新增", "system:dept:create", 1),
		btn(1042, 104, "部门修改", "system:dept:update", 2),
		btn(1043, 104, "部门删除", "system:dept:delete", 3),
		btn(1051, 105, "岗位新增", "system:post:create", 1),
		btn(1052, 105, "岗位修改", "system:post:update", 2),
		btn(1053, 105, "岗位删除", "system:post:delete", 3),
		btn(1054, 105, "岗位简表", "system:post:simple-list", 4),
		btn(1061, 106, "字典新增", "system:dict:create", 1),
		btn(1062, 106, "字典修改", "system:dict:update", 2),
		btn(1063, 106, "字典删除", "system:dict:delete", 3),
		btn(1071, 107, "租户新增", "system:tenant:create", 1),
		btn(1072, 107, "租户修改", "system:tenant:update", 2),
		btn(1073, 107, "租户删除", "system:tenant:delete", 3),
		btn(1081, 108, "日志清空", "system:login-log:delete", 1),
		btn(1091, 109, "日志清空", "system:operate-log:delete", 1),

		dir(200, 0, "基础设施", "/infra", "Tools", 20),
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

		// CRM（issue #8 起逐步落地）：菜单挂在动态路由下，组件路径对应 web/src/views。
		// 联系人（issue #9）：无独立页面，作为客户详情的 tab 呈现，
		// 按钮权限码挂客户管理菜单下（可见性也跟随客户的数据范围）。
		dir(400, 0, "CRM", "/crm", "Suitcase", 40),
		menu(401, 400, "客户管理", "customer", "crm/customer/index", "crm:customer:query", "Avatar", 1),
		btn(4011, 401, "客户新增", "crm:customer:create", 1),
		btn(4012, 401, "客户修改", "crm:customer:update", 2),
		btn(4013, 401, "客户删除", "crm:customer:delete", 3),
		btn(4014, 401, "联系人查询", "crm:contact:query", 4),
		btn(4015, 401, "联系人新增", "crm:contact:create", 5),
		btn(4016, 401, "联系人修改", "crm:contact:update", 6),
		btn(4017, 401, "联系人删除", "crm:contact:delete", 7),
		// 公海纵切（issue #13）：公海页面 + 认领按钮，客户管理下加释放/转移按钮。
		// 按钮段让位 #9 的联系人 4014–4017，改用 4030 段（合并 #13 时重排）。
		btn(4030, 401, "客户释放", "crm:customer:release", 8),
		btn(4031, 401, "客户转移", "crm:customer:transfer", 9),
		// 跟进记录（issue #12）：无独立页面，作为客户详情的 tab 呈现，
		// 按钮权限码挂客户管理菜单下（可见性跟随客户的数据范围）；
		// ID 从 4040 起（4014–4017 联系人、4030/4031 释放/转移、402/4021 公海已占用）。
		btn(4040, 401, "跟进查询", "crm:follow-up:query", 10),
		btn(4041, 401, "跟进新增", "crm:follow-up:create", 11),
		btn(4042, 401, "跟进修改", "crm:follow-up:update", 12),
		btn(4043, 401, "跟进删除", "crm:follow-up:delete", 13),
		menu(402, 400, "公海", "pool", "crm/pool/index", "crm:customer:pool:view", "Umbrella", 2),
		btn(4021, 402, "客户认领", "crm:customer:claim", 1),
		// 公海配置（issue #14）：租户级回收天数/认领上限的编辑入口，
		// 按钮码接 4050（4014–4017/4030–4031/402/4021/4040–4043 已占用）。
		btn(4050, 402, "公海配置", "crm:pool-config:update", 2),
		// 产品与产品分类（issue #11）：产品管理目录（4060）+ 产品列表/产品分类两个菜单项。
		// ID 从 4060 起：4014–4017 联系人、4030/4031 释放/转移、4040–4043 跟进、
		// 402/4021/4050 公海已占用；每个菜单项带查询/新增/修改/删除四个按钮码（codegen menus 片段同款）。
		dir(4060, 400, "产品管理", "product", "Goods", 3),
		menu(4061, 4060, "产品列表", "list", "crm/product/index", "crm:product:query", "Goods", 1),
		btn(4063, 4061, "产品查询", "crm:product:query", 1),
		btn(4064, 4061, "产品新增", "crm:product:create", 2),
		btn(4065, 4061, "产品修改", "crm:product:update", 3),
		btn(4066, 4061, "产品删除", "crm:product:delete", 4),
		menu(4062, 4060, "产品分类", "category", "crm/product-category/index", "crm:product-category:query", "FolderOpened", 2),
		btn(4067, 4062, "分类查询", "crm:product-category:query", 1),
		btn(4068, 4062, "分类新增", "crm:product-category:create", 2),
		btn(4069, 4062, "分类修改", "crm:product-category:update", 3),
		btn(4070, 4062, "分类删除", "crm:product-category:delete", 4),
		// 商机（issue #15）：菜单 ID 从 4080 起（4060–4070 产品已占用，grep 去重确认）。
		// 阶段推进是独立动作，挂独立按钮码 crm:opportunity:stage（终态推进 service 拒绝）。
		menu(4080, 400, "商机管理", "opportunity", "crm/opportunity/index", "crm:opportunity:query", "Coin", 4),
		btn(4081, 4080, "商机查询", "crm:opportunity:query", 1),
		btn(4082, 4080, "商机新增", "crm:opportunity:create", 2),
		btn(4083, 4080, "商机修改", "crm:opportunity:update", 3),
		btn(4084, 4080, "商机删除", "crm:opportunity:delete", 4),
		btn(4085, 4080, "商机阶段推进", "crm:opportunity:stage", 5),
		// 合同（issue #16）：菜单 ID 从 4100 起（4060–4070 产品、4080–4085 商机已占用，
		// grep 去重确认）。审批通过与驳回挂独立按钮码 crm:contract:approve
		//（ApprovalService v1 直通实现据此放行，bpm 上线后同码走流程）。
		menu(4100, 400, "合同管理", "contract", "crm/contract/index", "crm:contract:query", "Tickets", 5),
		btn(4101, 4100, "合同查询", "crm:contract:query", 1),
		btn(4102, 4100, "合同新增", "crm:contract:create", 2),
		btn(4103, 4100, "合同修改", "crm:contract:update", 3),
		btn(4104, 4100, "合同删除", "crm:contract:delete", 4),
		btn(4105, 4100, "合同审批", "crm:contract:approve", 5),
		// 回款计划 + 回款记录（issue #18）：菜单 ID 从 4120 起（4060–4070 产品、
		// 4080–4085 商机、4100–4105 合同已占用，grep 去重确认）。
		// 计划与记录共用一个页面（记录以每行「记录」抽屉录入），按钮码共用
		// crm:receivable:query/create/update/delete。
		menu(4120, 400, "回款管理", "receivable", "crm/receivable/index", "crm:receivable:query", "Money", 6),
		btn(4121, 4120, "回款查询", "crm:receivable:query", 1),
		btn(4122, 4120, "回款新增", "crm:receivable:create", 2),
		btn(4123, 4120, "回款修改", "crm:receivable:update", 3),
		btn(4124, 4120, "回款删除", "crm:receivable:delete", 4),
		// Excel 导入导出（issue #20）：按钮码 4160–4166（#19 用 4140–4141，
		// 4080–4085/4100–4105/4120–4124 已占用，grep 去重确认）。
		// 导入/模板挂 :import，导出挂 :export；按钮挂在各实体所属菜单下，
		// 权限判定只看权限码，菜单层级只影响角色分配时的可见性。
		btn(4160, 401, "客户导入", "crm:customer:import", 14),
		btn(4161, 401, "客户导出", "crm:customer:export", 15),
		btn(4162, 401, "联系人导入", "crm:contact:import", 16),
		btn(4163, 4061, "产品导入", "crm:product:import", 5),
		btn(4164, 4061, "产品导出", "crm:product:export", 6),
		btn(4165, 4100, "合同导出", "crm:contract:export", 6),
		btn(4166, 4120, "回款导出", "crm:receivable:export", 5),

		// OA（issue #43）：业务套件根目录（spec §7.2 菜单段 500，权限码为空——
		// 目录不可授权动作）。子菜单与按钮码由各域工单按 500 段号段落地
		//（501 公告、503 请假、504 报销、505 考勤、506 日程、507 会议室、
		// 508 文档），开工前先 grep 本文件去重再取号。
		// OA 业务记录的共享可见范围见 oa/service.VisibilityScopeOf（spec §4.6）。
		dir(500, 0, "OA", "/oa", "Calendar", 50),

		// 工作日历（issue #50）：520 段（协调台账分配），按钮段 5201+（菜单ID×10+n，
		// grep 去重确认 520/5201–5203 未被占用）。查询码挂菜单（月视图入口），
		// 单日/批量/恢复默认三个维护动作各挂独立按钮码。
		menu(520, 500, "工作日历", "workday", "oa/workday/index", "oa:workday:query", "AlarmClock", 1),
		btn(5201, 520, "单日设置", "oa:workday:set", 1),
		btn(5202, 520, "批量设置", "oa:workday:batch", 2),
		btn(5203, 520, "恢复默认", "oa:workday:delete", 3),

		// 请假类型与额度（issue #51）：522 段（协调台账分配，grep 去重确认
		// 522/528 未被占用）。本工单所有管理动作（类型 CRUD/启停/删除、额度
		// 初始化/调整/分页）共用单一管理码 oa:leave:manage（#43 helper 登记、
		// 默认审批流 oa_leave_approval 节点 2 的 HR/管理员码，spec §7.3），
		// 挂菜单本身，无独立按钮码；类型列表与本人余额登录即可（不经权限码）。
		menu(522, 500, "请假类型与额度", "leave", "oa/leave/index", "oa:leave:manage", "List", 2),
		// 我的额度（issue #51）：员工侧页面，权限码为空——登录即可（分配了
		// 菜单的角色可见；后端 GET /oa/leave/balance/mine 同为登录即可）。
		// 我的请假页（含额度入口）由 #52 建立（下方菜单 529）；撤回/取消与
		// 部门可见范围由 #53 扩展。
		menu(528, 500, "我的额度", "leave-mine", "oa/leave/mine", "", "User", 8),

		// 我的请假（issue #52）：529 段（500–549 员工侧空位自取，grep 去重确认
		// 529/5291–5294 未被占用；522 管类型与额度、528 管本人额度，勿重复建）。
		// 页面本身登录即可（后端列表/详情按本人收口），写动作逐码：创建/编辑/
		// 删除/提交（按钮码 = 菜单 ID×10+n）。审批动作走通用待办 512 段。
		menu(529, 500, "我的请假", "leave-request", "oa/leave/request/index", "", "Calendar", 9),
		btn(5291, 529, "请假申请", "oa:leave:create", 1),
		btn(5292, 529, "请假编辑", "oa:leave:update", 2),
		btn(5293, 529, "请假删除", "oa:leave:delete", 3),
		btn(5294, 529, "请假提交", "oa:leave:submit", 4),

		// bpm（issue #44）：bpm 根目录 510 + 流程定义 511，按钮段 5111+（spec §7.2，
		// grep 去重确认 510/511/5111–5114 未被占用；512 待办任务菜单属 #46）。
		// 发布与停用共用 bpm:definition:publish。
		dir(510, 0, "流程中心", "/bpm", "SetLike", 51),
		menu(511, 510, "流程定义", "definition", "bpm/definition/index", "bpm:definition:query", "List", 1),
		btn(5111, 511, "流程新增", "bpm:definition:create", 1),
		btn(5112, 511, "流程修改", "bpm:definition:update", 2),
		btn(5113, 511, "流程删除", "bpm:definition:delete", 3),
		btn(5114, 511, "流程发布", "bpm:definition:publish", 4),

		// 通用待办（issue #46，spec §7.2：512 待办任务，按钮段 5121+，grep 去重
		// 确认 512/5121–5123 未被占用）：页面三 tab（待办/已办/我发起），页面与
		// 查询共用 bpm:task:query；办理按钮 bpm:task:handle 供前端显隐（动作接口
		// 以「当前办理人/发起人」身份收口，不设权限码门槛，spec §5）；实例查询
		// bpm:instance:query 挂管理视图与实例详情的第三类访问者；通知查询
		// notify:message:query 补挂在 512 段——普通用户经角色授权即可用顶栏
		// 铃铛与通知列表（notify 无独立菜单，#45 遗留接点）。
		menu(512, 510, "待办任务", "task", "bpm/task/index", "bpm:task:query", "Ticket", 2),
		btn(5121, 512, "任务办理", "bpm:task:handle", 1),
		btn(5122, 512, "实例查询", "bpm:instance:query", 2),
		btn(5123, 512, "通知查询", "notify:message:query", 3),
	}
	// 仪表盘（issue #19）：菜单 ID 4140（4060–4070 产品、4080–4085 商机、
	// 4100–4105 合同、4120–4124 回款已占用，grep 去重确认）。
	// 页面与查询按钮共用权限码 crm:dashboard:view（API 路由同码）。
	// 菜单名与系统首页同为「仪表盘」，组件名单独设为 CrmDashboard：
	// 前端动态路由以 componentName 作 route name，重名会让 Vue Router 告警并互相覆盖。
	crmDashboard := menu(4140, 400, "仪表盘", "dashboard", "crm/dashboard/index", "crm:dashboard:view", "DataLine", 0)
	crmDashboard.ComponentName = "CrmDashboard"
	menus = append(menus,
		crmDashboard,
		btn(4141, 4140, "仪表盘查询", "crm:dashboard:view", 1),
	)
	if err := db.Create(&menus).Error; err != nil {
		return err
	}
	slog.Info("seed menus created", "count", len(menus))
	return nil
}

func seedDepts(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.Dept{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	depts := []sysmodel.Dept{
		{BaseEntity: sysmodel.BaseEntity{ID: 100}, TenantID: 1, Name: "总公司", ParentID: 0, Sort: 0, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 101}, TenantID: 1, Name: "研发部", ParentID: 100, Sort: 1, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 102}, TenantID: 1, Name: "市场部", ParentID: 100, Sort: 2, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 103}, TenantID: 1, Name: "财务部", ParentID: 100, Sort: 3, Status: sysmodel.StatusEnable},
	}
	return tenant.Skip(db).Create(&depts).Error
}

func seedRoles(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.Role{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	roles := []sysmodel.Role{
		{
			BaseEntity: sysmodel.BaseEntity{ID: 1}, TenantID: 1, Name: "超级管理员", Code: "super_admin",
			Sort: 0, DataScope: sysmodel.DataScopeAll, Status: sysmodel.StatusEnable, Type: 1,
			Remark: "内置角色，拥有全部权限",
		},
		{
			BaseEntity: sysmodel.BaseEntity{ID: 2}, TenantID: 1, Name: "普通角色", Code: "common",
			Sort: 1, DataScope: sysmodel.DataScopeSelf, Status: sysmodel.StatusEnable, Type: 2,
			Remark: "示例角色：仅本人数据，可查看用户列表",
		},
	}
	if err := tenant.Skip(db).Create(&roles).Error; err != nil {
		return err
	}
	// 普通角色：仪表盘 + 系统管理目录 + 用户管理页面（无增删改按钮）。
	// 用户管理页渲染需要三条简表（角色/岗位下拉与用户选项），因此补绑
	// simple-list 按钮码（1014/1024/1054）；自定义角色未授权时前端会降级为空数组。
	roleMenus := []sysmodel.RoleMenu{
		{RoleID: 2, MenuID: 1},
		{RoleID: 2, MenuID: 100},
		{RoleID: 2, MenuID: 101},
		{RoleID: 2, MenuID: 104},
		{RoleID: 2, MenuID: 1014},
		{RoleID: 2, MenuID: 1024},
		{RoleID: 2, MenuID: 1054},
	}
	return tenant.Skip(db).Create(&roleMenus).Error
}

func seedPosts(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.Post{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	posts := []sysmodel.Post{
		{BaseEntity: sysmodel.BaseEntity{ID: 1}, TenantID: 1, Name: "董事长", Code: "chairman", Sort: 0, Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 2}, TenantID: 1, Name: "项目经理", Code: "project_manager", Sort: 1, Status: sysmodel.StatusEnable},
	}
	return tenant.Skip(db).Create(&posts).Error
}

func seedUsers(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.User{}).Count(&count).Error; err != nil {
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
	if err := tenant.Skip(db).Create(&users).Error; err != nil {
		return err
	}
	userRoles := []sysmodel.UserRole{
		{UserID: 1, RoleID: 1},
		{UserID: 2, RoleID: 2},
	}
	userPosts := []sysmodel.UserPost{{UserID: 2, PostID: 2}}
	if err := tenant.Skip(db).Create(&userRoles).Error; err != nil {
		return err
	}
	slog.Info("seed users created", "admin", "admin/123456", "demo", "demo/123456")
	return tenant.Skip(db).Create(&userPosts).Error
}

func seedDicts(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.DictType{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	types := []sysmodel.DictType{
		{BaseEntity: sysmodel.BaseEntity{ID: 1}, TenantID: 1, Name: "用户性别", Type: "sys_user_sex", Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 2}, TenantID: 1, Name: "通用状态", Type: "sys_normal_disable", Status: sysmodel.StatusEnable},
		{BaseEntity: sysmodel.BaseEntity{ID: 3}, TenantID: 1, Name: "CRM 产品单位", Type: "crm_product_unit", Status: sysmodel.StatusEnable},
	}
	if err := tenant.Skip(db).Create(&types).Error; err != nil {
		return err
	}
	data := []sysmodel.DictData{
		{TenantID: 1, DictType: "sys_user_sex", Label: "男", Value: "1", Sort: 1, Status: sysmodel.StatusEnable, ColorType: "primary"},
		{TenantID: 1, DictType: "sys_user_sex", Label: "女", Value: "2", Sort: 2, Status: sysmodel.StatusEnable, ColorType: "danger"},
		{TenantID: 1, DictType: "sys_user_sex", Label: "未知", Value: "0", Sort: 3, Status: sysmodel.StatusEnable, ColorType: "info"},
		{TenantID: 1, DictType: "sys_normal_disable", Label: "开启", Value: "0", Sort: 1, Status: sysmodel.StatusEnable, ColorType: "success"},
		{TenantID: 1, DictType: "sys_normal_disable", Label: "停用", Value: "1", Sort: 2, Status: sysmodel.StatusEnable, ColorType: "danger"},
		{TenantID: 1, DictType: "crm_product_unit", Label: "件", Value: "件", Sort: 1, Status: sysmodel.StatusEnable},
		{TenantID: 1, DictType: "crm_product_unit", Label: "台", Value: "台", Sort: 2, Status: sysmodel.StatusEnable},
		{TenantID: 1, DictType: "crm_product_unit", Label: "套", Value: "套", Sort: 3, Status: sysmodel.StatusEnable},
		{TenantID: 1, DictType: "crm_product_unit", Label: "个", Value: "个", Sort: 4, Status: sysmodel.StatusEnable},
		{TenantID: 1, DictType: "crm_product_unit", Label: "箱", Value: "箱", Sort: 5, Status: sysmodel.StatusEnable},
		{TenantID: 1, DictType: "crm_product_unit", Label: "人天", Value: "人天", Sort: 6, Status: sysmodel.StatusEnable},
	}
	return tenant.Skip(db).Create(&data).Error
}

func seedConfigs(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&inframodel.Config{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	configs := []inframodel.Config{
		{TenantID: 1, Category: "system", Name: "系统名称", Key: "system.name", Value: "go-admin-pro", Type: "string", Visible: true, Remark: "显示在登录页与管理后台"},
		{TenantID: 1, Category: "system", Name: "用户初始密码", Key: "user.init.password", Value: "123456", Type: "string", Visible: false, Remark: "新增用户默认密码"},
	}
	return tenant.Skip(db).Create(&configs).Error
}

func seedJobs(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&inframodel.Job{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	jobs := []inframodel.Job{
		{
			TenantID:       1,
			Name:           "演示任务",
			HandlerName:    "demoJob",
			HandlerParam:   "hello go-admin-pro",
			CronExpression: "0 */5 * * * *",
			RetryCount:     0,
			Status:         inframodel.JobStatusPaused,
			Remark:         "每 5 分钟执行一次的演示任务，默认暂停",
		},
		// 公海自动回收（issue #14）：默认暂停。
		// 理由：回收是批量清空负责人归属的破坏性动作，触发前管理员应先
		// 按租户确认公海配置（回收天数，默认 30）。合同排除已由 #16 接入
		//（有未作废合同的客户不被回收），但回收仍属破坏性动作，确认配置后再启用。
		{
			TenantID:       1,
			Name:           "公海自动回收",
			HandlerName:    "recyclePoolCustomers",
			CronExpression: "0 0 3 * * *",
			RetryCount:     0,
			Status:         inframodel.JobStatusPaused,
			Remark:         "每天 03:00 按租户公海配置回收超期未跟进的客户（清空负责人并写历史），默认暂停：请先确认各租户回收天数配置",
		},
	}
	return tenant.Skip(db).Create(&jobs).Error
}

// seedPoolConfigs 写入租户 1 的公海配置默认行（issue #14）：
// TenantID 显式赋值（seed 无租户上下文，不依赖插件回填），取值与
// crm/service 的默认值（DefaultRecycleDays/DefaultClaimLimit，规格 §4.2
// 缺省 30/50）一致。其余租户不预置——配置行缺失时业务层按同一默认值执行，
// 首次编辑才落行。
func seedPoolConfigs(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&crmmodel.PoolConfig{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	row := &crmmodel.PoolConfig{TenantID: 1, RecycleDays: 30, ClaimLimit: 50}
	return tenant.Skip(db).Create(row).Error
}
