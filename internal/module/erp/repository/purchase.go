package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// purchase.go 是采购三单的数据访问（issue #91，docs/erp-mvp-spec.md §3.3/§4.4/§5）。
// 主表 + 明细：明细随主表整体替换（编辑 = 软删旧明细 + 插入新明细，仅在未审核态
// 允许）；审核/反审核的库存动作在 service 唯一入账入口，不在本层。
//
// 占用校验与进度回写的聚合口径（spec §4.4，两步查询避免依赖插件对 JOIN 的改写，
// 聚合表均经租户插件自动带租户条件）：
//   - 订单的入库占用/进度：入库单（order_id）→ 明细按产品 SUM(count)；
//   - 订单的退货占用/进度：入库单（order_id）→ 退货单（in_id IN 入库单）→
//     明细按产品 SUM(count)；
//   - 入库单的退货占用：退货单（in_id）→ 明细按产品 SUM(count)。
// 占用统计不过滤状态（未审核 + 已审核都占量），进度回写只取已审核（status=20）。

// ---------- 采购订单（CG） ----------

// PurchaseOrderFilter 是采购订单列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type PurchaseOrderFilter struct {
	No         string
	SupplierId int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreatePurchaseOrder 创建采购订单主表。
func (r *Repository) CreatePurchaseOrder(ctx context.Context, row *model.PurchaseOrder) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreatePurchaseOrderItems 批量创建采购订单明细。
func (r *Repository) CreatePurchaseOrderItems(ctx context.Context, items []model.PurchaseOrderItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindPurchaseOrderByID 按 ID 查订单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindPurchaseOrderByID(ctx context.Context, id int64) (*model.PurchaseOrder, error) {
	var row model.PurchaseOrder
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindPurchaseOrderWithItems 查询订单带头 + 明细（详情/进度回写用）。
func (r *Repository) FindPurchaseOrderWithItems(ctx context.Context, id int64) (*model.PurchaseOrder, error) {
	var row model.PurchaseOrder
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// LockPurchaseOrder 取订单行锁并返回主表（占用校验与挂单写入的串行化点：
// 同一订单的一切占量写路径先锁本行再读写，并发抢余量在同一行锁上排队，
// spec §4.4 防双占）。只能在事务内调用。
func (r *Repository) LockPurchaseOrder(ctx context.Context, id int64) (*model.PurchaseOrder, error) {
	var row model.PurchaseOrder
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListPurchaseOrders 按筛选与数据范围谓词分页查询订单（固定 id DESC）。
func (r *Repository) ListPurchaseOrders(ctx context.Context, f PurchaseOrderFilter, offset, limit int) ([]model.PurchaseOrder, int64, error) {
	q := r.purchaseOrderQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.PurchaseOrder
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllPurchaseOrders 按筛选与数据范围谓词查询全部订单（Excel 导出用，不分页）。
func (r *Repository) ListAllPurchaseOrders(ctx context.Context, f PurchaseOrderFilter) ([]model.PurchaseOrder, error) {
	var list []model.PurchaseOrder
	if err := r.purchaseOrderQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// purchaseOrderQuery 构造订单列表/导出的公共查询。
func (r *Repository) purchaseOrderQuery(ctx context.Context, f PurchaseOrderFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.PurchaseOrder{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.SupplierId > 0 {
		q = q.Where("supplier_id = ?", f.SupplierId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("order_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("order_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// SavePurchaseOrder 全量保存订单主表（更新路径）。
func (r *Repository) SavePurchaseOrder(ctx context.Context, row *model.PurchaseOrder) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeletePurchaseOrder 按 ID 软删除订单主表（仅未审核态，service 层把关）。
func (r *Repository) DeletePurchaseOrder(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.PurchaseOrder{}, id).Error
}

// DeletePurchaseOrderItems 软删除订单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeletePurchaseOrderItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("purchase_order_id = ?", docID).
		Delete(&model.PurchaseOrderItem{}).Error
}

// UpdatePurchaseOrderStatusIf 乐观认领审核状态（与 #90 同款防双审模式）：仅当
// 当前状态为 fromStatus 时翻转为 toStatus，返回影响行数（0 = 状态已被并发请求
// 改变，service 层翻译 409）。审核/反审核先认领再落账。
func (r *Repository) UpdatePurchaseOrderStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.PurchaseOrder{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// FindPurchaseOrderItems 查询订单明细行（占用校验的配额侧与进度回写目标行）。
func (r *Repository) FindPurchaseOrderItems(ctx context.Context, orderID int64) ([]model.PurchaseOrderItem, error) {
	var items []model.PurchaseOrderItem
	if err := r.db.WithContext(ctx).
		Where("purchase_order_id = ?", orderID).
		Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// UpdatePurchaseOrderItemProgress 回写订单明细进度（in_count/return_count，
// 只计已审核单的重算结果，spec §4.4）。
func (r *Repository) UpdatePurchaseOrderItemProgress(ctx context.Context, itemID int64, inCount, returnCount qty.Qty) error {
	return r.db.WithContext(ctx).Model(&model.PurchaseOrderItem{}).
		Where("id = ?", itemID).
		Updates(map[string]any{"in_count": inCount, "return_count": returnCount}).Error
}

// CountPurchaseOrderItemsByProduct 产品删除引用计数的一部分：订单明细引用行数。
func (r *Repository) CountPurchaseOrderItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.PurchaseOrderItem{}, productID)
}

// ---------- 采购入库（RK） ----------

// PurchaseInFilter 是采购入库单列表查询条件（筛选 + 数据范围谓词）。
type PurchaseInFilter struct {
	No         string
	OrderId    int64
	SupplierId int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreatePurchaseIn 创建采购入库单主表。
func (r *Repository) CreatePurchaseIn(ctx context.Context, row *model.PurchaseIn) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreatePurchaseInItems 批量创建采购入库单明细。
func (r *Repository) CreatePurchaseInItems(ctx context.Context, items []model.PurchaseInItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindPurchaseInByID 按 ID 查入库单主表，不存在返回 nil。
func (r *Repository) FindPurchaseInByID(ctx context.Context, id int64) (*model.PurchaseIn, error) {
	var row model.PurchaseIn
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindPurchaseInWithItems 查询入库单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindPurchaseInWithItems(ctx context.Context, id int64) (*model.PurchaseIn, error) {
	var row model.PurchaseIn
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// LockPurchaseIn 取入库单行锁并返回主表（退货占量的串行化点：同一入库单的
// 一切退货写路径与退货占用校验先锁本行）。只能在事务内调用。
func (r *Repository) LockPurchaseIn(ctx context.Context, id int64) (*model.PurchaseIn, error) {
	var row model.PurchaseIn
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListPurchaseIns 按筛选与数据范围谓词分页查询入库单（固定 id DESC）。
func (r *Repository) ListPurchaseIns(ctx context.Context, f PurchaseInFilter, offset, limit int) ([]model.PurchaseIn, int64, error) {
	q := r.purchaseInQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.PurchaseIn
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllPurchaseIns 按筛选与数据范围谓词查询全部入库单（Excel 导出用）。
func (r *Repository) ListAllPurchaseIns(ctx context.Context, f PurchaseInFilter) ([]model.PurchaseIn, error) {
	var list []model.PurchaseIn
	if err := r.purchaseInQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// purchaseInQuery 构造入库单列表/导出的公共查询。
func (r *Repository) purchaseInQuery(ctx context.Context, f PurchaseInFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.PurchaseIn{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.OrderId > 0 {
		q = q.Where("order_id = ?", f.OrderId)
	}
	if f.SupplierId > 0 {
		q = q.Where("supplier_id = ?", f.SupplierId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("in_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("in_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// SavePurchaseIn 全量保存入库单主表（更新路径）。
func (r *Repository) SavePurchaseIn(ctx context.Context, row *model.PurchaseIn) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeletePurchaseIn 按 ID 软删除入库单主表（仅未审核态）。
func (r *Repository) DeletePurchaseIn(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.PurchaseIn{}, id).Error
}

// DeletePurchaseInItems 软删除入库单的全部明细。
func (r *Repository) DeletePurchaseInItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("purchase_in_id = ?", docID).
		Delete(&model.PurchaseInItem{}).Error
}

// UpdatePurchaseInStatusIf 乐观认领入库单审核状态（防双审模式同 #90）。
func (r *Repository) UpdatePurchaseInStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.PurchaseIn{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountPurchaseInItemsByProduct 产品删除引用计数的一部分：入库单明细引用行数。
func (r *Repository) CountPurchaseInItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.PurchaseInItem{}, productID)
}

// ---------- 采购退货（RT） ----------

// PurchaseReturnFilter 是采购退货单列表查询条件（筛选 + 数据范围谓词）。
type PurchaseReturnFilter struct {
	No         string
	InId       int64
	SupplierId int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreatePurchaseReturn 创建采购退货单主表。
func (r *Repository) CreatePurchaseReturn(ctx context.Context, row *model.PurchaseReturn) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreatePurchaseReturnItems 批量创建采购退货单明细。
func (r *Repository) CreatePurchaseReturnItems(ctx context.Context, items []model.PurchaseReturnItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindPurchaseReturnByID 按 ID 查退货单主表，不存在返回 nil。
func (r *Repository) FindPurchaseReturnByID(ctx context.Context, id int64) (*model.PurchaseReturn, error) {
	var row model.PurchaseReturn
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindPurchaseReturnWithItems 查询退货单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindPurchaseReturnWithItems(ctx context.Context, id int64) (*model.PurchaseReturn, error) {
	var row model.PurchaseReturn
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListPurchaseReturns 按筛选与数据范围谓词分页查询退货单（固定 id DESC）。
func (r *Repository) ListPurchaseReturns(ctx context.Context, f PurchaseReturnFilter, offset, limit int) ([]model.PurchaseReturn, int64, error) {
	q := r.purchaseReturnQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.PurchaseReturn
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllPurchaseReturns 按筛选与数据范围谓词查询全部退货单（Excel 导出用）。
func (r *Repository) ListAllPurchaseReturns(ctx context.Context, f PurchaseReturnFilter) ([]model.PurchaseReturn, error) {
	var list []model.PurchaseReturn
	if err := r.purchaseReturnQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// purchaseReturnQuery 构造退货单列表/导出的公共查询。
func (r *Repository) purchaseReturnQuery(ctx context.Context, f PurchaseReturnFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.PurchaseReturn{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.InId > 0 {
		q = q.Where("in_id = ?", f.InId)
	}
	if f.SupplierId > 0 {
		q = q.Where("supplier_id = ?", f.SupplierId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("return_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("return_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// SavePurchaseReturn 全量保存退货单主表（更新路径）。
func (r *Repository) SavePurchaseReturn(ctx context.Context, row *model.PurchaseReturn) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeletePurchaseReturn 按 ID 软删除退货单主表（仅未审核态）。
func (r *Repository) DeletePurchaseReturn(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.PurchaseReturn{}, id).Error
}

// DeletePurchaseReturnItems 软删除退货单的全部明细。
func (r *Repository) DeletePurchaseReturnItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("purchase_return_id = ?", docID).
		Delete(&model.PurchaseReturnItem{}).Error
}

// UpdatePurchaseReturnStatusIf 乐观认领退货单审核状态（防双审模式同 #90）。
func (r *Repository) UpdatePurchaseReturnStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.PurchaseReturn{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountPurchaseReturnItemsByProduct 产品删除引用计数的一部分：退货单明细引用行数。
func (r *Repository) CountPurchaseReturnItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.PurchaseReturnItem{}, productID)
}

// ---------- 占用/进度聚合（spec §4.4；两步查询，均经租户插件限定） ----------

// SumPurchaseInItemsByProduct 汇总挂某订单的入库单明细数量（按产品聚合）。
// approvedOnly=false 统计占用（未审核 + 已审核都占量）；true 只统计进度回写
// （已审核），spec §4.4。excludeInID > 0 时排除该入库单自身——审核/编辑时的
// 占用复核不能把本单明细同时计入「既有占用」与「本次写入」，否则重复计数。
func (r *Repository) SumPurchaseInItemsByProduct(ctx context.Context, orderID int64, approvedOnly bool, excludeInID int64) (map[int64]qty.Qty, error) {
	docQuery := r.db.WithContext(ctx).Model(&model.PurchaseIn{}).Select("id").Where("order_id = ?", orderID)
	if approvedOnly {
		docQuery = docQuery.Where("status = ?", model.DocStatusApproved)
	}
	if excludeInID > 0 {
		docQuery = docQuery.Where("id <> ?", excludeInID)
	}
	var docIDs []int64
	if err := docQuery.Pluck("id", &docIDs).Error; err != nil {
		return nil, err
	}
	return sumQtyByProduct(ctx, r.db, &model.PurchaseInItem{}, "purchase_in_id", docIDs)
}

// SumPurchaseReturnItemsByProductForOrder 汇总挂某订单的退货单明细数量
// （退货单经入库单关联到订单，按产品聚合；approvedOnly 同上）。
func (r *Repository) SumPurchaseReturnItemsByProductForOrder(ctx context.Context, orderID int64, approvedOnly bool) (map[int64]qty.Qty, error) {
	docQuery := r.db.WithContext(ctx).Model(&model.PurchaseIn{}).Select("id").Where("order_id = ?", orderID)
	var inIDs []int64
	if err := docQuery.Pluck("id", &inIDs).Error; err != nil {
		return nil, err
	}
	if len(inIDs) == 0 {
		return map[int64]qty.Qty{}, nil
	}
	retQuery := r.db.WithContext(ctx).Model(&model.PurchaseReturn{}).Select("id").Where("in_id IN ?", inIDs)
	if approvedOnly {
		retQuery = retQuery.Where("status = ?", model.DocStatusApproved)
	}
	var retIDs []int64
	if err := retQuery.Pluck("id", &retIDs).Error; err != nil {
		return nil, err
	}
	return sumQtyByProduct(ctx, r.db, &model.PurchaseReturnItem{}, "purchase_return_id", retIDs)
}

// SumPurchaseReturnItemsByProductForIn 汇总挂某入库单的退货单明细数量
// （按产品聚合；approvedOnly 同上）。excludeReturnID > 0 时排除该退货单自身
// （审核/编辑时的占用复核防重复计数，同 SumPurchaseInItemsByProduct）。
func (r *Repository) SumPurchaseReturnItemsByProductForIn(ctx context.Context, inID int64, approvedOnly bool, excludeReturnID int64) (map[int64]qty.Qty, error) {
	retQuery := r.db.WithContext(ctx).Model(&model.PurchaseReturn{}).Select("id").Where("in_id = ?", inID)
	if approvedOnly {
		retQuery = retQuery.Where("status = ?", model.DocStatusApproved)
	}
	if excludeReturnID > 0 {
		retQuery = retQuery.Where("id <> ?", excludeReturnID)
	}
	var retIDs []int64
	if err := retQuery.Pluck("id", &retIDs).Error; err != nil {
		return nil, err
	}
	return sumQtyByProduct(ctx, r.db, &model.PurchaseReturnItem{}, "purchase_return_id", retIDs)
}

// sumQtyByProduct 明细表按产品聚合数量（docIDs 为空返回空 map）。
func sumQtyByProduct(ctx context.Context, db *gorm.DB, table any, docColumn string, docIDs []int64) (map[int64]qty.Qty, error) {
	out := make(map[int64]qty.Qty)
	if len(docIDs) == 0 {
		return out, nil
	}
	var aggs []struct {
		ProductID int64
		Total     qty.Qty
	}
	if err := db.WithContext(ctx).Model(table).
		Select("product_id, SUM(`count`) AS total").
		Where(docColumn+" IN ?", docIDs).
		Group("product_id").
		Find(&aggs).Error; err != nil {
		return nil, err
	}
	for _, agg := range aggs {
		out[agg.ProductID] = agg.Total
	}
	return out, nil
}

// ---------- 名称/单号批量解析（列表与详情展示列，service 层组装） ----------

// FindSupplierNamesIn 返回租户内供应商 ID→名称映射（未删除行）。
func (r *Repository) FindSupplierNamesIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNamesIn(ctx, r.db, &model.Supplier{}, ids)
}

// FindPurchaseOrderNosIn 返回租户内采购订单 ID→单号映射（展示挂单订单号）。
func (r *Repository) FindPurchaseOrderNosIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNosIn(ctx, r.db, &model.PurchaseOrder{}, ids)
}

// FindPurchaseInNosIn 返回租户内采购入库单 ID→单号映射（展示挂单入库号）。
func (r *Repository) FindPurchaseInNosIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNosIn(ctx, r.db, &model.PurchaseIn{}, ids)
}

// findNosIn 通用 ID→no 批量解析（租户插件自动带租户条件）。
func findNosIn(ctx context.Context, db *gorm.DB, table any, ids []int64) (map[int64]string, error) {
	nos := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return nos, nil
	}
	var rows []struct {
		ID int64
		No string
	}
	if err := db.WithContext(ctx).Model(table).
		Select("id, no").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		nos[row.ID] = row.No
	}
	return nos, nil
}

// ---------- 供应商删除引用（spec §4.9：供应商被任何采购单据引用禁删） ----------

// CountPurchaseDocsBySupplier 供应商被采购单据（订单/入库/退货）引用的主表行数
// （软删行不算引用）。
func (r *Repository) CountPurchaseDocsBySupplier(ctx context.Context, supplierID int64) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.PurchaseOrder{}).
		Where("supplier_id = ?", supplierID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	var m int64
	if err := r.db.WithContext(ctx).Model(&model.PurchaseIn{}).
		Where("supplier_id = ?", supplierID).
		Count(&m).Error; err != nil {
		return 0, err
	}
	var k int64
	if err := r.db.WithContext(ctx).Model(&model.PurchaseReturn{}).
		Where("supplier_id = ?", supplierID).
		Count(&k).Error; err != nil {
		return 0, err
	}
	return n + m + k, nil
}
