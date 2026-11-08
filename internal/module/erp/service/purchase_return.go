// purchase_return.go 是采购退货单（RT）的业务方法（issue #91，docs/erp-mvp-spec.md
// §3.3/§4.1/§4.4/§4.8/§5）：必挂已审核入库单、仓库取自入库单（入参不收）、审核
// 经唯一入账入口减库存并回写订单进度（经入库单关联）、反审核经反向流水回滚。
// 占用校验（退货占入库单余量，并计入订单的退货占用）在创建/编辑/审核时全量
// 执行；行锁纪律统一为入库单行→订单行（in→order，与入库审核/反审核同序，
// 防死锁；#98 审查修正注释与代码一致）。
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
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// purchaseReturnKernel 构造采购退货单内核：锁序统一为入库单行→订单行→自身
// （BeforeClaim/BeforeUpdateLock 先锁父单，再认领/锁自身）；反审核用
// READ COMMITTED（付款核销禁令的聚合需要当前读）。
func (s *Service) purchaseReturnKernel() *doc.Kernel[*model.PurchaseReturn, model.PurchaseReturnItem] {
	return doc.New(docEnv{s}, s.purchaseReturnStore(), doc.Spec[*model.PurchaseReturn, model.PurchaseReturnItem]{
		Labels:             doc.Labels{Doc: "采购退货单", Item: "退货明细"},
		NoPrefix:           PurchaseReturnNoPrefix,
		SeqBizType:         SeqBizTypePurchaseReturn,
		UnapproveIsolation: doc.IsolationReadCommitted,
		BeforeClaim:        s.lockPurchaseReturnParents,
		Approve:            s.approvePurchaseReturnDoc,
		Unapprove:          s.unapprovePurchaseReturnDoc,
	})
}

// purchaseReturnStore 装配采购退货单的泛型存储。
func (s *Service) purchaseReturnStore() doc.Store[*model.PurchaseReturn, model.PurchaseReturnItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.PurchaseReturn, model.PurchaseReturnItem]{
		New:     func() *model.PurchaseReturn { return &model.PurchaseReturn{} },
		ItemsFK: "purchase_return_id",
	})
}

