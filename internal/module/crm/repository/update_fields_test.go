package repository_test

// 字段级更新测试（PR #39 审查 P1）：更新接口只能写入请求字段，不得回写
// status/stage/owner_*/customer_id/contract_id 等非请求列。
// 用 sqlite 建真实表 + 真实 UPDATE 语句验证：先模拟并发方改动这些列，
// 再调用 repository 的字段级更新方法，断言并发改动没有被覆盖。
//
// 这是行为断言而非 SQL 字符串断言：GORM 生成什么 SQL 不重要，
// 重要的是「没被请求的列在并发修改后仍然保持并发写入的值」。

import (
	"context"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"github.com/lautung/go-admin-pro/internal/pkg/money"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newCRMTestDB 建立装好租户插件的独立 sqlite 库（单连接，保证内存库共享）。
func newCRMTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取 sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}
	return db
}

// tenantCtx 返回带租户 1 的上下文（租户插件 fail-closed 要求显式上下文）。
func tenantCtx() context.Context {
	return tenant.WithTenantID(context.Background(), 1)
}

// createRow 写入一行基线数据（绕过字段级更新路径）。
func createRow(t *testing.T, db *gorm.DB, row any) {
	t.Helper()
	if err := tenant.Skip(db.WithContext(tenantCtx())).Create(row).Error; err != nil {
		t.Fatalf("写入基线行: %v", err)
	}
}

// concurrentUpdate 模拟并发方直接改库（租户插件不参与系统级夹具写入）。
func concurrentUpdate(t *testing.T, db *gorm.DB, row any, id int64, fields map[string]any) {
	t.Helper()
	if err := tenant.Skip(db.Model(row)).Where("id = ?", id).Updates(fields).Error; err != nil {
		t.Fatalf("模拟并发更新: %v", err)
	}
}

func mustMoney(t *testing.T, text string) money.Money {
	t.Helper()
	m, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", text, err)
	}
	return m
}

// TestUpdateContractFieldsSkipsStatusAndOwner 合同更新只写请求字段：
// 并发方已把状态改为已签、负责人改为他人，编辑接口不得把 status/owner_* 回写。
func TestUpdateContractFieldsSkipsStatusAndOwner(t *testing.T) {
	db := newCRMTestDB(t, &model.Contract{})
	repo := repository.New(db)

	c := &model.Contract{
		TenantID: 1, ContractNo: "HT202611050001", CustomerID: 7, Name: "原合同",
		Amount: mustMoney(t, "100.00"), Status: model.ContractStatusDraft,
		OwnerUserID: 1, OwnerDeptID: 100, Remark: "原备注",
	}
	createRow(t, db, c)
	concurrentUpdate(t, db, &model.Contract{}, c.ID, map[string]any{
		"status": model.ContractStatusSigned, "owner_user_id": 9, "owner_dept_id": 99,
	})

	signDate := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rows, err := repo.UpdateContractFields(tenantCtx(), c.ID, map[string]any{
		"name": "新合同", "amount": mustMoney(t, "200.00"), "sign_date": signDate,
		"start_date": signDate, "end_date": signDate, "remark": "新备注",
	})
	if err != nil || rows != 1 {
		t.Fatalf("字段级更新合同: rows=%d err=%v", rows, err)
	}

	var got struct {
		Name   string
		Amount string
		Remark string
		Status int8
		Owner  int64 `gorm:"column:owner_user_id"`
		Dept   int64 `gorm:"column:owner_dept_id"`
	}
	if err := tenant.Skip(db.Model(&model.Contract{})).
		Select("name, CAST(amount AS TEXT) AS amount, remark, status, owner_user_id, owner_dept_id").
		Where("id = ?", c.ID).Scan(&got).Error; err != nil {
		t.Fatalf("回读合同: %v", err)
	}
	if got.Name != "新合同" || mustMoney(t, got.Amount).Cents() != mustMoney(t, "200.00").Cents() || got.Remark != "新备注" {
		t.Fatalf("请求字段未更新：name=%q amount=%q remark=%q", got.Name, got.Amount, got.Remark)
	}
	if got.Status != model.ContractStatusSigned {
		t.Fatalf("status 被回写覆盖：期望 3（并发写入的已签），实际 %d", got.Status)
	}
	if got.Owner != 9 || got.Dept != 99 {
		t.Fatalf("owner 被回写覆盖：owner=%d dept=%d，期望 9/99", got.Owner, got.Dept)
	}
}

