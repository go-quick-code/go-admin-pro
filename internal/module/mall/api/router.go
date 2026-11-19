package api

import "github.com/gin-gonic/gin"

// RegisterProtected 注册 mall 模块管理端接口（调用方需已挂载 Auth 中间件）。
// 权限码见 docs/mall-mvp-spec.md §4/§7：会员 mall:member:*（query/create/
// update——无 delete：会员是业务身份，生命周期由停启用覆盖，issue #174）；
// 积分配置 mall:point:query/update（issue #175，积分是会员域功能，按钮落
// 901 会员管理下）；等级 mall:level:*（query/create/update/delete）；地址随
// 会员查询码 mall:member:query（地址是会员详情数据，spec §7 无独立码）；商品
// mall:category:* / mall:brand:* / mall:property:*（各四码，属性值是属性名
// 子资源随 mall:property:* 同码，issue #176）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	// 会员（租户级）：分页/详情/创建/更新/停启用。停启用挂 mall:member:update
	//（状态位更新随更新码，不设独立码——pay approve/close 的独立码先例针对
	// 业务状态机动作，会员启停是普通字段更新）。
	member := rg.Group("/mall/member")
	member.GET("/page", h.perm("mall:member:query"), h.PageMembers)
	member.GET("/:id", h.perm("mall:member:query"), h.GetMember)
	member.POST("", h.perm("mall:member:create"), h.CreateMember)
	member.PUT("", h.perm("mall:member:update"), h.UpdateMember)
	member.PUT("/:id/status", h.perm("mall:member:update"), h.UpdateMemberStatus)

	// 积分（issue #175）：调整挂 mall:member:update（管理动作是会员更新语义
	// 的子集，不设独立码——同停启用口径）；流水分页挂 mall:member:query
	//（会员详情抽屉数据源，同地址口径）。
	member.POST("/adjust-point", h.perm("mall:member:update"), h.AdjustMemberPoint)
	member.GET("/:id/point-records", h.perm("mall:member:query"), h.PageMemberPointRecords)

	// 等级（租户级）：CRUD 四码。清单端点挂 mall:member:query——会员表单的
	// 等级下拉数据源（同 pay channel/list 挂 pay:app:query 的「绑定 tab
	// 数据源」形状：持有会员菜单即可读，无凭证类敏感列）。
	level := rg.Group("/mall/level")
	level.GET("/page", h.perm("mall:level:query"), h.PageLevels)
	level.GET("/list", h.perm("mall:member:query"), h.ListLevelOptions)
	level.GET("/:id", h.perm("mall:level:query"), h.GetLevel)
	level.POST("", h.perm("mall:level:create"), h.CreateLevel)
	level.PUT("", h.perm("mall:level:update"), h.UpdateLevel)
	level.DELETE("/:id", h.perm("mall:level:delete"), h.DeleteLevel)

	// 地址（租户级，只读）：按会员查 + 管理视图分页，随 mall:member:query
	//（spec §4「地址列表（按会员）」，无写入端点——维护随 C 端自助）。
	address := rg.Group("/mall/address")
	address.GET("/page", h.perm("mall:member:query"), h.PageAddresses)
	address.GET("/member/:id", h.perm("mall:member:query"), h.ListMemberAddresses)

	// 商品（租户级，issue #176，spec §4）：分类树+CRUD / 品牌 CRUD /
	// 属性名+属性值 CRUD。权限码 mall:category:* / mall:brand:* /
	// mall:property:*（各四码，属性值是属性名子资源随 mall:property:* 同码，
	// issue #176）。删除守卫（分类有子级/被 SPU 引用禁删 409）在 service 层；
	// SPU 引用计数随 T4 SPU 表接线。
	product := rg.Group("/mall/product")
	// 分类（两级树）：树查询 + CRUD（同级 name 唯一 409）。
	product.GET("/category/tree", h.perm("mall:category:query"), h.TreeProductCategories)
	product.GET("/category/:id", h.perm("mall:category:query"), h.GetProductCategory)
	product.POST("/category", h.perm("mall:category:create"), h.CreateProductCategory)
	product.PUT("/category", h.perm("mall:category:update"), h.UpdateProductCategory)
	product.DELETE("/category/:id", h.perm("mall:category:delete"), h.DeleteProductCategory)
	// 品牌：分页/详情/创建/更新/删除（name 租户内唯一 409）。
	product.GET("/brand/page", h.perm("mall:brand:query"), h.PageBrands)
	product.GET("/brand/:id", h.perm("mall:brand:query"), h.GetBrand)
	product.POST("/brand", h.perm("mall:brand:create"), h.CreateBrand)
	product.PUT("/brand", h.perm("mall:brand:update"), h.UpdateBrand)
	product.DELETE("/brand/:id", h.perm("mall:brand:delete"), h.DeleteBrand)
	// 属性名：分页/详情/创建/更新/删除（name 租户内唯一 409；删除级联值）。
	product.GET("/property/page", h.perm("mall:property:query"), h.PageProperties)
	product.GET("/property/:id", h.perm("mall:property:query"), h.GetProductProperty)
	product.POST("/property", h.perm("mall:property:create"), h.CreateProperty)
	product.PUT("/property", h.perm("mall:property:update"), h.UpdateProperty)
	product.DELETE("/property/:id", h.perm("mall:property:delete"), h.DeleteProperty)
	// 属性值（属性名子资源，随 mall:property:* 同码）：属性内清单 + CRUD
	//（(property_id, name) 唯一 409）。gin 同层静态段 value 与参数段 :id
	// 共存（照 /mall/level/{list,:id} 先例）。
	product.GET("/property/:id/value", h.perm("mall:property:query"), h.ListPropertyValues)
	product.POST("/property/value", h.perm("mall:property:create"), h.CreatePropertyValue)
	product.PUT("/property/value", h.perm("mall:property:update"), h.UpdatePropertyValue)
	product.DELETE("/property/value/:id", h.perm("mall:property:delete"), h.DeletePropertyValue)
	// SPU/SKU（issue #177，spec §4）：分页/详情（含 SKU）/创建（SPU+SKU 同
	// 事务）/更新（SKU 集全量替换）/上下架/删除（订单引用 409 + 级联软删
	// SKU）。权限码 mall:spu:*（query/create/update/delete 四码，与菜单种子
	// 914 段一致）；上下架挂 mall:spu:update（状态位更新随更新码，同会员
	// 停启用口径）。删除守卫（订单引用计数 repo.CountOrdersBySPU，T6 前
	// 恒 0）与分类/品牌删除守卫（被未删 SPU 引用禁删）在 service 层。
	spu := product.Group("/spu")
	spu.GET("/page", h.perm("mall:spu:query"), h.PageProductSPUs)
	spu.GET("/:id", h.perm("mall:spu:query"), h.GetProductSPU)
	spu.POST("", h.perm("mall:spu:create"), h.CreateProductSPU)
	spu.PUT("", h.perm("mall:spu:update"), h.UpdateProductSPU)
	spu.PUT("/:id/status", h.perm("mall:spu:update"), h.UpdateProductSPUStatus)
	spu.DELETE("/:id", h.perm("mall:spu:delete"), h.DeleteProductSPU)

	// 积分配置（租户级单行，issue #175）：GET 惰性建行默认 1 / PUT 校验 ≥0，
	// 独立权限码 mall:point:query / mall:point:update（按钮落 901 会员管理下，
	// seed mall.go）。
	point := rg.Group("/mall/point-config")
	point.GET("", h.perm("mall:point:query"), h.GetPointConfig)
	point.PUT("", h.perm("mall:point:update"), h.UpdatePointConfig)

	// 交易订单（租户级，issue #180，T7，spec §4）：分页（状态/编号/会员/
	// 时间过滤）/详情（含 items 与 payOrderNo 等管理字段全量）/管理员备注/
	// 取消（仅待支付，走 trade_rules 409）/完成（仅已支付 10→20，走状态机）。
	// 权限码 mall:trade:{query,cancel,complete,update}——update 用于管理员
	// 备注（字段更新语义，同会员停启用挂 update 口径）；取消/完成是业务状态
	// 机动作，独立码（对齐 pay approve/close 先例）。菜单 920/921 + 按钮
	// 9211–9213（seed mall.go）。gin 同层静态段 page 与参数段 :id 分属不同
	// 链（照 /mall/app/trade/order/{page,:id} 先例）。
	trade := rg.Group("/mall/trade")
	trade.GET("/order/page", h.perm("mall:trade:query"), h.PageTradeOrders)
	trade.GET("/order/:id", h.perm("mall:trade:query"), h.GetTradeOrder)
	trade.PUT("/order/:id/remark", h.perm("mall:trade:update"), h.UpdateTradeOrderRemark)
	trade.POST("/order/:id/cancel", h.perm("mall:trade:cancel"), h.CancelTradeOrder)
	trade.POST("/order/:id/complete", h.perm("mall:trade:complete"), h.CompleteTradeOrder)
}

