// stock_out.go 是其它出库单（QC）的业务方法（issue #90，docs/erp-mvp-spec.md
// §3.5/§4.1/§4.8/§5）：与 stock_in.go 镜像同构（out_time/type、QC 前缀、
// biz_type=stock_out）；出库方向经唯一入账入口的 SQL 双保险（count >= ?）
// 拦截负库存，余额不足 409（提示产品/仓库/当前库存/变更量）。
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
	doc, err := s.findStockOut(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindStockOutWithItems(ctx, doc.ID)
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

// CreateStockOut 创建其它出库单（仅未审核）：明细校验 → 仓库/产品存在性 →
// 事务内发放单号（QC，erp_sequence）→ 落主表 + 明细。不动库存（spec §4.1）。
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
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.StockOut
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeStockOut, StockOutNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.StockOut{
			No:          no,
			WarehouseID: req.WarehouseId,
			OutTime:     outTime,
			Type:        docType,
			Status:      model.DocStatusUnaudited,
			Remark:      req.Remark,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreateStockOut(ctx, doc); err != nil {
			return err
		}
		return tx.CreateStockOutItems(ctx, buildStockOutItemRows(doc.ID, items))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdateStockOut 修改其它出库单（仅未审核，已审核 409，spec §4.1）。
func (s *Service) UpdateStockOut(ctx context.Context, req *dto.StockOutUpdateReq) error {
	row, err := s.findStockOut(ctx, req.ID)
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
	if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
		return err
	}
	outTime, err := s.parseDocTime(req.OutTime, "出库时间")
	if err != nil {
		return err
	}
	docType, err := validateDocType(req.Type, model.ValidStockOutType, "出库单")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row.WarehouseID = req.WarehouseId
		row.OutTime = outTime
		row.Type = docType
		row.Remark = req.Remark
		if err := tx.SaveStockOut(ctx, row); err != nil {
			return err
		}
		if err := tx.DeleteStockOutItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateStockOutItems(ctx, buildStockOutItemRows(row.ID, items))
	})
}

// DeleteStockOut 删除其它出库单（仅未审核，已审核 409，spec §4.1）。
func (s *Service) DeleteStockOut(ctx context.Context, id int64) error {
	row, err := s.findStockOut(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.DeleteStockOut(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteStockOutItems(ctx, row.ID)
	})
}

// UpdateStockOutStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateStockOutStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	row, err := s.findStockOut(ctx, req.ID)
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
		return s.approveStockOut(ctx, row)
	}
	return s.unapproveStockOut(ctx, row)
}

// approveStockOut 审核落账：事务内先乐观认领状态（防并发双重审核）→
// 重读明细 → 逐行经唯一入账入口出账（biz_type=stock_out，负出；余额不足 409，
// 事务整体回滚——一单一事务，绝无部分落账）。
func (s *Service) approveStockOut(ctx context.Context, row *model.StockOut) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockOutStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockOutWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockOutItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.StockOutItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         negQty(item.Count),
				BizType:       model.StockRecordBizStockOut,
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

// unapproveStockOut 反审核回滚（spec §4.3）：新增 _CANCEL 反向流水
// （count 取负——出库单的反向是正入），原流水保留、余额恢复。
func (s *Service) unapproveStockOut(ctx context.Context, row *model.StockOut) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockOutStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockOutWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockOutItemProducts(doc.Items))
		if err != nil {
			return err
		}
		for _, item := range sortItemsByID(doc.Items, func(it model.StockOutItem) int64 { return it.ID }) {
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         item.Count,
				BizType:       model.StockRecordBizStockOutCancel,
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
	rows := make([]dto.StockOutExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockOutExportRow{
			No:          row.No,
			Warehouse:   warehouseNames[row.WarehouseID],
			OutTime:     row.OutTime,
			Type:        stockOutTypeNames[row.Type],
			Status:      docStatusNames[row.Status],
			Remark:      row.Remark,
			OwnerUserID: row.OwnerUserID,
			CreateTime:  row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// findStockOut 取出库单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findStockOut(ctx context.Context, id int64) (*model.StockOut, error) {
	row, err := s.repo.FindStockOutByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("其它出库单不存在")
	}
	if err := s.checkRowVisible(ctx, "其它出库单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
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
