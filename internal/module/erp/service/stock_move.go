// stock_move.go 是库存调拨单（DB）的业务方法（issue #93，docs/erp-mvp-spec.md
// §3.5/§4.1/§4.2/§4.6/§4.8/§5）：创建/编辑/删除（仅未审核）、审核落账
// （同事务出仓负、入仓正两笔流水同额成对）、反审核各生成反向流水。
// 库存变动只经唯一入账入口 ApplyStockChange（spec §4.2）；多仓库先按仓库 ID
// 升序取行锁防死锁；编号在落库同事务经 erp_sequence 发放（spec §4.8）。
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

// stockMoveKernel 构造库存调拨单内核（无父单；审核/反审核各生成成对流水）。
func (s *Service) stockMoveKernel() *doc.Kernel[*model.StockMove, model.StockMoveItem] {
	return doc.New(docEnv{s}, s.stockMoveStore(), doc.Spec[*model.StockMove, model.StockMoveItem]{
		Labels:     doc.Labels{Doc: "库存调拨单"},
		NoPrefix:   StockMoveNoPrefix,
		SeqBizType: SeqBizTypeStockMove,
		Approve:    s.approveStockMoveDoc,
		Unapprove:  s.unapproveStockMoveDoc,
	})
}

// stockMoveStore 装配库存调拨单的泛型存储。
func (s *Service) stockMoveStore() doc.Store[*model.StockMove, model.StockMoveItem] {
	return newGenericDocStore(s.repo, s.businessNow, DocSchema[*model.StockMove, model.StockMoveItem]{
		New:          func() *model.StockMove { return &model.StockMove{} },
		ItemsFK:      "stock_move_id",
		SetItemDocID: func(it *model.StockMoveItem, id int64) { it.StockMoveID = id },
	})
}

// ListStockMoves 库存调拨单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListStockMoves(ctx context.Context, req *dto.StockMovePageReq) ([]dto.StockMoveRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.stockMoveFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListStockMoves(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	warehouseIDs := make([]int64, 0, len(list)*2)
	for _, row := range list {
		warehouseIDs = append(warehouseIDs, row.FromWarehouseID, row.ToWarehouseID)
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.StockMoveRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.StockMoveRow{
			ID:                row.ID,
			No:                row.No,
			FromWarehouseId:   row.FromWarehouseID,
			FromWarehouseName: warehouseNames[row.FromWarehouseID],
			ToWarehouseId:     row.ToWarehouseID,
			ToWarehouseName:   warehouseNames[row.ToWarehouseID],
			MoveTime:          row.MoveTime,
			Status:            row.Status,
			StatusName:        docStatusNames[row.Status],
			CreateTime:        row.CreatedAt,
		})
	}
	return out, total, nil
}

// stockMoveFilter 构造调拨单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) stockMoveFilter(ctx context.Context, req *dto.StockMovePageReq) (repository.StockMoveFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.StockMoveFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.StockMoveFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.StockMoveFilter{}, err
	}
	filter := repository.StockMoveFilter{
		No:              req.No,
		FromWarehouseId: req.FromWarehouseId,
		ToWarehouseId:   req.ToWarehouseId,
		Status:          req.Status,
		StartTime:       start,
		EndTime:         end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetStockMove 调拨单详情（带头 + 明细，产品/两仓名称服务端解析）。
func (s *Service) GetStockMove(ctx context.Context, id int64) (*dto.StockMoveDetail, error) {
	full, err := s.stockMoveKernel().FindWithItems(ctx, id)
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
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{full.FromWarehouseID, full.ToWarehouseID})
	if err != nil {
		return nil, err
	}
	items := make([]dto.StockMoveItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.StockMoveItemRow{
			ID:          item.ID,
			ProductId:   item.ProductID,
			ProductName: productNames[item.ProductID],
			Count:       item.Count,
		})
	}
	return &dto.StockMoveDetail{
		ID:                full.ID,
		No:                full.No,
		FromWarehouseId:   full.FromWarehouseID,
		FromWarehouseName: warehouseNames[full.FromWarehouseID],
		ToWarehouseId:     full.ToWarehouseID,
		ToWarehouseName:   warehouseNames[full.ToWarehouseID],
		MoveTime:          full.MoveTime,
		Status:            full.Status,
		StatusName:        docStatusNames[full.Status],
		Items:             items,
		CreateTime:        full.CreatedAt,
	}, nil
}