// TestUpdateOpportunityFieldsSkipsStageAndOwner 商机更新只写请求字段：
// 并发推进到赢单、负责人改为他人后，编辑不得回写 stage/owner_*（也不得改 customer_id）。
func TestUpdateOpportunityFieldsSkipsStageAndOwner(t *testing.T) {
	db := newCRMTestDB(t, &model.Opportunity{})
	repo := repository.New(db)

	o := &model.Opportunity{
		TenantID: 1, OpportunityNo: "SJ202611050001", CustomerID: 7, Name: "原商机",
		Stage: model.OpportunityStageInitial, OwnerUserID: 1, OwnerDeptID: 100,
	}
	createRow(t, db, o)
	concurrentUpdate(t, db, &model.Opportunity{}, o.ID, map[string]any{
		"stage": model.OpportunityStageWon, "owner_user_id": 9, "owner_dept_id": 99,
	})

	closeDate := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	rows, err := repo.UpdateOpportunityFields(tenantCtx(), o.ID, map[string]any{
		"name": "新商机", "expected_amount": mustMoney(t, "300.00"),
		"expected_close_date": closeDate, "remark": "新备注",
	})
	if err != nil || rows != 1 {
		t.Fatalf("字段级更新商机: rows=%d err=%v", rows, err)
	}

	var got struct {
		Name       string
		Amount     string `gorm:"column:expected_amount"`
		Stage      int8
		Owner      int64 `gorm:"column:owner_user_id"`
		Dept       int64 `gorm:"column:owner_dept_id"`
		CustomerID int64 `gorm:"column:customer_id"`
	}
	if err := tenant.Skip(db.Model(&model.Opportunity{})).
		Select("name, CAST(expected_amount AS TEXT) AS expected_amount, stage, owner_user_id, owner_dept_id, customer_id").
		Where("id = ?", o.ID).Scan(&got).Error; err != nil {
		t.Fatalf("回读商机: %v", err)
	}
	if got.Name != "新商机" || mustMoney(t, got.Amount).Cents() != mustMoney(t, "300.00").Cents() {
		t.Fatalf("请求字段未更新：name=%q amount=%q", got.Name, got.Amount)
	}
	if got.Stage != model.OpportunityStageWon {
		t.Fatalf("stage 被回写覆盖：期望 5（并发推进的赢单），实际 %d", got.Stage)
	}
	if got.Owner != 9 || got.Dept != 99 {
		t.Fatalf("owner 被回写覆盖：owner=%d dept=%d，期望 9/99", got.Owner, got.Dept)
	}
	if got.CustomerID != 7 {
		t.Fatalf("customer_id 不应被更新：期望 7，实际 %d", got.CustomerID)
	}
}

// TestUpdateCustomerFieldsSkipsStatusAndOwner 客户更新分两支：
// status 不由请求携带时不写（并发状态保留）；携带时只写 status，
// owner 永远不经过字段级更新（转移走 TransferCustomerOwner）。
func TestUpdateCustomerFieldsSkipsStatusAndOwner(t *testing.T) {
	db := newCRMTestDB(t, &model.Customer{})
	repo := repository.New(db)

	c := &model.Customer{
		TenantID: 1, CustomerNo: "KH202611050001", Name: "原客户",
		Status: model.CustomerStatusPotential, OwnerUserID: 1, OwnerDeptID: 100,
	}
	createRow(t, db, c)
	concurrentUpdate(t, db, &model.Customer{}, c.ID, map[string]any{
		"status": model.CustomerStatusLost, "owner_user_id": 9, "owner_dept_id": 99,
	})

	fields := map[string]any{
		"name": "新客户", "industry": "制造业", "level": "A", "source": "展会",
		"phone": "13800000000", "website": "https://example.com", "address": "上海",
		"remark": "新备注",
	}
	if rows, err := repo.UpdateCustomerFields(tenantCtx(), c.ID, fields); err != nil || rows != 1 {
		t.Fatalf("字段级更新客户: rows=%d err=%v", rows, err)
	}

	var got model.Customer
	if err := tenant.Skip(db).First(&got, c.ID).Error; err != nil {
		t.Fatalf("回读客户: %v", err)
	}
	if got.Name != "新客户" || got.Industry != "制造业" || got.Phone != "13800000000" {
		t.Fatalf("请求字段未更新：name=%q industry=%q phone=%q", got.Name, got.Industry, got.Phone)
	}
	if got.Status != model.CustomerStatusLost {
		t.Fatalf("status 未携带时被回写：期望 3（并发写入的流失），实际 %d", got.Status)
	}
	if got.OwnerUserID != 9 || got.OwnerDeptID != 99 {
		t.Fatalf("owner 被回写覆盖：owner=%d dept=%d，期望 9/99", got.OwnerUserID, got.OwnerDeptID)
	}

	// 显式携带 status：只写 status 与请求字段，owner 仍不动。
	fields["status"] = model.CustomerStatusActive
	if rows, err := repo.UpdateCustomerFields(tenantCtx(), c.ID, fields); err != nil || rows != 1 {
		t.Fatalf("字段级更新客户（带状态）: rows=%d err=%v", rows, err)
	}
	if err := tenant.Skip(db).First(&got, c.ID).Error; err != nil {
		t.Fatalf("回读客户: %v", err)
	}
	if got.Status != model.CustomerStatusActive {
		t.Fatalf("携带 status 时应更新为 2，实际 %d", got.Status)
	}
	if got.OwnerUserID != 9 {
		t.Fatalf("owner 仍不应被字段级更新改动：期望 9，实际 %d", got.OwnerUserID)
	}
}

