// stock_check.go 是库存盘点单（PD）的业务方法（issue #93，docs/erp-mvp-spec.md
// §3.5/§4.1/§4.2/§4.6/§4.8/§5）：创建/编辑/删除（仅未审核）、审核按差异调整
// （差异 = actual_count − 审核时账面数，差正盘盈入、差负盘亏出）、反审核按已
// 入账流水净额反向。库存变动只经唯一入账入口 ApplyStockChange（spec §4.2）；
// base_count 仅创建时快照记录，审核不使用（spec §4.6）。
package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListStockChecks 库存盘点单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListStockChecks(ctx context.Context, req *dto.StockCheckPageReq) ([]dto.StockCheckRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.stockCheckFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListStockChecks(ctx, filter, page.Offset(), page.PageSize)
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
	out := make([]dto.StockCheckRow, 0, len(list))
	for _, row := range list {
		out = append(out, dto.StockCheckRow{
			ID:            row.ID,
			No:            row.No,
			WarehouseId:   row.WarehouseID,
			WarehouseName: warehouseNames[row.WarehouseID],
			CheckTime:     row.CheckTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return out, total, nil
}

// stockCheckFilter 构造盘点单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) stockCheckFilter(ctx context.Context, req *dto.StockCheckPageReq) (repository.StockCheckFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.StockCheckFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.StockCheckFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.StockCheckFilter{}, err
	}
	filter := repository.StockCheckFilter{
		No:          req.No,
		WarehouseId: req.WarehouseId,
		Status:      req.Status,
		StartTime:   start,
		EndTime:     end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetStockCheck 盘点单详情（带头 + 明细，产品名称服务端解析；baseCount 为
// 创建时账面快照，仅供展示/追溯，spec §4.6）。
func (s *Service) GetStockCheck(ctx context.Context, id int64) (*dto.StockCheckDetail, error) {
	doc, err := s.findStockCheck(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindStockCheckWithItems(ctx, doc.ID)
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
	items := make([]dto.StockCheckItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		items = append(items, dto.StockCheckItemRow{
			ID:          item.ID,
			ProductId:   item.ProductID,
			ProductName: productNames[item.ProductID],
			ActualCount: item.ActualCount,
			BaseCount:   item.BaseCount,
		})
	}
	return &dto.StockCheckDetail{
		ID:            full.ID,
		No:            full.No,
		WarehouseId:   full.WarehouseID,
		WarehouseName: warehouseNames[full.WarehouseID],
		CheckTime:     full.CheckTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateStockCheck 创建库存盘点单（仅未审核）：明细校验 → 仓库/产品存在性 →
// 事务内发放单号（PD）并快照 create 时账面数 base_count → 落主表 + 明细。
// 不动库存（spec §4.1）。
func (s *Service) CreateStockCheck(ctx context.Context, req *dto.StockCheckCreateReq) (*model.StockCheck, error) {
	if _, err := buildCheckItemDrafts(req.Items, nil); err != nil {
		return nil, err
	}
	itemDrafts, err := s.buildCheckDraftsWithBase(ctx, req.WarehouseId, req.Items)
	if err != nil {
		return nil, err
	}
	checkTime, err := s.parseDocTime(req.CheckTime, "盘点时间")
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.StockCheck
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeStockCheck, StockCheckNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.StockCheck{
			No:          no,
			WarehouseID: req.WarehouseId,
			CheckTime:   checkTime,
			Status:      model.DocStatusUnaudited,
			Remark:      req.Remark,
			OwnerUserID: ownerID,
			OwnerDeptID: deptID,
		}
		if err := tx.CreateStockCheck(ctx, doc); err != nil {
			return err
		}
		return tx.CreateStockCheckItems(ctx, buildStockCheckItemRows(doc.ID, itemDrafts))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdateStockCheck 修改库存盘点单（仅未审核，已审核 409）：字段全量覆盖；
// 单号不采纳；明细整体替换，base_count 按保存时账面重新快照（明细重写视同
// 重新建单；审核仍以当时账面为准，spec §4.6）。
func (s *Service) UpdateStockCheck(ctx context.Context, req *dto.StockCheckUpdateReq) error {
	row, err := s.findStockCheck(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	if _, err := buildCheckItemDrafts(req.Items, nil); err != nil {
		return err
	}
	itemDrafts, err := s.buildCheckDraftsWithBase(ctx, req.WarehouseId, req.Items)
	if err != nil {
		return err
	}
	checkTime, err := s.parseDocTime(req.CheckTime, "盘点时间")
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 状态不变量绑定到写入路径（安全 F1）：事务内锁自身行并复核 status=10，
		// 与审核的 UPDATE ... WHERE status=10 认领在同一行锁上串行化。
		locked, err := tx.FindStockCheckByIDForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("库存盘点单不存在")
		}
		if locked.Status != model.DocStatusUnaudited {
			return xerr.Conflict("已审核单据不可修改")
		}
		locked.WarehouseID = req.WarehouseId
		locked.CheckTime = checkTime
		locked.Remark = req.Remark
		if err := tx.SaveStockCheck(ctx, locked); err != nil {
			return err
		}
		if err := tx.DeleteStockCheckItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateStockCheckItems(ctx, buildStockCheckItemRows(row.ID, itemDrafts))
	})
}

// DeleteStockCheck 删除库存盘点单（仅未审核，已审核 409）：主表 + 明细一并
// 软删除；不动库存（未审核未落账）。
func (s *Service) DeleteStockCheck(ctx context.Context, id int64) error {
	row, err := s.findStockCheck(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 条件软删 WHERE id=? AND status=10（安全 F1）：影响行数为 0 → 409。
		deleted, err := tx.DeleteStockCheckIfUnaudited(ctx, row.ID)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return xerr.Conflict("已审核单据不可删除")
		}
		return tx.DeleteStockCheckItems(ctx, row.ID)
	})
}

// UpdateStockCheckStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateStockCheckStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	row, err := s.findStockCheck(ctx, req.ID)
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
		return s.approveStockCheck(ctx, row)
	}
	return s.unapproveStockCheck(ctx, row)
}

// approveStockCheck 审核调整（spec §4.6）：事务内先乐观认领状态 → 重读明细 →
// 按产品 ID 升序取本仓行锁 → 逐行按「差异 = actual_count − 当时账面数」经
// 唯一入账入口调整（差正盘盈入、差负盘亏出、差零不产生流水）；审核后
// 账面 = 实际数。base_count 不参与计算。
func (s *Service) approveStockCheck(ctx context.Context, row *model.StockCheck) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockCheckStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockCheckWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		items := sortItemsByID(doc.Items, func(it model.StockCheckItem) int64 { return it.ID })
		keys := make([]stockLockKey, 0, len(items))
		for _, item := range items {
			keys = append(keys, stockLockKey{WarehouseID: doc.WarehouseID, ProductID: item.ProductID})
		}
		if err := s.lockStockRows(ctx, tx, keys); err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, stockCheckItemProducts(items))
		if err != nil {
			return err
		}
		tenantID := erpTenantID(ctx)
		for _, item := range items {
			// 审核以「当时账面」为准（行锁内读取；base_count 仅记录，spec §4.6）。
			book, err := tx.LockStock(ctx, tenantID, item.ProductID, doc.WarehouseID)
			if err != nil {
				return err
			}
			diff, err := checkDiff(item.ActualCount, book)
			if err != nil {
				return err
			}
			if diff.Milli() == 0 {
				// 账实一致：不产生流水（审核后账面已 = 实际数）。
				continue
			}
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     item.ProductID,
				WarehouseID:   doc.WarehouseID,
				Count:         diff,
				BizType:       model.StockRecordBizStockCheck,
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

// unapproveStockCheck 反审核回滚（spec §4.3/§4.6）：事务内先乐观认领状态 →
// 按该单据全部盘点流水（正向 + _CANCEL）的代数和得到当前生效调整量 → 逐产品
// 取负经唯一入账入口新增 _CANCEL 反向流水（原流水保留）。按净额而非重算
// 差异，保证「审核 → 反审核 → 再审核」多轮后仍能准确回滚。
func (s *Service) unapproveStockCheck(ctx context.Context, row *model.StockCheck) error {
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, []int64{row.WarehouseID})
	if err != nil {
		return err
	}
	warehouseName := warehouseNames[row.WarehouseID]
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateStockCheckStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindStockCheckWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		records, err := tx.ListStockRecordsByBiz(ctx, model.StockRecordBizStockCheck, doc.ID)
		if err != nil {
			return err
		}
		netByProduct := make(map[int64]qty.Qty, len(records))
		for _, record := range records {
			sum, err := netByProduct[record.ProductID].Add(record.Count)
			if err != nil {
				return xerr.BadRequest(fmt.Sprintf("盘点调整净额超出范围（产品 %d）", record.ProductID))
			}
			netByProduct[record.ProductID] = sum
		}
		itemIDs := make(map[int64]int64, len(doc.Items))
		keys := make([]stockLockKey, 0, len(netByProduct))
		productIDs := make([]int64, 0, len(netByProduct))
		for _, item := range sortItemsByID(doc.Items, func(it model.StockCheckItem) int64 { return it.ID }) {
			if _, ok := itemIDs[item.ProductID]; !ok {
				itemIDs[item.ProductID] = item.ID
			}
			productIDs = append(productIDs, item.ProductID)
		}
		for productID := range netByProduct {
			keys = append(keys, stockLockKey{WarehouseID: doc.WarehouseID, ProductID: productID})
		}
		if err := s.lockStockRows(ctx, tx, keys); err != nil {
			return err
		}
		productNames, err := s.productNamesForItems(ctx, tx, productIDs)
		if err != nil {
			return err
		}
		// 按产品 ID 升序反向（单一仓库内确定性加锁/入账顺序）。
		for _, productID := range sortedProductIDs(netByProduct) {
			net := netByProduct[productID]
			if net.Milli() == 0 {
				continue
			}
			if err := s.ApplyStockChange(ctx, tx, StockChange{
				ProductID:     productID,
				WarehouseID:   doc.WarehouseID,
				Count:         negQty(net),
				BizType:       model.StockRecordBizStockCheckCancel,
				BizID:         doc.ID,
				BizItemID:     itemIDs[productID],
				BizNo:         doc.No,
				ProductName:   productNames[productID],
				WarehouseName: warehouseName,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExportStockChecks 导出库存盘点单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportStockChecks(ctx context.Context, req *dto.StockCheckPageReq) ([]byte, error) {
	filter, err := s.stockCheckFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllStockChecks(ctx, filter)
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
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.StockCheck) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.StockCheckExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockCheckExportRow{
			No:            row.No,
			Warehouse:     warehouseNames[row.WarehouseID],
			CheckTime:     row.CheckTime,
			Status:        docStatusNames[row.Status],
			Remark:        row.Remark,
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// findStockCheck 取盘点单并做可见性校验（数据范围外一律 404，spec §4.10）。
func (s *Service) findStockCheck(ctx context.Context, id int64) (*model.StockCheck, error) {
	row, err := s.repo.FindStockCheckByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("库存盘点单不存在")
	}
	if err := s.checkRowVisible(ctx, "库存盘点单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// buildCheckDraftsWithBase 校验仓库/产品引用并快照当前账面数（base_count）。
// 引用校验复用其它单据的公共实现（仓库与全部产品必须存在）。
func (s *Service) buildCheckDraftsWithBase(ctx context.Context, warehouseID int64, items []dto.StockCheckItemPayload) ([]checkItemDraft, error) {
	if warehouseID <= 0 {
		return nil, xerr.BadRequest("缺少仓库")
	}
	warehouse, err := s.repo.FindWarehouseByID(ctx, warehouseID)
	if err != nil {
		return nil, err
	}
	if warehouse == nil {
		return nil, xerr.BadRequest("仓库不存在")
	}
	productIDs := make([]int64, 0, len(items))
	for _, item := range items {
		productIDs = append(productIDs, item.ProductId)
	}
	names, err := s.repo.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range productIDs {
		if _, ok := names[id]; !ok {
			return nil, xerr.BadRequest("产品不存在")
		}
	}
	baseCounts, err := s.repo.FindStockCountsIn(ctx, warehouseID, productIDs)
	if err != nil {
		return nil, err
	}
	return buildCheckItemDrafts(items, baseCounts)
}

func buildStockCheckItemRows(docID int64, drafts []checkItemDraft) []model.StockCheckItem {
	rows := make([]model.StockCheckItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.StockCheckItem{
			StockCheckID: docID,
			ProductID:    d.ProductID,
			ActualCount:  d.ActualCount,
			BaseCount:    d.BaseCount,
		})
	}
	return rows
}

func stockCheckItemProducts(items []model.StockCheckItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}

// sortedProductIDs 返回映射中的产品 ID 升序（盘点反审核的确定性入账顺序）。
func sortedProductIDs(m map[int64]qty.Qty) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortInt64s(ids)
	return ids
}

// sortInt64s 就地升序排序（避免为一个小切片引入额外依赖语义）。
func sortInt64s(ids []int64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
