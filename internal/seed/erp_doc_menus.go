package seed

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/doctype"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// menuRow 构造菜单行（菜单页：带路径/组件/图标，query 码挂菜单自身，
// KeepAlive 与可见状态按平台默认）。
func menuRow(id, parent int64, name, path, component, perm, icon string, sort int) sysmodel.Menu {
	return sysmodel.Menu{
		BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name, Permission: perm,
		Type: sysmodel.MenuTypeMenu, ParentID: parent, Path: path, Component: component,
		ComponentName: name, Icon: icon, Sort: sort, Visible: true,
		Status: sysmodel.StatusEnable, KeepAlive: true,
	}
}

// btnRow 构造按钮行（无路径/组件，权限码挂按钮）。
func btnRow(id, parent int64, name, perm string, sort int) sysmodel.Menu {
	return sysmodel.Menu{
		BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name, Permission: perm,
		Type: sysmodel.MenuTypeButton, ParentID: parent, Sort: sort,
		Visible: true, Status: sysmodel.StatusEnable,
	}
}

// dirRow 构造目录行（无权限码，仅侧栏分组）。
func dirRow(id, parent int64, name, path, icon string, sort int) sysmodel.Menu {
	return sysmodel.Menu{
		BaseEntity: sysmodel.BaseEntity{ID: id}, Name: name,
		Type: sysmodel.MenuTypeDir, ParentID: parent, Path: path, Icon: icon, Sort: sort,
		Visible: true, Status: sysmodel.StatusEnable,
	}
}

// erpDocMenuParent 是 12 类单据菜单挂载的 ERP 目录（spec §7 的 700 段）。
const erpDocMenuParent int64 = 700

