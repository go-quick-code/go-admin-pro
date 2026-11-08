package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// finance.go 是财务域的数据访问（issue #94，docs/erp-mvp-spec.md §3.6/§4.7）。
// 核销动作（审核上限、回写重算）在 service 事务内完成，本层只提供查询与写入
// 原语；跨表聚合用两步查询（先取主表 ID，再聚合明细），避免依赖租户插件对
// JOIN 的改写（与 #91/#92 的聚合口径一致）。

// ---------- 结算账户 ----------

// FinanceAccountFilter 是结算账户列表查询条件（筛选 + 数据范围谓词）。
type FinanceAccountFilter struct {
	Name   string
	Status *int8

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreateFinanceAccount 创建结算账户。
func (r *Repository) CreateFinanceAccount(ctx context.Context, row *model.FinanceAccount) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// FindFinanceAccountByID 按 ID 查结算账户（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindFinanceAccountByID(ctx context.Context, id int64) (*model.FinanceAccount, error) {
	var row model.FinanceAccount
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListFinanceAccounts 按筛选与数据范围谓词分页查询结算账户（固定 id DESC）。
func (r *Repository) ListFinanceAccounts(ctx context.Context, f FinanceAccountFilter, offset, limit int) ([]model.FinanceAccount, int64, error) {
	q := r.financeAccountQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.FinanceAccount
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// financeAccountQuery 构造结算账户列表的公共查询。
func (r *Repository) financeAccountQuery(ctx context.Context, f FinanceAccountFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.FinanceAccount{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
}

// SaveFinanceAccount 全量保存结算账户（更新路径）。
func (r *Repository) SaveFinanceAccount(ctx context.Context, row *model.FinanceAccount) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteFinanceAccount 按 ID 软删除结算账户（被收付款引用时 service 层先 409）。
func (r *Repository) DeleteFinanceAccount(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.FinanceAccount{}, id).Error
}

// LockFinanceAccounts 锁定租户内全部结算账户行（事务内）：置默认/清除默认的
// 串行化点，避免并发置默认出现两个默认账户（库层生成列唯一键兜底）。
func (r *Repository) LockFinanceAccounts(ctx context.Context) error {
	var rows []model.FinanceAccount
	return r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").Find(&rows).Error
}

// ClearFinanceAccountDefaults 取消租户内原默认账户（excludeID > 0 时保留目标）。
func (r *Repository) ClearFinanceAccountDefaults(ctx context.Context, excludeID int64) error {
	q := r.db.WithContext(ctx).Model(&model.FinanceAccount{}).
		Where("default_status = ?", model.FinanceDefaultYes)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	return q.Update("default_status", model.FinanceDefaultNo).Error
}

// UpdateFinanceAccountDefaultStatus 设置单个账户的默认标记。
func (r *Repository) UpdateFinanceAccountDefaultStatus(ctx context.Context, id int64, status int8) error {
	return r.db.WithContext(ctx).Model(&model.FinanceAccount{}).
		Where("id = ?", id).Update("default_status", status).Error
}

// CountFinanceReceiptsByAccount 引用计数：引用该结算账户的收款单数。
func (r *Repository) CountFinanceReceiptsByAccount(ctx context.Context, accountID int64) (int64, error) {
	return countRows(ctx, r.db, &model.FinanceReceipt{}, "account_id = ?", accountID)
}

// CountFinancePaymentsByAccount 引用计数：引用该结算账户的付款单数。
func (r *Repository) CountFinancePaymentsByAccount(ctx context.Context, accountID int64) (int64, error) {
	return countRows(ctx, r.db, &model.FinancePayment{}, "account_id = ?", accountID)
}

// CountFinanceReceiptsByCustomer 引用计数：引用该客户的收款单数（客户删除约束）。
func (r *Repository) CountFinanceReceiptsByCustomer(ctx context.Context, customerID int64) (int64, error) {
	return countRows(ctx, r.db, &model.FinanceReceipt{}, "customer_id = ?", customerID)
}

// CountFinancePaymentsBySupplier 引用计数：引用该供应商的付款单数（供应商删除约束）。
func (r *Repository) CountFinancePaymentsBySupplier(ctx context.Context, supplierID int64) (int64, error) {
	return countRows(ctx, r.db, &model.FinancePayment{}, "supplier_id = ?", supplierID)
}

// FindFinanceAccountNamesIn 返回租户内结算账户 ID→名称映射。
func (r *Repository) FindFinanceAccountNamesIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNamesIn(ctx, r.db, &model.FinanceAccount{}, ids)
}

// ---------- 收款单（SK） ----------

// FinanceReceiptFilter 是收款单列表查询条件（筛选 + 数据范围谓词）。
type FinanceReceiptFilter struct {
	No         string
	CustomerId int64
	AccountId  int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// ListFinanceReceipts 按筛选与数据范围谓词分页查询收款单（固定 id DESC）。
func (r *Repository) ListFinanceReceipts(ctx context.Context, f FinanceReceiptFilter, offset, limit int) ([]model.FinanceReceipt, int64, error) {
	q := r.financeReceiptQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.FinanceReceipt
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllFinanceReceipts 按筛选与数据范围谓词查询全部收款单（Excel 导出用）。
func (r *Repository) ListAllFinanceReceipts(ctx context.Context, f FinanceReceiptFilter) ([]model.FinanceReceipt, error) {
	var list []model.FinanceReceipt
	if err := r.financeReceiptQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// financeReceiptQuery 构造收款单列表/导出的公共查询。
func (r *Repository) financeReceiptQuery(ctx context.Context, f FinanceReceiptFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.FinanceReceipt{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.CustomerId > 0 {
		q = q.Where("customer_id = ?", f.CustomerId)
	}
	if f.AccountId > 0 {
		q = q.Where("account_id = ?", f.AccountId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("receipt_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("receipt_time < ?", *f.EndTime)
	}
	return applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
}

// ---------- 付款单（FK） ----------

// FinancePaymentFilter 是付款单列表查询条件（筛选 + 数据范围谓词）。
type FinancePaymentFilter struct {
	No         string
	SupplierId int64
	AccountId  int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreateFinancePayment 创建付款单主表。
func (r *Repository) CreateFinancePayment(ctx context.Context, row *model.FinancePayment) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateFinancePaymentItems 批量创建付款单明细。
func (r *Repository) CreateFinancePaymentItems(ctx context.Context, items []model.FinancePaymentItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindFinancePaymentByID 按 ID 查付款单主表，不存在返回 nil。
func (r *Repository) FindFinancePaymentByID(ctx context.Context, id int64) (*model.FinancePayment, error) {
	var row model.FinancePayment
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindFinancePaymentByIDForUpdate 行锁读取付款单（安全 F1：编辑事务内重读并
// 复核 status=10，与审核认领串行化）。只能在事务内调用。
func (r *Repository) FindFinancePaymentByIDForUpdate(ctx context.Context, id int64) (*model.FinancePayment, error) {
	var row model.FinancePayment
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindFinancePaymentWithItems 查询付款单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindFinancePaymentWithItems(ctx context.Context, id int64) (*model.FinancePayment, error) {
	var row model.FinancePayment
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListFinancePayments 按筛选与数据范围谓词分页查询付款单（固定 id DESC）。
func (r *Repository) ListFinancePayments(ctx context.Context, f FinancePaymentFilter, offset, limit int) ([]model.FinancePayment, int64, error) {
	q := r.financePaymentQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.FinancePayment
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllFinancePayments 按筛选与数据范围谓词查询全部付款单（Excel 导出用）。
func (r *Repository) ListAllFinancePayments(ctx context.Context, f FinancePaymentFilter) ([]model.FinancePayment, error) {
	var list []model.FinancePayment
	if err := r.financePaymentQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// financePaymentQuery 构造付款单列表/导出的公共查询。
func (r *Repository) financePaymentQuery(ctx context.Context, f FinancePaymentFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.FinancePayment{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.SupplierId > 0 {
		q = q.Where("supplier_id = ?", f.SupplierId)
	}
	if f.AccountId > 0 {
		q = q.Where("account_id = ?", f.AccountId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("payment_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("payment_time < ?", *f.EndTime)
	}
	return applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
}

// SaveFinancePayment 全量保存付款单主表（更新路径）。
func (r *Repository) SaveFinancePayment(ctx context.Context, row *model.FinancePayment) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteFinancePaymentIfUnaudited 条件软删除付款单（status 谓词 + 影响行数，安全 F1）。
func (r *Repository) DeleteFinancePaymentIfUnaudited(ctx context.Context, id int64) (int64, error) {
	return r.deleteIfStatus(ctx, &model.FinancePayment{}, id, model.DocStatusUnaudited)
}

// DeleteFinancePaymentItems 软删除付款单的全部明细。
func (r *Repository) DeleteFinancePaymentItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("payment_id = ?", docID).
		Delete(&model.FinancePaymentItem{}).Error
}

// UpdateFinancePaymentStatusIf 乐观认领付款单审核状态。
func (r *Repository) UpdateFinancePaymentStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.FinancePayment{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// ---------- 核销金额聚合 ----------

// SumFinanceReceiptItemPrices 汇总收款单明细金额（主表列表/详情展示，按单据）。
func (r *Repository) SumFinanceReceiptItemPrices(ctx context.Context, receiptIDs []int64) (map[int64]money.Money, error) {
	return sumFinanceItemPrices(ctx, r.db, &model.FinanceReceiptItem{}, "receipt_id", receiptIDs)
}

// SumFinancePaymentItemPrices 汇总付款单明细金额（按单据）。
func (r *Repository) SumFinancePaymentItemPrices(ctx context.Context, paymentIDs []int64) (map[int64]money.Money, error) {
	return sumFinanceItemPrices(ctx, r.db, &model.FinancePaymentItem{}, "payment_id", paymentIDs)
}

// sumFinanceItemPrices 明细表按主单聚合金额（docIDs 为空返回空 map）。
func sumFinanceItemPrices(ctx context.Context, db *gorm.DB, table any, docColumn string, docIDs []int64) (map[int64]money.Money, error) {
	out := make(map[int64]money.Money, len(docIDs))
	if len(docIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		DocID int64
		Total money.Money
	}
	if err := db.WithContext(ctx).Model(table).
		Select(docColumn+" AS doc_id, SUM(price) AS total").
		Where(docColumn+" IN ?", docIDs).
		Group(docColumn).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.DocID] = row.Total
	}
	return out, nil
}

// SumApprovedFinanceReceiptPricesByBiz 汇总指定业务单据被已审核收款单核销的
// 金额（bizType + bizIDs 维度）。excludeReceiptID > 0 时排除该收款单自身
// （审核时「其它已审核核销额」口径，spec §4.7）。
func (r *Repository) SumApprovedFinanceReceiptPricesByBiz(ctx context.Context, bizType string, bizIDs []int64, excludeReceiptID int64) (map[int64]money.Money, error) {
	return r.sumApprovedFinancePricesByBiz(ctx, &model.FinanceReceipt{}, &model.FinanceReceiptItem{}, "receipt_id", bizType, bizIDs, excludeReceiptID)
}

// SumApprovedFinancePaymentPricesByBiz 汇总指定业务单据被已审核付款单核销的金额。
func (r *Repository) SumApprovedFinancePaymentPricesByBiz(ctx context.Context, bizType string, bizIDs []int64, excludePaymentID int64) (map[int64]money.Money, error) {
	return r.sumApprovedFinancePricesByBiz(ctx, &model.FinancePayment{}, &model.FinancePaymentItem{}, "payment_id", bizType, bizIDs, excludePaymentID)
}

// sumApprovedFinancePricesByBiz 聚合指定业务单据被已审核收/付款核销的金额。
// 调用方在 READ COMMITTED 事务内先锁被核销单据行（串行化并发核销），本查询
// 只做当前读聚合（不锁收付款行，避免与并发核销形成锁环）。
func (r *Repository) sumApprovedFinancePricesByBiz(ctx context.Context, headerTable, itemTable any, docColumn, bizType string, bizIDs []int64, excludeDocID int64) (map[int64]money.Money, error) {
	out := make(map[int64]money.Money, len(bizIDs))
	if len(bizIDs) == 0 {
		return out, nil
	}
	q := r.db.WithContext(ctx).Model(itemTable).
		Select("id, "+docColumn+" AS doc_id, biz_id, price").
		Where("biz_type = ? AND biz_id IN ?", bizType, bizIDs)
	if excludeDocID > 0 {
		q = q.Where(docColumn+" <> ?", excludeDocID)
	}
	var items []struct {
		DocID int64
		BizID int64
		Price money.Money
	}
	if err := q.Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return out, nil
	}
	docIDs := make([]int64, 0, len(items))
	for _, item := range items {
		docIDs = append(docIDs, item.DocID)
	}
	approved, err := r.approvedFinanceDocIDs(ctx, headerTable, docIDs)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, ok := approved[item.DocID]; !ok {
			continue
		}
		total, err := out[item.BizID].Add(item.Price)
		if err != nil {
			return nil, err
		}
		out[item.BizID] = total
	}
	return out, nil
}

// approvedFinanceDocIDs 返回给定主单 ID 中已审核（status=20）的集合。
func (r *Repository) approvedFinanceDocIDs(ctx context.Context, headerTable any, docIDs []int64) (map[int64]struct{}, error) {
	approved := make(map[int64]struct{}, len(docIDs))
	if len(docIDs) == 0 {
		return approved, nil
	}
	var headers []struct{ ID int64 }
	if err := r.db.WithContext(ctx).Model(headerTable).
		Select("id").
		Where("id IN ? AND status = ?", docIDs, model.DocStatusApproved).
		Find(&headers).Error; err != nil {
		return nil, err
	}
	for _, header := range headers {
		approved[header.ID] = struct{}{}
	}
	return approved, nil
}

// CountApprovedFinanceReceiptItemsByBiz 统计引用某业务单据的已审核收款单明细行数
// （反审核禁令：出库/退货存在已审核收款时不可反审核，spec §4.1/§4.7）。
func (r *Repository) CountApprovedFinanceReceiptItemsByBiz(ctx context.Context, bizType string, bizID int64) (int64, error) {
	return r.countApprovedFinanceItemsByBiz(ctx, &model.FinanceReceipt{}, &model.FinanceReceiptItem{}, "receipt_id", bizType, bizID)
}

// CountApprovedFinancePaymentItemsByBiz 统计引用某业务单据的已审核付款单明细行数。
func (r *Repository) CountApprovedFinancePaymentItemsByBiz(ctx context.Context, bizType string, bizID int64) (int64, error) {
	return r.countApprovedFinanceItemsByBiz(ctx, &model.FinancePayment{}, &model.FinancePaymentItem{}, "payment_id", bizType, bizID)
}

// countApprovedFinanceItemsByBiz 统计引用某业务单据的已审核收付款明细行数
// （反审核禁令）。调用方在 READ COMMITTED 事务内持有目标单据行锁，本查询只做
// 当前读聚合（不锁收付款行，避免与并发核销形成锁环）。
func (r *Repository) countApprovedFinanceItemsByBiz(ctx context.Context, headerTable, itemTable any, docColumn, bizType string, bizID int64) (int64, error) {
	var items []struct{ DocID int64 }
	if err := r.db.WithContext(ctx).Model(itemTable).
		Select(docColumn+" AS doc_id").
		Where("biz_type = ? AND biz_id = ?", bizType, bizID).
		Find(&items).Error; err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}
	docIDs := make([]int64, 0, len(items))
	for _, item := range items {
		docIDs = append(docIDs, item.DocID)
	}
	approved, err := r.approvedFinanceDocIDs(ctx, headerTable, docIDs)
	if err != nil {
		return 0, err
	}
	count := int64(0)
	for _, item := range items {
		if _, ok := approved[item.DocID]; ok {
			count++
		}
	}
	return count, nil
}

// SumApprovedFinanceReceiptPricesByAccounts 按结算账户汇总已审核收款金额
// （余额列：账户 × 已审核收款合计，spec §3.6/§4.7）。
func (r *Repository) SumApprovedFinanceReceiptPricesByAccounts(ctx context.Context, accountIDs []int64) (map[int64]money.Money, error) {
	return r.sumApprovedFinancePricesByAccounts(ctx, &model.FinanceReceipt{}, &model.FinanceReceiptItem{}, "receipt_id", accountIDs)
}

// SumApprovedFinancePaymentPricesByAccounts 按结算账户汇总已审核付款金额。
func (r *Repository) SumApprovedFinancePaymentPricesByAccounts(ctx context.Context, accountIDs []int64) (map[int64]money.Money, error) {
	return r.sumApprovedFinancePricesByAccounts(ctx, &model.FinancePayment{}, &model.FinancePaymentItem{}, "payment_id", accountIDs)
}

// sumApprovedFinancePricesByAccounts 两步聚合（已审核主单 → 明细合计 → 按账户折叠）。
func (r *Repository) sumApprovedFinancePricesByAccounts(ctx context.Context, headerTable, itemTable any, docColumn string, accountIDs []int64) (map[int64]money.Money, error) {
	out := make(map[int64]money.Money, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	var headers []struct {
		ID        int64
		AccountID int64
	}
	if err := r.db.WithContext(ctx).Model(headerTable).
		Select("id, account_id").
		Where("status = ? AND account_id IN ?", model.DocStatusApproved, accountIDs).
		Find(&headers).Error; err != nil {
		return nil, err
	}
	if len(headers) == 0 {
		return out, nil
	}
	docIDs := make([]int64, 0, len(headers))
	accountOf := make(map[int64]int64, len(headers))
	for _, header := range headers {
		docIDs = append(docIDs, header.ID)
		accountOf[header.ID] = header.AccountID
	}
	docSums, err := sumFinanceItemPrices(ctx, r.db, itemTable, docColumn, docIDs)
	if err != nil {
		return nil, err
	}
	for docID, sum := range docSums {
		accountID := accountOf[docID]
		total, err := out[accountID].Add(sum)
		if err != nil {
			return nil, err
		}
		out[accountID] = total
	}
	return out, nil
}

// ---------- 被核销单据的加锁读取 / 回写 / 单号解析 ----------

// LockSaleReturnByID 行锁读取销售退货单（核销上限/回写按单据串行化）。
func (r *Repository) LockSaleReturnByID(ctx context.Context, id int64) (*model.SaleReturn, error) {
	var row model.SaleReturn
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// LockPurchaseReturnByID 行锁读取采购退货单。
func (r *Repository) LockPurchaseReturnByID(ctx context.Context, id int64) (*model.PurchaseReturn, error) {
	var row model.PurchaseReturn
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// UpdateSaleOutReceiptPrice 回写销售出库单已收款金额（核销审核/反审核重算）。
func (r *Repository) UpdateSaleOutReceiptPrice(ctx context.Context, id int64, price money.Money) error {
	return r.db.WithContext(ctx).Model(&model.SaleOut{}).
		Where("id = ?", id).Update("receipt_price", price).Error
}

// UpdateSaleReturnRefundPrice 回写销售退货单已退款金额。
func (r *Repository) UpdateSaleReturnRefundPrice(ctx context.Context, id int64, price money.Money) error {
	return r.db.WithContext(ctx).Model(&model.SaleReturn{}).
		Where("id = ?", id).Update("refund_price", price).Error
}

// UpdatePurchaseInPaymentPrice 回写采购入库单已付款金额。
func (r *Repository) UpdatePurchaseInPaymentPrice(ctx context.Context, id int64, price money.Money) error {
	return r.db.WithContext(ctx).Model(&model.PurchaseIn{}).
		Where("id = ?", id).Update("payment_price", price).Error
}

// UpdatePurchaseReturnRefundPrice 回写采购退货单已退款金额。
func (r *Repository) UpdatePurchaseReturnRefundPrice(ctx context.Context, id int64, price money.Money) error {
	return r.db.WithContext(ctx).Model(&model.PurchaseReturn{}).
		Where("id = ?", id).Update("refund_price", price).Error
}

// FindSaleReturnNosIn 返回租户内销售退货单 ID→单号映射（核销明细展示用）。
func (r *Repository) FindSaleReturnNosIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNosIn(ctx, r.db, &model.SaleReturn{}, "no", ids)
}

// FindPurchaseReturnNosIn 返回租户内采购退货单 ID→单号映射。
func (r *Repository) FindPurchaseReturnNosIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNosIn(ctx, r.db, &model.PurchaseReturn{}, "no", ids)
}
