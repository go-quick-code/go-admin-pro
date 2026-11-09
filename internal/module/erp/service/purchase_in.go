// purchase_in.go 是采购入库单（RK）的业务方法（issue #91，docs/erp-mvp-spec.md
// §3.3/§4.1/§4.4/§4.8/§5）：必挂已审核订单、审核经唯一入账入口增库存并回写订单
// 进度、反审核经反向流水回滚。占用校验（未审核单也占量）在创建/编辑/审核时
// 全量执行，防并发双占的行锁纪律：涉及既有入库单行的写路径统一先锁入库单行、
// 再锁挂单订单行（in→order，与退货路径同序，防死锁；#98 审查修正）。
package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service/doc"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// purchaseInKernel 构造采购入库单的内核实例：审核走默认隔离级别，反审核用
// READ COMMITTED（与既有口径一致；退货/付款禁令的聚合需要当前读）。
func (s *Service) purchaseInKernel() *doc.Kernel[*model.PurchaseIn, model.PurchaseInItem] {
	return doc.New(docEnv{s}, s.purchaseInStore(), doc.Spec[*model.PurchaseIn, model.PurchaseInItem]{
		Labels:     doc.Labels{Doc: "采购入库单"},
		NoPrefix:   PurchaseInNoPrefix,
		SeqBizType: SeqBizTypePurchaseIn,
		// 审核也必须 READ COMMITTED：内核在订单锁之前先做普通 SELECT 装载明细，
		// RR 快照会漏掉等待订单锁期间另一张入库单已提交的 status=20，重算
		// in_count 时覆盖成单边值（#119 P0，并发回归测试已复现）。
		ApproveIsolation:   doc.IsolationReadCommitted,
		UnapproveIsolation: doc.IsolationReadCommitted,
		Approve:            s.approvePurchaseInDoc,
		Unapprove:          s.unapprovePurchaseInDoc,
		BeforeDelete:       s.beforeDeletePurchaseIn,
	})
}

// purchaseInStore 装配采购入库单的泛型存储（候选 02a）。
func (s *Service) purchaseInStore() doc.Store[*model.PurchaseIn, model.PurchaseInItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.PurchaseIn, model.PurchaseInItem]{
		New:          func() *model.PurchaseIn { return &model.PurchaseIn{} },
		ItemsFK:      "purchase_in_id",
		SetItemDocID: func(it *model.PurchaseInItem, id int64) { it.PurchaseInID = id },
	})
}

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
	full, err := s.purchaseInKernel().FindWithItems(ctx, id)
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

// CreatePurchaseIn 创建采购入库单（仅未审核）：内核事务内 prepare 先锁已审核
// 订单（409）、校验供应商/仓库/产品引用、订单余量占用（未审核单也占量），
// 再发号落主表与明细。不动库存（spec §4.1：只有审核落账）。
func (s *Service) CreatePurchaseIn(ctx context.Context, req *dto.PurchaseInCreateReq) (*model.PurchaseIn, error) {
	items, err := buildDocItems(req.Items, "入库明细")
	if err != nil {
		return nil, err
	}
	inTime, err := s.parseDocTime(req.InTime, "入库时间")
	if err != nil {
		return nil, err
	}
	var totalPrice money.Money
	return s.purchaseInKernel().Create(ctx,
		func(ctx context.Context, tx doc.Tx[*model.PurchaseIn, model.PurchaseInItem]) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			order, err := s.lockApprovedOrder(ctx, repo, req.OrderId)
			if err != nil {
				return err
			}
			if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, order.SupplierID, items); err != nil {
				return err
			}
			if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
				return err
			}
			if err := s.checkOrderQuota(ctx, repo, order.ID, 0, items); err != nil {
				return err
			}
			totalPrice, err = sumDraftTotalPrice(items)
			return err
		},
		func(ctx context.Context) (*model.PurchaseIn, error) {
			return &model.PurchaseIn{
				SupplierID:  req.SupplierId,
				OrderID:     req.OrderId,
				WarehouseID: req.WarehouseId,
				InTime:      inTime,
				TotalPrice:  totalPrice,
				Remark:      req.Remark,
			}, nil
		},
		func(ctx context.Context, d *model.PurchaseIn) ([]model.PurchaseInItem, error) {
			return buildPurchaseInItemRows(d.ID, items), nil
		},
	)
}

