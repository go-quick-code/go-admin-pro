// sale_return.go 是销售退货单（ST）的业务方法（issue #92，docs/erp-mvp-spec.md
// §3.4/§4.1/§4.5/§5）：必挂已审核销售出库单（customer 与出库一致，仓库取自
// 出库单服务端派生），审核经唯一入账入口回库存并回写订单进度，反审核经
// _CANCEL 反向流水再扣库存（余额不足 409）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListSaleReturns 销售退货单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListSaleReturns(ctx context.Context, req *dto.SaleReturnPageReq) ([]dto.SaleReturnRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.saleReturnFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListSaleReturns(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return s.assembleSaleReturnRows(ctx, list, total)
}

// saleReturnFilter 构造退货单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) saleReturnFilter(ctx context.Context, req *dto.SaleReturnPageReq) (repository.SaleReturnFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.SaleReturnFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.SaleReturnFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.SaleReturnFilter{}, err
	}
	filter := repository.SaleReturnFilter{
		No:         req.No,
		OutId:      req.OutId,
		CustomerId: req.CustomerId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assembleSaleReturnRows 批量解析出库单号/客户/仓库名并组装退货单分页行。
func (s *Service) assembleSaleReturnRows(ctx context.Context, list []model.SaleReturn, total int64) ([]dto.SaleReturnRow, int64, error) {
	outIDs := make([]int64, 0, len(list))
	customerIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	for _, row := range list {
		outIDs = append(outIDs, row.OutID)
		customerIDs = append(customerIDs, row.CustomerID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
	}
	outNos, err := s.repo.FindSaleOutNosIn(ctx, outIDs)
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
	out := make([]dto.SaleReturnRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.SaleReturnRow{
			ID:            row.ID,
			No:            row.No,
			OutId:         row.OutID,
			OutNo:         outNos[row.OutID],
			CustomerId:    row.CustomerID,
			CustomerName:  customerNames[row.CustomerID],
			WarehouseId:   row.WarehouseID,
			WarehouseName: warehouseNames[row.WarehouseID],
			ReturnTime:    row.ReturnTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			TotalPrice:    row.TotalPrice,
			RefundPrice:   row.RefundPrice,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, total, nil
}

// GetSaleReturn 销售退货单详情（带头 + 明细）。
func (s *Service) GetSaleReturn(ctx context.Context, id int64) (*dto.SaleReturnDetail, error) {
	doc, err := s.findSaleReturn(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindSaleReturnWithItems(ctx, doc.ID)
	if err != nil {
		return nil, err
	}
	outNos, err := s.repo.FindSaleOutNosIn(ctx, []int64{full.OutID})
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
	items := make([]dto.SaleReturnItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.SaleReturnItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return &dto.SaleReturnDetail{
		ID:            full.ID,
		No:            full.No,
		OutId:         full.OutID,
		OutNo:         outNos[full.OutID],
		CustomerId:    full.CustomerID,
		CustomerName:  customerNames[full.CustomerID],
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		ReturnTime:    full.ReturnTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		TotalPrice:    full.TotalPrice,
		RefundPrice:   full.RefundPrice,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateSaleReturn 创建销售退货单（仅未审核）：明细校验 → 挂单校验（已审核
// 出库 + 客户一致，仓库取自出库单派生）→ 产品存在性 → 出库单行锁 + 占用校验
// （剩余可退 = 出库数量 − 已退占用，超量 409）→ 事务内发放单号（ST）→ 落库。
func (s *Service) CreateSaleReturn(ctx context.Context, req *dto.SaleReturnCreateReq) (*model.SaleReturn, error) {
	items, err := buildDocItems(req.Items, "退货明细")
	if err != nil {
		return nil, err
	}
	returnTime, err := s.parseDocTime(req.ReturnTime, "退货时间")
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.SaleReturn
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		out, err := s.lockSaleReturnLink(ctx, tx, req.CustomerId, req.OutId)
		if err != nil {
			return err
		}
		if err := s.checkSaleReturnProducts(ctx, tx, items); err != nil {
			return err
		}
		if err := s.checkSaleReturnOccupancy(ctx, tx, out, items, 0); err != nil {
			return err
		}
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeSaleReturn, SaleReturnNoPrefix)
		if err != nil {
			return err
		}
		total, err := sumDraftTotal(items)
		if err != nil {
			return err
		}
		doc = &model.SaleReturn{
			No:          no,
			CustomerID:  req.CustomerId,
			OutID:       req.OutId,
			WarehouseID: out.WarehouseID, // 仓库取自出库单（spec §3.3 采购镜像口径）
			ReturnTime:  returnTime,
			Status:      model.DocStatusUnaudited,
			TotalPrice:  total,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreateSaleReturn(ctx, doc); err != nil {
			return err
		}
		return tx.CreateSaleReturnItems(ctx, buildSaleReturnItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// lockSaleReturnLink 退货挂单校验并加锁（事务内）：出库单存在（400）→ 已审核
// （409，spec §4.5）→ 客户一致（400）→ 返回行锁的出库单行（退货占用与并发
// 创建在同一出库行上串行化）。
func (s *Service) lockSaleReturnLink(ctx context.Context, tx *repository.Repository, customerID, outID int64) (*model.SaleOut, error) {
	if err := s.checkSaleCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	out, err := tx.FindSaleOutByIDForUpdate(ctx, outID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, xerr.BadRequest("销售出库单不存在")
	}
	if out.Status != model.DocStatusApproved {
		return nil, xerr.Conflict("销售出库单未审核，退货单必须挂已审核出库单")
	}
	if out.CustomerID != customerID {
		return nil, xerr.BadRequest("退货单客户必须与销售出库单客户一致")
	}
	return out, nil
}

// checkSaleReturnProducts 退货明细的产品存在性（退货单无仓库入参，不走
// checkDocRefsForWrite 的仓库分支；产品校验与其同口径）。
func (s *Service) checkSaleReturnProducts(ctx context.Context, tx *repository.Repository, items []docItemDraft) error {
	productIDs := make([]int64, 0, len(items))
	for _, item := range items {
		productIDs = append(productIDs, item.ProductID)
	}
	names, err := tx.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return err
	}
	for _, id := range productIDs {
		if _, ok := names[id]; !ok {
			return xerr.BadRequest("产品不存在")
		}
	}
	return nil
}

// UpdateSaleReturn 修改销售退货单（仅未审核，已审核 409，spec §4.1）：字段全量
// 覆盖（可换挂出库单，客户/仓库/占用随新出库单重验）；单号不采纳；明细整体替换。
func (s *Service) UpdateSaleReturn(ctx context.Context, req *dto.SaleReturnUpdateReq) error {
	row, err := s.findSaleReturn(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	items, err := buildDocItems(req.Items, "退货明细")
	if err != nil {
		return err
	}
	returnTime, err := s.parseDocTime(req.ReturnTime, "退货时间")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		out, err := s.lockSaleReturnLink(ctx, tx, req.CustomerId, req.OutId)
		if err != nil {
			return err
		}
		if err := s.checkSaleReturnProducts(ctx, tx, items); err != nil {
			return err
		}
		// 编辑自检：聚合占用时排除自己（新明细替换旧明细，不双重计占用）。
		if err := s.checkSaleReturnOccupancy(ctx, tx, out, items, row.ID); err != nil {
			return err
		}
		total, err := sumDraftTotal(items)
		if err != nil {
			return err
		}
		row.CustomerID = req.CustomerId
		row.OutID = req.OutId
		row.WarehouseID = out.WarehouseID
		row.ReturnTime = returnTime
		row.TotalPrice = total
		if err := tx.SaveSaleReturn(ctx, row); err != nil {
			return err
		}
		if err := tx.DeleteSaleReturnItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateSaleReturnItems(ctx, buildSaleReturnItemRows(row.ID, items))
	})
}

// DeleteSaleReturn 删除销售退货单（仅未审核，已审核 409，spec §4.1）：主表 +
// 明细一并软删除；占用随删除自然释放。
func (s *Service) DeleteSaleReturn(ctx context.Context, id int64) error {
	row, err := s.findSaleReturn(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.DeleteSaleReturn(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteSaleReturnItems(ctx, row.ID)
	})
}

// UpdateSaleReturnStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateSaleReturnStatus(ctx context.Context, req *dto.SaleDocUpdateStatusReq) error {
	row, err := s.findSaleReturn(ctx, req.ID)
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
		return s.approveSaleReturn(ctx, row)
	}
	return s.unapproveSaleReturn(ctx, row)
}

// approveSaleReturn 审核落账：事务内先乐观认领状态（防并发双重审核）→ 重读
// 明细 → 逐行经唯一入账入口入账（biz_type=sale_return，正流水回库存）→ 重算
// 订单进度（return_count 只计已审核，spec §4.5）。
func (s *Service) approveSaleReturn(ctx context.Context, row *model.SaleReturn) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateSaleReturnStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindSaleReturnWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, saleReturnItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.SaleReturnItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizSaleReturn,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return s.recomputeSaleOrderProgressForOut(ctx, tx, doc.OutID)
	})
}

// unapproveSaleReturn 反审核回滚：乐观认领状态 → 逐行经唯一入账入口新增
// _CANCEL 反向流水（再扣库存，原流水保留；余额不足 409——货已被后续出库提走
// 属正常业务冲突）→ 重算订单进度。
func (s *Service) unapproveSaleReturn(ctx context.Context, row *model.SaleReturn) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateSaleReturnStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindSaleReturnWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, saleReturnItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.SaleReturnItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizSaleReturnCancel,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return s.recomputeSaleOrderProgressForOut(ctx, tx, doc.OutID)
	})
}

