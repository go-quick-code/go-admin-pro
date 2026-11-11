package seed

import (
	"fmt"

	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// pay.go 是支付域种子（issue #131，docs/pay-mvp-spec.md §3.1/§7）。
// pay_channel 是平台级表：14 个渠道码常量逐一预插（mock/wallet 启用、其余
// 停用且 config={}），种子常驻、渠道禁删只启停（service 层不提供删除端点）。
// 表为空时幂等写入，与 seedMessages 同款守卫。

// seedPayChannels 预插 14 行支付渠道（ID 按渠道码常量顺序 1..14）。
func seedPayChannels(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&paymodel.Channel{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	enabled := map[string]bool{
		paychannel.CodeMock:   true,
		paychannel.CodeWallet: true,
	}
	codes := paychannel.AllCodes()
	channels := make([]paymodel.Channel, 0, len(codes))
	for i, code := range codes {
		status := int8(paymodel.StatusDisable)
		if enabled[code] {
			status = paymodel.StatusEnable
		}
		channels = append(channels, paymodel.Channel{
			BaseEntity: paymodel.BaseEntity{ID: int64(i + 1)},
			Code:       code,
			Name:       paychannel.Name(code),
			Status:     status,
			FeeRate:    "0.00",
			Config:     "{}",
		})
	}
	if err := tenant.Skip(db).Create(&channels).Error; err != nil {
		return fmt.Errorf("seed pay channels: %w", err)
	}
	return nil
}

// payMenus 构造支付中心 800 段菜单（issue #131/#132/#133，spec §7）：dir 800 +
// 810 支付应用 + 811 支付订单 + 820 渠道配置 + 821 钱包管理 + 840 收银台；
// 按钮 = 菜单ID×10+n（8101–8103、8201，开工前 grep seed 去重确认未占用）。
// 812–814/822/830/831 留给后续票（退款/转账/回调/套餐/示例）。
//
// 支付订单（811）无按钮行：交易单据页只读（append-only，spec §4.10——无
// 新增/编辑/删除），导出按钮（pay:order:export）随 #138 的 export-excel
// 端点按「按页面实际授予」纪律一并补种；钱包管理（821）同理（余额调整
// 无管理端入口，spec §4.3 差异清单）。
//
// 收银台（840）是布局内独立操作页（照 OA 我的考勤 531 形态）：自助页无
// 权限码（登录即可，归属校验在 service 层强制，spec §4.11）。
//
// 渠道配置是平台级页面：「仅平台租户」由 service 层 requirePlatform 收口，
// 非平台租户即使持 pay:channel:* 也 403（照 message 渠道先例）。
// 内置普通角色（role id=2）默认不授予任何 pay 菜单（资金敏感，spec §7，
// 见 seedRoles 的 roleMenus——刻意无 800 段条目）。
func payMenus() []sysmodel.Menu {
	return []sysmodel.Menu{
		dirRow(800, 0, "支付中心", "/pay", "CreditCard", 75),
		menuRow(810, 800, "支付应用", "app", "pay/app/index", "pay:app:query", "Shop", 1),
		btnRow(8101, 810, "应用新增", "pay:app:create", 1),
		btnRow(8102, 810, "应用修改", "pay:app:update", 2),
		btnRow(8103, 810, "应用删除", "pay:app:delete", 3),
		menuRow(811, 800, "支付订单", "order", "pay/order/index", "pay:order:query", "List", 2),
		menuRow(820, 800, "渠道配置", "channel", "pay/channel/index", "pay:channel:query", "Connection", 3),
		btnRow(8201, 820, "渠道修改", "pay:channel:update", 1),
		menuRow(821, 800, "钱包管理", "wallet", "pay/wallet/index", "pay:wallet:query", "Wallet", 4),
		menuRow(840, 800, "收银台", "cashier", "pay/cashier/index", "", "ShoppingCart", 5),
	}
}
