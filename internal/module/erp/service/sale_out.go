// sale_out.go 是销售出库单（CK）的业务方法（issue #92，docs/erp-mvp-spec.md
// §3.4/§4.1/§4.5/§5）：必挂已审核销售订单（customer 与订单一致），审核经唯一
// 入账入口减库存并回写订单进度，反审核经 _CANCEL 反向流水回滚（存在已审核
// 退货单时禁令 409）。
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

// saleOutKernel 构造销售出库单内核：锁序为订单行→自身（BeforeClaim 先锁订单），
// 反审核用 READ COMMITTED（退货/收款禁令的聚合需要当前读）。
func (s *Service) saleOutKernel() *doc.Kernel[*model.SaleOut, model.SaleOutItem] {
	return doc.New(docEnv{s}, s.saleOutStore(), doc.Spec[*model.SaleOut, model.SaleOutItem]{
		Labels:             doc.Labels{Doc: "销售出库单"},
		NoPrefix:           SaleOutNoPrefix,
		SeqBizType:         SeqBizTypeSaleOut,
		UnapproveIsolation: doc.IsolationReadCommitted,
		BeforeClaim:        s.lockSaleOutOrderForClaim,
		Approve:            s.approveSaleOutDoc,
		Unapprove:          s.unapproveSaleOutDoc,
		BeforeDelete:       s.beforeDeleteSaleOut,
	})
}

// saleOutStore 装配销售出库单的泛型存储。
func (s *Service) saleOutStore() doc.Store[*model.SaleOut, model.SaleOutItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.SaleOut, model.SaleOutItem]{
		New:          func() *model.SaleOut { return &model.SaleOut{} },
		ItemsFK:      "sale_out_id",
		SetItemDocID: func(it *model.SaleOutItem, id int64) { it.SaleOutID = id },
	})
}

// ListSaleOuts 销售出库单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListSaleOuts(ctx context.Context, req *dto.SaleOutPageReq) ([]dto.SaleOutRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.saleOutFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListSaleOuts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return s.assembleSaleOutRows(ctx, list, total)
}

