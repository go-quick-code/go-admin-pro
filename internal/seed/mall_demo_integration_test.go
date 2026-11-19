//go:build integration

// mall 演示数据集成测试（issue #183，docs/mall-mvp-spec.md §1 第 8 条 演示数据/
// §6 验收）：真实 MySQL 上验证
//   - seed.Run(db, true) 一键写入完整 mall 演示链路（含 CRM/OA/ERP/平台/pay
//     演示，本用例只断言 mall 域）：会员 2 名（普通会员等级，密码 bcrypt）→
//     收货地址 → 分类两级/品牌/属性（颜色 黑/白）→ SPU 2 个（单规格 + 多规格，
//     全部上架）→ 张三加购手机真实下单（SO 单号/扣库存）→ mock 渠道发起支付
//     → 模拟回调成功 → 订单已支付 + 积分按实付入账；张三留一条耳机加购；
//     李四加购耳机下单留待支付（下单即挂 pay 单）→ 李四积分调整 +100；
//   - 全部业务行经 mall/pay service 真实入口落库（种子内自建服务实例，时序
//     约束见 mall_demo.go 文件头）：积分余额可由流水重放（唯一入账入口，
//     spec §3.4 硬约束），库存扣减/订单项快照/金额三列与入口规则一致；
//   - 表哨兵幂等：重复执行不重复插入。
package seed_test

import (
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// mallDemoCounts 是一次 mall 演示数据写入后的行数快照（幂等断言用）。
type mallDemoCounts struct {
	Members        int64
	Addresses      int64
	PointRecords   int64
	Categories     int64
	Brands         int64
	Properties     int64
	PropertyValues int64
	SPUs           int64
	SKUs           int64
	Carts          int64
	Orders         int64
	OrderItems     int64
}

func countMallDemo(t *testing.T, db *gorm.DB) mallDemoCounts {
	t.Helper()
	count := func(model any) int64 {
		var total int64
		if err := tenant.Skip(db).Model(model).Count(&total).Error; err != nil {
			t.Fatalf("统计 %T: %v", model, err)
		}
		return total
	}
	return mallDemoCounts{
		Members:        count(&mallmodel.MemberUser{}),
		Addresses:      count(&mallmodel.MemberAddress{}),
		PointRecords:   count(&mallmodel.MemberPointRecord{}),
		Categories:     count(&mallmodel.ProductCategory{}),
		Brands:         count(&mallmodel.ProductBrand{}),
		Properties:     count(&mallmodel.ProductProperty{}),
		PropertyValues: count(&mallmodel.ProductPropertyValue{}),
		SPUs:           count(&mallmodel.ProductSPU{}),
		SKUs:           count(&mallmodel.ProductSKU{}),
		Carts:          count(&mallmodel.TradeCart{}),
		Orders:         count(&mallmodel.TradeOrder{}),
		OrderItems:     count(&mallmodel.TradeOrderItem{}),
	}
}

// TestIntegrationMallDemoSeed 验证 mall 演示链路的完整性、幂等性与快照可重放。
func TestIntegrationMallDemoSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 测试基座默认关闭演示数据；显式写入第一遍。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("写入 mall 演示数据: %v", err)
	}
	first := countMallDemo(t, env.DB)
	want := mallDemoCounts{
		Members:        2, // 张三（13900001001）/ 李四（13900001002）
		Addresses:      2, // 各会员 1 条默认地址
		PointRecords:   2, // 张三 order_pay +1999（支付联动）/ 李四 admin_adjust +100
		Categories:     3, // 家电 → 手机 + 影音（两级树）
		Brands:         1, // 星皓电器
		Properties:     1, // 颜色
		PropertyValues: 2, // 黑色 / 白色
		SPUs:           2, // 单规格手机 + 多规格耳机（全部上架）
		SKUs:           3, // 手机 1 + 耳机黑/白 2
		Carts:          1, // 张三的耳机加购（已支付订单结算后新增，留购物车演示）
		Orders:         2, // 已支付 1 + 待支付 1
		OrderItems:     2, // 每单 1 项（成交快照）
	}
	if first != want {
		t.Fatalf("mall 演示数据行数不符：\n got %+v\nwant %+v", first, want)
	}

	// 重复执行必须幂等：所有 mall 表计数完全不变。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("重复写入 mall 演示数据: %v", err)
	}
	second := countMallDemo(t, env.DB)
	if second != first {
		t.Fatalf("重复 seed 不应新增行：\n before %+v\n after %+v", first, second)
	}

	assertMallDemoMembers(t, env.DB)
	assertMallDemoProducts(t, env.DB)
	assertMallDemoTradeChain(t, env.DB)
	assertMallDemoPointLedger(t, env.DB)
}

