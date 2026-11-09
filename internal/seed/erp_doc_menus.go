package seed

import (
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

// erpDocMenus 构造 ERP 单据段菜单（12 单据菜单 + 60 按钮，共 72 行，parent
// 均在 700 段下）。单据页按 query 菜单 + create/update/delete/approve/export
// 全套按钮（approve 为独立审核权限码，spec §4.1）；按钮 = 菜单ID×10+n，
// n=1..5 对应 create/update/delete/approve/export，sort 与 n 一致。
// 号段分配（spec §7，grep 去重确认未占用）：723/724 其它出入库（#90）、
// 725/726 调拨/盘点（#93）、731–733 采购三单（#91）、741–743 销售三单（#92）、
// 751/752 收付款（#94）。
func erpDocMenus() []sysmodel.Menu {
	return []sysmodel.Menu{
		// 其它入库/出库（issue #90，spec §6/§7）：723 其它入库、724 其它出库。
		menuRow(723, 700, "其它入库", "stock-in", "erp/stock-in/index", "erp:stock-in:query", "Download", 7),
		btnRow(7231, 723, "入库新增", "erp:stock-in:create", 1),
		btnRow(7232, 723, "入库修改", "erp:stock-in:update", 2),
		btnRow(7233, 723, "入库删除", "erp:stock-in:delete", 3),
		btnRow(7234, 723, "入库审核", "erp:stock-in:approve", 4),
		btnRow(7235, 723, "入库导出", "erp:stock-in:export", 5),
		menuRow(724, 700, "其它出库", "stock-out", "erp/stock-out/index", "erp:stock-out:query", "Upload", 8),
		btnRow(7241, 724, "出库新增", "erp:stock-out:create", 1),
		btnRow(7242, 724, "出库修改", "erp:stock-out:update", 2),
		btnRow(7243, 724, "出库删除", "erp:stock-out:delete", 3),
		btnRow(7244, 724, "出库审核", "erp:stock-out:approve", 4),
		btnRow(7245, 724, "出库导出", "erp:stock-out:export", 5),
		// 采购三单（issue #91，spec §6/§7）：731 采购订单、732 采购入库、733 采购退货。
		menuRow(731, 700, "采购订单", "purchase-order", "erp/purchase-order/index", "erp:purchase-order:query", "Tickets", 12),
		btnRow(7311, 731, "订单新增", "erp:purchase-order:create", 1),
		btnRow(7312, 731, "订单修改", "erp:purchase-order:update", 2),
		btnRow(7313, 731, "订单删除", "erp:purchase-order:delete", 3),
		btnRow(7314, 731, "订单审核", "erp:purchase-order:approve", 4),
		btnRow(7315, 731, "订单导出", "erp:purchase-order:export", 5),
		menuRow(732, 700, "采购入库", "purchase-in", "erp/purchase-in/index", "erp:purchase-in:query", "GoodsFilled", 13),
		btnRow(7321, 732, "入库新增", "erp:purchase-in:create", 1),
		btnRow(7322, 732, "入库修改", "erp:purchase-in:update", 2),
		btnRow(7323, 732, "入库删除", "erp:purchase-in:delete", 3),
		btnRow(7324, 732, "入库审核", "erp:purchase-in:approve", 4),
		btnRow(7325, 732, "入库导出", "erp:purchase-in:export", 5),
		menuRow(733, 700, "采购退货", "purchase-return", "erp/purchase-return/index", "erp:purchase-return:query", "RefreshLeft", 14),
		btnRow(7331, 733, "退货新增", "erp:purchase-return:create", 1),
		btnRow(7332, 733, "退货修改", "erp:purchase-return:update", 2),
		btnRow(7333, 733, "退货删除", "erp:purchase-return:delete", 3),
		btnRow(7334, 733, "退货审核", "erp:purchase-return:approve", 4),
		btnRow(7335, 733, "退货导出", "erp:purchase-return:export", 5),
		// 销售三单（issue #92，spec §6/§7）：741 销售订单、742 销售出库、743 销售退货。
		menuRow(741, 700, "销售订单", "sale-order", "erp/sale-order/index", "erp:sale-order:query", "Ticket", 16),
		btnRow(7411, 741, "订单新增", "erp:sale-order:create", 1),
		btnRow(7412, 741, "订单修改", "erp:sale-order:update", 2),
		btnRow(7413, 741, "订单删除", "erp:sale-order:delete", 3),
		btnRow(7414, 741, "订单审核", "erp:sale-order:approve", 4),
		btnRow(7415, 741, "订单导出", "erp:sale-order:export", 5),
		menuRow(742, 700, "销售出库", "sale-out", "erp/sale-out/index", "erp:sale-out:query", "Position", 17),
		btnRow(7421, 742, "出库新增", "erp:sale-out:create", 1),
		btnRow(7422, 742, "出库修改", "erp:sale-out:update", 2),
		btnRow(7423, 742, "出库删除", "erp:sale-out:delete", 3),
		btnRow(7424, 742, "出库审核", "erp:sale-out:approve", 4),
		btnRow(7425, 742, "出库导出", "erp:sale-out:export", 5),
		menuRow(743, 700, "销售退货", "sale-return", "erp/sale-return/index", "erp:sale-return:query", "RefreshLeft", 18),
		btnRow(7431, 743, "退货新增", "erp:sale-return:create", 1),
		btnRow(7432, 743, "退货修改", "erp:sale-return:update", 2),
		btnRow(7433, 743, "退货删除", "erp:sale-return:delete", 3),
		btnRow(7434, 743, "退货审核", "erp:sale-return:approve", 4),
		btnRow(7435, 743, "退货导出", "erp:sale-return:export", 5),
		// 收付款（issue #94，spec §6/§7）：751 付款单、752 收款单。
		menuRow(751, 700, "付款单", "finance-payment", "erp/finance-payment/index", "erp:finance-payment:query", "Money", 20),
		btnRow(7511, 751, "付款新增", "erp:finance-payment:create", 1),
		btnRow(7512, 751, "付款修改", "erp:finance-payment:update", 2),
		btnRow(7513, 751, "付款删除", "erp:finance-payment:delete", 3),
		btnRow(7514, 751, "付款审核", "erp:finance-payment:approve", 4),
		btnRow(7515, 751, "付款导出", "erp:finance-payment:export", 5),
		menuRow(752, 700, "收款单", "finance-receipt", "erp/finance-receipt/index", "erp:finance-receipt:query", "Coin", 21),
		btnRow(7521, 752, "收款新增", "erp:finance-receipt:create", 1),
		btnRow(7522, 752, "收款修改", "erp:finance-receipt:update", 2),
		btnRow(7523, 752, "收款删除", "erp:finance-receipt:delete", 3),
		btnRow(7524, 752, "收款审核", "erp:finance-receipt:approve", 4),
		btnRow(7525, 752, "收款导出", "erp:finance-receipt:export", 5),
		// 库存调拨/盘点（issue #93，spec §6/§7）：725 库存调拨（DB）、726 库存盘点（PD）。
		menuRow(725, 700, "库存调拨", "stock-move", "erp/stock-move/index", "erp:stock-move:query", "Switch", 9),
		btnRow(7251, 725, "调拨新增", "erp:stock-move:create", 1),
		btnRow(7252, 725, "调拨修改", "erp:stock-move:update", 2),
		btnRow(7253, 725, "调拨删除", "erp:stock-move:delete", 3),
		btnRow(7254, 725, "调拨审核", "erp:stock-move:approve", 4),
		btnRow(7255, 725, "调拨导出", "erp:stock-move:export", 5),
		menuRow(726, 700, "库存盘点", "stock-check", "erp/stock-check/index", "erp:stock-check:query", "Finished", 10),
		btnRow(7261, 726, "盘点新增", "erp:stock-check:create", 1),
		btnRow(7262, 726, "盘点修改", "erp:stock-check:update", 2),
		btnRow(7263, 726, "盘点删除", "erp:stock-check:delete", 3),
		btnRow(7264, 726, "盘点审核", "erp:stock-check:approve", 4),
		btnRow(7265, 726, "盘点导出", "erp:stock-check:export", 5),
	}
}
