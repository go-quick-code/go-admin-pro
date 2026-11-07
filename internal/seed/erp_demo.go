// erp_demo.go 写入 ERP 演示数据（issue #97，docs/erp-mvp-spec.md §7/§9）：
//
//	仓库/产品分类/单位/产品/客户/供应商/结算账户
//	→ 采购订单（CG，已审核）→ 采购入库（RK，已审核，库存增加）
//	→ 盘点（PD，已审核，盘盈 +1 与盘亏 −1）
//	→ 销售订单（XS，已审核）→ 销售出库（CK，已审核，库存减少）
//	→ 收款单（SK，已审核，挂销售出库）→ 付款单（FK，已审核，挂采购入库）
//
// 目标：`make run-demo` / GAP_SEED_DEMO=true 后，ERP 首页统计的今日/本月
// 有值，产品库存、出入库流水、十二类单据与财务页面都能看到一条完整链路。
//
// 幂等：按表哨兵——ERP 任一主数据/单据/库存表已有行（含软删）时整段跳过，
// 重复执行不重复插入。演示行使用固定 ID 段（主数据 3001 起、单据 4001 起、
// 库存 4201 起、流水 4301 起，各表独立），与 CRM（1001 起）/OA（2001 起）
// 演示 ID 不冲突；运行期自增 ID 由数据库发放，不会撞主键。
//
// 落库口径（spec §4，所有不变量与 service 审核路径一致，均在此注释说明）：
//   - 演示数据不走 HTTP/service（种子阶段没有登录上下文），而是按各表不变量
//     手工落库：明细行金额 = 单价 × 数量（Money.MulQty）、主表金额 = 明细合计、
//     订单进度 in_count/out_count 只计已审核单、收付款回写 receipt_price/
//     payment_price = 已审核核销合计、库存余额 = 流水代数和且 total_count 快照
//     逐笔正确、任何时点无负库存。
//   - 单号按 spec §4.8 的「前缀 + yyyyMMdd + 4 位序号」生成，并同步写
//     erp_sequence（GREATEST 合并，绝不回退已有序列），后续 UI 新建不会撞号。
//   - 业务时间全部落在演示当天（北京时间/进程本地时区，与其它演示种子一致），
//     保证统计接口的「今日/本月」可见；顺序按采购 → 盘点 → 销售 → 收付款。
//
// 注意：本文件依赖 erp model/service，代码生成器的 fresh-module 编译校验会
// 连同真实 erp 模块一起剪除本文件（见 internal/module/codegen/compile_check_test.go）。
package seed