// TestUpdateProductFieldsSkipsStatusUnlessRequested 产品更新：status 未携带时
// 保留并发写入的值，携带时只写 status。
func TestUpdateProductFieldsSkipsStatusUnlessRequested(t *testing.T) {
	db := newCRMTestDB(t, &model.Product{})
	repo := repository.New(db)

	p := &model.Product{
		TenantID: 1, CategoryId: 1, Name: "原产品", Code: "P001",
		Unit: "个", Price: mustMoney(t, "10.00"), Status: model.ProductStatusOnSale,
	}
	createRow(t, db, p)
	concurrentUpdate(t, db, &model.Product{}, p.ID, map[string]any{"status": model.ProductStatusOffShelf})

	fields := map[string]any{
		"category_id": int64(2), "name": "新产品", "code": "P002",
		"unit": "箱", "price": mustMoney(t, "20.00"), "description": "新描述",
	}
	if rows, err := repo.UpdateProductFields(tenantCtx(), p.ID, fields); err != nil || rows != 1 {
		t.Fatalf("字段级更新产品: rows=%d err=%v", rows, err)
	}

	var got struct {
		Name   string
		Code   string
		Price  string
		Status int8
	}
	if err := tenant.Skip(db.Model(&model.Product{})).
		Select("name, code, CAST(price AS TEXT) AS price, status").
		Where("id = ?", p.ID).Scan(&got).Error; err != nil {
		t.Fatalf("回读产品: %v", err)
	}
	if got.Name != "新产品" || got.Code != "P002" || mustMoney(t, got.Price).Cents() != mustMoney(t, "20.00").Cents() {
		t.Fatalf("请求字段未更新：name=%q code=%q price=%q", got.Name, got.Code, got.Price)
	}
	if got.Status != model.ProductStatusOffShelf {
		t.Fatalf("未携带 status 时被回写：期望 0（并发下架），实际 %d", got.Status)
	}

	fields["status"] = model.ProductStatusOnSale
	if rows, err := repo.UpdateProductFields(tenantCtx(), p.ID, fields); err != nil || rows != 1 {
		t.Fatalf("字段级更新产品（带状态）: rows=%d err=%v", rows, err)
	}
	if err := tenant.Skip(db.Model(&model.Product{})).
		Select("status").Where("id = ?", p.ID).Scan(&got).Error; err != nil {
		t.Fatalf("回读产品状态: %v", err)
	}
	if got.Status != model.ProductStatusOnSale {
		t.Fatalf("携带 status 时应更新为 1，实际 %d", got.Status)
	}
}

// TestUpdateReceivablePlanFieldsSkipsContractID 回款计划更新只写请求字段：
// 合同归属不可经编辑接口改写（并发切换合同后编辑不得回写 contract_id）。
func TestUpdateReceivablePlanFieldsSkipsContractID(t *testing.T) {
	db := newCRMTestDB(t, &model.ReceivablePlan{})
	repo := repository.New(db)

	p := &model.ReceivablePlan{
		TenantID: 1, ContractID: 7, PeriodNo: 1,
		Amount: mustMoney(t, "100.00"), DueDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	}
	createRow(t, db, p)
	concurrentUpdate(t, db, &model.ReceivablePlan{}, p.ID, map[string]any{"contract_id": 99})

	dueDate := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := repo.UpdateReceivablePlanFields(tenantCtx(), p.ID, map[string]any{
		"period_no": 2, "amount": mustMoney(t, "150.00"), "due_date": dueDate, "remark": "新备注",
	})
	if err != nil || rows != 1 {
		t.Fatalf("字段级更新回款计划: rows=%d err=%v", rows, err)
	}

	var got struct {
		PeriodNo   int `gorm:"column:period_no"`
		Amount     string
		Remark     string
		ContractID int64 `gorm:"column:contract_id"`
	}
	if err := tenant.Skip(db.Model(&model.ReceivablePlan{})).
		Select("period_no, CAST(amount AS TEXT) AS amount, remark, contract_id").
		Where("id = ?", p.ID).Scan(&got).Error; err != nil {
		t.Fatalf("回读回款计划: %v", err)
	}
	if got.PeriodNo != 2 || mustMoney(t, got.Amount).Cents() != mustMoney(t, "150.00").Cents() || got.Remark != "新备注" {
		t.Fatalf("请求字段未更新：period=%d amount=%q remark=%q", got.PeriodNo, got.Amount, got.Remark)
	}
	if got.ContractID != 99 {
		t.Fatalf("contract_id 被回写覆盖：期望 99（并发切换），实际 %d", got.ContractID)
	}
}
