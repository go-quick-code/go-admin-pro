// stock_in.go 是其它入库单（QR）的业务方法（issue #90，docs/erp-mvp-spec.md
// §3.5/§4.1/§4.8/§5）：创建/编辑/删除（仅未审核）、审核落账、反审核回滚。
// 库存变动只经唯一入账入口 ApplyStockChange（spec §4.2）；编号在落库同事务
// 经 erp_sequence 发放（spec §4.8）。其它出库单（stock_out.go）镜像同构。
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

// ListStockIns 其它入库单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListStockIns(ctx context.Context, req *dto.StockInPageReq) ([]dto.StockDocRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.stockInFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListStockIns(ctx, filter, page.Offset(), page.PageSize)
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
			DocTime:       row.InTime,
			Type:          row.Type,
			TypeName:      stockInTypeNames[row.Type],
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, total, nil
}

// stockInFilter 构造入库单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) stockInFilter(ctx context.Context, req *dto.StockInPageReq) (repository.StockInFilter, error) {
	if req.Type != nil && !model.ValidStockInType(*req.Type) {
		return repository.StockInFilter{}, xerr.BadRequest("入库单类型不合法")
	}
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.StockInFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.StockInFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.StockInFilter{}, err
	}
	filter := repository.StockInFilter{
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

// GetStockIn 其它入库单详情（带头 + 明细，产品名称服务端解析）。
func (s *Service) GetStockIn(ctx context.Context, id int64) (*dto.StockInDetail, error) {
	doc, err := s.findStockIn(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindStockInWithItems(ctx, doc.ID)
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
	items := make([]dto.StockInItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.StockInItemRow{
			ID:           item.ID,
			ProductId:    item.ProductID,
			ProductName:  productNames[item.ProductID],
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   item.TotalPrice,
		})
	}
	return &dto.StockInDetail{
		ID:            full.ID,
		No:            full.No,
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		InTime:        full.InTime,
		Type:          full.Type,
		TypeName:      stockInTypeNames[full.Type],
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateStockIn 创建其它入库单（仅未审核）：明细校验 → 仓库/产品存在性 →
// 事务内发放单号（QR，erp_sequence）→ 落主表 + 明细。不动库存（spec §4.1：
// 只有审核动作落账）。
func (s *Service) CreateStockIn(ctx context.Context, req *dto.StockInCreateReq) (*model.StockIn, error) {
	items, err := buildDocItems(req.Items, "入库明细")
	if err != nil {
		return nil, err
	}
	if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
		return nil, err
	}
	inTime, err := s.parseDocTime(req.InTime, "入库时间")
	if err != nil {
		return nil, err
	}
	docType, err := validateDocType(req.Type, model.ValidStockInType, "入库单")
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.StockIn
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeStockIn, StockInNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.StockIn{
			No:          no,
			WarehouseID: req.WarehouseId,
			InTime:      inTime,
			Type:        docType,
			Status:      model.DocStatusUnaudited,
			Remark:      req.Remark,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreateStockIn(ctx, doc); err != nil {
			return err
		}
		return tx.CreateStockInItems(ctx, buildStockInItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdateStockIn 修改其它入库单（仅未审核，已审核 409，spec §4.1）：
// 字段全量覆盖；单号不采纳（保持原号）；明细整体替换（软删旧明细）。
func (s *Service) UpdateStockIn(ctx context.Context, req *dto.StockInUpdateReq) error {
	row, err := s.findStockIn(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	items, err := buildDocItems(req.Items, "入库明细")
	if err != nil {
		return err
	}
	if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
		return err
	}
	inTime, err := s.parseDocTime(req.InTime, "入库时间")
	if err != nil {
		return err
	}
	docType, err := validateDocType(req.Type, model.ValidStockInType, "入库单")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 状态不变量绑定到写入路径（安全 F1）：事务内对自身行加锁重读并复核
		// status=10。与审核的 UPDATE ... WHERE status=10 认领共用同一行锁——
		// 审核先提交则这里读到 20 → 409；编辑先提交则审核随后照常认领。
		locked, err := tx.FindStockInByIDForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("其它入库单不存在")
		}
		if locked.Status != model.DocStatusUnaudited {
			return xerr.Conflict("已审核单据不可修改")
		}
		locked.WarehouseID = req.WarehouseId
		locked.InTime = inTime
		locked.Type = docType
		locked.Remark = req.Remark
		if err := tx.SaveStockIn(ctx, locked); err != nil {
			return err
		}
		// 明细整体替换：软删旧行 + 插入新行（未审核单无流水引用，安全）。
		if err := tx.DeleteStockInItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateStockInItems(ctx, buildStockInItemRows(row.ID, items))
	})
}

// DeleteStockIn 删除其它入库单（仅未审核，已审核 409，spec §4.1）：
// 主表 + 明细一并软删除；不动库存（未审核未落账）。
func (s *Service) DeleteStockIn(ctx context.Context, id int64) error {
	row, err := s.findStockIn(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 状态不变量绑定到删除语句（安全 F1）：条件软删 WHERE id=? AND status=10，
		// 影响行数为 0 说明已被并发审核（或删除）→ 409。条件写与审核认领在同一
		// 行锁上串行化：审核先提交则状态谓词不再匹配，绝不影响已审核行。
		deleted, err := tx.DeleteStockInIfUnaudited(ctx, row.ID)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return xerr.Conflict("已审核单据不可删除")
		}
		return tx.DeleteStockInItems(ctx, row.ID)
	})
}

// UpdateStockInStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateStockInStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	row, err := s.findStockIn(ctx, req.ID)
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
		return s.approveStockIn(ctx, row)
	}
	return s.unapproveStockIn(ctx, row)
}

// approveStockIn 审核落账：事务内先乐观认领状态（防并发双重审核）→
// 重读明细 → 逐行经唯一入账入口入账（biz_type=stock_in，正入）。
func (s *Service) approveStockIn(ctx context.Context, row *model.StockIn) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockInStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockInWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockInItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.StockInItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizStockIn,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// unapproveStockIn 反审核回滚（spec §4.3）：事务内先乐观认领状态 →
// 逐行经唯一入账入口新增 _CANCEL 反向流水（count 取负，原流水保留）→
// 余额恢复。
func (s *Service) unapproveStockIn(ctx context.Context, row *model.StockIn) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockInStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockInWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockInItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.StockInItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizStockInCancel,
				BizID:         doc.ID,
				BizItemID:     item.ID,
				BizNo:         doc.No,
				ProductName:   productNames[item.ProductID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExportStockIns 导出其它入库单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportStockIns(ctx context.Context, req *dto.StockInPageReq) ([]byte, error) {
	filter, err := s.stockInFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllStockIns(ctx, filter)
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
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.StockIn) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.StockInExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockInExportRow{
			No:            row.No,
			Warehouse:     warehouseNames[row.WarehouseID],
			InTime:        row.InTime,
			Type:          stockInTypeNames[row.Type],
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// findStockIn 取入库单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findStockIn(ctx context.Context, id int64) (*model.StockIn, error) {
	row, err := s.repo.FindStockInByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("其它入库单不存在")
	}
	if err := s.checkRowVisible(ctx, "其它入库单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// checkDocRefsForWrite 校验单据写入引用：仓库与全部产品必须存在（租户内，
// 跨租户/不存在一律「不存在」400，不泄露存在性）。
func (s *Service) checkDocRefsForWrite(ctx context.Context, warehouseID int64, items []docItemDraft) error {
	if warehouseID <= 0 {
		return xerr.BadRequest("缺少仓库")
	}
	warehouse, err := s.repo.FindWarehouseByID(ctx, warehouseID)
	if err != nil {
		return err
	}
	if warehouse == nil {
		return xerr.BadRequest("仓库不存在")
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

// productNamesForItems 在事务内批量解析产品名（409 文案与详情展示用）。
func (s *Service) productNamesForItems(ctx context.Context, tx *repository.Repository, ids []int64) (map[int64]string, error) {
	return tx.FindProductNamesIn(ctx, ids)
}

func buildStockInItemRows(docID int64, drafts []docItemDraft) []model.StockInItem {
	rows := make([]model.StockInItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.StockInItem{
			StockInID:    docID,
			ProductID:    d.ProductID,
			Count:        d.Count,
			ProductPrice: d.ProductPrice,
			TotalPrice:   d.TotalPrice,
		})
	}
	return rows
}

func stockInItemProducts(items []model.StockInItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}
