// purchase_in.go 是采购入库单（RK）的业务方法（issue #91，docs/erp-mvp-spec.md
// §3.3/§4.1/§4.4/§4.8/§5）：必挂已审核订单、审核经唯一入账入口增库存并回写订单
// 进度、反审核经反向流水回滚。占用校验（未审核单也占量）在创建/编辑/审核时
// 全量执行，防并发双占的行锁纪律：涉及既有入库单行的写路径统一先锁入库单行、
// 再锁挂单订单行（in→order，与退货路径同序，防死锁；#98 审查修正）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListPurchaseIns 采购入库单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListPurchaseIns(ctx context.Context, req *dto.PurchaseInPageReq) ([]dto.PurchaseInRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.purchaseInFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListPurchaseIns(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.assemblePurchaseInRows(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// purchaseInFilter 构造入库单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) purchaseInFilter(ctx context.Context, req *dto.PurchaseInPageReq) (repository.PurchaseInFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.PurchaseInFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.PurchaseInFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.PurchaseInFilter{}, err
	}
	filter := repository.PurchaseInFilter{
		No:         req.No,
		OrderId:    req.OrderId,
		SupplierId: req.SupplierId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assemblePurchaseInRows 批量解析供应商/仓库/挂单订单号并组装展示行。
func (s *Service) assemblePurchaseInRows(ctx context.Context, list []model.PurchaseIn) ([]dto.PurchaseInRow, error) {
	supplierIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	orderIDs := make([]int64, 0, len(list))
	for _, row := range list {
		supplierIDs = append(supplierIDs, row.SupplierID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
		orderIDs = append(orderIDs, row.OrderID)
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, supplierIDs)
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	orderNos, err := s.repo.FindPurchaseOrderNosIn(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	out := make([]dto.PurchaseInRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.PurchaseInRow{
			ID:            row.ID,
			No:            row.No,
			SupplierId:    row.SupplierID,
			SupplierName:  supplierNames[row.SupplierID],
			OrderId:       row.OrderID,
			OrderNo:       orderNos[row.OrderID],
			WarehouseId:   row.WarehouseID,
			WarehouseName: warehouseNames[row.WarehouseID],
			InTime:        row.InTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			TotalPrice:    row.TotalPrice,
			PaymentPrice:  row.PaymentPrice,
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, nil
}

// GetPurchaseIn 采购入库单详情（带头 + 明细）。
func (s *Service) GetPurchaseIn(ctx context.Context, id int64) (*dto.PurchaseInDetail, error) {
	doc, err := s.findPurchaseIn(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindPurchaseInWithItems(ctx, doc.ID)
	if err != nil {
		return nil, err
	}
	productIDs := make([]int64, 0, len(full.Items))
	for _, item := range full.Items {
		productIDs = append(productIDs, item.ProductID)
	}
	productNames, err := s.repo.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, []int64{full.SupplierID})
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{full.WarehouseID})
	if err != nil {
		return nil, err
	}
	orderNos, err := s.repo.FindPurchaseOrderNosIn(ctx, []int64{full.OrderID})
	if err != nil {
		return nil, err
	}
	items := make([]dto.PurchaseInItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.PurchaseInItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return &dto.PurchaseInDetail{
		ID:            full.ID,
		No:            full.No,
		SupplierId:    full.SupplierID,
		SupplierName:  supplierNames[full.SupplierID],
		OrderId:       full.OrderID,
		OrderNo:       orderNos[full.OrderID],
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		InTime:        full.InTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		TotalPrice:    full.TotalPrice,
		PaymentPrice:  full.PaymentPrice,
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreatePurchaseIn 创建采购入库单（仅未审核）：必挂已审核订单（409）、供应商
// 与订单一致、仓库必填 → 订单余量占用校验（未审核单也占量，超量 409）→
// 事务内发放单号（RK）→ 落主表 + 明细。不动库存（spec §4.1：只有审核落账）。
func (s *Service) CreatePurchaseIn(ctx context.Context, req *dto.PurchaseInCreateReq) (*model.PurchaseIn, error) {
	items, err := buildDocItems(req.Items, "入库明细")
	if err != nil {
		return nil, err
	}
	inTime, err := s.parseDocTime(req.InTime, "入库时间")
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.PurchaseIn
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		order, err := s.lockApprovedOrder(ctx, tx, req.OrderId)
		if err != nil {
			return err
		}
		if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, order.SupplierID, items); err != nil {
			return err
		}
		if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
			return err
		}
		if err := s.checkOrderQuota(ctx, tx, order.ID, 0, items); err != nil {
			return err
		}
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypePurchaseIn, PurchaseInNoPrefix)
		if err != nil {
			return err
		}
		totalPrice, err := sumDraftTotalPrice(items)
		if err != nil {
			return err
		}
		doc = &model.PurchaseIn{
			No:          no,
			SupplierID:  req.SupplierId,
			OrderID:     req.OrderId,
			WarehouseID: req.WarehouseId,
			InTime:      inTime,
			Status:      model.DocStatusUnaudited,
			TotalPrice:  totalPrice,
			Remark:      req.Remark,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreatePurchaseIn(ctx, doc); err != nil {
			return err
		}
		return tx.CreatePurchaseInItems(ctx, buildPurchaseInItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdatePurchaseIn 修改采购入库单（仅未审核，已审核 409，spec §4.1）：
// 挂单订单不可更换（改挂等于换单，删除重建）；明细整体替换；替换后全量占用
// 校验（软删旧明细后按产品聚合，失败随事务回滚）。存在退货引用（含未审核）
// 时禁止修改仓库/供应商（409，退货行是父单快照，#98 审查）。
func (s *Service) UpdatePurchaseIn(ctx context.Context, req *dto.PurchaseInUpdateReq) error {
	row, err := s.findPurchaseIn(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	if req.OrderId != row.OrderID {
		return xerr.BadRequest("采购入库单不可更换挂单订单，请删除后重建")
	}
	items, err := buildDocItems(req.Items, "入库明细")
	if err != nil {
		return err
	}
	inTime, err := s.parseDocTime(req.InTime, "入库时间")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 先锁自身行（与退货创建/审核的父单行锁同锚点，统一 in→order 锁序）：
		// 存在任何退货引用（含未审核）时，禁止修改仓库与供应商——退货行保存
		// 的是创建时父单快照，父单漂移会让退货审核在旧仓/旧供应商口径下落账
		// （#98 审查 MAJOR 1）。其余字段照常可改。
		locked, err := tx.LockPurchaseIn(ctx, row.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("采购入库单不存在")
		}
		if locked.Status != model.DocStatusUnaudited {
			return xerr.Conflict("已审核单据不可修改")
		}
		retRefs, err := tx.CountPurchaseReturnsByIn(ctx, row.ID, 0)
		if err != nil {
			return err
		}
		if retRefs > 0 {
			if req.WarehouseId != locked.WarehouseID {
				return xerr.Conflict("采购入库单存在退货单引用，禁止修改仓库")
			}
			if req.SupplierId != locked.SupplierID {
				return xerr.Conflict("采购入库单存在退货单引用，禁止修改供应商")
			}
		}
		order, err := s.lockApprovedOrder(ctx, tx, row.OrderID)
		if err != nil {
			return err
		}
		if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, order.SupplierID, items); err != nil {
			return err
		}
		if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
			return err
		}
		// 明细整体替换（软删旧行 + 插入新行），替换后占用校验含新行全量口径；
		// 校验失败返回错误 → 事务回滚恢复旧明细。
		if err := tx.DeletePurchaseInItems(ctx, row.ID); err != nil {
			return err
		}
		if err := tx.CreatePurchaseInItems(ctx, buildPurchaseInItemRows(row.ID, items)); err != nil {
			return err
		}
		if err := s.checkOrderQuota(ctx, tx, order.ID, row.ID, items); err != nil {
			return err
		}
		totalPrice, err := sumDraftTotalPrice(items)
		if err != nil {
			return err
		}
		locked.SupplierID = req.SupplierId
		locked.WarehouseID = req.WarehouseId
		locked.InTime = inTime
		locked.TotalPrice = totalPrice
		locked.Remark = req.Remark
		return tx.SavePurchaseIn(ctx, locked)
	})
}

// DeletePurchaseIn 删除采购入库单（仅未审核，已审核 409，spec §4.1/§4.9）：
// 主表 + 明细一并软删除；未审核未落账不动库存，删除自然释放订单占用
// （spec §4.4）。存在退货单引用（含未审核）时禁删——挂单引用悬空会让退货
// 占用校验失去锚点（与销售出库对称）。
func (s *Service) DeletePurchaseIn(ctx context.Context, id int64) error {
	row, err := s.findPurchaseIn(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		retRefs, err := tx.CountPurchaseReturnsByIn(ctx, row.ID, 0)
		if err != nil {
			return err
		}
		if retRefs > 0 {
			return xerr.Conflict("采购入库单存在退货单引用，禁止删除")
		}
		// 条件软删 WHERE id=? AND status=10（安全 F1）：引用检查之后仍可能被并发
		// 审核，影响行数为 0 → 409，绝不删掉已审核行。
		deleted, err := tx.DeletePurchaseInIfUnaudited(ctx, row.ID)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return xerr.Conflict("已审核单据不可删除")
		}
		return tx.DeletePurchaseInItems(ctx, row.ID)
	})
}

// UpdatePurchaseInStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdatePurchaseInStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	row, err := s.findPurchaseIn(ctx, req.ID)
	if err != nil {
		return err
	}
	idempotent, err := checkDocStatusTransition(row.Status, req.Status)
	if err != nil {
		return err
	}
	if idempotent {
		return nil
	}
	if req.Status == model.DocStatusApproved {
		return s.approvePurchaseIn(ctx, row)
	}
	return s.unapprovePurchaseIn(ctx, row)
}

// approvePurchaseIn 审核落账（spec §4.2/§4.4）：事务内先乐观认领本单状态
// （防并发双重审核）→ 锁挂单订单行 → 订单必须仍为已审核（创建后被反审核则
// 409，认领随事务回滚）→ 占用校验全量复核 → 逐行经唯一入账入口入账
// （biz_type=purchase_in，正入）→ 重算订单进度（in_count 只计已审核）。
// 锁序与退货路径统一为 in→order（#98 审查 MINOR：避免与退货审核反向加锁
// 形成 1213 死锁）。
func (s *Service) approvePurchaseIn(ctx context.Context, row *model.PurchaseIn) error {
	warehouseName, err := s.warehouseName(ctx, row.WarehouseID)
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdatePurchaseInStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		order, err := s.lockApprovedOrder(ctx, tx, row.OrderID)
		if err != nil {
			return err
		}
		doc, err := tx.FindPurchaseInWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		if err := s.checkOrderQuota(ctx, tx, order.ID, doc.ID, purchaseInItemDrafts(doc.Items)); err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, purchaseInItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.PurchaseInItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizPurchaseIn,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return recalcPurchaseOrderProgress(ctx, tx, order.ID)
	})
}