// erpCoreMenus 构造 ERP 700 段中不进单据类型注册表的部分（issue #125：
// 主数据、库存/流水只读页、统计与结算账户——形状不齐：/list 与 /page 并存、
// 无 approve、统计四条路由共用一个码，硬塞会让注册表长出一堆可选字段）。
// #88：700 dir、710–712（分类/单位/产品）+ 按钮 7101–7124；#89：720 仓库 /
// 730 供应商 / 740 客户 + 按钮 7201–7204/7301–7304/7401–7404；#95：701 首页
// 统计；#90：721–722 库存只读页；#94：750 结算账户。按钮 = 菜单ID×10+n
//（grep 去重确认未占用）。权限码仍为字面量，与路由的一致性由 S4 AST
// 双向比对兜底（erp_perm_sets_test.go）。
func erpCoreMenus() []sysmodel.Menu {
	return []sysmodel.Menu{
		dirRow(700, 0, "ERP 进销存", "/erp", "ShoppingCart", 70),
		// 首页统计（issue #95，spec §6/§7）：701 挂 erp:statistics:query；按钮
		// 7011 与菜单同码（对齐 CRM 仪表盘 4140/4141 的形态，grep 去重确认未占用）。
		menuRow(701, 700, "首页统计", "statistics", "erp/statistics/index", "erp:statistics:query", "DataLine", 22),
		btnRow(7011, 701, "统计查询", "erp:statistics:query", 1),
		menuRow(710, 700, "产品分类", "product-category", "erp/product-category/index", "erp:product-category:query", "Files", 1),
		btnRow(7101, 710, "分类新增", "erp:product-category:create", 1),
		btnRow(7102, 710, "分类修改", "erp:product-category:update", 2),
		btnRow(7103, 710, "分类删除", "erp:product-category:delete", 3),
		btnRow(7104, 710, "分类导出", "erp:product-category:export", 4),
		menuRow(711, 700, "产品单位", "product-unit", "erp/product-unit/index", "erp:product-unit:query", "CollectionTag", 2),
		btnRow(7111, 711, "单位新增", "erp:product-unit:create", 1),
		btnRow(7112, 711, "单位修改", "erp:product-unit:update", 2),
		btnRow(7113, 711, "单位删除", "erp:product-unit:delete", 3),
		btnRow(7114, 711, "单位导出", "erp:product-unit:export", 4),
		menuRow(712, 700, "产品管理", "product", "erp/product/index", "erp:product:query", "Goods", 3),
		btnRow(7121, 712, "产品新增", "erp:product:create", 1),
		btnRow(7122, 712, "产品修改", "erp:product:update", 2),
		btnRow(7123, 712, "产品删除", "erp:product:delete", 3),
		btnRow(7124, 712, "产品导出", "erp:product:export", 4),
		// 仓库/供应商/客户主数据（issue #89，spec §3.1/§7）：仓库管理员/采购员/
		// 销售员维护；权限码 erp:warehouse:* / erp:supplier:* / erp:customer:*。
		menuRow(720, 700, "仓库管理", "warehouse", "erp/warehouse/index", "erp:warehouse:query", "House", 4),
		btnRow(7201, 720, "仓库新增", "erp:warehouse:create", 1),
		btnRow(7202, 720, "仓库修改", "erp:warehouse:update", 2),
		btnRow(7203, 720, "仓库删除", "erp:warehouse:delete", 3),
		btnRow(7204, 720, "仓库导出", "erp:warehouse:export", 4),
		menuRow(730, 700, "供应商管理", "supplier", "erp/supplier/index", "erp:supplier:query", "Shop", 11),
		btnRow(7301, 730, "供应商新增", "erp:supplier:create", 1),
		btnRow(7302, 730, "供应商修改", "erp:supplier:update", 2),
		btnRow(7303, 730, "供应商删除", "erp:supplier:delete", 3),
		btnRow(7304, 730, "供应商导出", "erp:supplier:export", 4),
		menuRow(740, 700, "客户管理", "customer", "erp/customer/index", "erp:customer:query", "User", 15),
		btnRow(7401, 740, "客户新增", "erp:customer:create", 1),
		btnRow(7402, 740, "客户修改", "erp:customer:update", 2),
		btnRow(7403, 740, "客户删除", "erp:customer:delete", 3),
		btnRow(7404, 740, "客户导出", "erp:customer:export", 4),
		// 库存只读页（issue #90，spec §6/§7）：721 产品库存、722 出入库明细
		//（spec §7 号段；查询页——库存行无 owner 语义，只有 query/export 按钮，
		// 无增删改）。按钮 = 菜单ID×10+n，7211–7214/7221–7224 grep 去重确认未占用。
		menuRow(721, 700, "产品库存", "stock", "erp/stock/index", "erp:stock:query", "Box", 5),
		btnRow(7211, 721, "库存导出", "erp:stock:export", 1),
		menuRow(722, 700, "出入库明细", "stock-record", "erp/stock-record/index", "erp:stock-record:query", "List", 6),
		btnRow(7221, 722, "流水导出", "erp:stock-record:export", 1),
		// 财务（issue #94，spec §3.6/§6/§7）：750 结算账户。账户页 query 菜单 +
		// create/update/delete（无导出，spec §6 未列）。按钮 = 菜单ID×10+n，
		// 7501–7503 grep 去重确认未占用。
		menuRow(750, 700, "结算账户", "finance-account", "erp/finance-account/index", "erp:finance-account:query", "Wallet", 19),
		btnRow(7501, 750, "账户新增", "erp:finance-account:create", 1),
		btnRow(7502, 750, "账户修改", "erp:finance-account:update", 2),
		btnRow(7503, 750, "账户删除", "erp:finance-account:delete", 3),
	}
}

// erpDocMenus 从单据类型注册表（internal/module/erp/doctype，ADR 0020）投影
// 12 类单据的菜单段：每类 1 个菜单行（ID = MenuID、路径/组件 = key 派生、
// 权限 = <PermPrefix>:query、图标/排序/标题 = 条目字段）+ 5 个按钮行
//（ID = MenuID*10+n、名称 = 前缀 + 动作后缀、权限 = 派生动作码、sort = n，
// n=1..5 对应 create/update/delete/approve/export）。共 72 行，逐字段等于
// 重构前的 72 行字面量（golden CSV 兜底，issue #125 S3）。
// 号段分配（spec §7）：723/724 其它出入库（#90）、725/726 调拨/盘点（#93）、
// 731–733 采购三单（#91）、741–743 销售三单（#92）、751/752 收付款（#94）。
func erpDocMenus() []sysmodel.Menu {
	entries := doctype.All()
	menus := make([]sysmodel.Menu, 0, len(entries)*(1+5))
	for _, e := range entries {
		menus = append(menus, menuRow(
			e.MenuID, erpDocMenuParent, e.Name, string(e.Key),
			"erp/"+string(e.Key)+"/index", e.Perm(doctype.ActionQuery), e.Icon, e.Sort,
		))
		for _, b := range e.Buttons() {
			menus = append(menus, btnRow(b.ID, e.MenuID, b.Name, b.Perm, b.Sort))
		}
	}
	return menus
}