import (
	"fmt"
	"log/slog"
	"time"

	erpmodel "github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	erpservice "github.com/go-quick-code/go-admin-pro/internal/module/erp/service"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// ERP 演示数据使用的固定 ID（各表独立；与 CRM 1001 段、OA 2001 段错开）。
const (
	erpDemoTenantID int64 = 1

	// 主数据（3001 起）。
	erpDemoCategoryHardwareID int64 = 3001
	erpDemoCategoryOfficeID   int64 = 3002
	erpDemoUnitSetID          int64 = 3001
	erpDemoUnitBoxID          int64 = 3002
	erpDemoUnitPieceID        int64 = 3003
	erpDemoProductTerminalID  int64 = 3001
	erpDemoProductKeyboardID  int64 = 3002
	erpDemoProductPaperID     int64 = 3003
	erpDemoWarehouseMainID    int64 = 3001
	erpDemoWarehouseSpareID   int64 = 3002
	erpDemoCustomerFutureID   int64 = 3001
	erpDemoCustomerBlueSeaID  int64 = 3002
	erpDemoSupplierHQID       int64 = 3001
	erpDemoSupplierSDID       int64 = 3002
	erpDemoAccountICBCID      int64 = 3001
	erpDemoAccountAlipayID    int64 = 3002

	// 单据主表（4001 起；明细 4101 起、库存 4201 起、流水 4301 起）。
	erpDemoPurchaseOrderID int64 = 4001
	erpDemoPurchaseInID    int64 = 4002
	erpDemoStockCheckID    int64 = 4003
	erpDemoSaleOrderID     int64 = 4004
	erpDemoSaleOutID       int64 = 4005
	erpDemoReceiptID       int64 = 4006
	erpDemoPaymentID       int64 = 4007
)

// init 把 ERP 演示数据入口注册给 seed.Run（见 seed.go 的 seedERPDemoFunc 注释）。
func init() { seedERPDemoFunc = seedERPDemo }

// erpDemoUser 是演示数据 owner（ERP 无公海，owner = 创建人，spec §4.10）。
type erpDemoUser struct {
	ID     int64
	DeptID int64
}

func (u erpDemoUser) creator() string {
	if u.ID <= 0 {
		return "0"
	}
	return fmt.Sprintf("%d", u.ID)
}

// seedERPDemo 写入 ERP 演示数据：表哨兵非空时整段跳过，否则在同一事务里
// 按 主数据 → 采购 → 盘点 → 销售 → 收付款 → 库存余额 → 序列 的顺序落库。
func seedERPDemo(db *gorm.DB) error {
	reason, err := erpDemoSkipReason(db)
	if err != nil {
		return err
	}
	if reason != "" {
		slog.Info("seed erp demo skipped", "reason", reason)
		return nil
	}

	err = tenant.Skip(db).Transaction(func(tx *gorm.DB) error {
		admin, err := erpDemoAdminUser(tx)
		if err != nil {
			return err
		}
		now := time.Now()
		// 业务时间全部落在当天且不晚于现在：种子在凌晨执行时钳制到 00:00，
		// 保证统计接口的「今日」区间一定包含这些单据。
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		at := func(minutesAgo int) time.Time {
			t := now.Add(-time.Duration(minutesAgo) * time.Minute)
			if t.Before(dayStart) {
				return dayStart
			}
			return t
		}
		dateKey := now.Format("20060102")
		seq := newERPDemoSequencer(tx, erpDemoTenantID)

		if err := erpDemoSeedMaster(tx, admin, now); err != nil {
			return err
		}
		purchaseOrderTime := at(10)
		purchaseInTime := at(9)
		if err := erpDemoSeedPurchase(tx, admin, seq, dateKey, purchaseOrderTime, purchaseInTime); err != nil {
			return err
		}
		checkTime := at(7)
		if err := erpDemoSeedStockCheck(tx, admin, seq, dateKey, checkTime); err != nil {
			return err
		}
		saleOrderTime := at(6)
		saleOutTime := at(4)
		if err := erpDemoSeedSale(tx, admin, seq, dateKey, saleOrderTime, saleOutTime); err != nil {
			return err
		}
		if err := erpDemoSeedFinance(tx, admin, seq, dateKey, at(2), at(1)); err != nil {
			return err
		}
		if err := erpDemoSeedStocks(tx, purchaseInTime, saleOutTime); err != nil {
			return err
		}
		return seq.flush()
	})
	if err != nil {
		return err
	}
	slog.Info("seed erp demo created",
		"warehouses", 2, "products", 3, "purchaseOrders", 1, "purchaseIns", 1,
		"stockChecks", 1, "saleOrders", 1, "saleOuts", 1, "receipts", 1, "payments", 1)
	return nil
}

// erpDemoSkipReason 返回跳过原因（空串表示 ERP 全空、可以写入）。
// 任一代表性表已有行（含软删）即视为库里已有 ERP 数据，整段跳过，避免固定
// ID 与人工数据相撞或混入半套演示链路。
func erpDemoSkipReason(db *gorm.DB) (string, error) {
	checks := []struct {
		label string
		model any
	}{
		{"erp_product_category", &erpmodel.ProductCategory{}},
		{"erp_product_unit", &erpmodel.ProductUnit{}},
		{"erp_product", &erpmodel.Product{}},
		{"erp_warehouse", &erpmodel.Warehouse{}},
		{"erp_customer", &erpmodel.Customer{}},
		{"erp_supplier", &erpmodel.Supplier{}},
		{"erp_finance_account", &erpmodel.FinanceAccount{}},
		{"erp_purchase_order", &erpmodel.PurchaseOrder{}},
		{"erp_purchase_in", &erpmodel.PurchaseIn{}},
		{"erp_sale_order", &erpmodel.SaleOrder{}},
		{"erp_sale_out", &erpmodel.SaleOut{}},
		{"erp_stock_check", &erpmodel.StockCheck{}},
		{"erp_finance_receipt", &erpmodel.FinanceReceipt{}},
		{"erp_finance_payment", &erpmodel.FinancePayment{}},
		{"erp_stock", &erpmodel.Stock{}},
		{"erp_stock_record", &erpmodel.StockRecord{}},
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

// erpDemoAdminUser 取内置管理员作为演示数据 owner。基础种子保证 admin 存在；
// 缺失时报错而不是补建，避免掩盖 seed 顺序错误。
func erpDemoAdminUser(tx *gorm.DB) (erpDemoUser, error) {
	var user sysmodel.User
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND username = ?", erpDemoTenantID, "admin").
		First(&user).Error
	if err != nil {
		return erpDemoUser{}, fmt.Errorf("ERP 演示数据缺少管理员账号 admin（请确认 seedUsers 已执行）: %w", err)
	}
	return erpDemoUser{ID: user.ID, DeptID: user.DeptID}, nil
}

// ---------- 主数据（仓库/分类/单位/产品/客户/供应商/结算账户）----------

// erpDemoSeedMaster 写入完整主数据：两个仓库、两级分类、三个单位、三个产品、
// 两个客户、两个供应商与两个结算账户（一个默认）。owner 均为 admin。
func erpDemoSeedMaster(tx *gorm.DB, admin erpDemoUser, now time.Time) error {
	created := erpDemoDaysAgo(now, 14)
	creator := admin.creator()
	owner := func(id int64) basemodel.BaseEntity {
		return erpDemoBase(id, created, creator)
	}

	categories := []erpmodel.ProductCategory{
		{TenantID: erpDemoTenantID, ParentID: 0, Name: "数码硬件", Code: "hardware", Sort: 0, Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
		{TenantID: erpDemoTenantID, ParentID: 0, Name: "办公耗材", Code: "office", Sort: 1, Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
	}
	categories[0].BaseEntity = owner(erpDemoCategoryHardwareID)
	categories[1].BaseEntity = owner(erpDemoCategoryOfficeID)
	if err := tenant.Skip(tx).Create(&categories).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示产品分类: %w", err)
	}

	units := []erpmodel.ProductUnit{
		{TenantID: erpDemoTenantID, Name: "台", Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
		{TenantID: erpDemoTenantID, Name: "盒", Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
		{TenantID: erpDemoTenantID, Name: "支", Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
	}
	units[0].BaseEntity = owner(erpDemoUnitSetID)
	units[1].BaseEntity = owner(erpDemoUnitBoxID)
	units[2].BaseEntity = owner(erpDemoUnitPieceID)
	if err := tenant.Skip(tx).Create(&units).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示产品单位: %w", err)
	}

	expiry := int32(365)
	terminalWeight := qty.FromMilli(1200) // 1.2 kg，纯展示
	keyboardWeight := qty.FromMilli(800)  // 0.8 kg
	products := []erpmodel.Product{
		{
			TenantID: erpDemoTenantID, Name: "智能办公终端 X1", BarCode: "ERP-DEMO-X1",
			CategoryId: erpDemoCategoryHardwareID, UnitId: erpDemoUnitSetID, Status: erpmodel.StatusEnable,
			Standard: "8 核 / 16G / 512G", ExpiryDay: &expiry, Weight: &terminalWeight,
			PurchasePrice: erpDemoMoney(120000), SalePrice: erpDemoMoney(180000), MinPrice: erpDemoMoney(160000),
			Remark:      "演示产品：采购/销售链路的主商品",
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
		{
			TenantID: erpDemoTenantID, Name: "无线键鼠套装", BarCode: "ERP-DEMO-KB",
			CategoryId: erpDemoCategoryOfficeID, UnitId: erpDemoUnitBoxID, Status: erpmodel.StatusEnable,
			Standard: "2.4G 无线 / 静音", Weight: &keyboardWeight,
			PurchasePrice: erpDemoMoney(15000), SalePrice: erpDemoMoney(24900), MinPrice: erpDemoMoney(19900),
			Remark:      "演示产品：盘点盘亏对象",
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
		{
			TenantID: erpDemoTenantID, Name: "打印耗材套装", BarCode: "ERP-DEMO-PAPER",
			CategoryId: erpDemoCategoryOfficeID, UnitId: erpDemoUnitPieceID, Status: erpmodel.StatusEnable,
			Standard:      "A4 纸 500 张 + 墨盒",
			PurchasePrice: erpDemoMoney(8000), SalePrice: erpDemoMoney(12900), MinPrice: erpDemoMoney(9900),
			Remark:      "演示产品：仅主数据，未参与单据",
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
	}
	products[0].BaseEntity = owner(erpDemoProductTerminalID)
	products[1].BaseEntity = owner(erpDemoProductKeyboardID)
	products[2].BaseEntity = owner(erpDemoProductPaperID)
	if err := tenant.Skip(tx).Create(&products).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示产品: %w", err)
	}

	warehouses := []erpmodel.Warehouse{
		{TenantID: erpDemoTenantID, Name: "上海主仓", Address: "上海市浦东新区演示路 100 号", Principal: "王仓管", Sort: 0, Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
		{TenantID: erpDemoTenantID, Name: "苏州备件仓", Address: "苏州市工业园区演示大道 8 号", Principal: "李备件", Sort: 1, Status: erpmodel.StatusEnable, OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID},
	}
	warehouses[0].BaseEntity = owner(erpDemoWarehouseMainID)
	warehouses[1].BaseEntity = owner(erpDemoWarehouseSpareID)
	if err := tenant.Skip(tx).Create(&warehouses).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示仓库: %w", err)
	}

	customerTaxNo := "91310000MA1FL0DEMO"
	customerTaxPercent := money.FromCents(600) // 6.00%，纯展示
	customerBank := "中国工商银行上海分行"
	customerAccount := "6222 0000 0000 0001"
	customerBankAddress := "上海市浦东新区"
	customers := []erpmodel.Customer{
		{
			TenantID: erpDemoTenantID, Name: "未来科技（演示客户）", Contact: "陈经理",
			Mobile: "13900000001", Telephone: "021-60000001", Email: "chen@future.example.com",
			Status: erpmodel.StatusEnable, Sort: 0, Remark: "演示客户：销售链路对象",
			TaxNo: &customerTaxNo, TaxPercent: &customerTaxPercent,
			BankName: &customerBank, BankAccount: &customerAccount, BankAddress: &customerBankAddress,
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
		{
			TenantID: erpDemoTenantID, Name: "蓝海商贸（演示客户）", Contact: "周采购",
			Mobile: "13900000002", Telephone: "021-60000002", Email: "zhou@bluesea.example.com",
			Status: erpmodel.StatusEnable, Sort: 1, Remark: "演示客户：仅主数据",
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
	}
	customers[0].BaseEntity = owner(erpDemoCustomerFutureID)
	customers[1].BaseEntity = owner(erpDemoCustomerBlueSeaID)
	if err := tenant.Skip(tx).Create(&customers).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示客户: %w", err)
	}

	supplierTaxNo := "91310000MA1FL0SUPP"
	supplierTaxPercent := money.FromCents(1300) // 13.00%，纯展示
	supplierBank := "中国建设银行深圳分行"
	supplierAccount := "6217 0000 0000 0002"
	supplierBankAddress := "深圳市南山区"
	suppliers := []erpmodel.Supplier{
		{
			TenantID: erpDemoTenantID, Name: "华强电子（演示供应商）", Contact: "吴经理",
			Mobile: "13800000001", Telephone: "0755-80000001", Email: "wu@hq.example.com",
			Status: erpmodel.StatusEnable, Sort: 0, Remark: "演示供应商：采购链路对象",
			TaxNo: &supplierTaxNo, TaxPercent: &supplierTaxPercent,
			BankName: &supplierBank, BankAccount: &supplierAccount, BankAddress: &supplierBankAddress,
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
		{
			TenantID: erpDemoTenantID, Name: "顺达办公（演示供应商）", Contact: "郑经理",
			Mobile: "13800000002", Telephone: "0755-80000002", Email: "zheng@sd.example.com",
			Status: erpmodel.StatusEnable, Sort: 1, Remark: "演示供应商：仅主数据",
			OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
		},
	}
	suppliers[0].BaseEntity = owner(erpDemoSupplierHQID)
	suppliers[1].BaseEntity = owner(erpDemoSupplierSDID)
	if err := tenant.Skip(tx).Create(&suppliers).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示供应商: %w", err)
	}

	accounts := []erpmodel.FinanceAccount{
		{
			TenantID: erpDemoTenantID, Name: "工商银行基本户", No: "6222000000000001",
			Remark: "演示默认结算账户", Sort: 0, Status: erpmodel.StatusEnable,
			DefaultStatus: erpmodel.FinanceDefaultYes,
			OwnerUserID:   admin.ID, OwnerDeptID: admin.DeptID,
		},
		{
			TenantID: erpDemoTenantID, Name: "支付宝备付金", No: "alipay-demo-001",
			Remark: "演示非默认结算账户", Sort: 1, Status: erpmodel.StatusEnable,
			DefaultStatus: erpmodel.FinanceDefaultNo,
			OwnerUserID:   admin.ID, OwnerDeptID: admin.DeptID,
		},
	}
	accounts[0].BaseEntity = owner(erpDemoAccountICBCID)
	accounts[1].BaseEntity = owner(erpDemoAccountAlipayID)
	if err := tenant.Skip(tx).Create(&accounts).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示结算账户: %w", err)
	}
	return nil
}

// ---------- 采购：订单（CG）→ 入库（RK）----------

// erpDemoSeedPurchase 写入一张已审核采购订单与挂单的已审核采购入库单：
// 订单明细 in_count 回写为已审核入库数量（spec §4.4）；入库主表金额 =
// 明细合计、payment_price 由付款单核销回写（本文件随后写付款单）。
// 入库审核的库存效果写入 stock_record（正流水），余额行在链路末尾统一落库。
func erpDemoSeedPurchase(tx *gorm.DB, admin erpDemoUser, seq *erpDemoSequencer, dateKey string, orderTime, inTime time.Time) error {
	lines := []erpDemoLine{
		{ProductID: erpDemoProductTerminalID, Count: qty.FromMilli(10000), Price: erpDemoMoney(120000)},
		{ProductID: erpDemoProductKeyboardID, Count: qty.FromMilli(20000), Price: erpDemoMoney(15000)},
	}
	totals, orderTotal, err := erpDemoLineTotals(lines)
	if err != nil {
		return err
	}
	creator := admin.creator()
	orderNo, err := seq.nextNo(erpservice.SeqBizTypePurchaseOrder, erpservice.PurchaseOrderNoPrefix, dateKey)
	if err != nil {
		return err
	}

	order := erpmodel.PurchaseOrder{
		BaseEntity: erpDemoBase(erpDemoPurchaseOrderID, orderTime, creator),
		TenantID:   erpDemoTenantID, No: orderNo,
		SupplierID: erpDemoSupplierHQID, OrderTime: orderTime,
		Status: erpmodel.DocStatusApproved, Remark: "演示采购订单：已审核，已被采购入库占用",
		OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&order).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示采购订单: %w", err)
	}
	orderItems := make([]erpmodel.PurchaseOrderItem, 0, len(lines))
	for i, line := range lines {
		row := erpmodel.PurchaseOrderItem{
			BaseEntity: erpDemoBase(int64(4101+i), orderTime, creator),
			TenantID:   erpDemoTenantID, PurchaseOrderID: order.ID,
			ProductID: line.ProductID, Count: line.Count,
			ProductPrice: line.Price, TotalPrice: totals[i],
			// 进度回写只计已审核入库；本演示入库单已审核，故等于订单数量。
			InCount: line.Count, ReturnCount: qty.FromMilli(0),
		}
		orderItems = append(orderItems, row)
	}
	if err := tenant.Skip(tx).Create(&orderItems).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示采购订单明细: %w", err)
	}

	inNo, err := seq.nextNo(erpservice.SeqBizTypePurchaseIn, erpservice.PurchaseInNoPrefix, dateKey)
	if err != nil {
		return err
	}
	purchaseIn := erpmodel.PurchaseIn{
		BaseEntity: erpDemoBase(erpDemoPurchaseInID, inTime, creator),
		TenantID:   erpDemoTenantID, No: inNo,
		SupplierID:  erpDemoSupplierHQID,
		OrderID:     order.ID,
		WarehouseID: erpDemoWarehouseMainID,
		InTime:      inTime,
		Status:      erpmodel.DocStatusApproved,
		TotalPrice:  orderTotal,
		// 付款单（FK）审核后回写；本演示付款金额等于入库金额（全额核销）。
		PaymentPrice: orderTotal,
		Remark:       "演示采购入库：审核后库存增加",
		OwnerUserID:  admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&purchaseIn).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示采购入库单: %w", err)
	}
	inItems := make([]erpmodel.PurchaseInItem, 0, len(lines))
	for i, line := range lines {
		inItems = append(inItems, erpmodel.PurchaseInItem{
			BaseEntity: erpDemoBase(int64(4103+i), inTime, creator),
			TenantID:   erpDemoTenantID, PurchaseInID: purchaseIn.ID,
			ProductID: line.ProductID, Count: line.Count,
			ProductPrice: line.Price, TotalPrice: totals[i],
		})
	}
	if err := tenant.Skip(tx).Create(&inItems).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示采购入库明细: %w", err)
	}

	// 审核入账流水（唯一入口同口径：正入 + 变动后结存快照，spec §4.2/§4.3）。
	// 初始余额为 0，故第 1/2 笔结存即 10.000 / 20.000。
	records := []erpmodel.StockRecord{
		{
			BaseEntity: erpDemoBase(4301, inTime, creator),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductTerminalID, WarehouseID: erpDemoWarehouseMainID,
			Count: qty.FromMilli(10000), TotalCount: qty.FromMilli(10000),
			BizType: erpmodel.StockRecordBizPurchaseIn, BizID: purchaseIn.ID, BizItemID: inItems[0].ID, BizNo: inNo,
		},
		{
			BaseEntity: erpDemoBase(4302, inTime, creator),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductKeyboardID, WarehouseID: erpDemoWarehouseMainID,
			Count: qty.FromMilli(20000), TotalCount: qty.FromMilli(20000),
			BizType: erpmodel.StockRecordBizPurchaseIn, BizID: purchaseIn.ID, BizItemID: inItems[1].ID, BizNo: inNo,
		},
	}
	if err := tenant.Skip(tx).Create(&records).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示采购入库流水: %w", err)
	}
	return nil
}

// ---------- 盘点（PD）----------

// erpDemoSeedStockCheck 写入一张已审核盘点单：终端盘盈 +1、键鼠盘亏 −1，
// 一次演示盘盈与盘亏两个方向（spec §4.6：差异 = actual_count − 当时账面数）。
// base_count 是创建时账面快照（10/20），审核以当时账面为准；本演示单在
// 采购入库之后、销售出库之前审核，故当时账面即 10/20。
func erpDemoSeedStockCheck(tx *gorm.DB, admin erpDemoUser, seq *erpDemoSequencer, dateKey string, checkTime time.Time) error {
	creator := admin.creator()
	checkNo, err := seq.nextNo(erpservice.SeqBizTypeStockCheck, erpservice.StockCheckNoPrefix, dateKey)
	if err != nil {
		return err
	}
	doc := erpmodel.StockCheck{
		BaseEntity: erpDemoBase(erpDemoStockCheckID, checkTime, creator),
		TenantID:   erpDemoTenantID, No: checkNo,
		WarehouseID: erpDemoWarehouseMainID, CheckTime: checkTime,
		Status: erpmodel.DocStatusApproved, Remark: "演示盘点：终端盘盈 1 台、键鼠盘亏 1 盒",
		OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&doc).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示盘点单: %w", err)
	}
	items := []erpmodel.StockCheckItem{
		{
			BaseEntity: erpDemoBase(4105, checkTime, creator),
			TenantID:   erpDemoTenantID, StockCheckID: doc.ID, ProductID: erpDemoProductTerminalID,
			ActualCount: qty.FromMilli(11000), BaseCount: qty.FromMilli(10000),
		},
		{
			BaseEntity: erpDemoBase(4106, checkTime, creator),
			TenantID:   erpDemoTenantID, StockCheckID: doc.ID, ProductID: erpDemoProductKeyboardID,
			ActualCount: qty.FromMilli(19000), BaseCount: qty.FromMilli(20000),
		},
	}
	if err := tenant.Skip(tx).Create(&items).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示盘点明细: %w", err)
	}
	// 差异流水：终端 +1（结存 11）、键鼠 −1（结存 19）；差异为零不产生流水。
	records := []erpmodel.StockRecord{
		{
			BaseEntity: erpDemoBase(4303, checkTime, creator),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductTerminalID, WarehouseID: erpDemoWarehouseMainID,
			Count: qty.FromMilli(1000), TotalCount: qty.FromMilli(11000),
			BizType: erpmodel.StockRecordBizStockCheck, BizID: doc.ID, BizItemID: items[0].ID, BizNo: checkNo,
		},
		{
			BaseEntity: erpDemoBase(4304, checkTime, creator),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductKeyboardID, WarehouseID: erpDemoWarehouseMainID,
			Count: qty.FromMilli(-1000), TotalCount: qty.FromMilli(19000),
			BizType: erpmodel.StockRecordBizStockCheck, BizID: doc.ID, BizItemID: items[1].ID, BizNo: checkNo,
		},
	}
	if err := tenant.Skip(tx).Create(&records).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示盘点流水: %w", err)
	}
	return nil
}