// assertMallDemoMembers 校验演示会员：号段/等级（基础种子「普通会员」行）/
// 启用态/默认地址各一条。
func assertMallDemoMembers(t *testing.T, db *gorm.DB) {
	t.Helper()
	var level mallmodel.MemberLevel
	if err := tenant.Skip(db).First(&level, "tenant_id = 1 AND name = ?", "普通会员").Error; err != nil {
		t.Fatalf("读取普通会员等级: %v", err)
	}
	for _, w := range []struct {
		mobile   string
		nickname string
	}{
		{"13900001001", "演示会员·张三"},
		{"13900001002", "演示会员·李四"},
	} {
		var member mallmodel.MemberUser
		if err := tenant.Skip(db).First(&member, "tenant_id = 1 AND mobile = ?", w.mobile).Error; err != nil {
			t.Fatalf("读取演示会员 %s: %v", w.mobile, err)
		}
		if member.Nickname != w.nickname || member.LevelID != level.ID ||
			member.Status != mallmodel.StatusEnable || member.PasswordHash == "" {
			t.Fatalf("演示会员 %s 不符：%+v", w.mobile, member)
		}
		var addrCount int64
		if err := tenant.Skip(db).Model(&mallmodel.MemberAddress{}).
			Where("user_id = ? AND is_default = 1", member.ID).Count(&addrCount).Error; err != nil {
			t.Fatalf("统计演示会员地址: %v", err)
		}
		if addrCount != 1 {
			t.Fatalf("演示会员 %s 应有 1 条默认地址，实际 %d", w.mobile, addrCount)
		}
	}
}

// assertMallDemoProducts 校验商品域：两级分类树、品牌、属性（黑/白）、
// SPU 上架态与 SKU 库存（下单扣减：手机 50→49、耳机黑 30→29）。
func assertMallDemoProducts(t *testing.T, db *gorm.DB) {
	t.Helper()
	var root mallmodel.ProductCategory
	if err := tenant.Skip(db).First(&root, "tenant_id = 1 AND name = ? AND parent_id = 0", "家电").Error; err != nil {
		t.Fatalf("读取演示分类（家电）: %v", err)
	}
	var childCount int64
	if err := tenant.Skip(db).Model(&mallmodel.ProductCategory{}).
		Where("parent_id = ?", root.ID).Count(&childCount).Error; err != nil {
		t.Fatalf("统计家电子分类: %v", err)
	}
	if childCount != 2 {
		t.Fatalf("家电下应有 2 个子分类，实际 %d", childCount)
	}
	var spus []mallmodel.ProductSPU
	if err := tenant.Skip(db).Order("id ASC").Find(&spus, "tenant_id = 1").Error; err != nil {
		t.Fatalf("读取演示 SPU: %v", err)
	}
	if len(spus) != 2 {
		t.Fatalf("演示 SPU 应有 2 个，实际 %d", len(spus))
	}
	if spus[0].SpecType != mallmodel.ProductSpecTypeSingle || spus[1].SpecType != mallmodel.ProductSpecTypeMulti {
		t.Fatalf("演示 SPU 应为 单规格+多规格：%+v", spus)
	}
	for _, spu := range spus {
		if spu.Status != mallmodel.ProductSPUStatusOn {
			t.Fatalf("演示 SPU %s 应上架：%+v", spu.Name, spu)
		}
	}
	var phoneSKU, podBlackSKU mallmodel.ProductSKU
	if err := tenant.Skip(db).First(&phoneSKU, "spu_id = ?", spus[0].ID).Error; err != nil {
		t.Fatalf("读取手机 SKU: %v", err)
	}
	if phoneSKU.Stock != 49 || phoneSKU.First != 1 {
		t.Fatalf("手机 SKU 库存应已扣至 49（下单扣减）且为主 SKU：%+v", phoneSKU)
	}
	if err := tenant.Skip(db).First(&podBlackSKU, "spu_id = ? AND `first` = 1", spus[1].ID).Error; err != nil {
		t.Fatalf("读取耳机主 SKU: %v", err)
	}
	if podBlackSKU.Stock != 29 {
		t.Fatalf("耳机黑 SKU 库存应已扣至 29（待支付订单扣减），实际 %d", podBlackSKU.Stock)
	}
	var podSKUs []mallmodel.ProductSKU
	if err := tenant.Skip(db).Find(&podSKUs, "spu_id = ?", spus[1].ID).Error; err != nil {
		t.Fatalf("读取耳机 SKU 列表: %v", err)
	}
	if len(podSKUs) != 2 {
		t.Fatalf("耳机应有黑/白 2 个 SKU，实际 %d", len(podSKUs))
	}
}