// unapprovePurchaseIn 反审核回滚（spec §4.1/§4.3/§4.4）：先乐观认领本单状态
// （UPDATE 即对本行加 X 锁，与退货审核的父单行锁串行化）→ 再锁挂单订单行
// （统一 in→order 锁序）→ 聚合已审核退货，存在则 409 并回滚认领（绝不留下
// 「入库单未审核 + 已审核退货单」组合，#98 审查 MINOR/MAJOR 2）→ 逐行经唯一
// 入账入口新增 purchase_in_CANCEL 反向流水（count 取负，原流水保留）回滚库存
// → 重算订单进度。
func (s *Service) unapprovePurchaseIn(ctx context.Context, row *model.PurchaseIn) error {
	warehouseName, err := s.warehouseName(ctx, row.WarehouseID)
	if err != nil {
		return err
	}
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdatePurchaseInStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		if _, err := s.lockApprovedOrder(ctx, tx, row.OrderID); err != nil {
			return err
		}
		retSum, err := tx.SumPurchaseReturnItemsByProductForIn(ctx, row.ID, true, 0)
		if err != nil {
			return err
		}
		if len(retSum) > 0 {
			return xerr.Conflict("入库单存在已审核退货单，不可反审核")
		}
		// 财务反审核禁令（issue #94，spec §4.1/§4.7）：存在已审核付款单核销时
		// 不可反审核；付款单反审核释放额度后可退。放在认领之后：与付款审核的
		// 锁序一致（先目标单据行、后收付款行），防死锁。
		paymentItems, err := tx.CountApprovedFinancePaymentItemsByBiz(ctx, model.FinanceBizPurchaseIn, row.ID)
		if err != nil {
			return err
		}
		if paymentItems > 0 {
			return xerr.Conflict("入库单存在已审核付款单，不可反审核")
		}
		doc, err := tx.FindPurchaseInWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, purchaseInItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.PurchaseInItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizPurchaseInCancel,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return recalcPurchaseOrderProgress(ctx, tx, row.OrderID)
	})
}

