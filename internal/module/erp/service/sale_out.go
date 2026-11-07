// sale_out.go 是销售出库单（CK）的业务方法（issue #92，docs/erp-mvp-spec.md
// §3.4/§4.1/§4.5/§5）：必挂已审核销售订单（customer 与订单一致），审核经唯一
// 入账入口减库存并回写订单进度，反审核经 _CANCEL 反向流水回滚（存在已审核
// 退货单时禁令 409）。
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
	doc, err := s.findSaleOut(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindSaleOutWithItems(ctx, doc.ID)
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
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.SaleOut
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		order, err := s.lockSaleOutLink(ctx, tx, req.CustomerId, req.OrderId)
		if err != nil {
			return err
		}
		if err := s.checkSaleOutOccupancy(ctx, tx, order, items, 0); err != nil {
			return err
		}
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeSaleOut, SaleOutNoPrefix)
		if err != nil {
			return err
		}
		total, err := sumDraftTotal(items)
		if err != nil {
			return err
		}
		doc = &model.SaleOut{
			No:          no,
			CustomerID:  req.CustomerId,
			OrderID:     req.OrderId,
			WarehouseID: req.WarehouseId,
			OutTime:     outTime,
			Status:      model.DocStatusUnaudited,
			TotalPrice:  total,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreateSaleOut(ctx, doc); err != nil {
			return err
		}
		return tx.CreateSaleOutItems(ctx, buildSaleOutItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
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
// 覆盖（可换挂订单，客户/占用随新订单重验）；单号不采纳；明细整体替换。
func (s *Service) UpdateSaleOut(ctx context.Context, req *dto.SaleOutUpdateReq) error {
	row, err := s.findSaleOut(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	items, err := buildDocItems(req.Items, "出库明细")
	if err != nil {
		return err
	}
	outTime, err := s.parseDocTime(req.OutTime, "出库时间")
	if err != nil {
		return err
	}
	if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		order, err := s.lockSaleOutLink(ctx, tx, req.CustomerId, req.OrderId)
		if err != nil {
			return err
		}
		// 编辑自检：聚合占用时排除自己（新明细替换旧明细，不双重计占用）。
		if err := s.checkSaleOutOccupancy(ctx, tx, order, items, row.ID); err != nil {
			return err
		}
		total, err := sumDraftTotal(items)
		if err != nil {
			return err
		}
		row.CustomerID = req.CustomerId
		row.OrderID = req.OrderId
		row.WarehouseID = req.WarehouseId
		row.OutTime = outTime
		row.TotalPrice = total
		if err := tx.SaveSaleOut(ctx, row); err != nil {
			return err
		}
		if err := tx.DeleteSaleOutItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateSaleOutItems(ctx, buildSaleOutItemRows(row.ID, items))
	})
}

// DeleteSaleOut 删除销售出库单（仅未审核，已审核 409，spec §4.1）：主表 + 明细
// 一并软删除；占用随删除自然释放（未审核占用计入聚合，软删行不计）；存在
// 退货单引用（含未审核）时禁删——挂单引用悬空会让占用校验失去锚点。
func (s *Service) DeleteSaleOut(ctx context.Context, id int64) error {
	row, err := s.findSaleOut(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		retRefs, err := tx.CountSaleReturnsByOut(ctx, row.ID, 0)
		if err != nil {
			return err
		}
		if retRefs > 0 {
			return xerr.Conflict("销售出库单存在退货单引用，禁止删除")
		}
		if err := tx.DeleteSaleOut(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteSaleOutItems(ctx, row.ID)
	})
}

// UpdateSaleOutStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateSaleOutStatus(ctx context.Context, req *dto.SaleDocUpdateStatusReq) error {
	row, err := s.findSaleOut(ctx, req.ID)
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
		return s.approveSaleOut(ctx, row)
	}
	return s.unapproveSaleOut(ctx, row)
}

// approveSaleOut 审核落账：事务内先乐观认领状态（防并发双重审核）→ 重读明细 →
// 逐行经唯一入账入口出账（biz_type=sale_out，负流水；余额不足 409）→ 重算
// 订单进度（out_count 只计已审核，spec §4.5）。
func (s *Service) approveSaleOut(ctx context.Context, row *model.SaleOut) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateSaleOutStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindSaleOutWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, saleOutItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.SaleOutItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizSaleOut,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return s.recomputeSaleOrderProgress(ctx, tx, doc.OrderID)
	})
}

// unapproveSaleOut 反审核回滚（spec §4.1/§4.3）：存在已审核退货单 → 409
// （反审核禁令）；乐观认领状态 → 逐行经唯一入账入口新增 _CANCEL 反向流水
// （库存回补，原流水保留）→ 重算订单进度。
func (s *Service) unapproveSaleOut(ctx context.Context, row *model.SaleOut) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		retRefs, err := tx.CountSaleReturnsByOut(ctx, row.ID, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if retRefs > 0 {
			return xerr.Conflict("销售出库单存在已审核退货单，不可反审核")
		}
		claimed, err := tx.UpdateSaleOutStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindSaleOutWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, saleOutItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.SaleOutItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizSaleOutCancel,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return s.recomputeSaleOrderProgress(ctx, tx, doc.OrderID)
	})
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
	rows := make([]dto.SaleOutExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.SaleOutExportRow{
			No:          row.No,
			OrderNo:     orderNos[row.OrderID],
			Customer:    customerNames[row.CustomerID],
			Warehouse:   warehouseNames[row.WarehouseID],
			OutTime:     row.OutTime,
			Status:      docStatusNames[row.Status],
			TotalPrice:  row.TotalPrice,
			OwnerUserID: row.OwnerUserID,
			CreateTime:  row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// findSaleOut 取出库单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findSaleOut(ctx context.Context, id int64) (*model.SaleOut, error) {
	row, err := s.repo.FindSaleOutByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("销售出库单不存在")
	}
	if err := s.checkRowVisible(ctx, "销售出库单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
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
