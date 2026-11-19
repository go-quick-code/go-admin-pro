package seed

import (
	"fmt"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// mall.go 是 mall 会员域与商品域、交易域种子（issue #174/#175/#176/#177/#180，
// docs/mall-mvp-spec.md §3.1/§3.2/§3.3/§5/§7）：
// 会员中心 900 段菜单 + 「普通会员」等级种子一行 + 商品 910 段菜单 +
// 交易 920 段菜单（issue #180，T7）；
// 支付联动种子（issue #181，T8）：mall 挂单支付应用（app_key=mall，
// 5011）+ mock/wallet 渠道绑定（5012/5013），基础种子常驻（文件尾）。
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
// 导出按钮（spec §1 第 8 步 Excel 导出，PR #184 审查补票，开工前 grep
// seed 去重确认 9015/9145/9214 空闲）：9015 会员导出 / 9145 SPU 导出 /
// 9214 订单导出，权限码 mall:<entity>:export 与路由守卫一一对应（路由侧
// 双向收敛由 TestPermSetsConverge 自动覆盖）。
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
		btnRow(9015, 901, "会员导出", "mall:member:export", 5),
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
		btnRow(9145, 914, "SPU导出", "mall:spu:export", 5),
		dirRow(920, 0, "交易订单", "/mall/trade", "List", 82),
		menuRow(921, 920, "订单管理", "order", "mall/trade/order", "mall:trade:query", "Tickets", 1),
		btnRow(9211, 921, "订单取消", "mall:trade:cancel", 1),
		btnRow(9212, 921, "订单完成", "mall:trade:complete", 2),
		btnRow(9213, 921, "订单备注", "mall:trade:update", 3),
		btnRow(9214, 921, "订单导出", "mall:trade:export", 4),
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

// mall 支付联动种子固定 ID（issue #181，T8）：5011 段位模式照 pay_demo.go
// 的 payDemoAppID 5001 段（应用 5001/绑定 5001–5002），mall 取相邻 5011 段
// （应用 5011 + 绑定 5012/5013）——开工前已 grep seed 确认 501x 未被占用。
// 与 CRM 1001/OA 2001/ERP 3001+4001/pay 5001 段错开。
const (
	// mallPayAppID 是 mall 挂单支付应用（app_key=mall，service.MallPayAppKey
	// 的建单定位键）。
	mallPayAppID int64 = 5011
	// mallPayAppChannelMockID/mallPayAppChannelWalletID 是 mall 应用的
	// mock/wallet 渠道绑定（C 端发起支付的渠道域）。
	mallPayAppChannelMockID   int64 = 5012
	mallPayAppChannelWalletID int64 = 5013
)

// seedMallPayApp 写入 mall 挂单支付应用与渠道绑定（issue #181，T8；默认
// 租户 ID=1，mock/wallet 双绑定）：支付联动是 mall 的常驻能力（非演示数据），
// 基础种子写入（seed.Run，GAP_SEED_DEMO 之外）。
//
// 幂等守卫是行级哨兵（pay_app 存在 app_key=mall 行即跳过），不是「表空才
// 插」：pay_app 同时承载 pay 演示应用（pay_demo.go，5001），基础种子先行
// 落行后「表空」永假，行级哨兵才能既保证 mall 应用常驻、又不动 pay 演示
// 哨兵的判定域（pay_demo.go payDemoSkipReason 对 mall 固定 ID 行显式排除，
// 两处配套）。回调地址留空（OrderNotifyURL=""）——mall 无外部回调端点，
// 支付成功走进程内 OrderSuccessHandler 扩展点，商户回调任务投递无消费方
// （照 demo 应用必填回调地址的差异：demo 联动依赖 HTTP 回调，mall 联动
// 不依赖）。其余租户不预置：app_key=mall 是租户内唯一键，非默认租户的
// 管理员经「支付应用」页自行创建（与会员等级种子「其余租户不预置」同口径）。
func seedMallPayApp(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&paymodel.App{}).
		Where("app_key = ?", mallservice.MallPayAppKey).Count(&count).Error; err != nil {
		return fmt.Errorf("seed mall pay app: %w", err)
	}
	if count > 0 {
		return nil
	}
	app := paymodel.App{
		BaseEntity: paymodel.BaseEntity{ID: mallPayAppID},
		TenantID:   1,
		AppKey:     mallservice.MallPayAppKey,
		Name:       "商城应用",
		Status:     paymodel.StatusEnable,
		Remark:     "mall 交易订单挂单应用（issue #181 种子）：C 端发起支付的建单定位键，绑定 mock/wallet",
	}
	if err := tenant.Skip(db).Create(&app).Error; err != nil {
		return fmt.Errorf("seed mall pay app: %w", err)
	}
	bindings := []paymodel.AppChannel{
		{
			BaseEntity:  paymodel.BaseEntity{ID: mallPayAppChannelMockID},
			TenantID:    1,
			AppID:       mallPayAppID,
			ChannelCode: paychannel.CodeMock,
		},
		{
			BaseEntity:  paymodel.BaseEntity{ID: mallPayAppChannelWalletID},
			TenantID:    1,
			AppID:       mallPayAppID,
			ChannelCode: paychannel.CodeWallet,
		},
	}
	if err := tenant.Skip(db).Create(&bindings).Error; err != nil {
		return fmt.Errorf("seed mall pay app channels: %w", err)
	}
	return nil
}
