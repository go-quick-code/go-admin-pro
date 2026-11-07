package seed

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	msgmodel "github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
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
	if err := seedTenantPackages(db); err != nil {
		return err
	}
	if err := seedBPMDefinitions(db); err != nil {
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
	if err := seedAttendanceDefaults(db); err != nil {
		return err
	}
	if err := seedDicts(db); err != nil {
		return err
	}
	if err := seedExpenseCategoryDict(db); err != nil {
		return err
	}
	// ERP 字典（issue #90，spec §7）：审核状态 + 其它入库/出库类型。
	if err := seedErpDicts(db); err != nil {
		return err
	}
	if err := seedConfigs(db); err != nil {
		return err
	}
	if err := seedMessages(db); err != nil {
		return err
	}
	if err := seedPoolConfigs(db); err != nil {
		return err
	}
	if err := seedJobs(db); err != nil {
		return err
	}
	// 行政区划种子（issue #82，spec §4.8.2）：省市两级，表为空时幂等写入。
	if err := Areas(db); err != nil {
		return err
	}
	if withDemo {
		if seedCRMDemoFunc != nil {
			if err := seedCRMDemoFunc(db); err != nil {
				return err
			}
		}
		if seedOADemoFunc != nil {
			if err := seedOADemoFunc(db); err != nil {
				return err
			}
		}
		// 平台能力演示数据（issue #85，spec §7.3/§9）：渠道/模板/套餐/敏感词/
		// 社交客户端（停用）/演示消息日志，配置与密钥全部占位符。不依赖 CRM/OA
		// 演示组织，顺序无关，放最后。
		if seedPlatformDemoFunc != nil {
			if err := seedPlatformDemoFunc(db); err != nil {
				return err
			}
		}
	}
	return nil
}

// seedCRMDemoFunc 是 CRM 演示数据的写入入口，由 crm_demo.go 的 init 注册。
// 用函数变量而不是直接调用，是为了让代码生成器的编译校验（会剔换真实
// 产品实体及其消费者）可以在不携带 crm_demo.go 的副本上继续编译 seed 包；
// 正常构建里 crm_demo.go 始终存在，行为等价于直接调用。
var seedCRMDemoFunc func(*gorm.DB) error

// seedOADemoFunc 是 OA 演示数据的写入入口，由 oa_demo.go 的 init 注册。
// OA 演示数据依赖 CRM 演示组织（sales1/sales2/manager）与基础种子（默认
// 班次/考勤组、默认审批流程、报销类别字典），因此固定排在 CRM 演示数据之后。
var seedOADemoFunc func(*gorm.DB) error

