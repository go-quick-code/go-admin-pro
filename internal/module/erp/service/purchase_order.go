// purchase_order.go 是采购订单（CG）的业务方法（issue #91，docs/erp-mvp-spec.md
// §3.3/§4.1/§4.8/§5）：创建/编辑/删除（仅未审核）、审核/反审核（订单审核不动
// 库存，spec §4.1——落账只发生在入库/退货审核）。入库/退货必挂已审核订单，
// 所以订单反审核在存在已审核入库/退货时被 409 拒绝（spec §4.4 反审核禁令）。
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

// ListPurchaseOrders 采购订单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListPurchaseOrders(ctx context.Context, req *dto.PurchaseOrderPageReq) ([]dto.PurchaseOrderRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.purchaseOrderFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListPurchaseOrders(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, purchaseOrderSupplierID(list))
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.PurchaseOrderRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.PurchaseOrderRow{
			ID:           row.ID,
			No:           row.No,
			SupplierId:   row.SupplierID,
			SupplierName: supplierNames[row.SupplierID],
			OrderTime:    row.OrderTime,
			Status:       row.Status,
			StatusName:   docStatusNames[row.Status],
			Remark:       row.Remark,
			CreateTime:   row.CreatedAt,
		})
	}
	return out, total, nil
}

// purchaseOrderFilter 构造订单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) purchaseOrderFilter(ctx context.Context, req *dto.PurchaseOrderPageReq) (repository.PurchaseOrderFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.PurchaseOrderFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.PurchaseOrderFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.PurchaseOrderFilter{}, err
	}
	filter := repository.PurchaseOrderFilter{
		No:         req.No,
		SupplierId: req.SupplierId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetPurchaseOrder 采购订单详情（带头 + 明细 + 进度回写列，产品名服务端解析）。
func (s *Service) GetPurchaseOrder(ctx context.Context, id int64) (*dto.PurchaseOrderDetail, error) {
	doc, err := s.findPurchaseOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindPurchaseOrderWithItems(ctx, doc.ID)
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
	items := make([]dto.PurchaseOrderItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.PurchaseOrderItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
			InCount:      item.InCount,
			ReturnCount:  item.ReturnCount,
		})
	}
	return &dto.PurchaseOrderDetail{
		ID:           full.ID,
		No:           full.No,
		SupplierId:   full.SupplierID,
		SupplierName: supplierNames[full.SupplierID],
		OrderTime:    full.OrderTime,
		Status:       full.Status,
		StatusName:   docStatusNames[full.Status],
		Remark:       full.Remark,
		Items:        items,
		CreateTime:   full.CreatedAt,
	}, nil
}

// CreatePurchaseOrder 创建采购订单（仅未审核）：明细校验 → 供应商/产品存在性 →
// 事务内发放单号（CG，erp_sequence）→ 落主表 + 明细。不动库存、不回写进度
// （spec §4.1：只有审核动作落账，订单审核亦不动库存）。
func (s *Service) CreatePurchaseOrder(ctx context.Context, req *dto.PurchaseOrderCreateReq) (*model.PurchaseOrder, error) {
	items, err := buildDocItems(req.Items, "订单明细")
	if err != nil {
		return nil, err
	}
	if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, 0, items); err != nil {
		return nil, err
	}
	orderTime, err := s.parseDocTime(req.OrderTime, "下单时间")
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.PurchaseOrder
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypePurchaseOrder, PurchaseOrderNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.PurchaseOrder{
			No:          no,
			SupplierID:  req.SupplierId,
			OrderTime:   orderTime,
			Status:      model.DocStatusUnaudited,
			Remark:      req.Remark,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreatePurchaseOrder(ctx, doc); err != nil {
			return err
		}
		return tx.CreatePurchaseOrderItems(ctx, buildPurchaseOrderItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdatePurchaseOrder 修改采购订单（仅未审核，已审核 409，spec §4.1）：
// 字段全量覆盖；单号不采纳（保持原号）；明细整体替换（软删旧明细）。
func (s *Service) UpdatePurchaseOrder(ctx context.Context, req *dto.PurchaseOrderUpdateReq) error {
	row, err := s.findPurchaseOrder(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	items, err := buildDocItems(req.Items, "订单明细")
	if err != nil {
		return err
	}
	if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, 0, items); err != nil {
		return err
	}
	orderTime, err := s.parseDocTime(req.OrderTime, "下单时间")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row.SupplierID = req.SupplierId
		row.OrderTime = orderTime
		row.Remark = req.Remark
		if err := tx.SavePurchaseOrder(ctx, row); err != nil {
			return err
		}
		// 明细整体替换：软删旧行 + 插入新行（未审核单无入库/退货可挂，安全）。
		if err := tx.DeletePurchaseOrderItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreatePurchaseOrderItems(ctx, buildPurchaseOrderItemRows(row.ID, items))
	})
}

