// mall_demo.go 写入 mall 演示数据（issue #183，docs/mall-mvp-spec.md §1 第 8 条
// 演示数据/§6 验收）：
//
//	演示会员 2 名（13900001001 张三 / 13900001002 李四，普通会员等级，
//	密码均为 123456，演示库专用号段——与 erp_demo 139000000xx 及集成测试
//	138xxx 号段错开）
//	→ 收货地址 2 条（各会员 1 条默认地址）
//	→ 分类两级（家电 → 手机/影音）+ 品牌 1（星皓电器）+ 属性「颜色」（黑/白）
//	→ SPU 2 个：单规格（星皓 X1 智能手机 ¥1999）与多规格（星皓 Pod 无线耳机
//	  ¥299，黑/白两 SKU），全部上架
//	→ 交易链：张三加购手机 → 真实下单（SO 单号/扣库存）→ mock 渠道发起支付
//	  → 模拟回调成功 → 订单已支付 + 积分按实付入账（+1999，order_pay）；
//	  张三另留一条耳机加购（未结算，购物车页有数据）；李四加购耳机 → 下单
//	  留待支付（下单即挂 pay 单，收银台/发起支付交互演示入口）
//	→ 李四积分调整 +100（admin_adjust，经唯一入账入口）
//
// 目标：`make run-demo` / GAP_SEED_DEMO=true 后，mall 三域（会员/商品/交易）
// 每个管理面页面都有真实数据可看；待支付订单保留交互入口，管理员/会员可
// 亲手再走一遍「发起支付 → mock 模拟回调 → 订单已支付 + 积分入账」链路
// （issue #183 验收「待支付+已支付各一，走真实 pay mock 链路」）。
//
// 时序约束（本文件最关键的设计决策）：seed.Run 在模块 service 装配之前执行
// （app.New 先 seed 后装配，ADR 0024 补遗的时序否决正是为此）——演示种子
// **不能使用 app 装配的 service 实例**。处理方式照 pay_demo.go 的先例：
// 种子内部自建 pay/mall service 实例（挂在 seed 事务句柄上），渠道注册表
// 与 OrderSuccessHandler 联动接线与 app.go 装配同构（paySvc 对本进程内
// mall 联动的注册只作用于种子局部实例，无全局副作用）。因此业务行全部经
// 真实服务入口落库，而不是直插模型：
//   - 会员/地址/分类/品牌/属性/SPU-SKU 经 mall service 管理端/C 端真实入口
//     （CreateMember/CreateProductCategory/CreateBrand/CreateProperty/
//     CreatePropertyValue/CreateProductSPU/AppCreateAddress）——bcrypt 密码、
//     规格快照反查、SPU+SKU 同事务等规则与 HTTP 端点同路径；
//   - 交易链经 C 端真实入口（AppAddCart/AppCreateOrder/AppPayOrder）+
//     paySvc.MockNotifyOrder 模拟回调（照 T8 集成测试手法，与 HTTP 端点同一
//     代码路径）——SO 单号（mall_sequence 发放）、下单扣库存、订单项成交
//     快照、pay 挂单幂等、支付联动同事务置单 + 积分入账全部为真实链路；
//   - 积分只经唯一入账入口（AdjustPoint 管理端调整 + 支付联动的
//     applyPointChangeTx 事务内核）：member_user.point_balance 与流水的
//     「快照可重放」不变量（spec §3.4 硬约束）不允许任何直插路径——余额
//     可由流水重放验证，与 pay 演示「钱包只经 ApplyWalletChange」同口径。
//
// ID 纪律：全部业务行经 service 入口落库，ID 走数据库自增，不占固定段
// （照 pay_demo「经 service 入口的业务行不收 ID」决策；mall 挂单应用等
// 固定 ID 配置行由基础种子 seed/mall.go 常驻，与本文件无关）。
//
// 幂等：表哨兵——任一 mall 业务表已有行（含软删）即整段跳过，重复执行
// 不重复插入，也不与已有租户的 mall 数据混写（非演示租户不受影响）。
// 哨兵不含 member_level（基础种子常驻）、member_point_config（租户配置行，
// 惰性 upsert）、mall_sequence（序号计数行，非业务数据信号）——同
// pay_demo 哨兵不含 pay_channel 的口径。支付联动依赖基础种子的 mall 挂单
// 应用（app_key=mall，固定 ID 5011）与 14 行渠道种子，均在 seed.Run 基础
// 段先行落库。
//
// 注意：本文件依赖 mall model/repository/service 与 pay service，代码生成器
// 的 fresh-module 编译校验剪除策略同 erp_demo.go（init 自注册下剪除 = 少一条
// 注册，seed 包照常编译）。
package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	malldto "github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	paydto "github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// mall 演示数据使用的固定取值（租户/号段常量；业务行 ID 走自增不占固定段）。
const (
	mallDemoTenantID int64 = 1

	// mallDemoMemberMobile1/2 是演示会员手机号（演示库专用 13xx 号段，
	// 与 erp_demo 的 139000000xx、集成测试的 138xxx 号段错开；演示种子只在
	// GAP_SEED_DEMO=true 时写入，与测试库物理隔离，号段错开是纵深防御）。
	mallDemoMemberMobile1 = "13900001001"
	mallDemoMemberMobile2 = "13900001002"

	// mallDemoMemberPassword 是两名演示会员的 C 端登录密码（管理端明文入参，
	// bcrypt 散列落库——CreateMember 真实入口处理）。
	mallDemoMemberPassword = "123456"

	// mallDemoUserIP 是服务入口的 userIP 入参（审计/extension 落列，演示环境
	// 固定本机回环）。
	mallDemoUserIP = "127.0.0.1"
)