// CreateStockMove 创建库存调拨单（仅未审核）：明细校验 → 两仓存在且不同 →
// 事务内发放单号（DB，erp_sequence）→ 落主表 + 明细。不动库存（spec §4.1）。
func (s *Service) CreateStockMove(ctx context.Context, req *dto.StockMoveCreateReq) (*model.StockMove, error) {
	if err := ensureDistinctWarehouses(req.FromWarehouseId, req.ToWarehouseId); err != nil {
		return nil, err
	}
	items, err := buildMoveItemDrafts(req.Items)
	if err != nil {
		return nil, err
	}
	if err := s.checkWarehousesExist(ctx, req.FromWarehouseId, req.ToWarehouseId); err != nil {
		return nil, err
	}
	if err := s.checkProductsExist(ctx, moveItemProductIDs(items)); err != nil {
		return nil, err
	}
	moveTime, err := s.parseDocTime(req.MoveTime, "调拨时间")
	if err != nil {
		return nil, err
	}
	return s.stockMoveKernel().Create(ctx, nil,
		func(ctx context.Context) (*model.StockMove, error) {
			return &model.StockMove{
				FromWarehouseID: req.FromWarehouseId,
				ToWarehouseID:   req.ToWarehouseId,
				MoveTime:        moveTime,
			}, nil
		},
		func(ctx context.Context, d *model.StockMove) ([]model.StockMoveItem, error) {
			return buildStockMoveItemRows(d.ID, items), nil
		},
	)
}

// UpdateStockMove 修改库存调拨单（仅未审核，已审核 409）：字段全量覆盖；
// 单号不采纳；明细整体替换。
func (s *Service) UpdateStockMove(ctx context.Context, req *dto.StockMoveUpdateReq) error {
	var items []moveItemDraft
	var moveTime time.Time
	return s.stockMoveKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.StockMove) error {
			if err := ensureDistinctWarehouses(req.FromWarehouseId, req.ToWarehouseId); err != nil {
				return err
			}
			var err error
			items, err = buildMoveItemDrafts(req.Items)
			if err != nil {
				return err
			}
			if err := s.checkWarehousesExist(ctx, req.FromWarehouseId, req.ToWarehouseId); err != nil {
				return err
			}
			if err := s.checkProductsExist(ctx, moveItemProductIDs(items)); err != nil {
				return err
			}
			moveTime, err = s.parseDocTime(req.MoveTime, "调拨时间")
			return err
		},
		nil,
		func(ctx context.Context, _ doc.Tx[*model.StockMove, model.StockMoveItem], d *model.StockMove) error {
			d.FromWarehouseID = req.FromWarehouseId
			d.ToWarehouseID = req.ToWarehouseId
			d.MoveTime = moveTime
			return nil
		},
		func(ctx context.Context, d *model.StockMove) ([]model.StockMoveItem, error) {
			return buildStockMoveItemRows(d.ID, items), nil
		},
	)
}

// DeleteStockMove 删除库存调拨单（仅未审核，已审核 409）。
func (s *Service) DeleteStockMove(ctx context.Context, id int64) error {
	return s.stockMoveKernel().Delete(ctx, id)
}

// UpdateStockMoveStatus 审核（10→20）/反审核（20→10），幂等目标态。
func (s *Service) UpdateStockMoveStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	return s.stockMoveKernel().SetStatus(ctx, req.ID, req.Status)
}

// approveStockMoveDoc 审核落账（内核已认领并装载明细）：按仓库 ID 升序锁两仓
// 行 → 逐行出仓负、入仓正，两笔流水同额成对（同一 biz_type stock_move）。
func (s *Service) approveStockMoveDoc(ctx context.Context, tx doc.Tx[*model.StockMove, model.StockMoveItem], d *model.StockMove) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	if err := ensureDistinctWarehouses(d.FromWarehouseID, d.ToWarehouseID); err != nil {
		return err
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.FromWarehouseID, d.ToWarehouseID})
	if err != nil {
		return err
	}
	items := sortItemsByID(d.Items, func(it model.StockMoveItem) int64 { return it.ID })
	keys := make([]stockLockKey, 0, len(items)*2)
	for _, item := range items {
		keys = append(keys, stockLockKey{WarehouseID: d.FromWarehouseID, ProductID: item.ProductID})
		keys = append(keys, stockLockKey{WarehouseID: d.ToWarehouseID, ProductID: item.ProductID})
	}
	if err := s.lockStockRows(ctx, repo, keys); err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, stockMoveItemProducts(items))
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID: item.ProductID, WarehouseID: d.FromWarehouseID, Count: negQty(item.Count),
			BizType: model.StockRecordBizStockMove, BizID: d.ID, BizItemID: item.ID, BizNo: d.No,
			ProductName: productNames[item.ProductID], WarehouseName: warehouseNames[d.FromWarehouseID],
		}); err != nil {
			return err
		}
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID: item.ProductID, WarehouseID: d.ToWarehouseID, Count: item.Count,
			BizType: model.StockRecordBizStockMove, BizID: d.ID, BizItemID: item.ID, BizNo: d.No,
			ProductName: productNames[item.ProductID], WarehouseName: warehouseNames[d.ToWarehouseID],
		}); err != nil {
			return err
		}
	}
	return nil
}

