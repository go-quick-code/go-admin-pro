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

// payMenus 构造支付中心 800 段菜单（issue #131/#132/#133/#134/#135/#136，
// spec §7）：dir 800 + 810 支付应用 + 811 支付订单 + 812 退款单 + 814 回调通知
// + 820 渠道配置 + 821 钱包管理 + 822 充值套餐 + 823 我的充值 + 840 收银台；
// 按钮 = 菜单ID×10+n（8101–8103、8201、8221–8223，开工前 grep seed 去重确认
// 未占用）。813/830/831 留给后续票（转账/示例；并行工单下 sort 文本冲突在
// 合并时保持两侧菜单并重排 sort）。
//
// 支付订单（811）/退款单（812）/回调通知（814）无按钮行：交易单据页只读
// （append-only，spec §4.10——无新增/编辑/删除），导出按钮（pay:order:export /
// pay:refund:export）随 #138 的 export-excel 端点按「按页面实际授予」纪律
// 一并补种；钱包管理（821）同理（余额调整无管理端入口，spec §4.3 差异清单）；
// 充值单（#136）走 823 我的充值自助页（无管理端写入入口）。
//
// 充值套餐（822，issue #136，spec §6/§7）：管理端 CRUD 页，按钮
// 8221 新增 / 8222 修改 / 8223 删除（pay:wallet-recharge-package:create|update|
// delete，query 为菜单权限码）。
//
// 我的充值（823，issue #136，spec §4.11 自助端点）：自助页无权限码（登录即可，
// 服务层强制本人钱包归属，同 840 收银台形态）。菜单 ID 为本票补充分配——
// spec §7 的菜单清单未给自助「我的充值」留槽位（822 为充值套餐），取 822
// 相邻的 823；#139 前端整装按页面清单（§6 的 11 页 + 自助页）复核。
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
		// 退款单（812，issue #135，spec §6）：只读列表 + 详情 drawer。
		menuRow(812, 800, "退款单", "refund", "pay/refund/index", "pay:refund:query", "RefreshLeft", 3),
		// 回调通知（814，issue #134，spec §6）：任务列表 + 日志子表，只读。
		menuRow(814, 800, "回调通知", "notify-task", "pay/notify-task/index", "pay:notify-task:query", "Bell", 4),
		menuRow(820, 800, "渠道配置", "channel", "pay/channel/index", "pay:channel:query", "Connection", 5),
		btnRow(8201, 820, "渠道修改", "pay:channel:update", 1),
		menuRow(821, 800, "钱包管理", "wallet", "pay/wallet/index", "pay:wallet:query", "Wallet", 6),
		// 充值套餐（822，issue #136，spec §6）：管理端 CRUD + 引用禁删。
		menuRow(822, 800, "充值套餐", "recharge-package", "pay/recharge-package/index", "pay:wallet-recharge-package:query", "Present", 7),
		btnRow(8221, 822, "套餐新增", "pay:wallet-recharge-package:create", 1),
		btnRow(8222, 822, "套餐修改", "pay:wallet-recharge-package:update", 2),
		btnRow(8223, 822, "套餐删除", "pay:wallet-recharge-package:delete", 3),
		// 我的充值（823，issue #136，spec §4.11）：自助页（发起充值 + 我的充值单
		// 列表），无权限码（归属校验在 service 层强制）；发起充值后的支付走 840
		// 收银台（套餐选择经 GET /pay/wallet-recharge-package/list）。
		menuRow(823, 800, "我的充值", "my-recharge", "pay/my-recharge/index", "", "Coin", 8),
		menuRow(840, 800, "收银台", "cashier", "pay/cashier/index", "", "ShoppingCart", 9),
	}
}
