// stock_out.go 是其它出库单（QC）的适配器（ADR 0018）：与 stock_in.go
// 镜像同构（out_time/type、QC 前缀、biz_type=stock_out）；出库方向经唯一
// 入账入口的 SQL 双保险（count >= ?）拦截负库存，余额不足 409（提示产品/
// 仓库/当前库存/变更量）。写侧生命周期由 service/doc 内核拥有。
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

// stockOutKernel 构造其它出库单的内核实例（每次调用廉价；钩子捕获 Service）。
func (s *Service) stockOutKernel() *doc.Kernel[*model.StockOut, model.StockOutItem] {
	return doc.New(docEnv{s}, s.stockOutStore(), doc.Spec[*model.StockOut, model.StockOutItem]{
		Labels:     doc.Labels{Doc: "其它出库单"},
		NoPrefix:   StockOutNoPrefix,
		SeqBizType: SeqBizTypeStockOut,
		Approve:    s.approveStockOutDoc,
		Unapprove:  s.unapproveStockOutDoc,
	})
}

// stockOutStore 装配其它出库单的泛型存储（候选 02a；表名/关联由模型自带）。
func (s *Service) stockOutStore() doc.Store[*model.StockOut, model.StockOutItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.StockOut, model.StockOutItem]{
		New:          func() *model.StockOut { return &model.StockOut{} },
		ItemsFK:      "stock_out_id",
		SetItemDocID: func(it *model.StockOutItem, id int64) { it.StockOutID = id },
	})
}

// ListStockOuts 其它出库单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListStockOuts(ctx context.Context, req *dto.StockOutPageReq) ([]dto.StockDocRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.stockOutFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListStockOuts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	warehouseIDs := make([]int64, 0, len(list))
	for _, row := range list {
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.StockDocRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.StockDocRow{
			ID:            row.ID,
			No:            row.No,
			WarehouseId:   row.WarehouseID,
			WarehouseName: warehouseNames[row.WarehouseID],
			DocTime:       row.OutTime,
			Type:          row.Type,
			TypeName:      stockOutTypeNames[row.Type],
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, total, nil
}

// stockOutFilter 构造出库单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) stockOutFilter(ctx context.Context, req *dto.StockOutPageReq) (repository.StockOutFilter, error) {
	if req.Type != nil && !model.ValidStockOutType(*req.Type) {
		return repository.StockOutFilter{}, xerr.BadRequest("出库单类型不合法")
	}
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.StockOutFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.StockOutFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.StockOutFilter{}, err
	}
	filter := repository.StockOutFilter{
		No:          req.No,
		WarehouseId: req.WarehouseId,
		Type:        req.Type,
		Status:      req.Status,
		StartTime:   start,
		EndTime:     end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetStockOut 其它出库单详情（带头 + 明细，产品名称服务端解析）。
func (s *Service) GetStockOut(ctx context.Context, id int64) (*dto.StockOutDetail, error) {
	full, err := s.stockOutKernel().FindWithItems(ctx, id)
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
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{full.WarehouseID})
	if err != nil {
		return nil, err
	}
	items := make([]dto.StockOutItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.StockOutItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return &dto.StockOutDetail{
		ID:            full.ID,
		No:            full.No,
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		OutTime:       full.OutTime,
		Type:          full.Type,
		TypeName:      stockOutTypeNames[full.Type],
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateStockOut 创建其它出库单（仅未审核）：明细/引用/时间校验 → 内核事务内
// 发号、落主表与明细。不动库存（spec §4.1）。
func (s *Service) CreateStockOut(ctx context.Context, req *dto.StockOutCreateReq) (*model.StockOut, error) {
	items, err := buildDocItems(req.Items, "出库明细")
	if err != nil {
		return nil, err
	}
	if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
		return nil, err
	}
	outTime, err := s.parseDocTime(req.OutTime, "出库时间")
	if err != nil {
		return nil, err
	}
	docType, err := validateDocType(req.Type, model.ValidStockOutType, "出库单")
	if err != nil {
		return nil, err
	}
	return s.stockOutKernel().Create(ctx, nil,
		func(ctx context.Context) (*model.StockOut, error) {
			return &model.StockOut{
				WarehouseID: req.WarehouseId,
				OutTime:     outTime,
				Type:        docType,
				Remark:      req.Remark,
			}, nil
		},
		func(ctx context.Context, d *model.StockOut) ([]model.StockOutItem, error) {
			return buildStockOutItemRows(d.ID, items), nil
		},
	)
}

// UpdateStockOut 修改其它出库单（仅未审核，已审核 409，spec §4.1）。
func (s *Service) UpdateStockOut(ctx context.Context, req *dto.StockOutUpdateReq) error {
	var items []docItemDraft
	var outTime time.Time
	var docType int8
	return s.stockOutKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.StockOut) error {
			var err error
			items, err = buildDocItems(req.Items, "出库明细")
			if err != nil {
				return err
			}
			if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
				return err
			}
			outTime, err = s.parseDocTime(req.OutTime, "出库时间")
			if err != nil {
				return err
			}
			docType, err = validateDocType(req.Type, model.ValidStockOutType, "出库单")
			return err
		},
		nil,
		func(ctx context.Context, _ doc.Tx[*model.StockOut, model.StockOutItem], d *model.StockOut) error {
			d.WarehouseID = req.WarehouseId
			d.OutTime = outTime
			d.Type = docType
			d.Remark = req.Remark
			return nil
		},
		func(ctx context.Context, d *model.StockOut) ([]model.StockOutItem, error) {
			return buildStockOutItemRows(d.ID, items), nil
		},
	)
}