// ---------- 销售：订单（XS）→ 出库（CK）----------

// erpDemoSeedSale 写入一张已审核销售订单与挂单的已审核销售出库单：
// 订单明细 out_count 回写为已审核出库数量（spec §4.5）；出库主表金额 =
// 明细合计、receipt_price 由收款单核销回写。出库审核的库存效果写入
// stock_record（负流水），余额行在链路末尾统一落库。
func erpDemoSeedSale(tx *gorm.DB, admin erpDemoUser, seq *erpDemoSequencer, dateKey string, orderTime, outTime time.Time) error {
	lines := []erpDemoLine{
		{ProductID: erpDemoProductTerminalID, Count: qty.FromMilli(5000), Price: erpDemoMoney(180000)},
		{ProductID: erpDemoProductKeyboardID, Count: qty.FromMilli(8000), Price: erpDemoMoney(24900)},
	}
	totals, orderTotal, err := erpDemoLineTotals(lines)
	if err != nil {
		return err
	}
	creator := admin.creator()
	orderNo, err := seq.nextNo(erpservice.SeqBizTypeSaleOrder, erpservice.SaleOrderNoPrefix, dateKey)
	if err != nil {
		return err
	}
	order := erpmodel.SaleOrder{
		BaseEntity: erpDemoBase(erpDemoSaleOrderID, orderTime, creator),
		TenantID:   erpDemoTenantID, OrderNo: orderNo,
		CustomerID: erpDemoCustomerFutureID, SaleUserID: admin.ID, OrderTime: orderTime,
		Status: erpmodel.DocStatusApproved, Remark: "演示销售订单：已审核，已被销售出库占用",
		OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&order).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示销售订单: %w", err)
	}
	orderItems := make([]erpmodel.SaleOrderItem, 0, len(lines))
	for i, line := range lines {
		orderItems = append(orderItems, erpmodel.SaleOrderItem{
			BaseEntity: erpDemoBase(int64(4107+i), orderTime, creator),
			TenantID:   erpDemoTenantID, SaleOrderID: order.ID,
			ProductID: line.ProductID, Count: line.Count,
			ProductPrice: line.Price, TotalPrice: totals[i],
			OutCount: line.Count, ReturnCount: qty.FromMilli(0),
		})
	}
	if err := tenant.Skip(tx).Create(&orderItems).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示销售订单明细: %w", err)
	}

	outNo, err := seq.nextNo(erpservice.SeqBizTypeSaleOut, erpservice.SaleOutNoPrefix, dateKey)
	if err != nil {
		return err
	}
	saleOut := erpmodel.SaleOut{
		BaseEntity: erpDemoBase(erpDemoSaleOutID, outTime, creator),
		TenantID:   erpDemoTenantID, No: outNo,
		CustomerID: erpDemoCustomerFutureID, OrderID: order.ID,
		WarehouseID: erpDemoWarehouseMainID, OutTime: outTime,
		Status:     erpmodel.DocStatusApproved,
		TotalPrice: orderTotal,
		// 收款单（SK）审核后回写；本演示收款金额等于出库金额（全额核销）。
		ReceiptPrice: orderTotal,
		OwnerUserID:  admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&saleOut).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示销售出库单: %w", err)
	}
	outItems := make([]erpmodel.SaleOutItem, 0, len(lines))
	for i, line := range lines {
		outItems = append(outItems, erpmodel.SaleOutItem{
			BaseEntity: erpDemoBase(int64(4109+i), outTime, creator),
			TenantID:   erpDemoTenantID, SaleOutID: saleOut.ID,
			ProductID: line.ProductID, Count: line.Count,
			ProductPrice: line.Price, TotalPrice: totals[i],
		})
	}
	if err := tenant.Skip(tx).Create(&outItems).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示销售出库明细: %w", err)
	}

	// 审核出账流水（负流水）：终端 11 − 5 = 6、键鼠 19 − 8 = 11，均非负。
	records := []erpmodel.StockRecord{
		{
			BaseEntity: erpDemoBase(4305, outTime, creator),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductTerminalID, WarehouseID: erpDemoWarehouseMainID,
			Count: qty.FromMilli(-5000), TotalCount: qty.FromMilli(6000),
			BizType: erpmodel.StockRecordBizSaleOut, BizID: saleOut.ID, BizItemID: outItems[0].ID, BizNo: outNo,
		},
		{
			BaseEntity: erpDemoBase(4306, outTime, creator),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductKeyboardID, WarehouseID: erpDemoWarehouseMainID,
			Count: qty.FromMilli(-8000), TotalCount: qty.FromMilli(11000),
			BizType: erpmodel.StockRecordBizSaleOut, BizID: saleOut.ID, BizItemID: outItems[1].ID, BizNo: outNo,
		},
	}
	if err := tenant.Skip(tx).Create(&records).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示销售出库流水: %w", err)
	}
	return nil
}