// saleOutFilter 构造出库单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) saleOutFilter(ctx context.Context, req *dto.SaleOutPageReq) (repository.SaleOutFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.SaleOutFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.SaleOutFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.SaleOutFilter{}, err
	}
	filter := repository.SaleOutFilter{
		No:          req.No,
		OrderId:     req.OrderId,
		CustomerId:  req.CustomerId,
		WarehouseId: req.WarehouseId,
		Status:      req.Status,
		StartTime:   start,
		EndTime:     end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assembleSaleOutRows 批量解析订单号/客户/仓库名并组装出库单分页行。
func (s *Service) assembleSaleOutRows(ctx context.Context, list []model.SaleOut, total int64) ([]dto.SaleOutRow, int64, error) {
	orderIDs := make([]int64, 0, len(list))
	customerIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	for _, row := range list {
		orderIDs = append(orderIDs, row.OrderID)
		customerIDs = append(customerIDs, row.CustomerID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
	}
	orderNos, err := s.repo.FindSaleOrderNosIn(ctx, orderIDs)
	if err != nil {
		return nil, 0, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, customerIDs)
	if err != nil {
		return nil, 0, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.SaleOutRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.SaleOutRow{
			ID:            row.ID,
			No:            row.No,
			OrderId:       row.OrderID,
			OrderNo:       orderNos[row.OrderID],
			CustomerId:    row.CustomerID,
			CustomerName:  customerNames[row.CustomerID],
			WarehouseId:   row.WarehouseID,
			WarehouseName: warehouseNames[row.WarehouseID],
			OutTime:       row.OutTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			TotalPrice:    row.TotalPrice,
			ReceiptPrice:  row.ReceiptPrice,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, total, nil
}

// GetSaleOut 销售出库单详情（带头 + 明细）。
func (s *Service) GetSaleOut(ctx context.Context, id int64) (*dto.SaleOutDetail, error) {
	full, err := s.saleOutKernel().FindWithItems(ctx, id)
	if err != nil {
		return nil, err
	}
	orderNos, err := s.repo.FindSaleOrderNosIn(ctx, []int64{full.OrderID})
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
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, []int64{full.CustomerID})
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{full.WarehouseID})
	if err != nil {
		return nil, err
	}
	items := make([]dto.SaleOutItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.SaleOutItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return &dto.SaleOutDetail{
		ID:            full.ID,
		No:            full.No,
		OrderId:       full.OrderID,
		OrderNo:       orderNos[full.OrderID],
		CustomerId:    full.CustomerID,
		CustomerName:  customerNames[full.CustomerID],
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		OutTime:       full.OutTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		TotalPrice:    full.TotalPrice,
		ReceiptPrice:  full.ReceiptPrice,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateSaleOut 创建销售出库单（仅未审核）：明细校验 → 挂单校验（已审核订单 +
// 客户一致）→ 仓库/产品存在性 → 订单行锁 + 占用校验（spec §4.5，超量 409）→
// 事务内发放单号（CK）→ 落主表 + 明细。不动库存（spec §4.1：只有审核落账）。
func (s *Service) CreateSaleOut(ctx context.Context, req *dto.SaleOutCreateReq) (*model.SaleOut, error) {
	items, err := buildDocItems(req.Items, "出库明细")
	if err != nil {
		return nil, err
	}
	outTime, err := s.parseDocTime(req.OutTime, "出库时间")
	if err != nil {
		return nil, err
	}
	if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
		return nil, err
	}
	var total money.Money
	return s.saleOutKernel().Create(ctx,
		func(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem]) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			order, err := s.lockSaleOutLink(ctx, repo, req.CustomerId, req.OrderId)
			if err != nil {
				return err
			}
			if err := s.checkSaleOutOccupancy(ctx, repo, order, items, 0); err != nil {
				return err
			}
			total, err = sumDraftTotal(items)
			return err
		},
		func(ctx context.Context) (*model.SaleOut, error) {
			return &model.SaleOut{
				CustomerID:  req.CustomerId,
				OrderID:     req.OrderId,
				WarehouseID: req.WarehouseId,
				OutTime:     outTime,
				TotalPrice:  total,
			}, nil
		},
		func(ctx context.Context, d *model.SaleOut) ([]model.SaleOutItem, error) {
			return buildSaleOutItemRows(d.ID, items), nil
		},
	)
}

// lockSaleOutLink 出库挂单校验并加锁（事务内）：订单存在（400）→ 已审核（409，
// spec §4.5）→ 客户一致（400，防跨客户错挂）→ 返回行锁的订单行（占用校验与
// 并发创建在同一订单行上串行化）。
func (s *Service) lockSaleOutLink(ctx context.Context, tx *repository.Repository, customerID, orderID int64) (*model.SaleOrder, error) {
	if err := s.checkSaleCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	order, err := tx.FindSaleOrderByIDForUpdate(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, xerr.BadRequest("销售订单不存在")
	}
	if order.Status != model.DocStatusApproved {
		return nil, xerr.Conflict("销售订单未审核，出库单必须挂已审核订单")
	}
	if order.CustomerID != customerID {
		return nil, xerr.BadRequest("出库单客户必须与销售订单客户一致")
	}
	return order, nil
}

// UpdateSaleOut 修改销售出库单（仅未审核，已审核 409，spec §4.1）：字段全量
// 覆盖（可换挂订单，客户/占用随新订单重验）；单号不采纳；明细整体替换。存在
// 退货引用（含未审核）时禁止修改仓库/客户（409，退货行是父单快照，#98 审查）。
func (s *Service) UpdateSaleOut(ctx context.Context, req *dto.SaleOutUpdateReq) error {
	var items []docItemDraft
	var outTime time.Time
	return s.saleOutKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.SaleOut) error {
			var err error
			items, err = buildDocItems(req.Items, "出库明细")
			if err != nil {
				return err
			}
			outTime, err = s.parseDocTime(req.OutTime, "出库时间")
			if err != nil {
				return err
			}
			return s.checkDocRefsForWrite(ctx, req.WarehouseId, items)
		},
		func(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem], d *model.SaleOut) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			_, err = s.lockSaleOutLink(ctx, repo, req.CustomerId, req.OrderId)
			return err
		},
		func(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem], d *model.SaleOut) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			order, err := s.lockSaleOutLink(ctx, repo, req.CustomerId, req.OrderId)
			if err != nil {
				return err
			}
			// 存在任何退货引用（含未审核）时，禁止修改仓库与客户——退货行保存
			// 的是创建时父单快照，父单漂移会让退货审核在旧仓/旧客户口径下落账
			// （#98 审查 MAJOR 1）。其余字段照常可改。
			retRefs, err := repo.CountSaleReturnsByOut(ctx, d.ID, 0)
			if err != nil {
				return err
			}
			if retRefs > 0 {
				if req.WarehouseId != d.WarehouseID {
					return xerr.Conflict("销售出库单存在退货单引用，禁止修改仓库")
				}
				if req.CustomerId != d.CustomerID {
					return xerr.Conflict("销售出库单存在退货单引用，禁止修改客户")
				}
			}
			if err := s.checkSaleOutOccupancy(ctx, repo, order, items, d.ID); err != nil {
				return err
			}
			total, err := sumDraftTotal(items)
			if err != nil {
				return err
			}
			d.CustomerID = req.CustomerId
			d.OrderID = req.OrderId
			d.WarehouseID = req.WarehouseId
			d.OutTime = outTime
			d.TotalPrice = total
			return nil
		},
		func(ctx context.Context, d *model.SaleOut) ([]model.SaleOutItem, error) {
			return buildSaleOutItemRows(d.ID, items), nil
		},
	)
}