// ExportPurchaseIns 导出采购入库单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportPurchaseIns(ctx context.Context, req *dto.PurchaseInPageReq) ([]byte, error) {
	filter, err := s.purchaseInFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllPurchaseIns(ctx, filter)
	if err != nil {
		return nil, err
	}
	supplierIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	orderIDs := make([]int64, 0, len(list))
	for _, row := range list {
		supplierIDs = append(supplierIDs, row.SupplierID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
		orderIDs = append(orderIDs, row.OrderID)
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, supplierIDs)
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	orderNos, err := s.repo.FindPurchaseOrderNosIn(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.PurchaseIn) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	out := make([]dto.PurchaseInExportRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.PurchaseInExportRow{
			No:            row.No,
			Supplier:      supplierNames[row.SupplierID],
			OrderNo:       orderNos[row.OrderID],
			Warehouse:     warehouseNames[row.WarehouseID],
			InTime:        row.InTime,
			TotalPrice:    row.TotalPrice,
			PaymentPrice:  row.PaymentPrice,
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(out, excel.ExportOptions{NativeDates: true})
}

// findPurchaseIn 取入库单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findPurchaseIn(ctx context.Context, id int64) (*model.PurchaseIn, error) {
	row, err := s.repo.FindPurchaseInByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("采购入库单不存在")
	}
	if err := s.checkRowVisible(ctx, "采购入库单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// ---------- 采购域共享助手（purchase_return.go 复用） ----------

// lockApprovedOrder 锁定挂单订单行并校验已审核（spec §4.4：入库/退货必挂已审核
// 订单；行锁串行化同订单的一切占量写路径）。订单不存在（含跨租户）或未审核
// 一律 409，不泄露存在性细节。
func (s *Service) lockApprovedOrder(ctx context.Context, tx *repository.Repository, orderID int64) (*model.PurchaseOrder, error) {
	order, err := tx.LockPurchaseOrder(ctx, orderID)
	if err != nil || order == nil {
		// 不存在（含跨租户，租户插件过滤后未命中）与未审核一律 409，不泄露存在性。
		return nil, xerr.Conflict("采购订单不存在或未审核")
	}
	if order.Status != model.DocStatusApproved {
		return nil, xerr.Conflict("采购订单不存在或未审核")
	}
	return order, nil
}

// checkOrderQuota 订单余量占用校验（spec §4.4）：剩余可入 = Σ订单行数量 −
// （未审核+已审核入库数量）−（未审核+已审核退货数量），超量 409。必须在持有
// 订单行锁的事务内调用（聚合读与写入在同一串行化域）。
func (s *Service) checkOrderQuota(ctx context.Context, tx *repository.Repository, orderID int64, excludeInID int64, drafts []docItemDraft) error {
	orderItems, err := tx.FindPurchaseOrderItems(ctx, orderID)
	if err != nil {
		return err
	}
	occupiedIn, err := tx.SumPurchaseInItemsByProduct(ctx, orderID, false, excludeInID)
	if err != nil {
		return err
	}
	occupiedReturn, err := tx.SumPurchaseReturnItemsByProductForOrder(ctx, orderID, false)
	if err != nil {
		return err
	}
	productIDs := make([]int64, 0, len(orderItems))
	for _, item := range orderItems {
		productIDs = append(productIDs, item.ProductID)
	}
	productNames, err := tx.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return err
	}
	return checkPurchaseInOccupancy(orderItems, occupiedIn, occupiedReturn, drafts, productNames)
}

// recalcPurchaseOrderProgress 重算订单明细进度并回写（spec §4.4：in_count/
// return_count 只计已审核单，审核/反审核时重算）。
func recalcPurchaseOrderProgress(ctx context.Context, tx *repository.Repository, orderID int64) error {
	items, err := tx.FindPurchaseOrderItems(ctx, orderID)
	if err != nil {
		return err
	}
	inSum, err := tx.SumPurchaseInItemsByProduct(ctx, orderID, true, 0)
	if err != nil {
		return err
	}
	retSum, err := tx.SumPurchaseReturnItemsByProductForOrder(ctx, orderID, true)
	if err != nil {
		return err
	}
	// 同产品跨多行按明细 ID 序分摊（与销售 fillOrderProgress 共用纯函数），
	// 聚合量不会写给每一行导致 Σ in_count 虚高（spec §4.4 进度回写口径）。
	fillProgressByItemOrder(items,
		func(it model.PurchaseOrderItem) int64 { return it.ProductID },
		func(it model.PurchaseOrderItem) qty.Qty { return it.Count },
		inSum, retSum,
		func(i int, inCount, returnCount qty.Qty) {
			items[i].InCount = inCount
			items[i].ReturnCount = returnCount
		})
	for _, item := range items {
		if err := tx.UpdatePurchaseOrderItemProgress(ctx, item.ID, item.InCount, item.ReturnCount); err != nil {
			return err
		}
	}
	return nil
}

// warehouseName 409 文案用的仓库名（缺省回退 ID 展示）。
func (s *Service) warehouseName(ctx context.Context, warehouseID int64) (string, error) {
	names, err := s.repo.FindWarehouseNamesIn(ctx, []int64{warehouseID})
	if err != nil {
		return "", err
	}
	return names[warehouseID], nil
}

func buildPurchaseInItemRows(docID int64, drafts []docItemDraft) []model.PurchaseInItem {
	rows := make([]model.PurchaseInItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.PurchaseInItem{
			PurchaseInID: docID,
			ProductID:    d.ProductID,
			Count:        d.Count,
			ProductPrice: d.ProductPrice,
			TotalPrice:   d.TotalPrice,
		})
	}
	return rows
}

// sumDraftTotalPrice 主表金额 = 明细行金额合计（服务端计算冗余，spec §3.3）。
// 单笔行金额已过 MulQty 范围校验，合计仍可能越 decimal(12,2) 界（多行大额），
// 越界返回 400。
func sumDraftTotalPrice(drafts []docItemDraft) (money.Money, error) {
	total := money.FromCents(0)
	for _, d := range drafts {
		next, err := total.Add(d.TotalPrice)
		if err != nil {
			return money.FromCents(0), xerr.BadRequest("单据金额合计超出范围（明细行金额之和）")
		}
		total = next
	}
	return total, nil
}