// ---------- 财务：收款单（SK）→ 付款单（FK）----------

// erpDemoSeedFinance 写入已审核收款单（挂销售出库）与已审核付款单（挂采购
// 入库）：明细金额不超过单据 total_price（本演示为全额核销），往来单位与
// 被核销单据一致（spec §4.7）。
func erpDemoSeedFinance(tx *gorm.DB, admin erpDemoUser, seq *erpDemoSequencer, dateKey string, receiptTime, paymentTime time.Time) error {
	creator := admin.creator()
	receiptNo, err := seq.nextNo(erpservice.SeqBizTypeFinanceReceipt, erpservice.FinanceReceiptNoPrefix, dateKey)
	if err != nil {
		return err
	}
	receipt := erpmodel.FinanceReceipt{
		BaseEntity: erpDemoBase(erpDemoReceiptID, receiptTime, creator),
		TenantID:   erpDemoTenantID, No: receiptNo,
		CustomerID: erpDemoCustomerFutureID, AccountID: erpDemoAccountICBCID,
		ReceiptUserID: admin.ID, ReceiptTime: receiptTime,
		Status: erpmodel.DocStatusApproved, Remark: "演示收款：全额核销销售出库",
		OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&receipt).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示收款单: %w", err)
	}
	receiptItem := erpmodel.FinanceReceiptItem{
		BaseEntity: erpDemoBase(4111, receiptTime, creator),
		TenantID:   erpDemoTenantID, ReceiptID: receipt.ID,
		BizType: erpmodel.FinanceBizSaleOut, BizID: erpDemoSaleOutID,
		Price: erpDemoMoney(1099200), // 销售出库合计 10992.00，全额核销
	}
	if err := tenant.Skip(tx).Create(&receiptItem).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示收款明细: %w", err)
	}

	paymentNo, err := seq.nextNo(erpservice.SeqBizTypeFinancePayment, erpservice.FinancePaymentNoPrefix, dateKey)
	if err != nil {
		return err
	}
	payment := erpmodel.FinancePayment{
		BaseEntity: erpDemoBase(erpDemoPaymentID, paymentTime, creator),
		TenantID:   erpDemoTenantID, No: paymentNo,
		SupplierID: erpDemoSupplierHQID, AccountID: erpDemoAccountICBCID,
		PaymentUserID: admin.ID, PaymentTime: paymentTime,
		Status: erpmodel.DocStatusApproved, Remark: "演示付款：全额核销采购入库",
		OwnerUserID: admin.ID, OwnerDeptID: admin.DeptID,
	}
	if err := tenant.Skip(tx).Create(&payment).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示付款单: %w", err)
	}
	paymentItem := erpmodel.FinancePaymentItem{
		BaseEntity: erpDemoBase(4112, paymentTime, creator),
		TenantID:   erpDemoTenantID, PaymentID: payment.ID,
		BizType: erpmodel.FinanceBizPurchaseIn, BizID: erpDemoPurchaseInID,
		Price: erpDemoMoney(1500000), // 采购入库合计 15000.00，全额核销
	}
	if err := tenant.Skip(tx).Create(&paymentItem).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示付款明细: %w", err)
	}
	return nil
}