// UpdatePurchaseIn 修改采购入库单（仅未审核，已审核 409，spec §4.1）：
// 挂单订单不可更换（改挂等于换单，删除重建）；内核锁自身行后由 mutate 做
// 退货引用禁令、订单锁与全量占用校验；明细整体替换由内核完成。存在退货引用
// （含未审核）时禁止修改仓库/供应商（409，退货行是父单快照，#98 审查）。
func (s *Service) UpdatePurchaseIn(ctx context.Context, req *dto.PurchaseInUpdateReq) error {
	var items []docItemDraft
	var inTime time.Time
	return s.purchaseInKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.PurchaseIn) error {
			if req.OrderId != d.OrderID {
				return xerr.BadRequest("采购入库单不可更换挂单订单，请删除后重建")
			}
			var err error
			items, err = buildDocItems(req.Items, "入库明细")
			if err != nil {
				return err
			}
			inTime, err = s.parseDocTime(req.InTime, "入库时间")
			return err
		},
		nil,
		func(ctx context.Context, tx doc.Tx[*model.PurchaseIn, model.PurchaseInItem], d *model.PurchaseIn) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			// 存在任何退货引用（含未审核）时，禁止修改仓库与供应商——退货行
			// 保存的是创建时父单快照，父单漂移会让退货审核在旧仓/旧供应商
			// 口径下落账（#98 审查 MAJOR 1）。其余字段照常可改。
			retRefs, err := repo.CountPurchaseReturnsByIn(ctx, d.ID, 0)
			if err != nil {
				return err
			}
			if retRefs > 0 {
				if req.WarehouseId != d.WarehouseID {
					return xerr.Conflict("采购入库单存在退货单引用，禁止修改仓库")
				}
				if req.SupplierId != d.SupplierID {
					return xerr.Conflict("采购入库单存在退货单引用，禁止修改供应商")
				}
			}
			order, err := s.lockApprovedOrder(ctx, repo, d.OrderID)
			if err != nil {
				return err
			}
			if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, order.SupplierID, items); err != nil {
				return err
			}
			if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
				return err
			}
			// 占用校验按 drafts + excludeInID 口径（与替换后全量一致）；失败随
			// 内核事务回滚，旧明细与主表保持原样。
			if err := s.checkOrderQuota(ctx, repo, order.ID, d.ID, items); err != nil {
				return err
			}
			totalPrice, err := sumDraftTotalPrice(items)
			if err != nil {
				return err
			}
			d.SupplierID = req.SupplierId
			d.WarehouseID = req.WarehouseId
			d.InTime = inTime
			d.TotalPrice = totalPrice
			d.Remark = req.Remark
			return nil
		},
		func(ctx context.Context, d *model.PurchaseIn) ([]model.PurchaseInItem, error) {
			return buildPurchaseInItemRows(d.ID, items), nil
		},
	)
}

// DeletePurchaseIn 删除采购入库单（仅未审核，已审核 409，spec §4.1/§4.9）：
// 主表 + 明细一并软删除；未审核未落账不动库存，删除自然释放订单占用
// （spec §4.4）。存在退货单引用（含未审核）时禁删——挂单引用悬空会让退货
// 占用校验失去锚点（与销售出库对称）。
func (s *Service) DeletePurchaseIn(ctx context.Context, id int64) error {
	return s.purchaseInKernel().Delete(ctx, id)
}

// beforeDeletePurchaseIn 下游禁令：存在退货单引用（含未审核）时禁删——
// 挂单引用悬空会让退货占用校验失去锚点（与销售出库对称）。
func (s *Service) beforeDeletePurchaseIn(ctx context.Context, tx doc.Tx[*model.PurchaseIn, model.PurchaseInItem], d *model.PurchaseIn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	retRefs, err := repo.CountPurchaseReturnsByIn(ctx, d.ID, 0)
	if err != nil {
		return err
	}
	if retRefs > 0 {
		return xerr.Conflict("采购入库单存在退货单引用，禁止删除")
	}
	return nil
}

// UpdatePurchaseInStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdatePurchaseInStatus(ctx context.Context, req *dto.DocUpdateStatusReq) error {
	return s.purchaseInKernel().SetStatus(ctx, req.ID, req.Status)
}

// approvePurchaseInDoc 审核落账（内核已认领 status=10→20 并装载明细）：
// 锁挂单订单行（in→order 锁序）→ 订单仍须已审核 → 占用校验全量复核 →
// 逐行经唯一入账入口入账（biz_type=purchase_in，正入）→ 重算订单进度。
func (s *Service) approvePurchaseInDoc(ctx context.Context, tx doc.Tx[*model.PurchaseIn, model.PurchaseInItem], d *model.PurchaseIn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	order, err := s.lockApprovedOrder(ctx, repo, d.OrderID)
	if err != nil {
		return err
	}
	if err := s.checkOrderQuota(ctx, repo, order.ID, d.ID, purchaseInItemDrafts(d.Items)); err != nil {
		return err
	}
	warehouseName, err := s.warehouseNameIn(ctx, repo, d.WarehouseID)
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, purchaseInItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.PurchaseInItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         item.Count,
			BizType:       model.StockRecordBizPurchaseIn,
			BizID:         d.ID,
			BizItemID:     item.ID,
			BizNo:         d.No,
			ProductName:   productNames[item.ProductID],
			WarehouseName: warehouseName,
		}); err != nil {
			return err
		}
	}
	return recalcPurchaseOrderProgress(ctx, repo, order.ID)
}

// unapprovePurchaseInDoc 反审核回滚（内核已认领 status=20→10 并装载明细，
// 事务隔离级别为 READ COMMITTED）：锁挂单订单行（in→order 锁序）→ 聚合已
// 审核退货，存在则 409（绝不留下「入库单未审核 + 已审核退货单」组合）→
// 财务付款核销禁令 → 逐行新增 purchase_in_CANCEL 反向流水 → 重算订单进度。
func (s *Service) unapprovePurchaseInDoc(ctx context.Context, tx doc.Tx[*model.PurchaseIn, model.PurchaseInItem], d *model.PurchaseIn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	if _, err := s.lockApprovedOrder(ctx, repo, d.OrderID); err != nil {
		return err
	}
	retSum, err := repo.SumPurchaseReturnItemsByProductForIn(ctx, d.ID, true, 0)
	if err != nil {
		return err
	}
	if len(retSum) > 0 {
		return xerr.Conflict("入库单存在已审核退货单，不可反审核")
	}
	// 财务反审核禁令（issue #94，spec §4.1/§4.7）：存在已审核付款单核销时
	// 不可反审核；付款单反审核释放额度后可退。放在认领之后：与付款审核的
	// 锁序一致（先目标单据行、后收付款行），防死锁。
	paymentItems, err := repo.CountApprovedFinancePaymentItemsByBiz(ctx, model.FinanceBizPurchaseIn, d.ID)
	if err != nil {
		return err
	}
	if paymentItems > 0 {
		return xerr.Conflict("入库单存在已审核付款单，不可反审核")
	}
	warehouseName, err := s.warehouseNameIn(ctx, repo, d.WarehouseID)
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, purchaseInItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.PurchaseInItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         negQty(item.Count),
			BizType:       model.StockRecordBizPurchaseInCancel,
			BizID:         d.ID,
			BizItemID:     item.ID,
			BizNo:         d.No,
			ProductName:   productNames[item.ProductID],
			WarehouseName: warehouseName,
		}); err != nil {
			return err
		}
	}
	return recalcPurchaseOrderProgress(ctx, repo, d.OrderID)
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

// warehouseNameIn 409 文案用的仓库名（在事务句柄内读取，缺省回退 ID 展示）。
func (s *Service) warehouseNameIn(ctx context.Context, repo *repository.Repository, warehouseID int64) (string, error) {
	names, err := repo.FindWarehouseNamesIn(ctx, []int64{warehouseID})
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
