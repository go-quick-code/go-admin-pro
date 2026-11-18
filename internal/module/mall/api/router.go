package api

import "github.com/gin-gonic/gin"

// RegisterProtected 注册 mall 模块管理端接口（调用方需已挂载 Auth 中间件）。
// 权限码见 docs/mall-mvp-spec.md §4/§7：会员 mall:member:*（query/create/
// update——无 delete：会员是业务身份，生命周期由停启用覆盖，issue #174）；
// 积分配置 mall:point:query/update（issue #175，积分是会员域功能，按钮落
// 901 会员管理下）；等级 mall:level:*（query/create/update/delete）；地址随
// 会员查询码 mall:member:query（地址是会员详情数据，spec §7 无独立码）。
// C 端 /mall/app/* 公开组与会员令牌自校验随 T5，由后续票追加注册。
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

	// 积分配置（租户级单行，issue #175）：GET 惰性建行默认 1 / PUT 校验 ≥0，
	// 独立权限码 mall:point:query / mall:point:update（按钮落 901 会员管理下，
	// seed mall.go）。
	point := rg.Group("/mall/point-config")
	point.GET("", h.perm("mall:point:query"), h.GetPointConfig)
	point.PUT("", h.perm("mall:point:update"), h.UpdatePointConfig)
}
