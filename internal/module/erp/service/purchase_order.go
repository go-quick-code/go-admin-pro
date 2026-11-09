// purchase_order.go 是采购订单（CG）的业务方法（issue #91，docs/erp-mvp-spec.md
// §3.3/§4.1/§4.8/§5）：创建/编辑/删除（仅未审核）、审核/反审核（订单审核不动
// 库存，spec §4.1——落账只发生在入库/退货审核）。入库/退货必挂已审核订单，
// 所以订单反审核在存在已审核入库/退货时被 409 拒绝（spec §4.4 反审核禁令）。
package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service/doc"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// purchaseOrderKernel 构造采购订单内核：审核只翻状态（无落账钩子），
// 反审核在存在已审核入库/退货时 409，删除在存在入库引用时 409。
func (s *Service) purchaseOrderKernel() *doc.Kernel[*model.PurchaseOrder, model.PurchaseOrderItem] {
	return doc.New(docEnv{s}, s.purchaseOrderStore(), doc.Spec[*model.PurchaseOrder, model.PurchaseOrderItem]{
		Labels:       doc.Labels{Doc: "采购订单"},
		NoPrefix:     PurchaseOrderNoPrefix,
		SeqBizType:   SeqBizTypePurchaseOrder,
		Unapprove:    s.unapprovePurchaseOrderDoc,
		BeforeDelete: s.beforeDeletePurchaseOrder,
	})
}

// purchaseOrderStore 装配采购订单的泛型存储。
func (s *Service) purchaseOrderStore() doc.Store[*model.PurchaseOrder, model.PurchaseOrderItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.PurchaseOrder, model.PurchaseOrderItem]{
		New:          func() *model.PurchaseOrder { return &model.PurchaseOrder{} },
		ItemsFK:      "purchase_order_id",
		SetItemDocID: func(it *model.PurchaseOrderItem, id int64) { it.PurchaseOrderID = id },
	})
}

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
			OrderNo:      row.OrderNo,
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
		OrderNo:    req.OrderNo,
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
	full, err := s.purchaseOrderKernel().FindWithItems(ctx, id)
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
		OrderNo:      full.OrderNo,
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

// CreatePurchaseOrder 创建采购订单（仅未审核）：明细/供应商校验 → 内核事务内
// 发号落主表与明细。不动库存、不回写进度（spec §4.1）。
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
	return s.purchaseOrderKernel().Create(ctx, nil,
		func(ctx context.Context) (*model.PurchaseOrder, error) {
			return &model.PurchaseOrder{
				SupplierID: req.SupplierId,
				OrderTime:  orderTime,
				Remark:     req.Remark,
			}, nil
		},
		func(ctx context.Context, d *model.PurchaseOrder) ([]model.PurchaseOrderItem, error) {
			return buildPurchaseOrderItemRows(d.ID, items), nil
		},
	)
}

// UpdatePurchaseOrder 修改采购订单（仅未审核，已审核 409，spec §4.1）：
// 字段全量覆盖；单号不采纳（保持原号）；明细整体替换（软删旧明细）。
func (s *Service) UpdatePurchaseOrder(ctx context.Context, req *dto.PurchaseOrderUpdateReq) error {
	var items []docItemDraft
	var orderTime time.Time
	return s.purchaseOrderKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.PurchaseOrder) error {
			var err error
			items, err = buildDocItems(req.Items, "订单明细")
			if err != nil {
				return err
			}
			if err := s.checkPurchasePartyForWrite(ctx, req.SupplierId, 0, items); err != nil {
				return err
			}
			orderTime, err = s.parseDocTime(req.OrderTime, "下单时间")
			return err
		},
		nil,
		func(ctx context.Context, _ doc.Tx[*model.PurchaseOrder, model.PurchaseOrderItem], d *model.PurchaseOrder) error {
			d.SupplierID = req.SupplierId
			d.OrderTime = orderTime
			d.Remark = req.Remark
			return nil
		},
		func(ctx context.Context, d *model.PurchaseOrder) ([]model.PurchaseOrderItem, error) {
			return buildPurchaseOrderItemRows(d.ID, items), nil
		},
	)
}

// DeletePurchaseOrder 删除采购订单（仅未审核，已审核 409，spec §4.1/§4.9）：
// 主表 + 明细一并软删除；存在入库单引用（含未审核，退货经入库单归属到订单）
// 时禁删——挂单引用悬空会让占用/进度校验失去锚点（与销售订单对称）。
func (s *Service) DeletePurchaseOrder(ctx context.Context, id int64) error {
	return s.purchaseOrderKernel().Delete(ctx, id)
}

// beforeDeletePurchaseOrder 下游禁令：存在入库单引用（含未审核，退货经入库单
// 归属到订单）时禁删——挂单引用悬空会让占用/进度校验失去锚点。
func (s *Service) beforeDeletePurchaseOrder(ctx context.Context, tx doc.Tx[*model.PurchaseOrder, model.PurchaseOrderItem], d *model.PurchaseOrder) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	inRefs, err := repo.CountPurchaseInsByOrder(ctx, d.ID, 0)
	if err != nil {
		return err
	}
	if inRefs > 0 {
		return xerr.Conflict("采购订单存在入库/退货单引用，禁止删除")
	}
	return nil
}

// UpdatePurchaseOrderStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1）。
// 订单审核不动库存；反审核在存在已审核入库/退货时 409（spec §4.4 反审核禁令）。
func (s *Service) UpdatePurchaseOrderStatus(ctx context.Context, req *dto.DocUpdateStatusReq) error {
	return s.purchaseOrderKernel().SetStatus(ctx, req.ID, req.Status)
}

// unapprovePurchaseOrder 订单反审核（spec §4.4 反审核禁令）：先乐观认领自身
// （UPDATE 对本行加 X 锁，与入库审核先锁挂单订单串行化）再聚合下游禁令
// （#98 审查 MAJOR 2）——存在已审核入库或已审核退货（经入库单挂本订单）则
// 409 并回滚认领，任何并发交错都不会留下「订单未审核 + 已审核子单」的组合。
// 未审核挂单不拦截（删除或反审核后自然解除占用）。反审核只翻状态：
// in_count/return_count 只计已审核单，而禁令保证反审核时点不存在已审核挂单，
// 进度恒为零值无需重算。
func (s *Service) unapprovePurchaseOrderDoc(ctx context.Context, tx doc.Tx[*model.PurchaseOrder, model.PurchaseOrderItem], d *model.PurchaseOrder) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	inSum, err := repo.SumPurchaseInItemsByProduct(ctx, d.ID, true, 0)
	if err != nil {
		return err
	}
	retSum, err := repo.SumPurchaseReturnItemsByProductForOrder(ctx, d.ID, true)
	if err != nil {
		return err
	}
	if len(inSum) > 0 || len(retSum) > 0 {
		return xerr.Conflict("订单存在已审核入库/退货单，不可反审核")
	}
	return nil
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
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.PurchaseOrder) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.PurchaseOrderExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.PurchaseOrderExportRow{
			OrderNo:       row.OrderNo,
			Supplier:      supplierNames[row.SupplierID],
			OrderTime:     row.OrderTime,
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
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