// seedPlatformDemoFunc 是平台能力演示数据的写入入口，由 platform_demo.go 的
// init 注册。不依赖 CRM/OA 演示数据（自带渠道/模板/套餐兜底），顺序无关。
var seedPlatformDemoFunc func(*gorm.DB) error

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
		// 租户套餐（issue #69）：110 段（109 操作日志已占用，110 起空闲）。
		// 套餐是平台级资源，页面仅平台租户可操作；按钮码按菜单 ID×10+n。
		menu(110, 100, "租户套餐", "tenant-package", "system/tenant-package/index", "system:tenant-package:query", "Box", 10),
		btn(1101, 110, "套餐新增", "system:tenant-package:create", 1),
		btn(1102, 110, "套餐修改", "system:tenant-package:update", 2),
		btn(1103, 110, "套餐删除", "system:tenant-package:delete", 3),
		// 社交登录（issue #76，spec §7.2）：111 段。客户端管理仅平台租户可操作；
		// 绑定用户查询/解绑是租户内管理动作。按钮码 1111–1115（菜单 ID×10+n，
		// grep 去重确认空闲）。provider 实现（微信/钉钉/企微）由 #77 承接。
		menu(111, 100, "社交登录", "social", "system/social/index", "system:social:query", "Connection", 11),
		btn(1111, 111, "客户端新增", "system:social:create", 1),
		btn(1112, 111, "客户端修改", "system:social:update", 2),
		btn(1113, 111, "客户端删除", "system:social:delete", 3),
		btn(1114, 111, "绑定用户查询", "system:social-user:query", 4),
		btn(1115, 111, "绑定用户解绑", "system:social-user:delete", 5),
		// 敏感词（issue #81，spec §7.2）：112 段。租户级词库管理 + 检测文本框；
		// 按钮码 1121–1123（菜单 ID×10+n，grep 去重确认空闲）。
		menu(112, 100, "敏感词", "sensitive-word", "system/sensitive-word/index", "system:sensitive-word:query", "Key", 12),
		btn(1121, 112, "敏感词新增", "system:sensitive-word:create", 1),
		btn(1122, 112, "敏感词修改", "system:sensitive-word:update", 2),
		btn(1123, 112, "敏感词删除", "system:sensitive-word:delete", 3),

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
		// 地区管理（issue #82，spec §4.8/§5.7/§7.2）：113 段（grep 去重确认
		// 113/1131 空闲）。行政区划是平台级参考数据，页面只读 + 平台导入；
		// 查询码挂菜单（树/子级接口对所有登录用户开放，spec §4.8.1，
		// 供 AreaCascader 在任意页面使用），导入是平台租户专属动作。
		menu(113, 100, "地区管理", "area", "system/area/index", "system:area:query", "Location", 13),
		btn(1131, 113, "地区导入", "system:area:import", 1),

		dir(200, 0, "基础设施", "/infra", "Tools", 20),
		menu(201, 200, "文件管理", "file", "infra/file/index", "infra:file:query", "FolderOpened", 1),
		menu(202, 200, "参数配置", "config", "infra/config/index", "infra:config:query", "SetUp", 2),
		menu(203, 200, "定时任务", "job", "infra/job/index", "infra:job:query", "Timer", 3),
		// 访问日志（issue #80，spec §7.2）：204 段，按钮码 2041（菜单 ID×10+n，
		// grep 去重确认 204/2041 未被占用）。
		menu(204, 200, "访问日志", "api-log", "infra/api-log/index", "infra:api-log:query", "Document", 4),
		// 数据库文档（issue #83，spec §7.2）：205 段（grep 去重确认 205 空闲）。
		// 表清单/详情/导出共用 infra:db-doc:query（spec §5.8 无其他权限码，
		// 不设按钮），「仅平台租户」由 service 层校验，非平台租户即使持码也 403。
		menu(205, 200, "数据库文档", "db-doc", "infra/db-doc/index", "infra:db-doc:query", "Document", 5),
		// 监控面板（issue #84，spec §7.2）：206 段（grep 去重确认 206/2060 空闲）。
		// overview/requests 共用 infra:monitor:query（spec §5.8 无其他权限码，
		// 不设按钮），「仅平台租户」由 service 层校验，非平台租户即使持码也 403。
		menu(206, 200, "监控面板", "monitor", "infra/monitor/index", "infra:monitor:query", "Monitor", 6),

		btn(2011, 201, "文件上传", "infra:file:upload", 1),
		btn(2012, 201, "文件删除", "infra:file:delete", 2),
		btn(2021, 202, "参数新增", "infra:config:create", 1),
		btn(2022, 202, "参数修改", "infra:config:update", 2),
		btn(2023, 202, "参数删除", "infra:config:delete", 3),
		btn(2031, 203, "任务新增", "infra:job:create", 1),
		btn(2032, 203, "任务修改", "infra:job:update", 2),
		btn(2033, 203, "任务删除", "infra:job:delete", 3),
		// 批量删除与清空共用删除码（spec §5.5：删除/清空均为 infra:api-log:delete）。
		btn(2041, 204, "日志删除", "infra:api-log:delete", 1),

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
		// grep 去重确认）。crm:contract:approve 自 issue #47 起是默认流程
		// crm_contract_approval 的审批人规则权限码（候选池由 bpm 物化待办）。
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

		// 公告（issue #48）：521 段（协调台账分配；grep 去重确认 521/5211–5215
		// 未被占用）。查询码挂菜单（管理列表与草稿预览入口），创建/编辑/删除/
		// 发布撤回逐码；5215 已读统计由 #49 承载（码先登记，接口由 #49 落地）。
		menu(521, 500, "公告", "announcement", "oa/announcement/index", "oa:announcement:query", "Notification", 3),
		btn(5211, 521, "公告新增", "oa:announcement:create", 1),
		btn(5212, 521, "公告修改", "oa:announcement:update", 2),
		btn(5213, 521, "公告删除", "oa:announcement:delete", 3),
		btn(5214, 521, "公告发布", "oa:announcement:publish", 4),
		btn(5215, 521, "已读统计", "oa:announcement:read-stats", 5),
		// 我的公告（issue #48）：员工侧页面，权限码为空——登录即可（分配了
		// 菜单的角色可见；后端 GET /oa/announcement/my/page 与已发布详情同为
		// 登录即可，可见性由受众判定收口）。532 在 500–549 空位自取（#52 占
		// 529 我的请假、#60 占 530 考勤组管理/531 我的考勤），grep 去重确认
		// 532 未被占用。
		menu(532, 500, "我的公告", "announcement-mine", "oa/announcement/mine", "", "Bell", 10),

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

		// 我的请假（issue #52/#53）：529 段（500–549 员工侧空位自取，grep 去重
		// 确认 529/5291–5296 未被占用；522 管类型与额度、528 管本人额度，勿重复建）。
		// 页面本身登录即可（后端列表/详情按 §4.6 可见范围收口），写动作逐码：
		// 创建/编辑/删除/提交/撤回/取消（按钮码 = 菜单 ID×10+n）。审批动作走
		// 通用待办 512 段；#53 的撤回/取消仅发起人可见（前端按 userId + 状态显隐）。
		menu(529, 500, "我的请假", "leave-request", "oa/leave/request/index", "", "Calendar", 9),
		btn(5291, 529, "请假申请", "oa:leave:create", 1),
		btn(5292, 529, "请假编辑", "oa:leave:update", 2),
		btn(5293, 529, "请假删除", "oa:leave:delete", 3),
		btn(5294, 529, "请假提交", "oa:leave:submit", 4),
		btn(5295, 529, "请假撤回", "oa:leave:withdraw", 5),
		btn(5296, 529, "请假取消", "oa:leave:cancel", 6),
		// 请假查询码（issue #64 review，spec §5）：列表/详情接口的查询门槛；
		// 菜单本身无权限码，员工侧页面必须显式授予本按钮才可读列表/详情。
		btn(5297, 529, "请假查询", "oa:leave:query", 7),

		// 报销（issue #54）：523 报销（管理/财务入口，查询码挂菜单；spec §7.2 的
		// 504 段在本仓库协调台账改为 523 段）+ 535 我的报销（员工侧空位自取：
		// 529 我的请假/530 考勤组/531 我的考勤/532 我的公告/533 考勤结果/
		// 534 考勤汇总已占用，合并 #62 后 grep 去重取 535）。两页面共用同一
		// 视图，可见范围由后端 §4.6 收口。
		// 按钮码 = 菜单 ID×10+n：5231 创建/5232 编辑/5233 删除/5234 提交
		//（提交逻辑属 #55，码先登记，路由由 #55 落地）/5235 管理（财务/管理
		// 可见范围，spec §4.6/§4.9 默认审批流节点 2）。
		menu(523, 500, "报销", "expense", "oa/expense/index", "oa:expense:query", "Money", 4),
		btn(5231, 523, "报销申请", "oa:expense:create", 1),
		btn(5232, 523, "报销编辑", "oa:expense:update", 2),
		btn(5233, 523, "报销删除", "oa:expense:delete", 3),
		btn(5234, 523, "报销提交", "oa:expense:submit", 4),
		btn(5235, 523, "报销管理", "oa:expense:manage", 5),
		// 报销查询码（issue #64 review，spec §5）：员工侧 535 菜单无权限码，
		// 列表/详情接口挂 oa:expense:query，需显式授予本按钮（管理菜单 523
		// 本身也携带该码，供财务/管理员使用）。
		btn(5236, 523, "报销查询", "oa:expense:query", 6),
		menu(535, 500, "我的报销", "expense-mine", "oa/expense/index", "", "Money", 13),

		// 考勤（issue #60）：527 班次、530 考勤组、531 我的考勤（协调台账分配
		// 527 段；528/529 已被 #51/#52 的请假员工侧页面占用，grep 去重后改取
		// 530/531）。权限码按 spec §5/§7.2：
		// 班次 oa:attendance:shift:manage、考勤组 oa:attendance:group:manage
		//（码挂在菜单上，授予菜单即授予该域全部维护动作）；我的考勤为员工自助页，
		// 登录即可用（权限码为空，与仪表盘同款；打卡动作以登录身份收口）。
		menu(527, 500, "班次管理", "attendance-shift", "oa/attendance/shift", "oa:attendance:shift:manage", "Timer", 5),
		menu(530, 500, "考勤组管理", "attendance-group", "oa/attendance/group", "oa:attendance:group:manage", "UserFilled", 6),
		menu(531, 500, "我的考勤", "attendance-punch", "oa/attendance/punch", "", "Clock", 7),

		// 考勤结果与校正（issue #61）：533 段（500–549 空位自取，grep 去重确认
		// 533/5331–5332 未被占用）。533 查询码挂菜单（结果查询页入口）；5331
		// 校正/撤销校正/手工重算共用 oa:attendance:result:correct；5332 挂 #43
		// helper 登记的全租户可见范围码 oa:attendance:manage（super_admin "*" 直接
		// 命中，HR 角色授予本按钮即获得全租户结果可见）。员工侧结果列表追加在
		// 「我的考勤」531 页面，登录即可，不新增菜单。
		menu(533, 500, "考勤结果", "attendance-result", "oa/attendance/result", "oa:attendance:result:query", "DataAnalysis", 11),
		btn(5331, 533, "考勤校正", "oa:attendance:result:correct", 1),
		btn(5332, 533, "全租户可见", "oa:attendance:manage", 2),

		// 考勤月度汇总（issue #62）：534 段（500–549 空位自取，grep 去重确认
		// 534 未被占用）。查询码挂菜单（汇总页入口）；可见范围复用 #43 helper——
		// oa:attendance:manage（5332 按钮承载）看全租户、部门负责人看子树、
		// 否则仅本人。员工「本月汇总」卡片在「我的考勤」531 页面内登录即可，
		// 不新增菜单。
		menu(534, 500, "考勤汇总", "attendance-summary", "oa/attendance/summary", "oa:attendance:summary:query", "TrendCharts", 12),

		// 日程（issue #56）：524 段（协调台账分配；grep 去重确认 524/5241–5244
		// 未被占用）。查询码挂菜单（月/周/列表三视图入口）；创建/编辑/取消逐码，
		// 5243 同时承载 DELETE（spec §5）与「取消」动作（软取消，取消后不再
		// 展示）；5244 是公开日程权限码（创建/编辑可见性=2 时服务层校验，
		// 缺失 403）。员工侧「我的日程」536 复用同一页面（500–549 空位自取：
		// 529 我的请假/530 考勤组/531 我的考勤/532 我的公告/533 考勤结果/
		// 534 考勤汇总/535 我的报销已占用，grep 去重取 536），查询码挂菜单本身，
		// 普通角色授予 536 + 5241–5243 即可自建私有/部门日程。
		menu(524, 500, "日程", "calendar", "oa/calendar/index", "oa:calendar:query", "Calendar", 14),
		btn(5241, 524, "日程创建", "oa:calendar:create", 1),
		btn(5242, 524, "日程编辑", "oa:calendar:update", 2),
		btn(5243, 524, "日程取消", "oa:calendar:delete", 3),
		btn(5244, 524, "公开日程", "oa:calendar:public", 4),
		menu(536, 500, "我的日程", "calendar-mine", "oa/calendar/index", "oa:calendar:query", "Calendar", 15),

		// 会议室与预约（issue #57）：525 段（协调台账分配；grep 去重确认
		// 525/5251–5252 未被占用）。会议室查询/详情与预约列表挂
		// oa:room:query；会议室 CRUD/启停/删除挂 oa:room:manage（按钮 5251，
		// 有未来有效预约时删除返回 409，只能停用）；创建预约挂
		// oa:room:booking（按钮 5252）。管理端预约视图 538 与员工入口
		// 「我的会议」539 从 500–549 空位自取（523/524/526/529–537 已占用，
		// 其中 537 是 #58 我的文档；grep 去重取 538/539）；两页复用同一
		// oa/room/booking 组件——无 oa:room:manage 的查看者由后端强制只看
		// 本人预约。普通角色授予 539 + 5252 即可预约并查看自己的会议。
		menu(525, 500, "会议室管理", "room", "oa/room/index", "oa:room:query", "OfficeBuilding", 18),
		btn(5251, 525, "会议室维护", "oa:room:manage", 1),
		btn(5252, 525, "预约会议", "oa:room:booking", 2),
		menu(538, 500, "会议室预约", "room-booking", "oa/room/booking", "oa:room:query", "Calendar", 19),
		menu(539, 500, "我的会议", "room-mine", "oa/room/booking", "oa:room:query", "Clock", 20),

		// 文档目录与 ACL（issue #58，spec §3.8/§4.12）：526 段（协调台账分配；
		// grep 去重确认 525/526/5261–5263/537 未被占用）。查询码挂菜单（目录树
		// 入口）；5261 创建目录（顶层必须带显式 ACL）；5262 是管理旁路码
		//（owner 之外的全目录可见/可管理，服务层判定 403/404）；5263 文件上传
		// 按钮码由 #59 沿用（后端上传/删除只校验目录管理权，不强制该码）。
		// 员工侧「我的文档」537 复用
		// 同一页面（500–549 空位自取：523 报销/524 日程/529 我的请假/530 考勤组/
		// 531 我的考勤/532 我的公告/533 考勤结果/534 考勤汇总/535 我的报销/
		// 536 我的日程/538 会议室预约/539 我的会议已占用，grep 去重取 537），
		// 只挂查询码，可见目录按 ACL 过滤。
		menu(526, 500, "文档", "document", "oa/document/index", "oa:document:query", "FolderOpened", 16),
		btn(5261, 526, "目录创建", "oa:document:create", 1),
		btn(5262, 526, "目录管理", "oa:document:manage", 2),
		btn(5263, 526, "文件上传", "oa:document:upload", 3),
		menu(537, 500, "我的文档", "document-mine", "oa/document/index", "oa:document:query", "FolderOpened", 17),

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

		// 消息中心（issue #72，spec §7.2）：600 目录 + 601 短信管理 + 602 邮件管理
		// + 603 发送日志。菜单查询码按 spec：601 挂 message:channel:query、
		// 602 挂 message:mail:query、603 挂 message:log:query；模板没有独立菜单，
		// 模板查询/增删改按钮码挂 601/602 段（ID = 菜单ID*10+n，grep 去重确认
		// 600/601/602/603 与 6011–6019/6021–6029/6031–6032 未被占用）。
		// 渠道/模板的「仅平台租户」由 service 层校验，非平台租户即使持码也 403。
		dir(600, 0, "消息中心", "/message", "ChatDotRound", 60),
		menu(601, 600, "短信管理", "sms", "message/sms/index", "message:channel:query", "Iphone", 1),
		btn(6011, 601, "渠道查询", "message:channel:query", 1),
		btn(6012, 601, "渠道新增", "message:channel:create", 2),
		btn(6013, 601, "渠道修改", "message:channel:update", 3),
		btn(6014, 601, "渠道删除", "message:channel:delete", 4),
		btn(6015, 601, "模板查询", "message:template:query", 5),
		btn(6016, 601, "模板新增", "message:template:create", 6),
		btn(6017, 601, "模板修改", "message:template:update", 7),
		btn(6018, 601, "模板删除", "message:template:delete", 8),
		btn(6019, 601, "短信发送", "message:sms:send", 9),
		menu(602, 600, "邮件管理", "mail", "message/mail/index", "message:mail:query", "Message", 2),
		btn(6021, 602, "渠道查询", "message:channel:query", 1),
		btn(6022, 602, "渠道新增", "message:channel:create", 2),
		btn(6023, 602, "渠道修改", "message:channel:update", 3),
		btn(6024, 602, "渠道删除", "message:channel:delete", 4),
		btn(6025, 602, "模板查询", "message:template:query", 5),
		btn(6026, 602, "模板新增", "message:template:create", 6),
		btn(6027, 602, "模板修改", "message:template:update", 7),
		btn(6028, 602, "模板删除", "message:template:delete", 8),
		btn(6029, 602, "邮件发送", "message:mail:send", 9),
		menu(603, 600, "发送日志", "log", "message/log/index", "message:log:query", "Tickets", 3),
		btn(6031, 603, "日志重发", "message:log:resend", 1),
		btn(6032, 603, "日志删除", "message:log:delete", 2),

		// ERP（issue #88/#89，docs/erp-mvp-spec.md §6/§7）：700 段（协调台账分配）。
		// #88：700 dir、710–712（分类/单位/产品）+ 按钮 7101–7124；
		// #89：720 仓库 / 730 供应商 / 740 客户 + 按钮 7201–7204/7301–7304/
		// 7401–7404（菜单ID×10+n，grep 去重确认未占用）；后续票追加 701 首页
		// 统计、721–722 库存、723–726 其它出入库/调拨/盘点、731–733 采购、
		// 741–743 销售、750 结算账户/751–752 收付款；按钮 = 菜单ID×10+n
		//（query/create/update/delete/export）。
		// 财务与库存敏感：内置普通角色（role id=2）默认不授予任何 ERP 菜单
		//（spec §7，见 seedRoles 的 roleMenus——刻意无 700 段条目）。
		dir(700, 0, "ERP 进销存", "/erp", "ShoppingCart", 70),
		menu(710, 700, "产品分类", "product-category", "erp/product-category/index", "erp:product-category:query", "Files", 1),
		btn(7101, 710, "分类新增", "erp:product-category:create", 1),
		btn(7102, 710, "分类修改", "erp:product-category:update", 2),
		btn(7103, 710, "分类删除", "erp:product-category:delete", 3),
		btn(7104, 710, "分类导出", "erp:product-category:export", 4),
		menu(711, 700, "产品单位", "product-unit", "erp/product-unit/index", "erp:product-unit:query", "CollectionTag", 2),
		btn(7111, 711, "单位新增", "erp:product-unit:create", 1),
		btn(7112, 711, "单位修改", "erp:product-unit:update", 2),
		btn(7113, 711, "单位删除", "erp:product-unit:delete", 3),
		btn(7114, 711, "单位导出", "erp:product-unit:export", 4),
		menu(712, 700, "产品管理", "product", "erp/product/index", "erp:product:query", "Goods", 3),
		btn(7121, 712, "产品新增", "erp:product:create", 1),
		btn(7122, 712, "产品修改", "erp:product:update", 2),
		btn(7123, 712, "产品删除", "erp:product:delete", 3),
		btn(7124, 712, "产品导出", "erp:product:export", 4),
		// 仓库/供应商/客户主数据（issue #89，spec §3.1/§7）：仓库管理员/采购员/
		// 销售员维护；权限码 erp:warehouse:* / erp:supplier:* / erp:customer:*。
		menu(720, 700, "仓库管理", "warehouse", "erp/warehouse/index", "erp:warehouse:query", "House", 4),
		btn(7201, 720, "仓库新增", "erp:warehouse:create", 1),
		btn(7202, 720, "仓库修改", "erp:warehouse:update", 2),
		btn(7203, 720, "仓库删除", "erp:warehouse:delete", 3),
		btn(7204, 720, "仓库导出", "erp:warehouse:export", 4),
		menu(730, 700, "供应商管理", "supplier", "erp/supplier/index", "erp:supplier:query", "Shop", 5),
		btn(7301, 730, "供应商新增", "erp:supplier:create", 1),
		btn(7302, 730, "供应商修改", "erp:supplier:update", 2),
		btn(7303, 730, "供应商删除", "erp:supplier:delete", 3),
		btn(7304, 730, "供应商导出", "erp:supplier:export", 4),
		menu(740, 700, "客户管理", "customer", "erp/customer/index", "erp:customer:query", "User", 6),
		btn(7401, 740, "客户新增", "erp:customer:create", 1),
		btn(7402, 740, "客户修改", "erp:customer:update", 2),
		btn(7403, 740, "客户删除", "erp:customer:delete", 3),
		btn(7404, 740, "客户导出", "erp:customer:export", 4),

		// 库存核心与其它入库/出库单（issue #90，spec §6/§7）：721 产品库存、
		// 722 出入库明细、723 其它入库、724 其它出库（spec §7 号段；721/722 是
		// 查询页——库存行无 owner 语义，只有 query/export 按钮，无增删改）；
		// 723/724 单据页按 query/create/update/delete/approve/export 全套按钮
		//（approve 为独立审核权限码，spec §4.1）。按钮 = 菜单ID×10+n，
		// 7211–7214/7221–7224/7231–7235/7241–7245 grep 去重确认未占用。
		menu(721, 700, "产品库存", "stock", "erp/stock/index", "erp:stock:query", "Box", 7),
		btn(7211, 721, "库存导出", "erp:stock:export", 1),
		menu(722, 700, "出入库明细", "stock-record", "erp/stock-record/index", "erp:stock-record:query", "List", 8),
		btn(7221, 722, "流水导出", "erp:stock-record:export", 1),
		menu(723, 700, "其它入库", "stock-in", "erp/stock-in/index", "erp:stock-in:query", "Download", 9),
		btn(7231, 723, "入库新增", "erp:stock-in:create", 1),
		btn(7232, 723, "入库修改", "erp:stock-in:update", 2),
		btn(7233, 723, "入库删除", "erp:stock-in:delete", 3),
		btn(7234, 723, "入库审核", "erp:stock-in:approve", 4),
		btn(7235, 723, "入库导出", "erp:stock-in:export", 5),
		menu(724, 700, "其它出库", "stock-out", "erp/stock-out/index", "erp:stock-out:query", "Upload", 10),
		btn(7241, 724, "出库新增", "erp:stock-out:create", 1),
		btn(7242, 724, "出库修改", "erp:stock-out:update", 2),
		btn(7243, 724, "出库删除", "erp:stock-out:delete", 3),
		btn(7244, 724, "出库审核", "erp:stock-out:approve", 4),
		btn(7245, 724, "出库导出", "erp:stock-out:export", 5),

		// 采购三单（issue #91，spec §6/§7）：731 采购订单、732 采购入库、733
		// 采购退货（spec §7 号段；单据页按 query/create/update/delete/approve/
		// export 全套按钮，query 挂菜单自身）；按钮 7311–7315/7321–7325/
		// 7331–7335（菜单ID×10+n，grep 去重确认未占用）。
		menu(731, 700, "采购订单", "purchase-order", "erp/purchase-order/index", "erp:purchase-order:query", "Tickets", 11),
		btn(7311, 731, "订单新增", "erp:purchase-order:create", 1),
		btn(7312, 731, "订单修改", "erp:purchase-order:update", 2),
		btn(7313, 731, "订单删除", "erp:purchase-order:delete", 3),
		btn(7314, 731, "订单审核", "erp:purchase-order:approve", 4),
		btn(7315, 731, "订单导出", "erp:purchase-order:export", 5),
		menu(732, 700, "采购入库", "purchase-in", "erp/purchase-in/index", "erp:purchase-in:query", "GoodsFilled", 12),
		btn(7321, 732, "入库新增", "erp:purchase-in:create", 1),
		btn(7322, 732, "入库修改", "erp:purchase-in:update", 2),
		btn(7323, 732, "入库删除", "erp:purchase-in:delete", 3),
		btn(7324, 732, "入库审核", "erp:purchase-in:approve", 4),
		btn(7325, 732, "入库导出", "erp:purchase-in:export", 5),
		menu(733, 700, "采购退货", "purchase-return", "erp/purchase-return/index", "erp:purchase-return:query", "RefreshLeft", 13),
		btn(7331, 733, "退货新增", "erp:purchase-return:create", 1),
		btn(7332, 733, "退货修改", "erp:purchase-return:update", 2),
		btn(7333, 733, "退货删除", "erp:purchase-return:delete", 3),
		btn(7334, 733, "退货审核", "erp:purchase-return:approve", 4),
		btn(7335, 733, "退货导出", "erp:purchase-return:export", 5),
		// 销售三单（issue #92，spec §6/§7）：741 销售订单、742 销售出库、743 销售
		// 退货（spec §7 号段；#91 采购 731–733 同构）。单据页按 query 菜单 +
		// create/update/delete/approve/export 全套按钮（approve 为独立审核权限码，
		// spec §4.1）。按钮 = 菜单ID×10+n，7411–7415/7421–7425/7431–7435 grep
		// 去重确认未占用。
		menu(741, 700, "销售订单", "sale-order", "erp/sale-order/index", "erp:sale-order:query", "Ticket", 11),
		btn(7411, 741, "订单新增", "erp:sale-order:create", 1),
		btn(7412, 741, "订单修改", "erp:sale-order:update", 2),
		btn(7413, 741, "订单删除", "erp:sale-order:delete", 3),
		btn(7414, 741, "订单审核", "erp:sale-order:approve", 4),
		btn(7415, 741, "订单导出", "erp:sale-order:export", 5),
		menu(742, 700, "销售出库", "sale-out", "erp/sale-out/index", "erp:sale-out:query", "Position", 12),
		btn(7421, 742, "出库新增", "erp:sale-out:create", 1),
		btn(7422, 742, "出库修改", "erp:sale-out:update", 2),
		btn(7423, 742, "出库删除", "erp:sale-out:delete", 3),
		btn(7424, 742, "出库审核", "erp:sale-out:approve", 4),
		btn(7425, 742, "出库导出", "erp:sale-out:export", 5),
		menu(743, 700, "销售退货", "sale-return", "erp/sale-return/index", "erp:sale-return:query", "RefreshLeft", 13),
		btn(7431, 743, "退货新增", "erp:sale-return:create", 1),
		btn(7432, 743, "退货修改", "erp:sale-return:update", 2),
		btn(7433, 743, "退货删除", "erp:sale-return:delete", 3),
		btn(7434, 743, "退货审核", "erp:sale-return:approve", 4),
		btn(7435, 743, "退货导出", "erp:sale-return:export", 5),

		// 库存调拨/盘点（issue #93，spec §6/§7）：725 库存调拨（DB）、726 库存
		// 盘点（PD）（spec §7 号段）。单据页按 query 菜单 + create/update/delete/
		// approve/export 全套按钮（approve 为独立审核权限码，spec §4.1）。
		// 按钮 = 菜单ID×10+n，7251–7255/7261–7265 grep 去重确认未占用。
		menu(725, 700, "库存调拨", "stock-move", "erp/stock-move/index", "erp:stock-move:query", "Switch", 11),
		btn(7251, 725, "调拨新增", "erp:stock-move:create", 1),
		btn(7252, 725, "调拨修改", "erp:stock-move:update", 2),
		btn(7253, 725, "调拨删除", "erp:stock-move:delete", 3),
		btn(7254, 725, "调拨审核", "erp:stock-move:approve", 4),
		btn(7255, 725, "调拨导出", "erp:stock-move:export", 5),
		menu(726, 700, "库存盘点", "stock-check", "erp/stock-check/index", "erp:stock-check:query", "Finished", 12),
		btn(7261, 726, "盘点新增", "erp:stock-check:create", 1),
		btn(7262, 726, "盘点修改", "erp:stock-check:update", 2),
		btn(7263, 726, "盘点删除", "erp:stock-check:delete", 3),
		btn(7264, 726, "盘点审核", "erp:stock-check:approve", 4),
		btn(7265, 726, "盘点导出", "erp:stock-check:export", 5),
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
	// 菜单补齐（issue #69）：按 ID 只插入缺失项，不更新、不复活已软删菜单。
	// 旧实现「有菜单就整体跳过」会让存量库永远看不到新版本追加的菜单；
	// Unscoped 把软删 ID 也视为已存在，避免重新插入撞主键或复活管理员删除的菜单。
	var existing []int64
	if err := tenant.Skip(db).Unscoped().Model(&sysmodel.Menu{}).Pluck("id", &existing).Error; err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(existing))
	for _, id := range existing {
		seen[id] = struct{}{}
	}
	missing := make([]sysmodel.Menu, 0, len(menus))
	for _, menu := range menus {
		if _, ok := seen[menu.ID]; ok {
			continue
		}
		missing = append(missing, menu)
	}
	if len(missing) == 0 {
		return nil
	}
	if err := db.Create(&missing).Error; err != nil {
		return err
	}
	slog.Info("seed menus created", "count", len(missing))
	return nil
}

