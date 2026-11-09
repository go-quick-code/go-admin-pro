// sale_order.go 是销售订单（XS）的业务方法（issue #92，docs/erp-mvp-spec.md
// §3.4/§4.1/§4.5/§5）：创建/编辑/删除（仅未审核）、审核/反审核（纯状态翻转，
// 订单本身不动库存不产生流水）、反审核禁令（存在已审核出库/退货 409）。
// 订单编辑在既有出库/退货存在时重验占用不变量（spec §4.5 占用口径）。
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

// saleOrderKernel 构造销售订单内核：审核只翻状态（无落账钩子），反审核在
// 存在已审核出库/退货时 409，删除在存在出库引用时 409。
func (s *Service) saleOrderKernel() *doc.Kernel[*model.SaleOrder, model.SaleOrderItem] {
	return doc.New(docEnv{s}, s.saleOrderStore(), doc.Spec[*model.SaleOrder, model.SaleOrderItem]{
		Labels:       doc.Labels{Doc: "销售订单"},
		NoPrefix:     SaleOrderNoPrefix,
		SeqBizType:   SeqBizTypeSaleOrder,
		Unapprove:    s.unapproveSaleOrderDoc,
		BeforeDelete: s.beforeDeleteSaleOrder,
	})
}

// saleOrderStore 装配销售订单的泛型存储。
func (s *Service) saleOrderStore() doc.Store[*model.SaleOrder, model.SaleOrderItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.SaleOrder, model.SaleOrderItem]{
		New:          func() *model.SaleOrder { return &model.SaleOrder{} },
		ItemsFK:      "sale_order_id",
		SetItemDocID: func(it *model.SaleOrderItem, id int64) { it.SaleOrderID = id },
	})
}

// ListSaleOrders 销售订单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListSaleOrders(ctx context.Context, req *dto.SaleOrderPageReq) ([]dto.SaleOrderRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.saleOrderFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListSaleOrders(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.assembleSaleOrderRows(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// saleOrderFilter 构造订单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) saleOrderFilter(ctx context.Context, req *dto.SaleOrderPageReq) (repository.SaleOrderFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.SaleOrderFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.SaleOrderFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.SaleOrderFilter{}, err
	}
	filter := repository.SaleOrderFilter{
		OrderNo:    req.OrderNo,
		CustomerId: req.CustomerId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assembleSaleOrderRows 批量解析客户名并组装订单分页行。
func (s *Service) assembleSaleOrderRows(ctx context.Context, list []model.SaleOrder) ([]dto.SaleOrderRow, error) {
	customerIDs := make([]int64, 0, len(list))
	for _, row := range list {
		customerIDs = append(customerIDs, row.CustomerID)
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, customerIDs)
	if err != nil {
		return nil, err
	}
	out := make([]dto.SaleOrderRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.SaleOrderRow{
			ID:           row.ID,
			OrderNo:      row.OrderNo,
			CustomerId:   row.CustomerID,
			CustomerName: customerNames[row.CustomerID],
			SaleUserId:   row.SaleUserID,
			OrderTime:    row.OrderTime,
			Status:       row.Status,
			StatusName:   docStatusNames[row.Status],
			Remark:       row.Remark,
			CreateTime:   row.CreatedAt,
		})
	}
	return out, nil
}

// GetSaleOrder 销售订单详情（带头 + 明细 + 进度，产品/客户名服务端解析）。
func (s *Service) GetSaleOrder(ctx context.Context, id int64) (*dto.SaleOrderDetail, error) {
	full, err := s.saleOrderKernel().FindWithItems(ctx, id)
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
	items := make([]dto.SaleOrderItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.SaleOrderItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
			OutCount:     item.OutCount,
			ReturnCount:  item.ReturnCount,
		})
	}
	return &dto.SaleOrderDetail{
		ID:           full.ID,
		OrderNo:      full.OrderNo,
		CustomerId:   full.CustomerID,
		CustomerName: customerNames[full.CustomerID],
		SaleUserId:   full.SaleUserID,
		OrderTime:    full.OrderTime,
		Status:       full.Status,
		StatusName:   docStatusNames[full.Status],
		Remark:       full.Remark,
		Items:        items,
		CreateTime:   full.CreatedAt,
	}, nil
}

// CreateSaleOrder 创建销售订单（仅未审核）：明细校验 → 客户/销售员存在性 →
// 事务内发放单号（XS，erp_sequence）→ 落主表 + 明细。订单不动库存不回写进度
// （spec §4.1：只有审核动作落账；进度在出库/退货审批时重算）。
func (s *Service) CreateSaleOrder(ctx context.Context, req *dto.SaleOrderCreateReq) (*model.SaleOrder, error) {
	items, err := buildDocItems(req.Items, "销售订单明细")
	if err != nil {
		return nil, err
	}
	if err := s.checkSaleCustomer(ctx, req.CustomerId); err != nil {
		return nil, err
	}
	if err := s.checkSaleUser(ctx, req.SaleUserId); err != nil {
		return nil, err
	}
	orderTime, err := s.parseDocTime(req.OrderTime, "下单时间")
	if err != nil {
		return nil, err
	}
	return s.saleOrderKernel().Create(ctx, nil,
		func(ctx context.Context) (*model.SaleOrder, error) {
			return &model.SaleOrder{
				CustomerID: req.CustomerId,
				SaleUserID: req.SaleUserId,
				OrderTime:  orderTime,
				Remark:     req.Remark,
			}, nil
		},
		func(ctx context.Context, d *model.SaleOrder) ([]model.SaleOrderItem, error) {
			return buildSaleOrderItemRows(d.ID, items), nil
		},
	)
}

