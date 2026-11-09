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