// assertMallDemoTradeChain 校验交易链：已支付订单（pay 四列回填 + pay 单成功
// mock 渠道 + 订单项成交快照）、待支付订单（下单即挂 pay 单待支付）、购物车
// 残留行（张三耳机、选中态）。
func assertMallDemoTradeChain(t *testing.T, db *gorm.DB) {
	t.Helper()
	var member1, member2 mallmodel.MemberUser
	if err := tenant.Skip(db).First(&member1, "mobile = ?", "13900001001").Error; err != nil {
		t.Fatalf("读取张三: %v", err)
	}
	if err := tenant.Skip(db).First(&member2, "mobile = ?", "13900001002").Error; err != nil {
		t.Fatalf("读取李四: %v", err)
	}

	var paidOrder mallmodel.TradeOrder
	if err := tenant.Skip(db).First(&paidOrder, "user_id = ? AND status = ?",
		member1.ID, mallmodel.TradeOrderStatusPaid).Error; err != nil {
		t.Fatalf("读取已支付演示订单: %v", err)
	}
	if len(paidOrder.No) < 3 || paidOrder.No[:2] != "SO" {
		t.Fatalf("已支付订单号应为 SO 前缀：%q", paidOrder.No)
	}
	if paidOrder.PayOrderID == 0 || paidOrder.PayChannelCode != "mock" || paidOrder.PayTime == nil {
		t.Fatalf("已支付订单 pay 四列应回填：%+v", paidOrder)
	}
	var paidPayOrder paymodel.Order
	if err := tenant.Skip(db).First(&paidPayOrder, "id = ?", paidOrder.PayOrderID).Error; err != nil {
		t.Fatalf("读取已支付订单的 pay 单: %v", err)
	}
	if paidPayOrder.Status != paymodel.OrderStatusSuccess ||
		paidPayOrder.ChannelCode != "mock" ||
		paidPayOrder.MerchantOrderID != paidOrder.No {
		t.Fatalf("已支付订单 pay 单应成功且同号挂单：%+v", paidPayOrder)
	}
	// mock 支付成功建商户回调任务（paymentSuccessTx 同事务）：mall 应用回调
	// 地址为空（进程内 OrderSuccessHandler 联动，无外部回调端点——seed/mall.go
	// seedMallPayApp 口径），任务投递无消费方，由 pay 超时关单 job 收口。
	var notifyTaskCount int64
	if err := tenant.Skip(db).Model(&paymodel.NotifyTask{}).
		Where("app_id = ? AND data_id = ? AND type = ?",
			paidPayOrder.AppID, paidPayOrder.ID, paymodel.NotifyTypePayment).
		Count(&notifyTaskCount).Error; err != nil {
		t.Fatalf("统计 mall 演示支付回调任务: %v", err)
	}
	if notifyTaskCount != 1 {
		t.Fatalf("mall 演示支付应建 1 条回调任务，实际 %d", notifyTaskCount)
	}

	var pendingOrder mallmodel.TradeOrder
	if err := tenant.Skip(db).First(&pendingOrder, "user_id = ? AND status = ?",
		member2.ID, mallmodel.TradeOrderStatusPending).Error; err != nil {
		t.Fatalf("读取待支付演示订单: %v", err)
	}
	if pendingOrder.PayOrderID == 0 {
		t.Fatalf("待支付订单应已挂 pay 单（下单即建单）：%+v", pendingOrder)
	}
	var pendingPayOrder paymodel.Order
	if err := tenant.Skip(db).First(&pendingPayOrder, "id = ?", pendingOrder.PayOrderID).Error; err != nil {
		t.Fatalf("读取待支付订单的 pay 单: %v", err)
	}
	if pendingPayOrder.Status != paymodel.OrderStatusPending {
		t.Fatalf("待支付订单的 pay 单应待支付：%+v", pendingPayOrder)
	}

	var items []mallmodel.TradeOrderItem
	if err := tenant.Skip(db).Order("id ASC").Find(&items, "order_id IN ?",
		[]int64{paidOrder.ID, pendingOrder.ID}).Error; err != nil {
		t.Fatalf("读取演示订单项: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("演示订单项应有 2 行，实际 %d", len(items))
	}
	for _, item := range items {
		if item.Subtotal.Cents() != item.Price.Cents()*int64(item.Count) {
			t.Fatalf("订单项 subtotal 应 = price × count：%+v", item)
		}
		if item.SPUName == "" {
			t.Fatalf("订单项应带成交快照（spu_name）：%+v", item)
		}
	}

	var cartCount int64
	if err := tenant.Skip(db).Model(&mallmodel.TradeCart{}).
		Where("user_id = ? AND selected = 1", member1.ID).Count(&cartCount).Error; err != nil {
		t.Fatalf("统计张三购物车: %v", err)
	}
	if cartCount != 1 {
		t.Fatalf("张三应留 1 条选中购物车行（耳机加购演示），实际 %d", cartCount)
	}
	var member2CartCount int64
	if err := tenant.Skip(db).Model(&mallmodel.TradeCart{}).
		Where("user_id = ?", member2.ID).Count(&member2CartCount).Error; err != nil {
		t.Fatalf("统计李四购物车: %v", err)
	}
	if member2CartCount != 0 {
		t.Fatalf("李四购物车应已随下单清空，实际 %d 行", member2CartCount)
	}
}

// assertMallDemoPointLedger 校验积分流水：张三 order_pay +1999（支付联动按
// 实付整数元）、李四 admin_adjust +100；余额 = 流水代数和 = 末笔快照（可重放，
// 唯一入账入口——spec §3.4）。
func assertMallDemoPointLedger(t *testing.T, db *gorm.DB) {
	t.Helper()
	var member1, member2 mallmodel.MemberUser
	if err := tenant.Skip(db).First(&member1, "mobile = ?", "13900001001").Error; err != nil {
		t.Fatalf("读取张三: %v", err)
	}
	if err := tenant.Skip(db).First(&member2, "mobile = ?", "13900001002").Error; err != nil {
		t.Fatalf("读取李四: %v", err)
	}
	assertMemberPointLedger := func(t *testing.T, member mallmodel.MemberUser, wantBiz string, wantPoint int) {
		t.Helper()
		var records []mallmodel.MemberPointRecord
		if err := tenant.Skip(db).Order("id ASC").Find(&records, "user_id = ?", member.ID).Error; err != nil {
			t.Fatalf("读取积分流水（%s）: %v", member.Mobile, err)
		}
		if len(records) != 1 {
			t.Fatalf("演示会员 %s 应有 1 条积分流水，实际 %d", member.Mobile, len(records))
		}
		record := records[0]
		if record.BizType != wantBiz || record.Point != wantPoint {
			t.Fatalf("演示会员 %s 流水应为 %s %+d：%+v", member.Mobile, wantBiz, wantPoint, record)
		}
		var sum int
		for _, r := range records {
			sum += r.Point
		}
		if member.PointBalance != sum || member.PointBalance != record.BalanceAfter {
			t.Fatalf("积分快照应可重放（余额 %d = 代数和 %d = 末笔结存 %d）",
				member.PointBalance, sum, record.BalanceAfter)
		}
	}
	assertMemberPointLedger(t, member1, mallmodel.MemberPointBizOrderPay, 1999)
	assertMemberPointLedger(t, member2, mallmodel.MemberPointBizAdminAdjust, 100)
}
