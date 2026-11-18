package seed

import (
	"fmt"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// mall.go 是 mall 会员域种子（issue #174/#175，docs/mall-mvp-spec.md §3.1/§5/§7）：
// 会员中心 900 段菜单 + 「普通会员」等级种子一行。
//
// 菜单（spec §5「9xx 段——900 会员中心（member/level/address 页）」）：
// dir 900 会员中心 + 901 会员管理（member 页）+ 902 会员等级（level 页）；
// 按钮按 菜单ID×10+n：9011/9012 会员新增/修改（member 无删除端点——业务
// 身份禁删，生命周期由停启用覆盖，路由侧无 mall:member:delete 码）、
// 9013/9014 积分配置查询/修改（issue #175：积分是会员域功能，按钮挂 901
// 会员管理下——910 段随 T3 商品域占用；调整接口复用 mall:member:update、
// 流水查询复用 mall:member:query，不设独立按钮）、
// 9021/9022/9023 等级新增/修改/删除（开工前 grep seed 去重确认 900/901/902
// 与 9011–9019/9021–9029 未被占用）。地址随 T1 无独立页面（在会员详情抽屉
// 按会员查看），菜单随 T5 C 端自助落地面时再补。
//
// 会员/等级是普通租户业务数据：内置普通角色（role id=2）默认不授予 mall
// 菜单（角色绑定清单见 seedRoles——mall 与 pay/erp 同款「资金/业务敏感，
// 普通角色零默认授权」口径；本票菜单无资金属性，但会员/等级管理面属租户
// 管理员职责，照 erp 700 段先例不给普通角色）。

// mallMenus 构造会员中心 900 段菜单。
func mallMenus() []sysmodel.Menu {
	return []sysmodel.Menu{
		dirRow(900, 0, "会员中心", "/mall", "ShoppingCartFull", 80),
		menuRow(901, 900, "会员管理", "member", "mall/member/index", "mall:member:query", "User", 1),
		btnRow(9011, 901, "会员新增", "mall:member:create", 1),
		btnRow(9012, 901, "会员修改", "mall:member:update", 2),
		btnRow(9013, 901, "积分配置查询", "mall:point:query", 3),
		btnRow(9014, 901, "积分配置修改", "mall:point:update", 4),
		menuRow(902, 900, "会员等级", "level", "mall/level/index", "mall:level:query", "Medal", 2),
		btnRow(9021, 902, "等级新增", "mall:level:create", 1),
		btnRow(9022, 902, "等级修改", "mall:level:update", 2),
		btnRow(9023, 902, "等级删除", "mall:level:delete", 3),
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