// init 把 mall 演示数据入口自注册进 seed.Run 的有序演示列表（ADR 0024）：
// order 取 DemoOrderMall（Pay=50 之后）。只依赖基础种子（admin 账号 + mall
// 挂单应用/渠道种子），不依赖 CRM/OA/ERP/Pay 演示数据，顺序无关。
func init() { RegisterDemo(DemoOrderMall, seedMallDemo) }

// MallDemo 写入 mall 演示数据（会员/商品/交易链路/积分流水），幂等可重复
// 执行。seed.Run 在 GAP_SEED_DEMO=true 时经 withDemo 分支调用；只想补写
// mall 演示数据（不带其它模块演示数据）时也可直接调用。
func MallDemo(db *gorm.DB) error { return seedMallDemo(db) }

// seedMallDemo 写入 mall 演示数据：表哨兵非空时整段跳过，否则在同一事务里
// 自建 pay/mall service 实例（时序约束见文件头），按 会员 → 地址 → 商品
// 主数据 → SPU/SKU → 交易链 → 积分 的顺序经真实服务入口落库。
func seedMallDemo(db *gorm.DB) error {
	reason, err := mallDemoSkipReason(db)
	if err != nil {
		return err
	}
	if reason != "" {
		slog.Info("seed mall demo skipped", "reason", reason)
		return nil
	}

	err = tenant.Skip(db).Transaction(func(tx *gorm.DB) error {
		// 演示主体：内置管理员（基础种子保证存在，缺失报错不补建——同 pay_demo）。
		var admin sysmodel.User
		if err := tenant.Skip(tx).
			Where("tenant_id = ? AND username = ?", mallDemoTenantID, "admin").
			First(&admin).Error; err != nil {
			return fmt.Errorf("mall 演示数据缺少管理员账号 admin（请确认 seedUsers 已执行）: %w", err)
		}

		// 种子没有登录会话，显式注入演示租户与主体（同 pay_demo/erp_demo 口径）：
		// 管理端入口用 admin 主体，C 端入口用会员主体（pay 委托主体同款
		// 「mall:member:<mobile>」Username 形状，见 mall service/trade_pay.go）。
		base := tenant.WithTenantID(context.Background(), mallDemoTenantID)
		adminCtx := authn.With(base, authn.Principal{
			UserID: admin.ID, TenantID: mallDemoTenantID, Username: admin.Username,
		})

		// 服务实例自建（pay_demo 先例）：渠道注册表与支付成功联动接线同
		// app.go 装配形状——pay 渠道实现从平台种子行构建，wallet 后端是 pay
		// service 自身；mall 经 SetPaySvc 消费 pay，pay 经
		// SetOrderSuccessHandler 注册 mall 联动（pay 对 mall 零依赖，只持
		// 函数值；注册只作用于种子局部实例）。业务时区注入演示时区。
		payRepo := payrepo.New(tx)
		paySvc := payservice.New(payRepo)
		paySvc.SetLocation(demoLocation)
		registry, err := payDemoChannelRegistry(base, payRepo, paySvc)
		if err != nil {
			return err
		}
		paySvc.SetRegistry(registry)
		mallSvc := mallservice.New(mallrepo.New(tx))
		mallSvc.SetLocation(demoLocation)
		mallSvc.SetPaySvc(paySvc)
		paySvc.SetOrderSuccessHandler(mallSvc.HandlePaySuccess)

		// 1) 会员 2 名（普通会员等级；等级取基础种子行，被清空时兜底补建）。
		levelID, err := mallDemoEnsureLevel(tx)
		if err != nil {
			return err
		}
		member1, err := mallSvc.CreateMember(adminCtx, &malldto.MemberSaveReq{
			Mobile:   mallDemoMemberMobile1,
			Password: mallDemoMemberPassword,
			Nickname: "演示会员·张三",
			LevelID:  levelID,
			Remark:   "run-demo 演示会员：已支付订单 + 积分支付入账视角",
		})
		if err != nil {
			return fmt.Errorf("演示会员 1 创建: %w", err)
		}
		member2, err := mallSvc.CreateMember(adminCtx, &malldto.MemberSaveReq{
			Mobile:   mallDemoMemberMobile2,
			Password: mallDemoMemberPassword,
			Nickname: "演示会员·李四",
			LevelID:  levelID,
			Remark:   "run-demo 演示会员：待支付订单 + 管理员积分调整视角",
		})
		if err != nil {
			return fmt.Errorf("演示会员 2 创建: %w", err)
		}
		member1Ctx := authn.With(base, authn.Principal{
			UserID: member1.ID, TenantID: mallDemoTenantID,
			Username: "mall:member:" + member1.Mobile,
		})
		member2Ctx := authn.With(base, authn.Principal{
			UserID: member2.ID, TenantID: mallDemoTenantID,
			Username: "mall:member:" + member2.Mobile,
		})

		// 2) 收货地址（C 端真实入口，首地址自动默认）。
		address1, err := mallSvc.AppCreateAddress(member1Ctx, member1.ID, &malldto.AppAddressSaveReq{
			ReceiverName: "张三", ReceiverMobile: mallDemoMemberMobile1,
			Province: "广东省", City: "深圳市", District: "南山区",
			Detail: "科技园演示路 1 号",
		})
		if err != nil {
			return fmt.Errorf("演示地址 1 创建: %w", err)
		}
		address2, err := mallSvc.AppCreateAddress(member2Ctx, member2.ID, &malldto.AppAddressSaveReq{
			ReceiverName: "李四", ReceiverMobile: mallDemoMemberMobile2,
			Province: "浙江省", City: "杭州市", District: "西湖区",
			Detail: "文三路演示街 2 号",
		})
		if err != nil {
			return fmt.Errorf("演示地址 2 创建: %w", err)
		}

		// 3) 商品主数据（管理端真实入口）：两级分类（家电 → 手机/影音，
		//    叶子才可挂 SPU）+ 品牌 + 属性「颜色」（黑/白）。
		rootCategory, err := mallSvc.CreateProductCategory(adminCtx, &malldto.CategorySaveReq{
			Name: "家电", Sort: 1,
		})
		if err != nil {
			return fmt.Errorf("演示分类（家电）创建: %w", err)
		}
		phoneCategory, err := mallSvc.CreateProductCategory(adminCtx, &malldto.CategorySaveReq{
			ParentID: rootCategory.ID, Name: "手机", Sort: 1,
		})
		if err != nil {
			return fmt.Errorf("演示分类（手机）创建: %w", err)
		}
		audioCategory, err := mallSvc.CreateProductCategory(adminCtx, &malldto.CategorySaveReq{
			ParentID: rootCategory.ID, Name: "影音", Sort: 2,
		})
		if err != nil {
			return fmt.Errorf("演示分类（影音）创建: %w", err)
		}
		brand, err := mallSvc.CreateBrand(adminCtx, &malldto.BrandSaveReq{
			Name: "星皓电器", Remark: "run-demo 演示品牌",
		})
		if err != nil {
			return fmt.Errorf("演示品牌创建: %w", err)
		}
		colorProperty, err := mallSvc.CreateProperty(adminCtx, &malldto.PropertySaveReq{Name: "颜色"})
		if err != nil {
			return fmt.Errorf("演示属性创建: %w", err)
		}
		blackValue, err := mallSvc.CreatePropertyValue(adminCtx, &malldto.PropertyValueSaveReq{
			PropertyID: colorProperty.ID, Name: "黑色",
		})
		if err != nil {
			return fmt.Errorf("演示属性值（黑）创建: %w", err)
		}
		whiteValue, err := mallSvc.CreatePropertyValue(adminCtx, &malldto.PropertyValueSaveReq{
			PropertyID: colorProperty.ID, Name: "白色",
		})
		if err != nil {
			return fmt.Errorf("演示属性值（白）创建: %w", err)
		}

		// 4) SPU 2 个（管理端真实入口，同事务落 SKU；规格快照由服务端按主
		//    数据反查写入，客户端不采纳）：单规格手机 + 多规格耳机，全部上架
		//   （只有上架可被购买/加购，spec §3.2）。
		onSale := mallmodel.ProductSPUStatusOn
		spu1, err := mallSvc.CreateProductSPU(adminCtx, &malldto.SPUSaveReq{
			CategoryID:    phoneCategory.ID,
			BrandID:       brand.ID,
			Name:          "星皓 X1 智能手机",
			PicURL:        "",
			SliderPicURLs: []string{},
			Detail:        "run-demo 演示商品：单规格（价格库存挂唯一 SKU）",
			SpecType:      mallmodel.ProductSpecTypeSingle,
			MarketPrice:   moneyPtr(money.FromCents(219900)),
			Status:        &onSale,
			Sort:          1,
			Skus: []malldto.SPUSkuReq{{
				Price:       money.FromCents(199900),
				MarketPrice: moneyPtr(money.FromCents(219900)),
				Stock:       50,
				First:       true,
			}},
		})
		if err != nil {
			return fmt.Errorf("演示 SPU 1（单规格）创建: %w", err)
		}
		spu2, err := mallSvc.CreateProductSPU(adminCtx, &malldto.SPUSaveReq{
			CategoryID:    audioCategory.ID,
			BrandID:       brand.ID,
			Name:          "星皓 Pod 无线耳机",
			PicURL:        "",
			SliderPicURLs: []string{},
			Detail:        "run-demo 演示商品：多规格（颜色 黑/白，规格 JSON 快照）",
			SpecType:      mallmodel.ProductSpecTypeMulti,
			MarketPrice:   moneyPtr(money.FromCents(32900)),
			Status:        &onSale,
			Sort:          2,
			Skus: []malldto.SPUSkuReq{
				{
					Properties: []malldto.SPUSkuPropertyReq{
						{PropertyID: colorProperty.ID, ValueID: blackValue.ID},
					},
					Price:       money.FromCents(29900),
					MarketPrice: moneyPtr(money.FromCents(32900)),
					Stock:       30,
					First:       true,
				},
				{
					Properties: []malldto.SPUSkuPropertyReq{
						{PropertyID: colorProperty.ID, ValueID: whiteValue.ID},
					},
					Price:       money.FromCents(29900),
					MarketPrice: moneyPtr(money.FromCents(32900)),
					Stock:       30,
				},
			},
		})
		if err != nil {
			return fmt.Errorf("演示 SPU 2（多规格）创建: %w", err)
		}
		// SKU ID 取详情（CreateProductSPU 不回填 Skus；详情按 first DESC,id ASC，
		// 主 SKU 恒首位——单规格只有一条，多规格黑 SKU first=1 在首位）。
		spu1Detail, err := mallSvc.GetProductSPU(adminCtx, spu1.ID)
		if err != nil {
			return fmt.Errorf("演示 SPU 1 详情: %w", err)
		}
		spu2Detail, err := mallSvc.GetProductSPU(adminCtx, spu2.ID)
		if err != nil {
			return fmt.Errorf("演示 SPU 2 详情: %w", err)
		}
		spu1SKU := spu1Detail.Skus[0]
		spu2BlackSKU := spu2Detail.Skus[0]

		// 5) 交易链（C 端真实入口）：张三加购手机 → 下单（SO 单号/扣库存/
		//    成交快照/清已结算购物车）→ mock 渠道发起支付 → 模拟回调成功
		//    → 订单已支付 + 积分按实付入账（order_pay，唯一入账入口事务内核）。
		if err := mallSvc.AppAddCart(member1Ctx, member1.ID, &malldto.AppCartAddReq{
			SKUID: spu1SKU.ID, Count: 1,
		}); err != nil {
			return fmt.Errorf("演示加购（张三·手机）: %w", err)
		}
		paidOrder, err := mallSvc.AppCreateOrder(member1Ctx, member1.ID, &malldto.AppCreateOrderReq{
			AddressID:  address1.ID,
			UserRemark: "演示订单：mock 渠道已支付",
		}, mallDemoUserIP)
		if err != nil {
			return fmt.Errorf("演示订单 1（已支付）创建: %w", err)
		}
		paySubmit, err := mallSvc.AppPayOrder(member1Ctx, member1.ID, paidOrder.ID, &malldto.AppOrderPayReq{
			ChannelCode: paychannel.CodeMock,
		}, mallDemoUserIP)
		if err != nil {
			return fmt.Errorf("演示订单 1 发起支付: %w", err)
		}
		if _, err := paySvc.MockNotifyOrder(member1Ctx, &paydto.MockNotifyReq{
			OrderID: paySubmit.PayOrderID, Result: "success",
		}); err != nil {
			return fmt.Errorf("演示订单 1 模拟回调: %w", err)
		}
		// 张三再留一条耳机加购（选中态，未结算）：购物车页/确认结算页有
		// 真实数据可演示（在已支付订单之后加购，不会被下单清掉）。
		if err := mallSvc.AppAddCart(member1Ctx, member1.ID, &malldto.AppCartAddReq{
			SKUID: spu2BlackSKU.ID, Count: 1,
		}); err != nil {
			return fmt.Errorf("演示加购（张三·耳机）: %w", err)
		}
		// 李四加购耳机 → 下单留待支付：下单即挂 pay 单（linkPayOrderAfterCreate
		// 独立事务，mock 渠道停在待支付等回调）——「发起支付 → mock 模拟回调
		// → 联动置单」交互演示入口（issue #183 验收：待支付+已支付各一）。
		if err := mallSvc.AppAddCart(member2Ctx, member2.ID, &malldto.AppCartAddReq{
			SKUID: spu2BlackSKU.ID, Count: 1,
		}); err != nil {
			return fmt.Errorf("演示加购（李四·耳机）: %w", err)
		}
		if _, err := mallSvc.AppCreateOrder(member2Ctx, member2.ID, &malldto.AppCreateOrderReq{
			AddressID:  address2.ID,
			UserRemark: "演示订单：待支付（可发起支付走 mock 回调）",
		}, mallDemoUserIP); err != nil {
			return fmt.Errorf("演示订单 2（待支付）创建: %w", err)
		}

		// 6) 积分调整（管理端真实入口 = 唯一入账入口）：李四 +100
		//   （admin_adjust；张三的积分已由支付联动按实付入账，流水可重放）。
		if _, err := mallSvc.AdjustPoint(adminCtx, &malldto.PointAdjustReq{
			UserID: member2.ID, Point: 100, Remark: "run-demo 演示积分调整",
		}); err != nil {
			return fmt.Errorf("演示积分调整: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.Info("seed mall demo created",
		"members", 2, "addresses", 2, "categories", 3, "brands", 1,
		"properties", 1, "propertyValues", 2, "spus", 2, "skus", 3,
		"carts", 1, "orders", 2, "paidOrders", 1, "pointRecords", 2, "payOrders", 2)
	return nil
}

// mallDemoSkipReason 返回跳过原因（空串表示 mall 全空、可以写入）。任一 mall
// 业务表已有行（含软删）即视为库里已有 mall 数据，整段跳过：避免与人工数据
// 混写、演示链路与已有租户数据冲突（同 payDemoSkipReason 口径）。member_level
// （基础种子常驻）、member_point_config（租户配置行）、mall_sequence（序号
// 计数行）刻意不在哨兵内——它们不是 mall 演示数据的信号。
func mallDemoSkipReason(db *gorm.DB) (string, error) {
	checks := []struct {
		label string
		model any
	}{
		{"member_user", &mallmodel.MemberUser{}},
		{"member_address", &mallmodel.MemberAddress{}},
		{"member_point_record", &mallmodel.MemberPointRecord{}},
		{"product_category", &mallmodel.ProductCategory{}},
		{"product_brand", &mallmodel.ProductBrand{}},
		{"product_property", &mallmodel.ProductProperty{}},
		{"product_property_value", &mallmodel.ProductPropertyValue{}},
		{"product_spu", &mallmodel.ProductSPU{}},
		{"product_sku", &mallmodel.ProductSKU{}},
		{"trade_cart", &mallmodel.TradeCart{}},
		{"trade_order", &mallmodel.TradeOrder{}},
		{"trade_order_item", &mallmodel.TradeOrderItem{}},
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

// mallDemoEnsureLevel 返回演示会员挂靠的「普通会员」等级 ID：优先取基础种子
// 行（seedMemberLevel，name=普通会员）；被管理员改名/停用时退取租户内最低
// level 的在册行；表彻底为空时兜底补建一行（自动 ID，不占固定段）——
// CreateMember 的等级存在性校验需要行真实存在。
func mallDemoEnsureLevel(tx *gorm.DB) (int64, error) {
	var level mallmodel.MemberLevel
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND name = ?", mallDemoTenantID, "普通会员").
		Order("id ASC").First(&level).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = tenant.Skip(tx).
			Where("tenant_id = ? AND status = ?", mallDemoTenantID, mallmodel.StatusEnable).
			Order("`level` ASC, id ASC").First(&level).Error
	}
	switch {
	case err == nil:
		return level.ID, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		row := &mallmodel.MemberLevel{
			TenantID:   mallDemoTenantID,
			Name:       "普通会员",
			Level:      1,
			Experience: 0,
			Status:     mallmodel.StatusEnable,
			Remark:     "mall 演示数据兜底等级（基础种子等级行缺失时补建）",
		}
		if err := tenant.Skip(tx).Create(row).Error; err != nil {
			return 0, fmt.Errorf("mall 演示兜底等级创建: %w", err)
		}
		return row.ID, nil
	default:
		return 0, err
	}
}

// moneyPtr 是 SPU 划线价（*money.Money 入参）的小助手。
func moneyPtr(m money.Money) *money.Money { return &m }