// RegisterPublic 注册 mall 模块 C 端公开接口（/mall/app/*，issue #178 T5，
// spec §2「C 端 /mall/app/* 挂公开组（RegisterPublic，无登录、限速）」与
// §7 裁决 2「C 端身份 = 模块内 HMAC 令牌自校验，公开组 + handler 内校验，
// 不动 router 组结构」）。router.go 对 RegisterPublic 按 type assert 探测
// 自动挂公开组（照 notify/pay 先例），公开组在 Auth 中间件之外——会员令牌
// 由 memberAuth 在 handler 内自校验（service.VerifyMemberToken：签名/过期/
// DB 查 member_user status），不经过 RequirePermission。
//
// 限速（照 pay demo 回调先例的 Limiter 用法，按直连对端 IP）：
//   - 注册/登录 10 次/分钟（弱凭证暴力尝试面）；
//   - 我的信息/商品浏览 60 次/分钟（浏览类放宽，照 system publicIPLimit）。
//
// 公开端点不记访问日志（公开组不在 router 的 protected 组内，结构使然）。
// 本仓前端是 admin SPA（spec §7 裁决 6）：C 端端点无 admin 消费方，
// web/src/api/mall.ts 不加这些函数。
func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	app := rg.Group("/mall/app")

	// 注册/登录（无令牌）：统一挂注册端限速（同桶——注册与登录是同一
	// 凭证面的两条入口，10 次/分钟合并计数）。
	authGroup := app.Group("/auth")
	authGroup.Use(h.limitAppIP(h.appAuthLimiter))
	authGroup.POST("/register", h.AppRegister)
	authGroup.POST("/login", h.AppLogin)

	// 需会员令牌端点：memberAuth handler 内自校验 + 浏览限速。auth/me、
	// 商品浏览与交易端点同桶（同是已获令牌的自助面）。gin 同层静态段 /auth、
	// /product、/cart、/trade 与参数段分属不同链，无路由冲突。
	tokenGroup := app.Group("")
	tokenGroup.Use(h.limitAppIP(h.appBrowseLimiter), h.memberAuth())
	tokenGroup.GET("/auth/me", h.AppMe)
	tokenGroup.GET("/product/spu/page", h.AppPageProductSPUs)
	tokenGroup.GET("/product/spu/:id", h.AppGetProductSPU)

	// 购物车（issue #179，spec §4）：加购/改数量/选中切换/删除/我的购物车。
	// 归属主体恒取 memberAuth 写入的 Principal.UserID（越权 404，不收客户端
	// 传参）；gin 同层静态段 cart 与参数段 :id 分属不同链：cart/delete 是
	// 独立静态段，/:id/count、/:id/selected 是参数段子路径。
	cart := tokenGroup.Group("/cart")
	cart.POST("", h.AppAddCart)
	cart.GET("", h.AppListCart)
	cart.POST("/delete", h.AppDeleteCart)
	cart.PUT("/:id/count", h.AppUpdateCartCount)
	cart.PUT("/:id/selected", h.AppSelectCart)

	// 交易（issue #179，spec §4）：结算试算/创建订单/我的订单分页/详情/取消/
	// 我的积分流水。发起支付（内部转 pay，issue #181 T8）：pay 侧错误透传，
	// mock 渠道返回待支付参数、wallet 渠道同步成功。
	trade := tokenGroup.Group("/trade")
	trade.GET("/settlement", h.AppSettlement)
	trade.POST("/order", h.AppCreateOrder)
	trade.GET("/order/page", h.AppPageOrders)
	trade.GET("/order/:id", h.AppGetOrder)
	trade.POST("/order/:id/cancel", h.AppCancelOrder)
	trade.POST("/order/:id/pay", h.AppPayOrder)
	trade.GET("/point-records", h.AppPageMyPointRecords)

	// 收货地址（规格 §4 C 端地址维护；T6 收口补口——下单读地址，地址写入
	// 唯一入口在 C 端）：列表（默认置顶）/新增（首地址自动默认）/更新/
	// 设默认（互斥清旧）/删除。归属谓词全程携带，越权 404。
	address := tokenGroup.Group("/address")
	address.GET("", h.AppListAddresses)
	address.POST("", h.AppCreateAddress)
	address.PUT("/:id", h.AppUpdateAddress)
	address.PUT("/:id/default", h.AppSetDefaultAddress)
	address.DELETE("/:id", h.AppDeleteAddress)
}