// DeleteStockOut 删除其它出库单（仅未审核，已审核 409，spec §4.1）。
func (s *Service) DeleteStockOut(ctx context.Context, id int64) error {
	return s.stockOutKernel().Delete(ctx, id)
}

// UpdateStockOutStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateStockOutStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	return s.stockOutKernel().SetStatus(ctx, req.ID, req.Status)
}

// approveStockOutDoc 审核落账（内核已认领 status=10→20 并装载明细）：
// 逐行经唯一入账入口出账（biz_type=stock_out，负出；余额不足 409，
// 事务整体回滚——一单一事务，绝无部分落账）。
func (s *Service) approveStockOutDoc(ctx context.Context, tx doc.Tx[*model.StockOut, model.StockOutItem], d *model.StockOut) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.WarehouseID})
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, stockOutItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.StockOutItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         negQty(item.Count),
			BizType:       model.StockRecordBizStockOut,
			BizID:         d.ID,
			BizItemID:     item.ID,
			BizNo:         d.No,
			ProductName:   productNames[item.ProductID],
			WarehouseName: warehouseNames[d.WarehouseID],
		}); err != nil {
			return err
		}
	}
	return nil
}

// unapproveStockOutDoc 反审核回滚（内核已认领 status=20→10 并装载明细）：
// 新增 _CANCEL 反向流水（count 取负——出库单的反向是正入），原流水保留。
func (s *Service) unapproveStockOutDoc(ctx context.Context, tx doc.Tx[*model.StockOut, model.StockOutItem], d *model.StockOut) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.WarehouseID})
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, stockOutItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.StockOutItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         item.Count,
			BizType:       model.StockRecordBizStockOutCancel,
			BizID:         d.ID,
			BizItemID:     item.ID,
			BizNo:         d.No,
			ProductName:   productNames[item.ProductID],
			WarehouseName: warehouseNames[d.WarehouseID],
		}); err != nil {
			return err
		}
	}
	return nil
}

// ExportStockOuts 导出其它出库单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportStockOuts(ctx context.Context, req *dto.StockOutPageReq) ([]byte, error) {
	filter, err := s.stockOutFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllStockOuts(ctx, filter)
	if err != nil {
		return nil, err
	}
	warehouseIDs := make([]int64, 0, len(list))
	for _, row := range list {
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.StockOut) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.StockOutExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockOutExportRow{
			No:            row.No,
			Warehouse:     warehouseNames[row.WarehouseID],
			OutTime:       row.OutTime,
			Type:          stockOutTypeNames[row.Type],
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

func buildStockOutItemRows(docID int64, drafts []docItemDraft) []model.StockOutItem {
	rows := make([]model.StockOutItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.StockOutItem{
			StockOutID:   docID,
			ProductID:    d.ProductID,
			Count:        d.Count,
			ProductPrice: d.ProductPrice,
			TotalPrice:   d.TotalPrice,
		})
	}
	return rows
}

func stockOutItemProducts(items []model.StockOutItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}