// ---------- 库存余额 ----------

// erpDemoSeedStocks 落最终库存余额（仓库×产品唯一）：
//
//	终端：入库 10 + 盘盈 1 − 出库 5 = 6
//	键鼠：入库 20 − 盘亏 1 − 出库 8 = 11
//
// 与 stock_record 代数和一致（spec §4.3：余额 = 初始 + 流水代数和），任何
// 时点均无负数。链路上每笔余额变动都经唯一入账入口的同口径流水记录。
func erpDemoSeedStocks(tx *gorm.DB, firstTime, lastTime time.Time) error {
	rows := []erpmodel.Stock{
		{
			BaseEntity: erpDemoBase(4201, firstTime, "0"),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductTerminalID,
			WarehouseID: erpDemoWarehouseMainID, Count: qty.FromMilli(6000),
		},
		{
			BaseEntity: erpDemoBase(4202, firstTime, "0"),
			TenantID:   erpDemoTenantID, ProductID: erpDemoProductKeyboardID,
			WarehouseID: erpDemoWarehouseMainID, Count: qty.FromMilli(11000),
		},
	}
	// 余额行的更新时间跟随最后一笔库存变动（销售出库）。
	for i := range rows {
		rows[i].UpdatedAt = lastTime
		rows[i].Updater = "0"
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入 ERP 演示库存余额: %w", err)
	}
	return nil
}