// ListPurchaseReturns 采购退货单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListPurchaseReturns(ctx context.Context, req *dto.PurchaseReturnPageReq) ([]dto.PurchaseReturnRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.purchaseReturnFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListPurchaseReturns(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.assemblePurchaseReturnRows(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// purchaseReturnFilter 构造退货单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) purchaseReturnFilter(ctx context.Context, req *dto.PurchaseReturnPageReq) (repository.PurchaseReturnFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.PurchaseReturnFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.PurchaseReturnFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.PurchaseReturnFilter{}, err
	}
	filter := repository.PurchaseReturnFilter{
		No:         req.No,
		InId:       req.InId,
		SupplierId: req.SupplierId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assemblePurchaseReturnRows 批量解析供应商/仓库/挂单入库号并组装展示行。
func (s *Service) assemblePurchaseReturnRows(ctx context.Context, list []model.PurchaseReturn) ([]dto.PurchaseReturnRow, error) {
	supplierIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	inIDs := make([]int64, 0, len(list))
	for _, row := range list {
		supplierIDs = append(supplierIDs, row.SupplierID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
		inIDs = append(inIDs, row.InID)
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, supplierIDs)
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	inNos, err := s.repo.FindPurchaseInNosIn(ctx, inIDs)
	if err != nil {
		return nil, err
	}
	out := make([]dto.PurchaseReturnRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.PurchaseReturnRow{
			ID:            row.ID,
			No:            row.No,
			SupplierId:    row.SupplierID,
			SupplierName:  supplierNames[row.SupplierID],
			InId:          row.InID,
			InNo:          inNos[row.InID],
			WarehouseId:   row.WarehouseID,
			WarehouseName: warehouseNames[row.WarehouseID],
			ReturnTime:    row.ReturnTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			TotalPrice:    row.TotalPrice,
			RefundPrice:   row.RefundPrice,
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, nil
}

// GetPurchaseReturn 采购退货单详情（带头 + 明细）。
func (s *Service) GetPurchaseReturn(ctx context.Context, id int64) (*dto.PurchaseReturnDetail, error) {
	full, err := s.purchaseReturnKernel().FindWithItems(ctx, id)
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
	inNos, err := s.repo.FindPurchaseInNosIn(ctx, []int64{full.InID})
	if err != nil {
		return nil, err
	}
	items := make([]dto.PurchaseReturnItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.PurchaseReturnItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return &dto.PurchaseReturnDetail{
		ID:            full.ID,
		No:            full.No,
		SupplierId:    full.SupplierID,
		SupplierName:  supplierNames[full.SupplierID],
		InId:          full.InID,
		InNo:          inNos[full.InID],
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		ReturnTime:    full.ReturnTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		TotalPrice:    full.TotalPrice,
		RefundPrice:   full.RefundPrice,
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreatePurchaseReturn 创建采购退货单（仅未审核）：必挂已审核入库单（409）、
// 供应商与入库单一致、仓库取自入库单（入参不收仓库字段）→ 入库单余量占用
// 校验（未审核退货也占量，超量 409）→ 事务内发放单号（RT）→ 落主表 + 明细。
// 不动库存（spec §4.1：只有审核落账）。
func (s *Service) CreatePurchaseReturn(ctx context.Context, req *dto.PurchaseReturnCreateReq) (*model.PurchaseReturn, error) {
	items, err := buildDocItems(req.Items, "退货明细")
	if err != nil {
		return nil, err
	}
	returnTime, err := s.parseDocTime(req.ReturnTime, "退货时间")
	if err != nil {
		return nil, err
	}
	var (
		warehouseID int64
		totalPrice  money.Money
	)
	return s.purchaseReturnKernel().Create(ctx,
		func(ctx context.Context, tx doc.Tx[*model.PurchaseReturn, model.PurchaseReturnItem]) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			inDoc, err := s.lockApprovedPurchaseIn(ctx, repo, req.InId)
			if err != nil {
				return err
			}
			// 锁序纪律：先入库单行、后订单行（in→order，与入库审核/反审核同序）。
			if _, err := s.lockApprovedOrder(ctx, repo, inDoc.OrderID); err != nil {
				return err
			}
			if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, inDoc.SupplierID, items); err != nil {
				return err
			}
			if err := s.checkDocRefsForWrite(ctx, inDoc.WarehouseID, items); err != nil {
				return err
			}
			if err := s.checkInReturnQuota(ctx, repo, inDoc.ID, 0, items); err != nil {
				return err
			}
			warehouseID = inDoc.WarehouseID
			totalPrice, err = sumDraftTotalPrice(items)
			return err
		},
		func(ctx context.Context) (*model.PurchaseReturn, error) {
			// 仓库取自入库单（spec §3.3），入参不收仓库字段。
			return &model.PurchaseReturn{
				SupplierID:  req.SupplierId,
				InID:        req.InId,
				WarehouseID: warehouseID,
				ReturnTime:  returnTime,
				TotalPrice:  totalPrice,
				Remark:      req.Remark,
			}, nil
		},
		func(ctx context.Context, d *model.PurchaseReturn) ([]model.PurchaseReturnItem, error) {
			return buildPurchaseReturnItemRows(d.ID, items), nil
		},
	)
}

// UpdatePurchaseReturn 修改采购退货单（仅未审核，已审核 409，spec §4.1）：
// 挂单入库不可更换（改挂等于换单，删除重建）；仓库随挂单入库取值；明细整体
// 替换；替换后全量占用校验（失败随事务回滚）。
func (s *Service) UpdatePurchaseReturn(ctx context.Context, req *dto.PurchaseReturnUpdateReq) error {
	var items []docItemDraft
	var returnTime time.Time
	return s.purchaseReturnKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.PurchaseReturn) error {
			if req.InId != d.InID {
				return xerr.BadRequest("采购退货单不可更换挂单入库单，请删除后重建")
			}
			var err error
			items, err = buildDocItems(req.Items, "退货明细")
			if err != nil {
				return err
			}
			returnTime, err = s.parseDocTime(req.ReturnTime, "退货时间")
			return err
		},
		s.lockPurchaseReturnParents,
		func(ctx context.Context, tx doc.Tx[*model.PurchaseReturn, model.PurchaseReturnItem], d *model.PurchaseReturn) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			// BeforeUpdateLock 已按 in→order 锁父单；这里重取（同事务重入无副作用）
			// 并用父单快照做仓库/供应商一致性校验。
			inDoc, err := s.lockApprovedPurchaseIn(ctx, repo, d.InID)
			if err != nil {
				return err
			}
			if _, err := s.lockApprovedOrder(ctx, repo, inDoc.OrderID); err != nil {
				return err
			}
			if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, inDoc.SupplierID, items); err != nil {
				return err
			}
			if err := s.checkDocRefsForWrite(ctx, inDoc.WarehouseID, items); err != nil {
				return err
			}
			// 占用校验按 drafts + excludeReturnID 口径（与替换后全量一致）。
			if err := s.checkInReturnQuota(ctx, repo, inDoc.ID, d.ID, items); err != nil {
				return err
			}
			totalPrice, err := sumDraftTotalPrice(items)
			if err != nil {
				return err
			}
			d.SupplierID = req.SupplierId
			d.WarehouseID = inDoc.WarehouseID
			d.ReturnTime = returnTime
			d.TotalPrice = totalPrice
			d.Remark = req.Remark
			return nil
		},
		func(ctx context.Context, d *model.PurchaseReturn) ([]model.PurchaseReturnItem, error) {
			return buildPurchaseReturnItemRows(d.ID, items), nil
		},
	)
}

// DeletePurchaseReturn 删除采购退货单（仅未审核，已审核 409，spec §4.1）：
// 主表 + 明细一并软删除；未审核未落账不动库存，删除自然释放入库单与订单占用
// （spec §4.4：删除未审核单自然释放占用）。
func (s *Service) DeletePurchaseReturn(ctx context.Context, id int64) error {
	return s.purchaseReturnKernel().Delete(ctx, id)
}

// UpdatePurchaseReturnStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdatePurchaseReturnStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	return s.purchaseReturnKernel().SetStatus(ctx, req.ID, req.Status)
}