// DeleteSaleOut 删除销售出库单（仅未审核，已审核 409，spec §4.1）：主表 + 明细
// 一并软删除；占用随删除自然释放（未审核占用计入聚合，软删行不计）；存在
// 退货单引用（含未审核）时禁删——挂单引用悬空会让占用校验失去锚点。
func (s *Service) DeleteSaleOut(ctx context.Context, id int64) error {
	return s.saleOutKernel().Delete(ctx, id)
}

// beforeDeleteSaleOut 下游禁令：存在退货单引用（含未审核）时禁删。
func (s *Service) beforeDeleteSaleOut(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem], d *model.SaleOut) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	retRefs, err := repo.CountSaleReturnsByOut(ctx, d.ID, 0)
	if err != nil {
		return err
	}
	if retRefs > 0 {
		return xerr.Conflict("销售出库单存在退货单引用，禁止删除")
	}
	return nil
}

// UpdateSaleOutStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateSaleOutStatus(ctx context.Context, req *dto.DocUpdateStatusReq) error {
	return s.saleOutKernel().SetStatus(ctx, req.ID, req.Status)
}

// lockSaleOutOrderForClaim 在认领前先锁挂单订单行（order→self 锁序，与
// 创建/编辑/审核一致）；存在性/状态由 Approve 钩子复核，反审核只需锁序。
func (s *Service) lockSaleOutOrderForClaim(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem], d *model.SaleOut) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	_, err = repo.FindSaleOrderByIDForUpdate(ctx, d.OrderID)
	return err
}

