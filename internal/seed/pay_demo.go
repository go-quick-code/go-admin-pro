// pay_demo.go 写入 pay 演示数据（issue #139，docs/pay-mvp-spec.md §7 演示数据/
// §9 验收 3）：
//
//	演示应用（app_key=demo，绑定 mock/wallet，回调地址指向本地 demo 回调端点）
//	→ 充值套餐 2 档（¥50+赠5 / ¥100+赠15）
//	→ 套餐充值 1（mock 支付 → 赠送入账 +55）→ 充值退款 1（全额扣回 -55）
//	→ 套餐充值 2（mock 支付 → 赠送入账 +115 持久留存）
//	→ 示例订单 1（wallet 支付 -19.90 → 部分退款 5 元回加 +5.00）
//	→ 示例订单 2（mock 支付 → 全额退款，订单置 20）
//	→ 示例订单 3（待支付，供收银台/模拟回调交互演示）
//	→ 示例提现 1（审批 → wallet 转账 → 联动成功 +30.00）
//	→ 示例提现 2/3（等待审批，供管理端审批演示）
//
// 目标：`make run-demo` / GAP_SEED_DEMO=true 后，pay 各页面的每条链路都有真实
// 数据可看；待支付示例订单与等待提现保留交互入口，管理员可亲手再走一遍
// 「示例下单 → 收银台选渠道 → mock 模拟回调 → 商户回调重试 → demo 状态联动
// → 审批转账」完整链路（spec §9 验收 3）。
//
// 落库口径（与 #131–#138 的 service 规则完全一致，均经真实入口）：
//   - 除演示应用/渠道绑定/套餐三组配置行外，**全部业务行经 pay service 真实
//     入口落库**（CreateRecharge/SubmitOrder/MockNotifyOrder/CreateRefund/
//     CreateDemoOrder/RefundDemoOrder/CreateDemoWithdraw/ApproveDemoWithdraw，
//     挂单定位经 GetCashierOrder 收银台入口）——钱包余额与流水只经唯一入账
//     入口（ApplyWalletChange，spec §4.3），快照 balance 与流水代数和天然一致
//     可重放；订单/退款/转账状态、extension、回调任务、充值联动（赠送入账/
//     扣回）全部走与 HTTP 端点相同的代码路径。种子没有登录会话，显式注入
//     演示租户 + admin 主体（同 erp_demo.go；审计列由 AuditPlugin 回填 admin ID）。
//   - demo 联动列（示例订单支付/退款回执、提现转账回执）经 HandleDemoNotify
//     服务入口回写——与 demo 回调 HTTP 端点（#134）完全同一路径，不是直写
//     （联动列的「唯一权威链路」决策照旧，种子只是进程内直调服务入口）。
//     回调任务保持待投递：make run-demo 启动后 payNotifyRetry job 逐条 POST
//     到演示应用的回调地址（指向本地 demo 回调端点，HMAC 验签通过即业务码 0），
//     任务转 10 成功、联动幂等跳过——商户回调重试链路在演示环境真实走通。
//   - 演示应用回调地址 = 本机 demo 回调端点。基座经 SetDemoPayNotifyBase 注入
//     （configs app.port + app.api-prefix，同 SetDemoLocation 先例——seed 包
//     不读配置）；未注入时回退 configs/config.yaml 的默认端口与前缀（8080/
//     /admin-api），改动部署端口需在「支付应用」页同步修改回调地址。
//
// ID 纪律：直接落库的配置行用固定 ID 段（应用/绑定/套餐 5001 起，与 CRM 1001/
// OA 2001/ERP 3001+4001 段错开）；经 service 入口落库的业务行 ID 走数据库自增
// （唯一入账入口/单据落库不收 ID——同 erp_demo.go 库存流水的决策，快照一致性
// 由入口保证，与固定 ID 段不冲突）。
//
// 幂等：表哨兵——任一 pay 业务/交易表已有行（含软删）即整段跳过，重复执行
// 不重复插入，也不与已有租户的 pay 数据混写（非演示租户不受影响）。哨兵不含
// pay_channel（平台级 14 行由基础种子常驻，非 pay 演示数据的信号）。
//
// 注意：本文件依赖 pay model/service/repository，代码生成器的 fresh-module
// 编译校验会连同真实 pay 模块一起剪除本文件（同 erp_demo.go 的处理）。
package seed

