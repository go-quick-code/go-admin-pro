package seed

import (
	"fmt"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// mall.go 是 mall 会员域与商品域、交易域种子（issue #174/#175/#176/#177/#180，
// docs/mall-mvp-spec.md §3.1/§3.2/§3.3/§5/§7）：
// 会员中心 900 段菜单 + 「普通会员」等级种子一行 + 商品 910 段菜单 +
// 交易 920 段菜单（issue #180，T7）。
//
// 菜单（spec §5「9xx 段——900 会员中心（member/level/address 页）、910 商品
//（category/brand/property/spu 页）、920 交易订单（order 页）」）：
// dir 900 会员中心 + 901 会员管理（member 页）+ 902 会员等级（level 页）；
// dir 910 商品 + 911 商品分类（category 页）+ 912 商品品牌（brand 页）+
// 913 商品属性（property 页）+ 914 商品 SPU（spu 页，issue #177）；
// dir 920 交易订单 + 921 订单管理（order 页，issue #180）；
// 按钮按 菜单ID×10+n：9011/9012 会员新增/修改（member 无删除端点——业务
// 身份禁删，生命周期由停启用覆盖，路由侧无 mall:member:delete 码）、
// 9013/9014 积分配置查询/修改（issue #175：积分是会员域功能，按钮挂 901
// 会员管理下——910 段随 T3 商品域占用；调整接口复用 mall:member:update、
// 流水查询复用 mall:member:query，不设独立按钮）、
// 9021–9023 等级增/改/删、9111–9113/9121–9123/9131–9133 分类/品牌/属性
// 三码、9141–9144 SPU 新增/修改/删除/上下架（issue #177：query 挂页面行；
// 上下架按钮 9144 挂 mall:spu:update——状态位更新随更新码，同会员停启用
// 口径，按钮显式化便于授权审计；开工前 grep seed 去重确认 900–914 与
// 901x/902x/911x/912x/913x/914x 未被占用）。地址随 T1 无独立页面（在会员
// 详情抽屉按会员查看），菜单随 T5 C 端自助落地面时再补。
// 交易订单按钮（issue #180，开工前 grep seed 去重确认 92xx 空闲）：
// 9211 订单取消 / 9212 订单完成（业务状态机动作，独立码——pay approve/close
// 先例）/ 9213 订单备注（管理员备注挂 mall:trade:update——字段更新语义，
// 同会员停启用口径，按钮显式化便于授权审计）；订单查询码挂 921 页面行。
//
// 商品/交易域 910/920 段 URL 挂 /mall 伞下（spec §2「前端路由 /mall：
// member/product/trade 三页组」）：dir 910 用绝对路径 /mall/product、
// dir 920 用绝对路径 /mall/trade，与 900 的 /mall/member、/mall/level 共伞
// 不同段，不构成路由冲突。
//
// 会员/等级/商品是普通租户业务数据：内置普通角色（role id=2）默认不授予
// mall 菜单（角色绑定清单见 seedRoles——mall 与 pay/erp 同款「资金/业务
// 敏感，普通角色零默认授权」口径；本票菜单无资金属性，但会员/商品管理面
// 属租户管理员职责，照 erp 700 段先例不给普通角色）。

// mallMenus 构造会员中心 900 段、商品 910 段与交易 920 段菜单。
func mallMenus() []sysmodel.Menu {
	return []sysmodel.Menu{
		dirRow(900, 0, "会员中心", "/mall", "ShoppingCartFull", 80),
		menuRow(901, 900, "会员管理", "member", "mall/member/index", "mall:member:query", "User", 1),
		btnRow(9011, 901, "会员新增", "mall:member:create", 1),
		btnRow(9012, 901, "会员修改", "mall:member:update", 2),
		btnRow(9013, 901, "积分配置查询", "mall:point:query", 3),
		btnRow(9014, 901, "积分配置修改", "mall:point:update", 4),
		menuRow(902, 900, "会员等级", "level", "mall/level", "mall:level:query", "Medal", 2),
		btnRow(9021, 902, "等级新增", "mall:level:create", 1),
		btnRow(9022, 902, "等级修改", "mall:level:update", 2),
		btnRow(9023, 902, "等级删除", "mall:level:delete", 3),
		dirRow(910, 0, "商品", "/mall/product", "Goods", 81),
		menuRow(911, 910, "商品分类", "category", "mall/product/category", "mall:category:query", "Grid", 1),
		btnRow(9111, 911, "分类新增", "mall:category:create", 1),
		btnRow(9112, 911, "分类修改", "mall:category:update", 2),
		btnRow(9113, 911, "分类删除", "mall:category:delete", 3),
		menuRow(912, 910, "商品品牌", "brand", "mall/product/brand", "mall:brand:query", "PriceTag", 2),
		btnRow(9121, 912, "品牌新增", "mall:brand:create", 1),
		btnRow(9122, 912, "品牌修改", "mall:brand:update", 2),
		btnRow(9123, 912, "品牌删除", "mall:brand:delete", 3),
		menuRow(913, 910, "商品属性", "property", "mall/product/property", "mall:property:query", "SetUp", 3),
		btnRow(9131, 913, "属性新增", "mall:property:create", 1),
		btnRow(9132, 913, "属性修改", "mall:property:update", 2),
		btnRow(9133, 913, "属性删除", "mall:property:delete", 3),
		menuRow(914, 910, "商品 SPU", "spu", "mall/product/spu", "mall:spu:query", "ShoppingBag", 4),
		btnRow(9141, 914, "SPU新增", "mall:spu:create", 1),
		btnRow(9142, 914, "SPU修改", "mall:spu:update", 2),
		btnRow(9143, 914, "SPU删除", "mall:spu:delete", 3),
		btnRow(9144, 914, "SPU上下架", "mall:spu:update", 4),
		dirRow(920, 0, "交易订单", "/mall/trade", "List", 82),
		menuRow(921, 920, "订单管理", "order", "mall/trade/order", "mall:trade:query", "Tickets", 1),
		btnRow(9211, 921, "订单取消", "mall:trade:cancel", 1),
		btnRow(9212, 921, "订单完成", "mall:trade:complete", 2),
		btnRow(9213, 921, "订单备注", "mall:trade:update", 3),
	}
}

// seedMemberLevel 写入默认租户（ID=1）的「普通会员」等级种子（spec §3.1：
// level=1 experience=0）。仅当等级表为空时写入——管理员删除/改名后重复
// 启动不复活，保持幂等（与 seedPoolConfigs 同款守卫）。其余租户不预置：
// C 端注册落 0（未分配），管理员首次维护等级后自行指派。
func seedMemberLevel(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&mallmodel.MemberLevel{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	row := &mallmodel.MemberLevel{
		BaseEntity: mallmodel.BaseEntity{ID: 1},
		TenantID:   1,
		Name:       "普通会员",
		Level:      1,
		Experience: 0,
		Status:     mallmodel.StatusEnable,
		Remark:     "默认等级：注册会员初始等级（level=1，experience=0）",
	}
	if err := tenant.Skip(db).Create(row).Error; err != nil {
		return fmt.Errorf("seed member level: %w", err)
	}
	return nil
}