// approveSaleOutDoc 审核落账（内核已按 order→self 锁序认领并装载明细）：
// 复核挂单订单仍为已审核 → 逐行经唯一入账入口出账（biz_type=sale_out，负流水；
// 余额不足 409）→ 重算订单进度（out_count 只计已审核，spec §4.5）。
func (s *Service) approveSaleOutDoc(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem], d *model.SaleOut) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	// 已知窄窗口（P3，非本次引入）：编辑换父单时 beforeLock 锁新父单、审核
	// BeforeClaim 锁旧父单；若二者在「新父单 == 对方旧父单」上交叉，InnoDB 可能
	// 返回 1213。旧代码同样是父单→自身，语义未变；事务回滚，无数据丢失。
	order, err := repo.FindSaleOrderByIDForUpdate(ctx, d.OrderID)
	if err != nil {
		return err
	}
	if order == nil || order.Status != model.DocStatusApproved {
		return xerr.Conflict("销售订单不存在或未审核")
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.WarehouseID})
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, saleOutItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.SaleOutItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         negQty(item.Count),
			BizType:       model.StockRecordBizSaleOut,
			BizID:         d.ID,
			BizItemID:     item.ID,
			BizNo:         d.No,
			ProductName:   productNames[item.ProductID],
			WarehouseName: warehouseNames[d.WarehouseID],
		}); err != nil {
			return err
		}
	}
	return s.recomputeSaleOrderProgress(ctx, repo, d.OrderID)
}

// unapproveSaleOutDoc 反审核回滚（内核已按 order→self 锁序认领并装载明细，
// READ COMMITTED）：已审核退货禁令 → 已审核收款核销禁令 → 逐行新增 _CANCEL
// 反向流水（库存回补，原流水保留）→ 重算订单进度。
func (s *Service) unapproveSaleOutDoc(ctx context.Context, tx doc.Tx[*model.SaleOut, model.SaleOutItem], d *model.SaleOut) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	retRefs, err := repo.CountSaleReturnsByOut(ctx, d.ID, model.DocStatusApproved)
	if err != nil {
		return err
	}
	if retRefs > 0 {
		return xerr.Conflict("销售出库单存在已审核退货单，不可反审核")
	}
	receiptItems, err := repo.CountApprovedFinanceReceiptItemsByBiz(ctx, model.FinanceBizSaleOut, d.ID)
	if err != nil {
		return err
	}
	if receiptItems > 0 {
		return xerr.Conflict("销售出库单存在已审核收款单，不可反审核")
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.WarehouseID})
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, saleOutItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.SaleOutItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         item.Count,
			BizType:       model.StockRecordBizSaleOutCancel,
			BizID:         d.ID,
			BizItemID:     item.ID,
			BizNo:         d.No,
			ProductName:   productNames[item.ProductID],
			WarehouseName: warehouseNames[d.WarehouseID],
		}); err != nil {
			return err
		}
	}
	return s.recomputeSaleOrderProgress(ctx, repo, d.OrderID)
}

// ExportSaleOuts 导出销售出库单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportSaleOuts(ctx context.Context, req *dto.SaleOutPageReq) ([]byte, error) {
	filter, err := s.saleOutFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllSaleOuts(ctx, filter)
	if err != nil {
		return nil, err
	}
	orderNos, err := s.repo.FindSaleOrderNosIn(ctx, saleOutOrderIDs(list))
	if err != nil {
		return nil, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, saleOutCustomerIDs(list))
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, saleOutWarehouseIDs(list))
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.SaleOut) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.SaleOutExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.SaleOutExportRow{
			No:            row.No,
			OrderNo:       orderNos[row.OrderID],
			Customer:      customerNames[row.CustomerID],
			Warehouse:     warehouseNames[row.WarehouseID],
			OutTime:       row.OutTime,
			Status:        docStatusNames[row.Status],
			TotalPrice:    row.TotalPrice,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

func saleOutItemProducts(items []model.SaleOutItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}

func saleOutOrderIDs(list []model.SaleOut) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.OrderID)
	}
	return ids
}

func saleOutCustomerIDs(list []model.SaleOut) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.CustomerID)
	}
	return ids
}

func saleOutWarehouseIDs(list []model.SaleOut) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.WarehouseID)
	}
	return ids
}