// seedTenantPackages 写入默认「标准版」套餐（issue #69）：全部启用目录/菜单、
// 配额不限。仅当套餐表为空时写入；管理员改名/删除后重复启动不复活。
func seedTenantPackages(db *gorm.DB) error {
	return ensureStandardTenantPackage(db)
}

// ensureStandardTenantPackage 保证存在默认「标准版」套餐（issue #69，spec §7.3）：
// 套餐表为空时写入一条全部启用目录/菜单、配额不限的套餐。基础种子与平台演示
// 种子（platform_demo.go）共用该兜底，幂等——表非空（含只剩管理端自建套餐）
// 即跳过，不覆盖已有数据。
func ensureStandardTenantPackage(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&sysmodel.TenantPackage{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var menuIDs sysmodel.Int64List
	if err := tenant.Skip(db).Model(&sysmodel.Menu{}).
		Where("type IN ?", []int8{sysmodel.MenuTypeDir, sysmodel.MenuTypeMenu}).
		Where("status = ?", sysmodel.StatusEnable).
		Order("id ASC").
		Pluck("id", &menuIDs).Error; err != nil {
		return err
	}
	pkg := &sysmodel.TenantPackage{
		BaseEntity: sysmodel.BaseEntity{ID: 1},
		Name:       "标准版",
		Status:     sysmodel.StatusEnable,
		MenuIDs:    menuIDs,
		Remark:     "默认套餐：全部菜单，配额不限",
	}
	if err := tenant.Skip(db).Create(pkg).Error; err != nil {
		return err
	}
	slog.Info("seed tenant package created", "name", pkg.Name, "menus", len(menuIDs))
	return nil
}

// seedAttendanceDefaults 写入默认班次与默认考勤组（spec §7.3：默认班次
// 09:00–18:00、宽限 5 分钟；默认考勤组绑定该班次）。仅当班次与考勤组都为空时
// 写入——管理员删掉/改名后重复启动不复活，保持幂等。默认组不写组员：新用户
// 仍需管理员按用户或部门物化入组（spec §4.10）。
func seedAttendanceDefaults(db *gorm.DB) error {
	var shiftCount, groupCount int64
	if err := tenant.Skip(db).Model(&oamodel.AttendanceShift{}).Count(&shiftCount).Error; err != nil {
		return err
	}
	if err := tenant.Skip(db).Model(&oamodel.AttendanceGroup{}).Count(&groupCount).Error; err != nil {
		return err
	}
	if shiftCount > 0 || groupCount > 0 {
		return nil
	}
	shift := &oamodel.AttendanceShift{
		BaseEntity:   oamodel.BaseEntity{ID: 1},
		TenantID:     1,
		Name:         "标准班",
		StartTime:    "09:00",
		EndTime:      "18:00",
		GraceMinutes: 5,
		Status:       oamodel.AttendanceStatusEnabled,
		Sort:         0,
	}
	if err := tenant.Skip(db).Create(shift).Error; err != nil {
		return err
	}
	group := &oamodel.AttendanceGroup{
		BaseEntity: oamodel.BaseEntity{ID: 1},
		TenantID:   1,
		Name:       "默认考勤组",
		ShiftID:    shift.ID,
		Status:     oamodel.AttendanceStatusEnabled,
	}
	if err := tenant.Skip(db).Create(group).Error; err != nil {
		return err
	}
	slog.Info("seed attendance defaults created", "shift", shift.Name, "group", group.Name)
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
		// OA 员工侧基础菜单（issue #48，spec §7.3「内置普通角色授予公告查看」）：
		// OA 根目录 + 我的公告（登录即可的只读页面，不含公告管理菜单 521）。
		// 我的考勤（531，issue #61 起含打卡与考勤结果列表）同为员工自助页，
		// 登录即可，纳入内置普通角色。
		{RoleID: 2, MenuID: 500},
		{RoleID: 2, MenuID: 532},
		{RoleID: 2, MenuID: 531},
		// 报销员工侧（issue #54，spec §7.3「内置普通角色授予…报销…的基础菜单」）：
		// 我的报销页面 + 草稿创建/编辑/删除/提交按钮码 + 查询码 5236
		//（issue #64 review：列表/详情接口挂 oa:expense:query，535 菜单本身
		// 无权限码）；不含管理菜单 523 与 oa:expense:manage（财务/管理员）。
		{RoleID: 2, MenuID: 535},
		{RoleID: 2, MenuID: 5231},
		{RoleID: 2, MenuID: 5232},
		{RoleID: 2, MenuID: 5233},
		{RoleID: 2, MenuID: 5234},
		{RoleID: 2, MenuID: 5236},
		// 日程员工侧（issue #56，spec §7.3「内置普通角色授予…日程…的基础
		// 菜单」）：我的日程页面（536，携带 oa:calendar:query）+ 创建/编辑/
		// 取消按钮码；不含 524 管理菜单与 5244 公开日程码（公开日程需要
		// 管理员另行授予 oa:calendar:public）。
		{RoleID: 2, MenuID: 536},
		{RoleID: 2, MenuID: 5241},
		{RoleID: 2, MenuID: 5242},
		{RoleID: 2, MenuID: 5243},
		// 会议室员工侧（issue #57）：我的会议页面（539，携带 oa:room:query）
		// + 预约按钮码 5252（oa:room:booking）；不含 525 管理菜单与
		// oa:room:manage（会议室维护与全租户预约查看）。
		{RoleID: 2, MenuID: 539},
		{RoleID: 2, MenuID: 5252},
		// 文档员工侧（issue #58，spec §7.3「内置普通角色授予…文档…的基础
		// 菜单」）：我的文档页面（537，携带 oa:document:query），可见目录由
		// 目录 ACL 过滤；不含 526 管理菜单、5261 创建与 5262 管理码
		//（顶层目录创建与全租户管理由管理员另行授予）。
		{RoleID: 2, MenuID: 537},
		// 请假员工侧（issue #63 收口）：我的额度（528）与我的请假（529）+
		// 创建/编辑/删除/提交/撤回/取消按钮码 + 查询码 5297（issue #64
		// review：列表/详情接口挂 oa:leave:query）。
		{RoleID: 2, MenuID: 528},
		{RoleID: 2, MenuID: 529},
		{RoleID: 2, MenuID: 5291},
		{RoleID: 2, MenuID: 5292},
		{RoleID: 2, MenuID: 5293},
		{RoleID: 2, MenuID: 5294},
		{RoleID: 2, MenuID: 5295},
		{RoleID: 2, MenuID: 5296},
		{RoleID: 2, MenuID: 5297},
		// 通用待办（PR #66 审查 P1-2）：部门负责人/普通员工经通知深链或菜单
		// 进入 /bpm/task 需要 512 菜单（bpm:task:query）与 5121 办理按钮
		// （bpm:task:handle）；缺少时页面 403、动态路由不注册、通知深链 404。
		// 办理动作本身仍以「当前办理人/发起人」身份收口（spec §5），
		// bpm:task:handle 只控制前端按钮显隐。
		{RoleID: 2, MenuID: 512},
		{RoleID: 2, MenuID: 5121},
		// 顶栏通知铃铛（issue #64 review）：notify:message:query 挂在 5123
		// 按钮，此前未授予普通角色，NotifyBell 轮询 403；普通角色需要读本人通知。
		{RoleID: 2, MenuID: 5123},
		// 注意（issue #64 review，spec §7.3「普通角色只授基础菜单」）：
		// 考勤结果页 533 与校正/全租户 5331/5332 是管理面，不再授予内置普通角色；
		// 员工自助页 531（含本人结果与本月汇总）已在上方授予。演示环境需要 HR
		// 视角时使用 seed-demo 创建的内置 hr 角色（见 internal/seed/oa_demo.go）。
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

// seedExpenseCategoryDict 写入报销类别字典（issue #54，spec §4.9/§7.3：
// 交通、餐饮、住宿、办公、其他）。与 seedDicts 的「整表为空才写」不同，
// 这里按字典类型与字典项逐行幂等：既有库升级时会补齐缺失的字典/字典项，
// 重复启动不重复插入；管理员改过的名称/排序不会被覆盖。
func seedExpenseCategoryDict(db *gorm.DB) error {
	const dictType = "oa_expense_category"
	var typ sysmodel.DictType
	err := tenant.Skip(db).Where("`type` = ?", dictType).First(&typ).Error
	switch {
	case err == nil:
		// 已存在，保持管理员的修改。
	case errors.Is(err, gorm.ErrRecordNotFound):
		row := sysmodel.DictType{
			TenantID: 1, Name: "报销类别", Type: dictType,
			Status: sysmodel.StatusEnable, Remark: "报销明细的费用类别（OA v1 默认值）",
		}
		if err := tenant.Skip(db).Create(&row).Error; err != nil {
			return fmt.Errorf("seed expense category dict type: %w", err)
		}
	default:
		return fmt.Errorf("seed expense category dict type: %w", err)
	}

	values := []string{"交通", "餐饮", "住宿", "办公", "其他"}
	for i, value := range values {
		var count int64
		if err := tenant.Skip(db).Model(&sysmodel.DictData{}).
			Where("`dict_type` = ? AND `value` = ?", dictType, value).
			Count(&count).Error; err != nil {
			return fmt.Errorf("seed expense category dict data: %w", err)
		}
		if count > 0 {
			continue
		}
		row := sysmodel.DictData{
			TenantID: 1, DictType: dictType, Label: value, Value: value,
			Sort: i + 1, Status: sysmodel.StatusEnable,
		}
		if err := tenant.Skip(db).Create(&row).Error; err != nil {
			return fmt.Errorf("seed expense category dict data: %w", err)
		}
	}
	return nil
}

// erpDictEntry 是 ERP 字典项描述（类型 + 项列表）。
type erpDictEntry struct {
	Type   string
	Name   string
	Remark string
	Items  []erpDictItem
}

type erpDictItem struct {
	Value string
	Label string
}

// seedErpDicts 写入 ERP 字典（issue #90，docs/erp-mvp-spec.md §7：审核状态 +
// 其它入库/出库类型），按字典类型与字典项逐行幂等（与 seedExpenseCategoryDict
// 同策略：既有库升级补齐缺失项，管理员改过的不覆盖）。
//
// 取值来源（终审关注）：
//   - erp_audit_status：对齐芋道字典（10 未审核 / 20 已审核，见芋道
//     ruoyi-vue-pro sql/mysql 的 system_dict_data #323/#324）；
//   - erp_stock_in_type / erp_stock_out_type：芋道 master 已把 ERP 杂项出入库
//     重构为 MES（权威 SQL 中不再有 erp_stock_in_type），检索不可靠，按任务
//     给定的合理中文枚举落地（数值风格对齐芋道 10/20 步进）：
//     入库 采购/退货/借入/归还/赠品/其它；出库 报损/领用/借出/其它。
func seedErpDicts(db *gorm.DB) error {
	entries := []erpDictEntry{
		{
			Type: "erp_audit_status", Name: "ERP 单据审核状态",
			Remark: "ERP 十二类单据共用（spec §4.1，对齐芋道 erp_audit_status）",
			Items: []erpDictItem{
				{Value: "10", Label: "未审核"},
				{Value: "20", Label: "已审核"},
			},
		},
		{
			Type: "erp_stock_in_type", Name: "ERP 其它入库类型",
			Remark: "其它入库单类型（issue #90，对齐芋道数值风格；芋道 master 无权威 SQL，枚举经终审确认）",
			Items: []erpDictItem{
				{Value: "10", Label: "采购"},
				{Value: "20", Label: "退货"},
				{Value: "30", Label: "借入"},
				{Value: "40", Label: "归还"},
				{Value: "50", Label: "赠品"},
				{Value: "99", Label: "其它"},
			},
		},
		{
			Type: "erp_stock_out_type", Name: "ERP 其它出库类型",
			Remark: "其它出库单类型（issue #90，对齐芋道数值风格；芋道 master 无权威 SQL，枚举经终审确认）",
			Items: []erpDictItem{
				{Value: "10", Label: "报损"},
				{Value: "20", Label: "领用"},
				{Value: "30", Label: "借出"},
				{Value: "99", Label: "其它"},
			},
		},
	}
	for _, entry := range entries {
		var typ sysmodel.DictType
		err := tenant.Skip(db).Where("`type` = ?", entry.Type).First(&typ).Error
		switch {
		case err == nil:
			// 已存在，保持管理员的修改。
		case errors.Is(err, gorm.ErrRecordNotFound):
			row := sysmodel.DictType{
				TenantID: 1, Name: entry.Name, Type: entry.Type,
				Status: sysmodel.StatusEnable, Remark: entry.Remark,
			}
			if err := tenant.Skip(db).Create(&row).Error; err != nil {
				return fmt.Errorf("seed erp dict type %s: %w", entry.Type, err)
			}
		default:
			return fmt.Errorf("seed erp dict type %s: %w", entry.Type, err)
		}
		for i, item := range entry.Items {
			var count int64
			if err := tenant.Skip(db).Model(&sysmodel.DictData{}).
				Where("`dict_type` = ? AND `value` = ?", entry.Type, item.Value).
				Count(&count).Error; err != nil {
				return fmt.Errorf("seed erp dict data %s: %w", entry.Type, err)
			}
			if count > 0 {
				continue
			}
			row := sysmodel.DictData{
				TenantID: 1, DictType: entry.Type, Label: item.Label, Value: item.Value,
				Sort: i + 1, Status: sysmodel.StatusEnable,
			}
			if err := tenant.Skip(db).Create(&row).Error; err != nil {
				return fmt.Errorf("seed erp dict data %s: %w", entry.Type, err)
			}
		}
	}
	return nil
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

// seedMessages 写入消息模块默认数据（issue #72，spec §7.3）：一条 log provider
// 的短信渠道、一条 log provider 的邮件渠道，以及两条欢迎模板。
// 仅当渠道表与模板表都为空时写入——管理员删除/改名后重复启动不复活；
// 渠道/模板是平台级表，仍显式 tenant.Skip 表示系统级写入。
func seedMessages(db *gorm.DB) error {
	var channelCount, templateCount int64
	if err := tenant.Skip(db).Model(&msgmodel.Channel{}).Count(&channelCount).Error; err != nil {
		return err
	}
	if err := tenant.Skip(db).Model(&msgmodel.Template{}).Count(&templateCount).Error; err != nil {
		return err
	}
	if channelCount > 0 || templateCount > 0 {
		return nil
	}

	channels := []msgmodel.Channel{
		{
			BaseEntity:  msgmodel.BaseEntity{ID: 1},
			ChannelType: msgmodel.ChannelTypeSMS,
			Code:        "sms-log",
			Name:        "短信日志渠道",
			Provider:    msgmodel.ProviderLog,
			Config:      "{}",
			Status:      msgmodel.StatusEnable,
			Remark:      "内置开发渠道：只写应用日志，不真正发送（ADR 0010）",
		},
		{
			BaseEntity:  msgmodel.BaseEntity{ID: 2},
			ChannelType: msgmodel.ChannelTypeMail,
			Code:        "mail-log",
			Name:        "邮件日志渠道",
			Provider:    msgmodel.ProviderLog,
			Config:      "{}",
			Status:      msgmodel.StatusEnable,
			Remark:      "内置开发渠道：只写应用日志，不真正发送（ADR 0010）",
		},
	}
	if err := tenant.Skip(db).Create(&channels).Error; err != nil {
		return fmt.Errorf("seed message channels: %w", err)
	}

	templates := []msgmodel.Template{
		{
			BaseEntity:  msgmodel.BaseEntity{ID: 1},
			ChannelType: msgmodel.ChannelTypeSMS,
			Code:        "welcome",
			Name:        "欢迎短信",
			Content:     "您好 {{name}}，欢迎使用 go-admin-pro。",
			Params:      `["name"]`,
			Status:      msgmodel.StatusEnable,
		},
		{
			BaseEntity:  msgmodel.BaseEntity{ID: 2},
			ChannelType: msgmodel.ChannelTypeMail,
			Code:        "welcome",
			Name:        "欢迎邮件",
			Title:       "欢迎使用 go-admin-pro",
			Content:     "<p>您好 {{name}}，欢迎使用 go-admin-pro。</p>",
			Params:      `["name"]`,
			Status:      msgmodel.StatusEnable,
		},
	}
	if err := tenant.Skip(db).Create(&templates).Error; err != nil {
		return fmt.Errorf("seed message templates: %w", err)
	}
	slog.Info("seed message defaults created", "channels", len(channels), "templates", len(templates))
	return nil
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
		{
			TenantID:       1,
			Name:           "考勤日结果生成",
			HandlerName:    "oaAttendanceDailyResult",
			CronExpression: "0 0 1 * * *",
			RetryCount:     0,
			// 默认启用（spec §4.10「infra_job 每天 01:00 生成前一日结果」）：
			// 派生幂等且只写本租户结果，非破坏性；handler_param 可填 YYYY-MM-DD
			// 回填指定日期。
			Status: inframodel.JobStatusRunning,
			Remark: "每天 01:00 生成前一日考勤结果（按启用租户迭代）；handler_param 可填 YYYY-MM-DD 回填指定日期",
		},
		// 会议提醒（issue #57，spec §4.11）：每 5 分钟扫描 (now, now+15m]
		// 内开始的有效预约，向预约人写 meeting_reminder 通知；reminded_at
		// 条件更新保证只发一次。默认启用（非破坏性）。
		{
			TenantID:       1,
			Name:           "会议提醒",
			HandlerName:    "oaRoomBookingReminder",
			CronExpression: "0 */5 * * * *",
			RetryCount:     0,
			Status:         inframodel.JobStatusRunning,
			Remark:         "每 5 分钟扫描 15 分钟内开始的会议室预约，向预约人发送 meeting_reminder 通知（reminded_at 保证只发一次）；handler_param 可填 YYYY-MM-DD HH:mm:ss 固定现在",
		},
		// 访问日志清理（issue #80，spec §4.6.7）：每日 03:00 按保留期清理
		// 跨租户的 infra_api_log（tenant.Skip）。保留期取配置
		// access-log.retention-days（默认 30），handler_param 可填天数覆盖；
		// 只删过期日志，按保留期例行清理，默认启用。
		{
			TenantID:       1,
			Name:           "访问日志清理",
			HandlerName:    "cleanAccessLogs",
			CronExpression: "0 0 3 * * *",
			RetryCount:     0,
			Status:         inframodel.JobStatusRunning,
			Remark:         "每天 03:00 清理超过保留期（access-log.retention-days，默认 30 天）的 API 访问日志（跨租户）；handler_param 可填天数覆盖保留期",
		},
	}
	// Create 会把带 default 标签的零值列回写成数据库默认值（1 暂停），
	// 因此在插入前先快照种子的意图状态，再逐条用 Exec 显式回写。
	statusByHandler := make(map[string]int8, len(jobs))
	for _, job := range jobs {
		statusByHandler[job.HandlerName] = job.Status
	}
	if err := tenant.Skip(db).Create(&jobs).Error; err != nil {
		return err
	}
	// GORM 对带 `default:1` 标签的零值 Status 会走「省略列 → 落库默认值」，
	// 导致「默认启用」的任务实际以暂停状态入库（issue #63 收口 #57 遗留）。
	// 逐条用 Exec 显式回写种子意图的状态：无论 0（启用）还是 1（暂停）都以
	// 种子为准，避免 ORM 对带 default 标签列的省略行为影响最终状态。
	for handlerName, status := range statusByHandler {
		if err := tenant.Skip(db).Exec(
			"UPDATE `infra_job` SET `status` = ? WHERE `tenant_id` = ? AND `handler_name` = ?",
			status, 1, handlerName,
		).Error; err != nil {
			return fmt.Errorf("seed job status %s: %w", handlerName, err)
		}
	}
	return nil
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