// recomputeSaleOrderProgressForOut 经挂单出库单定位订单后重算进度。
func (s *Service) recomputeSaleOrderProgressForOut(ctx context.Context, tx *repository.Repository, outID int64) error {
	out, err := tx.FindSaleOutByID(ctx, outID)
	if err != nil {
		return err
	}
	if out == nil {
		return xerr.NotFound("销售出库单不存在")
	}
	return s.recomputeSaleOrderProgress(ctx, tx, out.OrderID)
}

// ExportSaleReturns 导出销售退货单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportSaleReturns(ctx context.Context, req *dto.SaleReturnPageReq) ([]byte, error) {
	filter, err := s.saleReturnFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllSaleReturns(ctx, filter)
	if err != nil {
		return nil, err
	}
	outNos, err := s.repo.FindSaleOutNosIn(ctx, saleReturnOutIDs(list))
	if err != nil {
		return nil, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, saleReturnCustomerIDs(list))
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, saleReturnWarehouseIDs(list))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.SaleReturnExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.SaleReturnExportRow{
			No:          row.No,
			OutNo:       outNos[row.OutID],
			Customer:    customerNames[row.CustomerID],
			Warehouse:   warehouseNames[row.WarehouseID],
			ReturnTime:  row.ReturnTime,
			Status:      docStatusNames[row.Status],
			TotalPrice:  row.TotalPrice,
			OwnerUserID: row.OwnerUserID,
			CreateTime:  row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// findSaleReturn 取退货单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findSaleReturn(ctx context.Context, id int64) (*model.SaleReturn, error) {
	row, err := s.repo.FindSaleReturnByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("销售退货单不存在")
	}
	if err := s.checkRowVisible(ctx, "销售退货单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

func saleReturnItemProducts(items []model.SaleReturnItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}

func saleReturnOutIDs(list []model.SaleReturn) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.OutID)
	}
	return ids
}

func saleReturnCustomerIDs(list []model.SaleReturn) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.CustomerID)
	}
	return ids
}

func saleReturnWarehouseIDs(list []model.SaleReturn) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.WarehouseID)
	}
	return ids
}