// DeletePurchaseOrder 删除采购订单（仅未审核，已审核 409，spec §4.1）：
// 主表 + 明细一并软删除。未审核订单无入库/退货可挂，无占用/进度残留。
func (s *Service) DeletePurchaseOrder(ctx context.Context, id int64) error {
	row, err := s.findPurchaseOrder(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.DeletePurchaseOrder(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeletePurchaseOrderItems(ctx, row.ID)
	})
}

// UpdatePurchaseOrderStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1）。
// 订单审核不动库存；反审核在存在已审核入库/退货时 409（spec §4.4 反审核禁令）。
func (s *Service) UpdatePurchaseOrderStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	row, err := s.findPurchaseOrder(ctx, req.ID)
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
		// 订单审核：只翻状态（不动库存不回写进度——进度只来自已审核入库/退货，
		// 而入库/退货必挂已审核订单，审核时点不可能有挂单）。
		return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
			claimed, err := tx.UpdatePurchaseOrderStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
			if err != nil {
				return err
			}
			if claimed == 0 {
				return xerr.Conflict("单据状态已变化，请刷新后重试")
			}
			return nil
		})
	}
	return s.unapprovePurchaseOrder(ctx, row)
}

// unapprovePurchaseOrder 订单反审核（spec §4.4 反审核禁令）：存在已审核入库或
// 已审核退货（经入库单挂本订单）时 409；未审核挂单不拦截（删除或反审核后自然
// 解除占用）。反审核只翻状态：in_count/return_count 只计已审核单，而禁令保证
// 反审核时点不存在已审核挂单，进度恒为零值无需重算。
func (s *Service) unapprovePurchaseOrder(ctx context.Context, row *model.PurchaseOrder) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		inSum, err := tx.SumPurchaseInItemsByProduct(ctx, row.ID, true, 0)
		if err != nil {
			return err
		}
		retSum, err := tx.SumPurchaseReturnItemsByProductForOrder(ctx, row.ID, true)
		if err != nil {
			return err
		}
		if len(inSum) > 0 || len(retSum) > 0 {
			return xerr.Conflict("订单存在已审核入库/退货单，不可反审核")
		}
		claimed, err := tx.UpdatePurchaseOrderStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		return nil
	})
}

// ExportPurchaseOrders 导出采购订单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportPurchaseOrders(ctx context.Context, req *dto.PurchaseOrderPageReq) ([]byte, error) {
	filter, err := s.purchaseOrderFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllPurchaseOrders(ctx, filter)
	if err != nil {
		return nil, err
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, purchaseOrderSupplierID(list))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.PurchaseOrderExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.PurchaseOrderExportRow{
			No:          row.No,
			Supplier:    supplierNames[row.SupplierID],
			OrderTime:   row.OrderTime,
			Status:      docStatusNames[row.Status],
			Remark:      row.Remark,
			OwnerUserID: row.OwnerUserID,
			CreateTime:  row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// findPurchaseOrder 取订单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findPurchaseOrder(ctx context.Context, id int64) (*model.PurchaseOrder, error) {
	row, err := s.repo.FindPurchaseOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("采购订单不存在")
	}
	if err := s.checkRowVisible(ctx, "采购订单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// checkPurchasePartyForWrite 校验采购单据写入引用：供应商必须存在（租户内，
// 不存在 400）；expect > 0 时供应商必须与挂单来源一致（不一致 400，保证
// payment/refund 核销回写的供应商口径一致）；全部产品必须存在。
func (s *Service) checkPurchasePartyForWrite(ctx context.Context, supplierID, expectSupplierID int64, items []docItemDraft) error {
	if supplierID <= 0 {
		return xerr.BadRequest("缺少供应商")
	}
	supplier, err := s.repo.FindSupplierByID(ctx, supplierID)
	if err != nil {
		return err
	}
	if supplier == nil {
		return xerr.BadRequest("供应商不存在")
	}
	if expectSupplierID > 0 && supplierID != expectSupplierID {
		return xerr.BadRequest("供应商与挂单单据的供应商不一致")
	}
	productIDs := make([]int64, 0, len(items))
	for _, item := range items {
		productIDs = append(productIDs, item.ProductID)
	}
	names, err := s.repo.FindProductNamesIn(ctx, productIDs)
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

func buildPurchaseOrderItemRows(docID int64, drafts []docItemDraft) []model.PurchaseOrderItem {
	rows := make([]model.PurchaseOrderItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.PurchaseOrderItem{
			PurchaseOrderID: docID,
			ProductID:       d.ProductID,
			Count:           d.Count,
			ProductPrice:    d.ProductPrice,
			TotalPrice:      d.TotalPrice,
		})
	}
	return rows
}

func purchaseOrderSupplierID(list []model.PurchaseOrder) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.SupplierID)
	}
	return ids
}
