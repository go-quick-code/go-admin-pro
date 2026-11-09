// Package doctype 是 ERP 单据类型注册表（ADR 0020，issue #125）：12 类可审核
// 单据（采购订单/采购入库/采购退货、销售订单/销售出库/销售退货、其它入库/
// 其它出库、库存调拨/库存盘点、收款单/付款单）的静态元数据清单，是路由、
// 权限码与菜单种子的唯一事实来源。
//
// 纯元数据包：只依赖标准库，不引用 gin、service、model、dto——HTTP 形状由
// erp/api 的 docSpec 闭包挂接，菜单行由 internal/seed 投影。派生规则：
//
//   - 权限码 = <PermPrefix>:<action>，动作固定为 query/create/update/delete/
//     approve/export 六码，不提供逐码覆盖（12 类今天全部六码齐全；将来出现
//     不对称资源，那是另一种资源，不进本注册表）；
//   - 菜单行权限 = <PermPrefix>:query（query 码挂菜单自身）；
//   - 按钮 ID = MenuID*10+n，n=1..5 固定对应 create/update/delete/approve/
//     export，按钮 sort 与 n 一致；
//   - 完备性只在测试断言（本包与 api、seed 的白盒测试），不在启动期 panic
//     ——静态切片上的程序员错误 CI 必挂，换运行时崩溃没有安全收益。
package doctype

// Key 单据类型键：同时是 HTTP 路由段（/erp/<key>/...）与前端路径段
// （erp/<key>/index），也是 docSpec 闭包集合的索引类型。
type Key string

// 动作常量：权限码六动作（ADR 0020，不提供逐码覆盖）。
const (
	ActionQuery   = "query"
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionDelete  = "delete"
	ActionApprove = "approve"
	ActionExport  = "export"
)

// Type 是一类单据的注册表条目。字段语义（ADR 0020）：
// Key 路由段 = 前端路径段；Name 菜单标题；Icon/Sort 菜单侧栏图标与排序；
// MenuID 菜单 ID（按钮 ID 的基号）；ButtonPrefix 按钮名称前缀；PermPrefix
// 权限前缀；ExcelFile/ExcelTitle 导出文件名与标题（Content-Disposition）。
type Type struct {
	Key          Key
	Name         string
	Icon         string
	Sort         int
	MenuID       int64
	ButtonPrefix string
	PermPrefix   string
	ExcelFile    string
	ExcelTitle   string
}