// unapproveStockMoveDoc 反审核回滚（内核已认领并装载明细）：按仓库 ID 升序锁
// 两仓行 → 出仓反向正入、入仓反向负出（_CANCEL，原流水保留）。
func (s *Service) unapproveStockMoveDoc(ctx context.Context, tx doc.Tx[*model.StockMove, model.StockMoveItem], d *model.StockMove) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.FromWarehouseID, d.ToWarehouseID})
	if err != nil {
		return err
	}
	items := sortItemsByID(d.Items, func(it model.StockMoveItem) int64 { return it.ID })
	keys := make([]stockLockKey, 0, len(items)*2)
	for _, item := range items {
		keys = append(keys, stockLockKey{WarehouseID: d.FromWarehouseID, ProductID: item.ProductID})
		keys = append(keys, stockLockKey{WarehouseID: d.ToWarehouseID, ProductID: item.ProductID})
	}
	if err := s.lockStockRows(ctx, repo, keys); err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, stockMoveItemProducts(items))
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID: item.ProductID, WarehouseID: d.FromWarehouseID, Count: item.Count,
			BizType: model.StockRecordBizStockMoveCancel, BizID: d.ID, BizItemID: item.ID, BizNo: d.No,
			ProductName: productNames[item.ProductID], WarehouseName: warehouseNames[d.FromWarehouseID],
		}); err != nil {
			return err
		}
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID: item.ProductID, WarehouseID: d.ToWarehouseID, Count: negQty(item.Count),
			BizType: model.StockRecordBizStockMoveCancel, BizID: d.ID, BizItemID: item.ID, BizNo: d.No,
			ProductName: productNames[item.ProductID], WarehouseName: warehouseNames[d.ToWarehouseID],
		}); err != nil {
			return err
		}
	}
	return nil
}

// ExportStockMoves 导出库存调拨单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportStockMoves(ctx context.Context, req *dto.StockMovePageReq) ([]byte, error) {
	filter, err := s.stockMoveFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllStockMoves(ctx, filter)
	if err != nil {
		return nil, err
	}
	warehouseIDs := make([]int64, 0, len(list)*2)
	for _, row := range list {
		warehouseIDs = append(warehouseIDs, row.FromWarehouseID, row.ToWarehouseID)
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.StockMove) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.StockMoveExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockMoveExportRow{
			No:            row.No,
			FromWarehouse: warehouseNames[row.FromWarehouseID],
			ToWarehouse:   warehouseNames[row.ToWarehouseID],
			MoveTime:      row.MoveTime,
			Status:        docStatusNames[row.Status],
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// checkWarehousesExist 校验一批仓库在租户内存在（跨租户/不存在一律 400，
// 不泄露存在性）。
func (s *Service) checkWarehousesExist(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if id <= 0 {
			return xerr.BadRequest("缺少仓库")
		}
	}
	for _, id := range ids {
		warehouse, err := s.repo.FindWarehouseByID(ctx, id)
		if err != nil {
			return err
		}
		if warehouse == nil {
			return xerr.BadRequest("仓库不存在")
		}
	}
	return nil
}

func buildStockMoveItemRows(docID int64, drafts []moveItemDraft) []model.StockMoveItem {
	rows := make([]model.StockMoveItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.StockMoveItem{
			StockMoveID: docID,
			ProductID:   d.ProductID,
			Count:       d.Count,
		})
	}
	return rows
}

func stockMoveItemProducts(items []model.StockMoveItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}

// moveItemProductIDs 取调拨明细草稿的产品 ID（创建/编辑引用校验用）。
func moveItemProductIDs(items []moveItemDraft) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}