// lockPurchaseReturnParents 在认领/锁自身之前先锁「入库单行 → 订单行」，
// 统一退货路径的 in→order→self 锁序（#98 审查修正）。
func (s *Service) lockPurchaseReturnParents(ctx context.Context, tx doc.Tx[*model.PurchaseReturn, model.PurchaseReturnItem], d *model.PurchaseReturn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	inDoc, err := s.lockApprovedPurchaseIn(ctx, repo, d.InID)
	if err != nil {
		return err
	}
	_, err = s.lockApprovedOrder(ctx, repo, inDoc.OrderID)
	return err
}

// approvePurchaseReturnDoc 审核落账（内核已按 in→order→self 锁序认领并装载
// 明细）：复核父单仓库/供应商快照 → 退货占用全量复核 → 逐行经唯一入账入口
// 出账（biz_type=purchase_return，负出）→ 重算订单进度。
func (s *Service) approvePurchaseReturnDoc(ctx context.Context, tx doc.Tx[*model.PurchaseReturn, model.PurchaseReturnItem], d *model.PurchaseReturn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	inDoc, err := s.lockApprovedPurchaseIn(ctx, repo, d.InID)
	if err != nil {
		return err
	}
	order, err := s.lockApprovedOrder(ctx, repo, inDoc.OrderID)
	if err != nil {
		return err
	}
	// 父单一致性复核（#98 审查 MAJOR 1，与销售退货镜像）：退货行仓库/供应商
	// 是创建时入库单快照，父单反审核后可被编辑，直接按快照落账会在旧仓凭空
	// 增减。父单编辑守卫（UpdatePurchaseIn）挡住正常路径，这里入账前再复核
	// 一次，防御绕过服务层的脏数据（直接改库）。
	if d.WarehouseID != inDoc.WarehouseID {
		return xerr.Conflict("采购退货单仓库与入库单不一致，请删除后重建")
	}
	if d.SupplierID != inDoc.SupplierID {
		return xerr.Conflict("采购退货单供应商与入库单不一致，请删除后重建")
	}
	if err := s.checkInReturnQuota(ctx, repo, inDoc.ID, d.ID, purchaseReturnItemDrafts(d.Items)); err != nil {
		return err
	}
	warehouseName, err := s.warehouseName(ctx, d.WarehouseID)
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, purchaseReturnItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.PurchaseReturnItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         negQty(item.Count),
			BizType:       model.StockRecordBizPurchaseReturn,
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

// unapprovePurchaseReturnDoc 反审核回滚（内核已按 in→order→self 锁序认领并
// 装载明细，READ COMMITTED）：付款核销禁令 → 逐行新增 purchase_return_CANCEL
// 反向流水（count 取正，原流水保留）→ 重算订单进度。
func (s *Service) unapprovePurchaseReturnDoc(ctx context.Context, tx doc.Tx[*model.PurchaseReturn, model.PurchaseReturnItem], d *model.PurchaseReturn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	inDoc, err := s.lockApprovedPurchaseIn(ctx, repo, d.InID)
	if err != nil {
		return err
	}
	order, err := s.lockApprovedOrder(ctx, repo, inDoc.OrderID)
	if err != nil {
		return err
	}
	// 财务反审核禁令（issue #94，spec §4.1/§4.7）：存在已审核付款单核销时
	// 不可反审核；付款单反审核释放额度后可退。放在认领之后：与付款审核的
	// 锁序一致（先目标单据行、后收付款行），防死锁。
	paymentItems, err := repo.CountApprovedFinancePaymentItemsByBiz(ctx, model.FinanceBizPurchaseReturn, d.ID)
	if err != nil {
		return err
	}
	if paymentItems > 0 {
		return xerr.Conflict("退货单存在已审核付款单，不可反审核")
	}
	warehouseName, err := s.warehouseName(ctx, d.WarehouseID)
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, purchaseReturnItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.PurchaseReturnItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         item.Count,
			BizType:       model.StockRecordBizPurchaseReturnCancel,
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

// ExportPurchaseReturns 导出采购退货单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportPurchaseReturns(ctx context.Context, req *dto.PurchaseReturnPageReq) ([]byte, error) {
	filter, err := s.purchaseReturnFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllPurchaseReturns(ctx, filter)
	if err != nil {
		return nil, err
	}
	supplierIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	inIDs := make([]int64, 0, len(list))
	for _, row := range list {
		supplierIDs = append(supplierIDs, row.SupplierID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
		inIDs = append(inIDs, row.InID)
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, supplierIDs)
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	inNos, err := s.repo.FindPurchaseInNosIn(ctx, inIDs)
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.PurchaseReturn) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	out := make([]dto.PurchaseReturnExportRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.PurchaseReturnExportRow{
			No:            row.No,
			Supplier:      supplierNames[row.SupplierID],
			InNo:          inNos[row.InID],
			Warehouse:     warehouseNames[row.WarehouseID],
			ReturnTime:    row.ReturnTime,
			TotalPrice:    row.TotalPrice,
			RefundPrice:   row.RefundPrice,
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(out, excel.ExportOptions{NativeDates: true})
}

// findPurchaseReturn 取退货单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findPurchaseReturn(ctx context.Context, id int64) (*model.PurchaseReturn, error) {
	return s.purchaseReturnKernel().Find(ctx, id)
}

// lockApprovedPurchaseIn 锁定挂单入库单行并校验已审核（spec §4.4：退货必挂已审核
// 入库单；行锁串行化同入库单的一切退货写路径）。入库单不存在（含跨租户）或
// 未审核一律 409。
func (s *Service) lockApprovedPurchaseIn(ctx context.Context, tx *repository.Repository, inID int64) (*model.PurchaseIn, error) {
	inDoc, err := tx.LockPurchaseIn(ctx, inID)
	if err != nil || inDoc == nil {
		// 不存在（含跨租户，租户插件过滤后未命中）与未审核一律 409，不泄露存在性。
		return nil, xerr.Conflict("采购入库单不存在或未审核")
	}
	if inDoc.Status != model.DocStatusApproved {
		return nil, xerr.Conflict("采购入库单不存在或未审核")
	}
	return inDoc, nil
}

// checkInReturnQuota 入库单余量的退货占用校验（spec §4.4/§8.2）：剩余可退 =
// 入库行数量 −（未审核+已审核退货数量），超量 409；未审核退货也占量。必须在
// 持有入库单行锁（与订单行锁）的事务内调用。excludeReturnID 为本单自身 ID
// （编辑/审核时排除重复计数，同 checkOrderQuota；创建时传 0）。
func (s *Service) checkInReturnQuota(ctx context.Context, tx *repository.Repository, inID int64, excludeReturnID int64, drafts []docItemDraft) error {
	inDoc, err := tx.FindPurchaseInWithItems(ctx, inID)
	if err != nil {
		return err
	}
	occupiedReturn, err := tx.SumPurchaseReturnItemsByProductForIn(ctx, inID, false, excludeReturnID)
	if err != nil {
		return err
	}
	productIDs := make([]int64, 0, len(inDoc.Items))
	for _, item := range inDoc.Items {
		productIDs = append(productIDs, item.ProductID)
	}
	productNames, err := tx.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return err
	}
	return checkPurchaseReturnOccupancy(inDoc.Items, occupiedReturn, drafts, productNames)
}

func buildPurchaseReturnItemRows(docID int64, drafts []docItemDraft) []model.PurchaseReturnItem {
	rows := make([]model.PurchaseReturnItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.PurchaseReturnItem{
			PurchaseReturnID: docID,
			ProductID:        d.ProductID,
			Count:            d.Count,
			ProductPrice:     d.ProductPrice,
			TotalPrice:       d.TotalPrice,
		})
	}
	return rows
}

// purchaseInItemDrafts 把已落库的入库单明细换算为占用校验的草稿形态
// （行金额不变量在落库前已校验，此处只取数量口径）。
func purchaseInItemDrafts(items []model.PurchaseInItem) []docItemDraft {
	drafts := make([]docItemDraft, 0, len(items))
	for _, item := range items {
		drafts = append(drafts, docItemDraft{
			ProductID:    item.ProductID,
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return drafts
}

func purchaseInItemProducts(items []model.PurchaseInItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}

// purchaseReturnItemDrafts 同上（退货单明细形态）。
func purchaseReturnItemDrafts(items []model.PurchaseReturnItem) []docItemDraft {
	drafts := make([]docItemDraft, 0, len(items))
	for _, item := range items {
		drafts = append(drafts, docItemDraft{
			ProductID:    item.ProductID,
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return drafts
}

func purchaseReturnItemProducts(items []model.PurchaseReturnItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}