// ---------- 单号序列 ----------

// erpDemoSequencer 在事务内为演示单号分配序号，并在收尾时以 GREATEST 合并
// 写回 erp_sequence：已有序列更大时不回退，后续 UI 新建不会与演示单号冲突。
// 与 crm_demo.go 的 demoSequencer 同构（ERP 不 import crm，spec §2）。
type erpDemoSequencer struct {
	tx       *gorm.DB
	tenantID int64
	last     map[string]int64
}

func newERPDemoSequencer(tx *gorm.DB, tenantID int64) *erpDemoSequencer {
	return &erpDemoSequencer{tx: tx, tenantID: tenantID, last: make(map[string]int64)}
}

// nextNo 发放并格式化一个单号：前缀 + yyyyMMdd + 至少 4 位序号（spec §4.8）。
func (s *erpDemoSequencer) nextNo(bizType, prefix, dateKey string) (string, error) {
	seq, err := s.next(bizType, dateKey)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s%04d", prefix, dateKey, seq), nil
}

func (s *erpDemoSequencer) next(bizType, dateKey string) (int64, error) {
	key := bizType + "|" + dateKey
	if _, ok := s.last[key]; !ok {
		var current int64
		if err := tenant.Skip(s.tx).Raw(
			"SELECT COALESCE(MAX(`last_value`), 0) FROM `erp_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?",
			s.tenantID, bizType, dateKey,
		).Scan(&current).Error; err != nil {
			return 0, fmt.Errorf("读取演示单号序列 %s/%s: %w", bizType, dateKey, err)
		}
		s.last[key] = current
	}
	s.last[key]++
	return s.last[key], nil
}