// 12 类单据条目。菜单号段按 spec §7（723/724 #90、725/726 #93、731–733 #91、
// 741–743 #92、751/752 #94，grep 去重确认未占用）；新增一类单据在这里加一行。
var (
	StockIn        = Type{Key: "stock-in", Name: "其它入库", Icon: "Download", Sort: 7, MenuID: 723, ButtonPrefix: "入库", PermPrefix: "erp:stock-in", ExcelFile: "erp-stock-ins", ExcelTitle: "其它入库单"}
	StockOut       = Type{Key: "stock-out", Name: "其它出库", Icon: "Upload", Sort: 8, MenuID: 724, ButtonPrefix: "出库", PermPrefix: "erp:stock-out", ExcelFile: "erp-stock-outs", ExcelTitle: "其它出库单"}
	StockMove      = Type{Key: "stock-move", Name: "库存调拨", Icon: "Switch", Sort: 9, MenuID: 725, ButtonPrefix: "调拨", PermPrefix: "erp:stock-move", ExcelFile: "erp-stock-moves", ExcelTitle: "库存调拨单"}
	StockCheck     = Type{Key: "stock-check", Name: "库存盘点", Icon: "Finished", Sort: 10, MenuID: 726, ButtonPrefix: "盘点", PermPrefix: "erp:stock-check", ExcelFile: "erp-stock-checks", ExcelTitle: "库存盘点单"}
	PurchaseOrder  = Type{Key: "purchase-order", Name: "采购订单", Icon: "Tickets", Sort: 12, MenuID: 731, ButtonPrefix: "订单", PermPrefix: "erp:purchase-order", ExcelFile: "erp-purchase-orders", ExcelTitle: "采购订单"}
	PurchaseIn     = Type{Key: "purchase-in", Name: "采购入库", Icon: "GoodsFilled", Sort: 13, MenuID: 732, ButtonPrefix: "入库", PermPrefix: "erp:purchase-in", ExcelFile: "erp-purchase-ins", ExcelTitle: "采购入库单"}
	PurchaseReturn = Type{Key: "purchase-return", Name: "采购退货", Icon: "RefreshLeft", Sort: 14, MenuID: 733, ButtonPrefix: "退货", PermPrefix: "erp:purchase-return", ExcelFile: "erp-purchase-returns", ExcelTitle: "采购退货单"}
	SaleOrder      = Type{Key: "sale-order", Name: "销售订单", Icon: "Ticket", Sort: 16, MenuID: 741, ButtonPrefix: "订单", PermPrefix: "erp:sale-order", ExcelFile: "erp-sale-orders", ExcelTitle: "销售订单"}
	SaleOut        = Type{Key: "sale-out", Name: "销售出库", Icon: "Position", Sort: 17, MenuID: 742, ButtonPrefix: "出库", PermPrefix: "erp:sale-out", ExcelFile: "erp-sale-outs", ExcelTitle: "销售出库单"}
	SaleReturn     = Type{Key: "sale-return", Name: "销售退货", Icon: "RefreshLeft", Sort: 18, MenuID: 743, ButtonPrefix: "退货", PermPrefix: "erp:sale-return", ExcelFile: "erp-sale-returns", ExcelTitle: "销售退货单"}
	FinancePayment = Type{Key: "finance-payment", Name: "付款单", Icon: "Money", Sort: 20, MenuID: 751, ButtonPrefix: "付款", PermPrefix: "erp:finance-payment", ExcelFile: "erp-finance-payments", ExcelTitle: "付款单"}
	FinanceReceipt = Type{Key: "finance-receipt", Name: "收款单", Icon: "Coin", Sort: 21, MenuID: 752, ButtonPrefix: "收款", PermPrefix: "erp:finance-receipt", ExcelFile: "erp-finance-receipts", ExcelTitle: "收款单"}
)

// All 返回全部单据类型（静态清单，按菜单 sort 升序）。消费方：erp/api 的
// docSpec 注册循环与 internal/seed 的菜单投影。
func All() []Type {
	return []Type{
		StockIn, StockOut, StockMove, StockCheck,
		PurchaseOrder, PurchaseIn, PurchaseReturn,
		SaleOrder, SaleOut, SaleReturn,
		FinancePayment, FinanceReceipt,
	}
}

// Perm 返回动作权限码 <PermPrefix>:<action>（action 取 Action* 常量）。
func (t Type) Perm(action string) string {
	return t.PermPrefix + ":" + action
}

// Button 是单据页按钮行的派生数据（internal/seed 投影按钮行时消费）。
type Button struct {
	ID   int64  // MenuID*10+n
	Name string // ButtonPrefix + 动作中文后缀
	Perm string // PermPrefix + ":" + 动作
	Sort int    // n
}

// buttonNames 与 buttonActions 的下标与按钮序号 n-1 一一对应：n=1..5 固定
// 对应 create/update/delete/approve/export（ADR 0020 派生规则）。
var (
	buttonNames   = [5]string{"新增", "修改", "删除", "审核", "导出"}
	buttonActions = [5]string{ActionCreate, ActionUpdate, ActionDelete, ActionApprove, ActionExport}
)

// Buttons 派生单据页的五个按钮行数据（n=1..5，sort 与 n 一致）。
func (t Type) Buttons() []Button {
	buttons := make([]Button, len(buttonNames))
	for i, name := range buttonNames {
		n := i + 1
		buttons[i] = Button{
			ID:   t.MenuID*10 + int64(n),
			Name: t.ButtonPrefix + name,
			Perm: t.PermPrefix + ":" + buttonActions[i],
			Sort: n,
		}
	}
	return buttons
}
