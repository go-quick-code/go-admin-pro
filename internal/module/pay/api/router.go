package api

import "github.com/gin-gonic/gin"

// RegisterProtected 注册 pay 模块接口（调用方需已挂载 Auth 中间件）。
// 权限码见 docs/pay-mvp-spec.md §7；渠道的「仅平台租户」在 service 层收口
// （非平台租户 403），这里只做权限码门槛。交易/回调/钱包等路由由后续票追加。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	// 应用（租户级）：CRUD + 渠道绑定（全量替换）。
	app := rg.Group("/pay/app")
	app.GET("/page", h.perm("pay:app:query"), h.PageApps)
	app.GET("/:id", h.perm("pay:app:query"), h.GetApp)
	app.POST("", h.perm("pay:app:create"), h.CreateApp)
	app.PUT("", h.perm("pay:app:update"), h.UpdateApp)
	app.DELETE("/:id", h.perm("pay:app:delete"), h.DeleteApp)
	// 绑定是应用管理的一部分：查询随 pay:app:query、替换随 pay:app:update
	//（spec §7 权限码仅 pay:app:*，不设独立 bind 码）。
	app.GET("/:id/channels", h.perm("pay:app:query"), h.GetAppChannels)
	app.PUT("/:id/channels", h.perm("pay:app:update"), h.ReplaceAppChannels)

	// 渠道（平台级，requirePlatform 在 service 层收口）：只启停 + 凭证合并，
	// 无新增/删除端点（14 行种子常驻，spec §4.10）。
	channel := rg.Group("/pay/channel")
	channel.GET("/page", h.perm("pay:channel:query"), h.PageChannels)
	channel.PUT("/:id", h.perm("pay:channel:update"), h.UpdateChannel)
	// 租户侧只读清单（无凭证）：应用详情绑定 tab 的数据源，挂 pay:app:query
	//（持有支付应用菜单即可读；渠道凭证不在返回里）。
	channel.GET("/list", h.perm("pay:app:query"), h.ListChannelOptions)

	// 钱包（issue #132，spec §5/§4.11）：只读查询。自助端点只挂登录（无权限
	// 码），user_id 归属过滤在 service 层强制；管理端分页挂 pay:wallet:query
	//（钱包/流水共用一个查询码，spec §7）。无写入端点：余额变动只走 service
	// 层唯一入账入口（spec §4.3），余额调整 v1 无管理端入口。
	wallet := rg.Group("/pay/wallet")
	wallet.GET("/my", h.GetMyWallet)
	wallet.GET("/page", h.perm("pay:wallet:query"), h.PageWallets)

	walletTx := rg.Group("/pay/wallet-transaction")
	walletTx.GET("/my", h.PageMyWalletTransactions)
	walletTx.GET("/page", h.perm("pay:wallet:query"), h.PageWalletTransactions)

	// 订单与收银台（issue #133，spec §5/§4.2/§4.11）：下单/收银台/submit/mock
	// 触发只挂登录（自助端点，user_id 归属过滤在 service 层强制）；mock 触发
	// 不公开（登录 + 本人 + 渠道为 mock，防代他人确认支付，spec §4.6）。
	// 管理端分页/详情挂 pay:order:query（无写入端点：交易单据 append-only，
	// spec §4.10；导出随 #138 export-excel 补 pay:order:export）。
	order := rg.Group("/pay/order")
	order.POST("/create", h.CreateOrder)
	order.POST("/submit", h.SubmitOrder)
	order.GET("/page", h.perm("pay:order:query"), h.PageOrders)
	order.GET("/:id", h.perm("pay:order:query"), h.GetOrder)

	// 退款（issue #135，spec §5/§4.4/§4.11）：发起只挂登录（消费方服务端
	// 自助端点，订单归属校验在 service 层强制——非本人 403）；管理端分页/
	// 详情挂 pay:refund:query（交易单据 append-only 无写入端点，spec §4.10；
	// 导出随 #138 export-excel 补 pay:refund:export）。
	refund := rg.Group("/pay/refund")
	refund.POST("/create", h.CreateRefund)
	refund.GET("/page", h.perm("pay:refund:query"), h.PageRefunds)
	refund.GET("/:id", h.perm("pay:refund:query"), h.GetRefund)

	cashier := rg.Group("/pay/cashier")
	cashier.GET("/order", h.GetCashierOrder)

	rg.POST("/pay/mock/notify", h.MockNotifyOrder)

	// 回调通知（issue #134，spec §5）：交易单据只读（append-only，spec §4.10
	// ——无新增/编辑/删除端点），管理页不设 owner 行级数据权限（spec §4.11）；
	// 导出按钮随 #138 的 export-excel 端点补种。
	notifyTask := rg.Group("/pay/notify-task")
	notifyTask.GET("/page", h.perm("pay:notify-task:query"), h.PageNotifyTasks)
	notifyTask.GET("/:id", h.perm("pay:notify-task:query"), h.GetNotifyTask)

	// 充值套餐（issue #136，spec §5/§4.10）：管理端 CRUD 四码
	//（pay:wallet-recharge-package:*，query 为菜单权限码）；自助清单只挂登录
	//（发起充值的套餐选择数据源）。删除守卫：被充值单引用 409（spec §4.10）。
	rechargePackage := rg.Group("/pay/wallet-recharge-package")
	rechargePackage.GET("/page", h.perm("pay:wallet-recharge-package:query"), h.PageRechargePackages)
	rechargePackage.GET("/list", h.ListRechargePackageOptions)
	rechargePackage.GET("/:id", h.perm("pay:wallet-recharge-package:query"), h.GetRechargePackage)
	rechargePackage.POST("", h.perm("pay:wallet-recharge-package:create"), h.CreateRechargePackage)
	rechargePackage.PUT("", h.perm("pay:wallet-recharge-package:update"), h.UpdateRechargePackage)
	rechargePackage.DELETE("/:id", h.perm("pay:wallet-recharge-package:delete"), h.DeleteRechargePackage)

	// 充值单（issue #136，spec §5/§4.7/§4.11）：发起/我的充值只挂登录
	//（自助端点，服务层强制本人钱包归属）；管理端分页挂
	// pay:wallet-recharge:query。充值单只读（append-only 资金事实，无编辑/
	// 删除端点）；充值退款发起走 POST /pay/refund/create（挂充值单订单，
	// 服务层守卫仅全额 + 余额充足，spec §4.7）。
	recharge := rg.Group("/pay/wallet-recharge")
	recharge.POST("/create", h.CreateRecharge)
	recharge.GET("/my", h.PageMyRecharges)
	recharge.GET("/page", h.perm("pay:wallet-recharge:query"), h.PageRecharges)

	// 转账（issue #137，spec §5/§4.8/§4.11）：发起只挂登录（消费方服务端
	// 自助端点，同 #133 下单契约——幂等/渠道校验在 service 层收口）；管理端
	// 分页/详情挂 pay:transfer:query（交易单据 append-only 无写入端点之外的
	// 入口，spec §4.10；导出随 #138 export-excel 补 pay:transfer:export）。
	transfer := rg.Group("/pay/transfer")
	transfer.POST("/create", h.CreateTransfer)
	transfer.GET("/page", h.perm("pay:transfer:query"), h.PageTransfers)
	transfer.GET("/:id", h.perm("pay:transfer:query"), h.GetTransfer)

	// 示例订单（issue #137，spec §5/§6/§4.11）：创建/字典/退款只挂登录
	//（「demo 创建」自助端点，本人校验在 service 层强制——非本人 403）；
	// 分页挂 pay:demo-order:query。发起支付走 GET /pay/cashier/order +
	// POST /pay/order/submit（本人订单校验收口在 cashier/submit）。
	demoOrder := rg.Group("/pay/demo-order")
	demoOrder.POST("/create", h.CreateDemoOrder)
	demoOrder.GET("/page", h.perm("pay:demo-order:query"), h.PageDemoOrders)
	demoOrder.GET("/spus", h.ListDemoSPUs)
	demoOrder.PUT("/:id/refund", h.RefundDemoOrder)

	// 示例提现（issue #137，spec §5/§6/§4.8）：申请只挂登录（自助端点，
	// user_account 服务端取登录主体）；分页挂 pay:demo-withdraw:query；审批/
	// 关闭挂独立权限码 pay:demo-withdraw:approve|close（spec §6 操作按钮走
	// 独立权限码；close 为 spec §5 未列的页面实际授予端点，决策记录见
	// service/demo.go 头注释）。
	demoWithdraw := rg.Group("/pay/demo-withdraw")
	demoWithdraw.POST("/create", h.CreateDemoWithdraw)
	demoWithdraw.GET("/page", h.perm("pay:demo-withdraw:query"), h.PageDemoWithdraws)
	demoWithdraw.PUT("/:id/approve", h.perm("pay:demo-withdraw:approve"), h.ApproveDemoWithdraw)
	demoWithdraw.PUT("/:id/close", h.perm("pay:demo-withdraw:close"), h.CloseDemoWithdraw)
}