func (s *erpDemoSequencer) flush() error {
	for key, value := range s.last {
		bizType, dateKey, ok := erpDemoSplitSequenceKey(key)
		if !ok {
			return fmt.Errorf("非法的演示序列键 %q", key)
		}
		if err := tenant.Skip(s.tx).Exec(
			"INSERT INTO `erp_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, ?) "+
				"ON DUPLICATE KEY UPDATE `last_value` = GREATEST(`last_value`, ?)",
			s.tenantID, bizType, dateKey, value, value,
		).Error; err != nil {
			return fmt.Errorf("写回演示单号序列 %s: %w", key, err)
		}
	}
	return nil
}

// erpDemoSplitSequenceKey 拆分 "bizType|dateKey"（bizType 不含竖线，dateKey
// 固定 8 位；只切第一个竖线）。
func erpDemoSplitSequenceKey(key string) (bizType, dateKey string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// ---------- 小工具 ----------

// erpDemoLine 是演示单据的一行：产品 / 数量 / 单价。行金额与单据合计统一
// 由 Money.MulQty/Add 计算，保证「明细金额 = 单价 × 数量、主表金额 = 明细合计」
// 两条不变量（spec §4.2）与 service 路径完全一致。
type erpDemoLine struct {
	ProductID int64
	Count     qty.Qty
	Price     money.Money
}

// erpDemoLineTotals 返回逐行金额与合计；任一金额越界时报错（与 service 同口径）。
func erpDemoLineTotals(lines []erpDemoLine) ([]money.Money, money.Money, error) {
	totals := make([]money.Money, len(lines))
	sum := money.FromCents(0)
	for i, line := range lines {
		total, err := line.Price.MulQty(line.Count)
		if err != nil {
			return nil, money.Money{}, fmt.Errorf("演示明细第 %d 行金额: %w", i+1, err)
		}
		totals[i] = total
		if sum, err = sum.Add(total); err != nil {
			return nil, money.Money{}, fmt.Errorf("演示单据金额合计: %w", err)
		}
	}
	return totals, sum, nil
}

// erpDemoBase 构造统一实体底座（固定 ID；创建/更新时间由调用方指定）。
func erpDemoBase(id int64, createdAt time.Time, creator string) basemodel.BaseEntity {
	if creator == "" {
		creator = "0"
	}
	return basemodel.BaseEntity{
		ID:        id,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Creator:   creator,
		Updater:   creator,
	}
}

func erpDemoMoney(cents int64) money.Money { return money.FromCents(cents) }

// erpDemoDaysAgo 返回 n 天前当天 10:00（主数据创建时间，与其它演示种子同口径）。
func erpDemoDaysAgo(now time.Time, days int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location()).
		AddDate(0, 0, -days)
}
