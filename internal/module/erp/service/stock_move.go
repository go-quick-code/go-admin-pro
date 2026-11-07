// stock_move.go 是库存调拨单（DB）的业务方法（issue #93，docs/erp-mvp-spec.md
// §3.5/§4.1/§4.2/§4.6/§4.8/§5）：创建/编辑/删除（仅未审核）、审核落账
// （同事务出仓负、入仓正两笔流水同额成对）、反审核各生成反向流水。
// 库存变动只经唯一入账入口 ApplyStockChange（spec §4.2）；多仓库先按仓库 ID
// 升序取行锁防死锁；编号在落库同事务经 erp_sequence 发放（spec §4.8）。
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
	doc, err := s.findStockMove(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindStockMoveWithItems(ctx, doc.ID)
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
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.StockMove
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeStockMove, StockMoveNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.StockMove{
			No:              no,
			FromWarehouseID: req.FromWarehouseId,
			ToWarehouseID:   req.ToWarehouseId,
			MoveTime:        moveTime,
			Status:          model.DocStatusUnaudited,
			OwnerUserID:     ownerID,
			OwnerDeptID:     deptID,
		}
		if err := tx.CreateStockMove(ctx, doc); err != nil {
			return err
		}
		return tx.CreateStockMoveItems(ctx, buildStockMoveItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdateStockMove 修改库存调拨单（仅未审核，已审核 409）：字段全量覆盖；
// 单号不采纳（保持原号）；明细整体替换（软删旧明细）。
func (s *Service) UpdateStockMove(ctx context.Context, req *dto.StockMoveUpdateReq) error {
	row, err := s.findStockMove(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	if err := ensureDistinctWarehouses(req.FromWarehouseId, req.ToWarehouseId); err != nil {
		return err
	}
	items, err := buildMoveItemDrafts(req.Items)
	if err != nil {
		return err
	}
	if err := s.checkWarehousesExist(ctx, req.FromWarehouseId, req.ToWarehouseId); err != nil {
		return err
	}
	if err := s.checkProductsExist(ctx, moveItemProductIDs(items)); err != nil {
		return err
	}
	moveTime, err := s.parseDocTime(req.MoveTime, "调拨时间")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row.FromWarehouseID = req.FromWarehouseId
		row.ToWarehouseID = req.ToWarehouseId
		row.MoveTime = moveTime
		if err := tx.SaveStockMove(ctx, row); err != nil {
			return err
		}
		// 明细整体替换：软删旧行 + 插入新行（未审核单无流水引用，安全）。
		if err := tx.DeleteStockMoveItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateStockMoveItems(ctx, buildStockMoveItemRows(row.ID, items))
	})
}

// DeleteStockMove 删除库存调拨单（仅未审核，已审核 409）：主表 + 明细一并
// 软删除；不动库存（未审核未落账）。
func (s *Service) DeleteStockMove(ctx context.Context, id int64) error {
	row, err := s.findStockMove(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.DeleteStockMove(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteStockMoveItems(ctx, row.ID)
	})
}

// UpdateStockMoveStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateStockMoveStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	row, err := s.findStockMove(ctx, req.ID)
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
		return s.approveStockMove(ctx, row)
	}
	return s.unapproveStockMove(ctx, row)
}

// approveStockMove 审核落账（spec §4.6）：事务内先乐观认领状态（防并发双重
// 审核）→ 重读明细 → 按仓库 ID 升序先取两仓行锁防死锁 → 逐行经唯一入账入口
// 入账：出仓负、入仓正，两笔流水同额成对（同一 biz_type stock_move）。
// 源仓不足由入账入口 409，整单事务回滚（状态与流水均不落库）。
func (s *Service) approveStockMove(ctx context.Context, row *model.StockMove) error {
	if err := ensureDistinctWarehouses(row.FromWarehouseID, row.ToWarehouseID); err != nil {
		return err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.FromWarehouseID, row.ToWarehouseID})
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockMoveStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockMoveWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		items := sortItemsByID(doc.Items, func(it model.StockMoveItem) int64 { return it.ID })
		keys := make([]stockLockKey, 0, len(items)*2)
		for _, item := range items {
			keys = append(keys, stockLockKey{WarehouseID: doc.FromWarehouseID, ProductID: item.ProductID})
			keys = append(keys, stockLockKey{WarehouseID: doc.ToWarehouseID, ProductID: item.ProductID})
		}
		if err := s.lockStockRows(ctx, tx, keys); err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockMoveItemProducts(items))
		if err != nil {
			return err
		}
		for _, item := range items {
			// 出仓：count 取负入账（源仓不足 → 409）。
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.FromWarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizStockMove,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseNames[doc.FromWarehouseID],
			}); err != nil {
				return err
			}
			// 入仓：同额正数入账（与出仓流水成对）。
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.ToWarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizStockMove,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseNames[doc.ToWarehouseID],
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// unapproveStockMove 反审核回滚（spec §4.3/§4.6）：事务内先乐观认领状态 →
// 逐行经唯一入账入口新增 _CANCEL 反向流水（出仓 +count、入仓 −count，原流水
// 保留）→ 两仓余额恢复。调入仓若已被后续业务消耗，反向出账 409（整单回滚）。
func (s *Service) unapproveStockMove(ctx context.Context, row *model.StockMove) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.FromWarehouseID, row.ToWarehouseID})
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockMoveStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockMoveWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		items := sortItemsByID(doc.Items, func(it model.StockMoveItem) int64 { return it.ID })
		keys := make([]stockLockKey, 0, len(items)*2)
		for _, item := range items {
			keys = append(keys, stockLockKey{WarehouseID: doc.FromWarehouseID, ProductID: item.ProductID})
			keys = append(keys, stockLockKey{WarehouseID: doc.ToWarehouseID, ProductID: item.ProductID})
		}
		if err := s.lockStockRows(ctx, tx, keys); err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockMoveItemProducts(items))
		if err != nil {
			return err
		}
		for _, item := range items {
			// 调出仓反向入账（+count）。
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.FromWarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizStockMoveCancel,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseNames[doc.FromWarehouseID],
			}); err != nil {
				return err
			}
			// 调入仓反向出账（−count）。
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.ToWarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizStockMoveCancel,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseNames[doc.ToWarehouseID],
			}); err != nil {
				return err
			}
		}
		return nil
	})
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

// findStockMove 取调拨单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findStockMove(ctx context.Context, id int64) (*model.StockMove, error) {
	row, err := s.repo.FindStockMoveByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("库存调拨单不存在")
	}
	if err := s.checkRowVisible(ctx, "库存调拨单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
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