// UpdateSaleOrder 修改销售订单（仅未审核，已审核 409，spec §4.1）：字段全量
// 覆盖；单号不采纳（保持原号）；明细整体替换（软删旧明细）。存在出库/退货
// 单（含未审核占用）时重验占用不变量——删减订单行会让既有单据超量（409）。
func (s *Service) UpdateSaleOrder(ctx context.Context, req *dto.SaleOrderUpdateReq) error {
	var items []docItemDraft
	var orderTime time.Time
	return s.saleOrderKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.SaleOrder) error {
			var err error
			items, err = buildDocItems(req.Items, "销售订单明细")
			if err != nil {
				return err
			}
			if err := s.checkSaleCustomer(ctx, req.CustomerId); err != nil {
				return err
			}
			if err := s.checkSaleUser(ctx, req.SaleUserId); err != nil {
				return err
			}
			orderTime, err = s.parseDocTime(req.OrderTime, "下单时间")
			return err
		},
		nil,
		func(ctx context.Context, tx doc.Tx[*model.SaleOrder, model.SaleOrderItem], d *model.SaleOrder) error {
			repo, err := docRepo(tx)
			if err != nil {
				return err
			}
			if err := s.recheckSaleOrderOccupancy(ctx, repo, d, items); err != nil {
				return err
			}
			d.CustomerID = req.CustomerId
			d.SaleUserID = req.SaleUserId
			d.OrderTime = orderTime
			d.Remark = req.Remark
			return nil
		},
		func(ctx context.Context, d *model.SaleOrder) ([]model.SaleOrderItem, error) {
			return buildSaleOrderItemRows(d.ID, items), nil
		},
	)
}

// DeleteSaleOrder 删除销售订单（仅未审核，已审核 409，spec §4.1/§4.9）：
// 主表 + 明细一并软删除；存在出库/退货单引用（含未审核）时禁删——挂单引用
// 悬空会让占用校验失去锚点（引用禁删与主数据同口径）。
func (s *Service) DeleteSaleOrder(ctx context.Context, id int64) error {
	return s.saleOrderKernel().Delete(ctx, id)
}

// beforeDeleteSaleOrder 下游禁令：存在出库/退货单引用（含未审核）时禁删。
func (s *Service) beforeDeleteSaleOrder(ctx context.Context, tx doc.Tx[*model.SaleOrder, model.SaleOrderItem], d *model.SaleOrder) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	outRefs, err := repo.CountSaleOutsByOrder(ctx, d.ID, 0)
	if err != nil {
		return err
	}
	if outRefs > 0 {
		return xerr.Conflict("销售订单存在出库/退货单引用，禁止删除")
	}
	return nil
}

// UpdateSaleOrderStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
// 订单本身不动库存；反审核禁令：存在已审核出库/退货 409（spec §4.1）。
func (s *Service) UpdateSaleOrderStatus(ctx context.Context, req *dto.SaleDocUpdateStatusReq) error {
	return s.saleOrderKernel().SetStatus(ctx, req.ID, req.Status)
}

// unapproveSaleOrder 订单反审核：先乐观认领自身（UPDATE 对本行加 X 锁，与出库
// 审核先锁挂单订单串行化）再聚合下游禁令（#98 审查 MAJOR 2）——存在已审核
// 出库/退货单则 409 并回滚认领，任何并发交错都不会留下「订单未审核 + 已审核
// 子单」的组合。仅有未审核单据时不阻断（禁令按 spec 只看已审核）。
func (s *Service) unapproveSaleOrderDoc(ctx context.Context, tx doc.Tx[*model.SaleOrder, model.SaleOrderItem], d *model.SaleOrder) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	outRefs, err := repo.CountSaleOutsByOrder(ctx, d.ID, model.DocStatusApproved)
	if err != nil {
		return err
	}
	if outRefs > 0 {
		return xerr.Conflict("销售订单存在已审核出库单，不可反审核")
	}
	retRefs, err := repo.CountApprovedSaleReturnsByOrder(ctx, erpTenantID(ctx), d.ID)
	if err != nil {
		return err
	}
	if retRefs > 0 {
		return xerr.Conflict("销售订单存在已审核退货单，不可反审核")
	}
	return nil
}

// ExportSaleOrders 导出销售订单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportSaleOrders(ctx context.Context, req *dto.SaleOrderPageReq) ([]byte, error) {
	filter, err := s.saleOrderFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllSaleOrders(ctx, filter)
	if err != nil {
		return nil, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, customerIDsOfOrders(list))
	if err != nil {
		return nil, err
	}
	saleUserIDs := make([]int64, 0, len(list)*2)
	for _, row := range list {
		saleUserIDs = append(saleUserIDs, row.SaleUserID, row.OwnerUserID)
	}
	userNames, err := s.userNicknames(ctx, saleUserIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.SaleOrderExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.SaleOrderExportRow{
			OrderNo:       row.OrderNo,
			Customer:      customerNames[row.CustomerID],
			SaleUserName:  userNames[row.SaleUserID],
			OrderTime:     row.OrderTime,
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: userNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

func customerIDsOfOrders(list []model.SaleOrder) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.CustomerID)
	}
	return ids
}