import (
	"context"
	"fmt"
	"log/slog"

	paydto "github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// pay 演示数据使用的固定 ID（5001 段；与 CRM/OA/ERP 演示段错开）。
const (
	payDemoTenantID int64 = 1

	payDemoAppID              int64 = 5001
	payDemoAppChannelMockID   int64 = 5001
	payDemoAppChannelWalletID int64 = 5002
	payDemoPackageSmallID     int64 = 5001
	payDemoPackageLargeID     int64 = 5002
)

// payDemoNotifyBaseFallback 是回调地址基座的回退值：configs/config.yaml 的
// 默认端口与 API 前缀（config.Load 缺省值；SetDemoPayNotifyBase 未注入时使用，
// 与 demo_location.go 回退 DefaultTimezone 同款思路）。
const payDemoNotifyBaseFallback = "http://127.0.0.1:8080/admin-api"

// demoPayNotifyBase 是演示应用回调地址基座（http://127.0.0.1:<port><api-prefix>）；
// 空串 = 未注入，回退 payDemoNotifyBaseFallback。
var demoPayNotifyBase string

// SetDemoPayNotifyBase 注入演示应用回调地址基座（app 装配期：
// http://127.0.0.1:port + api-prefix，与 SetDemoLocation 同款注入方式）；空串忽略。
func SetDemoPayNotifyBase(base string) {
	if base != "" {
		demoPayNotifyBase = base
	}
}

// payDemoNotifyURL 返回演示应用某类回调地址（指向本地 demo 回调端点）。
func payDemoNotifyURL(kind string) string {
	base := demoPayNotifyBase
	if base == "" {
		base = payDemoNotifyBaseFallback
	}
	return base + "/pay/demo/notify/" + kind
}

// init 把 pay 演示数据入口注册给 seed.Run（同 seedCRMDemoFunc 的函数变量模式）。
func init() { seedPayDemoFunc = seedPayDemo }

// PayDemo 写入 pay 演示数据（应用/套餐/交易链路/钱包流水），幂等可重复执行。
// seed.Run 在 GAP_SEED_DEMO=true 时经 withDemo 分支调用；只想补写 pay 演示
// 数据（不带 CRM/OA/ERP 演示数据）时也可直接调用。
func PayDemo(db *gorm.DB) error { return seedPayDemo(db) }

// seedPayDemo 写入 pay 演示数据：表哨兵非空时整段跳过，否则在同一事务里按
// 配置行 → 充值链路 → 示例订单链路 → 示例提现链路 → demo 联动 的顺序落库。
func seedPayDemo(db *gorm.DB) error {
	reason, err := payDemoSkipReason(db)
	if err != nil {
		return err
	}
	if reason != "" {
		slog.Info("seed pay demo skipped", "reason", reason)
		return nil
	}

	err = tenant.Skip(db).Transaction(func(tx *gorm.DB) error {
		// 演示主体：内置管理员（基础种子保证存在，缺失报错不补建——同 erp_demo）。
		var admin sysmodel.User
		if err := tenant.Skip(tx).
			Where("tenant_id = ? AND username = ?", payDemoTenantID, "admin").
			First(&admin).Error; err != nil {
			return fmt.Errorf("pay 演示数据缺少管理员账号 admin（请确认 seedUsers 已执行）: %w", err)
		}

		// 种子没有登录会话，显式注入演示租户与 admin 主体：service 入口从 ctx
		// 取租户（payTenantID）与归属用户（钱包/订单 user_id），审计列由
		// AuditPlugin 回填 admin ID（同 erp_demo.go 口径）。
		ctx := tenant.WithTenantID(context.Background(), payDemoTenantID)
		ctx = authn.With(ctx, authn.Principal{
			UserID: admin.ID, TenantID: payDemoTenantID, Username: admin.Username,
		})

		// pay service 实例：注册表与 app.go 装配同构（渠道配置取平台种子行，
		// wallet 渠道后端是 service 自身），业务时区注入演示时区。rechargeAppKey
		// 不注入（缺省 demo，与 configs pay.recharge-app-key 缺省一致）。
		repo := payrepo.New(tx)
		svc := payservice.New(repo)
		svc.SetLocation(demoLocation)
		registry, err := payDemoChannelRegistry(ctx, repo, svc)
		if err != nil {
			return err
		}
		svc.SetRegistry(registry)

		if err := payDemoSeedConfig(tx); err != nil {
			return err
		}
		if err := payDemoSeedRechargeChain(ctx, svc); err != nil {
			return err
		}
		if err := payDemoSeedDemoOrderChain(ctx, svc); err != nil {
			return err
		}
		if err := payDemoSeedWithdrawChain(ctx, svc); err != nil {
			return err
		}
		return payDemoSeedLinkage(ctx, svc)
	})
	if err != nil {
		return err
	}
	slog.Info("seed pay demo created",
		"apps", 1, "packages", 2, "recharges", 2, "refunds", 3,
		"demoOrders", 3, "demoWithdraws", 3, "transfers", 1, "walletFlows", 6)
	return nil
}

// payDemoChannelRegistry 装配渠道 SPI 注册表（同 app.go payChannelRegistry）：
// mock/wallet 渠道实现从平台种子行的 config 构建，wallet 渠道的扣减/回加/入账
// 后端是 pay service 自身（依赖倒置同装配处）。
func payDemoChannelRegistry(ctx context.Context, repo *payrepo.Repository, svc *payservice.Service) (*paychannel.Registry, error) {
	rows, err := repo.ListChannelsByCodes(ctx, []string{paychannel.CodeMock, paychannel.CodeWallet})
	if err != nil {
		return nil, fmt.Errorf("load pay channel configs: %w", err)
	}
	configByCode := make(map[string]string, len(rows))
	for _, row := range rows {
		configByCode[row.Code] = row.Config
	}
	mockClient, err := paychannel.NewMockClient(configByCode[paychannel.CodeMock])
	if err != nil {
		return nil, fmt.Errorf("init mock pay client: %w", err)
	}
	walletClient, err := paychannel.NewWalletClient(configByCode[paychannel.CodeWallet], svc, svc, svc)
	if err != nil {
		return nil, fmt.Errorf("init wallet pay client: %w", err)
	}
	registry := paychannel.NewRegistry()
	registry.Register(mockClient)
	registry.Register(walletClient)
	return registry, nil
}

// payDemoSkipReason 返回跳过原因（空串表示 pay 全空、可以写入）。任一 pay
// 业务/交易表已有行（含软删）即视为库里已有 pay 数据，整段跳过：避免固定 ID
// 段与人工数据相撞、演示链路与已有租户数据混写。pay_channel（平台级 14 行
// 基础种子常驻）刻意不在哨兵内。
func payDemoSkipReason(db *gorm.DB) (string, error) {
	checks := []struct {
		label string
		model any
	}{
		{"pay_app", &paymodel.App{}},
		{"pay_app_channel", &paymodel.AppChannel{}},
		{"pay_wallet_recharge_package", &paymodel.WalletRechargePackage{}},
		{"pay_wallet_recharge", &paymodel.WalletRecharge{}},
		{"pay_wallet", &paymodel.Wallet{}},
		{"pay_wallet_transaction", &paymodel.WalletTransaction{}},
		{"pay_order", &paymodel.Order{}},
		{"pay_order_extension", &paymodel.OrderExtension{}},
		{"pay_refund", &paymodel.Refund{}},
		{"pay_transfer", &paymodel.Transfer{}},
		{"pay_notify_task", &paymodel.NotifyTask{}},
		{"pay_demo_order", &paymodel.DemoOrder{}},
		{"pay_demo_withdraw", &paymodel.DemoWithdraw{}},
	}
	for _, check := range checks {
		var count int64
		if err := tenant.Skip(db).Unscoped().Model(check.model).Count(&count).Error; err != nil {
			return "", fmt.Errorf("统计 %s: %w", check.label, err)
		}
		if count > 0 {
			return fmt.Sprintf("%s 已有 %d 行", check.label, count), nil
		}
	}
	return "", nil
}

// payDemoSeedConfig 写入配置行（固定 ID 5001 段）：演示应用（app_key=demo，
// 绑定 mock/wallet，回调地址指向本地 demo 回调端点——充值/示例挂单与
// pay.recharge-app-key 缺省解析都依赖它）与两档充值套餐（快照档位，spec §7）。
func payDemoSeedConfig(tx *gorm.DB) error {
	app := paymodel.App{
		BaseEntity:        paymodel.BaseEntity{ID: payDemoAppID},
		TenantID:          payDemoTenantID,
		AppKey:            payservice.DefaultRechargeAppKey,
		Name:              "演示应用",
		Status:            paymodel.StatusEnable,
		Remark:            "run-demo 演示应用：充值单/示例订单/提现转账的挂单应用（pay.recharge-app-key 缺省指向）",
		OrderNotifyURL:    payDemoNotifyURL(payservice.DemoNotifyKindOrder),
		RefundNotifyURL:   payDemoNotifyURL(payservice.DemoNotifyKindRefund),
		TransferNotifyURL: payDemoNotifyURL(payservice.DemoNotifyKindTransfer),
	}
	if err := tenant.Skip(tx).Create(&app).Error; err != nil {
		return fmt.Errorf("写入 pay 演示应用: %w", err)
	}
	bindings := []paymodel.AppChannel{
		{
			BaseEntity:  paymodel.BaseEntity{ID: payDemoAppChannelMockID},
			TenantID:    payDemoTenantID,
			AppID:       payDemoAppID,
			ChannelCode: paychannel.CodeMock,
		},
		{
			BaseEntity:  paymodel.BaseEntity{ID: payDemoAppChannelWalletID},
			TenantID:    payDemoTenantID,
			AppID:       payDemoAppID,
			ChannelCode: paychannel.CodeWallet,
		},
	}
	if err := tenant.Skip(tx).Create(&bindings).Error; err != nil {
		return fmt.Errorf("写入 pay 演示应用渠道绑定: %w", err)
	}
	packages := []paymodel.WalletRechargePackage{
		{
			BaseEntity: paymodel.BaseEntity{ID: payDemoPackageSmallID},
			TenantID:   payDemoTenantID,
			Name:       "演示充值 50 元（赠 5 元）",
			PayPrice:   money.FromCents(5000),
			BonusPrice: money.FromCents(500),
			Status:     paymodel.StatusEnable,
		},
		{
			BaseEntity: paymodel.BaseEntity{ID: payDemoPackageLargeID},
			TenantID:   payDemoTenantID,
			Name:       "演示充值 100 元（赠 15 元）",
			PayPrice:   money.FromCents(10000),
			BonusPrice: money.FromCents(1500),
			Status:     paymodel.StatusEnable,
		},
	}
	if err := tenant.Skip(tx).Create(&packages).Error; err != nil {
		return fmt.Errorf("写入 pay 演示充值套餐: %w", err)
	}
	return nil
}

// payDemoSeedRechargeChain 写入充值链路（spec §4.7 全路径，经真实入口）：
// 套餐 1 充值 → mock 支付（赠送入账 +55）→ 充值退款（全额扣回 -55，先校验后
// 退款）→ 套餐 2 充值 → mock 支付（赠送入账 +115，持久留存）。充值订单禁用
// 钱包渠道（submit 反查守卫），演示充值一律 mock 渠道。钱包在此首次懒创建。
func payDemoSeedRechargeChain(ctx context.Context, svc *payservice.Service) error {
	// 套餐 1（50+5）：发起充值（套餐快照进单）→ mock 支付 → 模拟回调成功。
	first, err := svc.CreateRecharge(ctx, &paydto.RechargeCreateReq{PackageID: payDemoPackageSmallID})
	if err != nil {
		return fmt.Errorf("演示充值 1 创建: %w", err)
	}
	if err := payDemoSubmitMockPay(ctx, svc, first.OrderID); err != nil {
		return fmt.Errorf("演示充值 1 支付: %w", err)
	}
	// 充值退款（spec §4.7：仅全额=实付档位 50，余额充足才可发起）——余额 55
	// 扣回 -55、total_recharge 调减，演示「充值→退款」闭环与三列回写。
	if _, err := svc.CreateRefund(ctx, &paydto.RefundCreateReq{
		AppKey:           payservice.DefaultRechargeAppKey,
		MerchantOrderID:  first.MerchantOrderID,
		MerchantRefundID: "RFR-" + first.MerchantOrderID + "-seed",
		RefundPrice:      money.FromCents(5000),
		Reason:           "演示充值退款",
	}, ""); err != nil {
		return fmt.Errorf("演示充值 1 退款: %w", err)
	}
	// 套餐 2（100+15）：赠送入账持久（total_recharge=115，供钱包页展示）。
	second, err := svc.CreateRecharge(ctx, &paydto.RechargeCreateReq{PackageID: payDemoPackageLargeID})
	if err != nil {
		return fmt.Errorf("演示充值 2 创建: %w", err)
	}
	if err := payDemoSubmitMockPay(ctx, svc, second.OrderID); err != nil {
		return fmt.Errorf("演示充值 2 支付: %w", err)
	}
	return nil
}

// payDemoSubmitMockPay 经收银台 submit（mock 渠道）+ 模拟回调成功（与真实渠道
// 回调同一解析 → 入账 → 建任务链路，spec §4.2）完成一笔 mock 支付。
func payDemoSubmitMockPay(ctx context.Context, svc *payservice.Service, orderID int64) error {
	if _, err := svc.SubmitOrder(ctx, &paydto.OrderSubmitReq{OrderID: orderID, ChannelCode: paychannel.CodeMock}, ""); err != nil {
		return err
	}
	if _, err := svc.MockNotifyOrder(ctx, &paydto.MockNotifyReq{OrderID: orderID, Result: "success"}); err != nil {
		return err
	}
	return nil
}

// payDemoSeedDemoOrderChain 写入示例订单链路（spec §4.8/§4.11 全路径）：
// 订单 1 wallet 支付（-19.90）→ 部分退款 5 元（wallet 原路回加 +5.00，累计
// ≤ 实付、订单保持 10）；订单 2 mock 支付 → 全额退款（RefundDemoOrder 服务端
// 定额=未退余额，订单置 20）；订单 3 只下单（待支付，收银台/模拟回调交互演示）。
// 支付成功后先经 HandleDemoNotify 回写示例订单支付回执再退款——与真实交互
// 时序一致（回调联动后商户才发起退款；RefundDemoOrder 校验示例行已支付）。
func payDemoSeedDemoOrderChain(ctx context.Context, svc *payservice.Service) error {
	// 订单 1（无线蓝牙耳机 ¥19.90）：wallet 渠道支付——余额扣减走唯一入账入口
	//（biz=3），随后部分退款 wallet 原路回加（biz=4）。
	first, err := svc.CreateDemoOrder(ctx, &paydto.DemoOrderCreateReq{SpuID: 1})
	if err != nil {
		return fmt.Errorf("演示示例订单 1 创建: %w", err)
	}
	firstOrderID, err := payDemoSubmitCashierPay(ctx, svc, first.AppKey, first.MerchantOrderID, paychannel.CodeWallet)
	if err != nil {
		return fmt.Errorf("演示示例订单 1 支付: %w", err)
	}
	if err := payDemoLink(ctx, svc, payservice.DemoNotifyKindOrder,
		paymodel.NotifyTypePayment, firstOrderID); err != nil {
		return fmt.Errorf("演示示例订单 1 支付联动: %w", err)
	}
	// 部分退款 5 元（消费方服务端退款入口，spec §4.4 多次部分退款累计）：
	// merchant_refund_id 取确定性派生号（哨兵幂等下单次执行，无并发双击）。
	if _, err := svc.CreateRefund(ctx, &paydto.RefundCreateReq{
		AppKey:           payservice.DefaultRechargeAppKey,
		MerchantOrderID:  first.MerchantOrderID,
		MerchantRefundID: fmt.Sprintf("DEMO-RFD-%d-seed-partial", first.ID),
		RefundPrice:      money.FromCents(500),
		Reason:           "演示部分退款",
	}, ""); err != nil {
		return fmt.Errorf("演示示例订单 1 部分退款: %w", err)
	}
	// 订单 2（机械键盘 ¥99.00）：mock 渠道支付 + 全额退款（示例退款端点，
	// 金额服务端定=未退余额；退款单 mock 渠道模拟成功分支）。
	second, err := svc.CreateDemoOrder(ctx, &paydto.DemoOrderCreateReq{SpuID: 2})
	if err != nil {
		return fmt.Errorf("演示示例订单 2 创建: %w", err)
	}
	secondOrderID, err := payDemoSubmitCashierPay(ctx, svc, second.AppKey, second.MerchantOrderID, paychannel.CodeMock)
	if err != nil {
		return fmt.Errorf("演示示例订单 2 支付: %w", err)
	}
	if err := payDemoLink(ctx, svc, payservice.DemoNotifyKindOrder,
		paymodel.NotifyTypePayment, secondOrderID); err != nil {
		return fmt.Errorf("演示示例订单 2 支付联动: %w", err)
	}
	if _, err := svc.RefundDemoOrder(ctx, second.ID, &paydto.DemoOrderRefundReq{Reason: "演示全额退款"}, ""); err != nil {
		return fmt.Errorf("演示示例订单 2 全额退款: %w", err)
	}
	// 订单 3（便携咖啡杯 ¥29.90）：只下单不支付——保留「收银台选渠道 → 发起
	// 支付 → mock 模拟回调 → 商户回调联动」的交互演示入口。
	if _, err := svc.CreateDemoOrder(ctx, &paydto.DemoOrderCreateReq{SpuID: 3}); err != nil {
		return fmt.Errorf("演示示例订单 3 创建: %w", err)
	}
	return nil
}

// payDemoSubmitCashierPay 经收银台入口（GetCashierOrder：appKey+merchantOrderId
// 定位 + 本人订单校验）发起支付：wallet 渠道同步成功（扣减入账），mock 渠道
// 停待支付后触发模拟回调（与真实渠道回调同一解析 → 入账 → 建任务链路）。
// 照前端收银台交互形态（cashier 定位订单 → submit），返回支付订单 ID（demo
// 联动定位键，与 #134 报文 DataID 同源）。
func payDemoSubmitCashierPay(ctx context.Context, svc *payservice.Service, appKey, merchantOrderID, channelCode string) (int64, error) {
	cashier, err := svc.GetCashierOrder(ctx, appKey, merchantOrderID)
	if err != nil {
		return 0, err
	}
	if _, err := svc.SubmitOrder(ctx, &paydto.OrderSubmitReq{OrderID: cashier.Order.ID, ChannelCode: channelCode}, ""); err != nil {
		return 0, err
	}
	if channelCode == paychannel.CodeMock {
		if _, err := svc.MockNotifyOrder(ctx, &paydto.MockNotifyReq{OrderID: cashier.Order.ID, Result: "success"}); err != nil {
			return 0, err
		}
	}
	return cashier.Order.ID, nil
}

// payDemoSeedWithdrawChain 写入示例提现链路（spec §4.8 全路径）：提现 1 审批
// → wallet 渠道转账（入账申请人钱包 biz=6 +30.00，merchant_transfer_id =
// WD-<id> 派生号）→ 回调联动成功；提现 2/3 保持等待（管理端审批/关闭演示入口）。
func payDemoSeedWithdrawChain(ctx context.Context, svc *payservice.Service) error {
	approved, err := svc.CreateDemoWithdraw(ctx, &paydto.DemoWithdrawCreateReq{
		Subject: "演示提现（已转账）",
		Price:   money.FromCents(3000),
	})
	if err != nil {
		return fmt.Errorf("演示提现 1 创建: %w", err)
	}
	if _, err := svc.ApproveDemoWithdraw(ctx, approved.ID, ""); err != nil {
		return fmt.Errorf("演示提现 1 审批: %w", err)
	}
	for _, w := range []struct {
		subject string
		cents   int64
	}{
		{"演示提现（等待审批）", 8800},
		{"演示提现（小额）", 1250},
	} {
		if _, err := svc.CreateDemoWithdraw(ctx, &paydto.DemoWithdrawCreateReq{
			Subject: w.subject,
			Price:   money.FromCents(w.cents),
		}); err != nil {
			return fmt.Errorf("演示提现 %s 创建: %w", w.subject, err)
		}
	}
	return nil
}

// payDemoSeedLinkage 链路收尾的联动回写（与 demo 回调 HTTP 端点同一路径，经
// HandleDemoNotify 服务入口，不是直写）：成功退款单的退款回执（示例订单 1
// 部分退款、示例订单 2 全额退款；充值退款无 demo 消费行，联动幂等应答）与
// 成功转账单的转账回执（提现 1 置 10）。支付回执已在各链路内即时回写（退款
// 端点校验示例行已支付）。只回放**事实成功**的 pay 单据（与 job 只在成功时
// 建任务的口径一致）。回调任务保持待投递：make run-demo 后 job 投递到本地
// demo 端点，任务转 10、联动幂等跳过（商户回调重试链路在演示环境真实走通）。
func payDemoSeedLinkage(ctx context.Context, svc *payservice.Service) error {
	// 定位键与 #134 报文一致：DataID = 对应 pay 单据 ID（退款单/转账单）。
	refunded := paymodel.RefundStatusSuccess
	okRefunds, _, err := svc.ListRefunds(ctx, &paydto.RefundPageReq{Status: &refunded})
	if err != nil {
		return fmt.Errorf("定位成功退款单（联动）: %w", err)
	}
	for _, refund := range okRefunds {
		if err := payDemoLink(ctx, svc, payservice.DemoNotifyKindRefund,
			paymodel.NotifyTypeRefund, refund.ID); err != nil {
			return err
		}
	}
	transferred := paymodel.TransferStatusSuccess
	okTransfers, _, err := svc.ListTransfers(ctx, &paydto.TransferPageReq{Status: &transferred})
	if err != nil {
		return fmt.Errorf("定位成功转账单（联动）: %w", err)
	}
	for _, transfer := range okTransfers {
		if err := payDemoLink(ctx, svc, payservice.DemoNotifyKindTransfer,
			paymodel.NotifyTypeTransfer, transfer.ID); err != nil {
			return err
		}
	}
	return nil
}

// payDemoLink 直调 HandleDemoNotify（demo 商户回调业务联动入口，签名校验已在
// HTTP 层完成、进程内直调免签）。
func payDemoLink(ctx context.Context, svc *payservice.Service, kind string, notifyType int8, dataID int64) error {
	err := svc.HandleDemoNotify(ctx, kind, &payservice.NotifyPayload{
		Type:   notifyType,
		Status: "success",
		DataID: dataID,
	})
	if err != nil {
		return fmt.Errorf("demo 联动（%s, dataID=%d）: %w", kind, dataID, err)
	}
	return nil
}
