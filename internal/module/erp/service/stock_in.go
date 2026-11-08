// stock_in.go 是其它入库单（QR）的适配器（ADR 0018）：写侧生命周期由
// service/doc 内核拥有，本文件只声明元数据、访问器、校验与落账钩子；
// 读侧（列表/详情/导出）与 DTO 映射仍在本文件。
// 库存变动只经唯一入账入口 ApplyStockChange（spec §4.2）；编号在落库同事务
// 经 erp_sequence 发放（spec §4.8）。其它出库单（stock_out.go）镜像同构。
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

// stockInKernel 构造其它入库单的内核实例（每次调用廉价；钩子捕获 Service）。
func (s *Service) stockInKernel() *doc.Kernel[*model.StockIn, model.StockInItem] {
	return doc.New(docEnv{s}, s.stockInStore(), doc.Spec[*model.StockIn, model.StockInItem]{
		Labels:     doc.Labels{Doc: "其它入库单", Item: "入库明细"},
		NoPrefix:   StockInNoPrefix,
		SeqBizType: SeqBizTypeStockIn,
		Approve:    s.approveStockInDoc,
		Unapprove:  s.unapproveStockInDoc,
	})
}

// stockInStore 装配其它入库单的仓储面（tracer 手写；候选 02 用泛型 store 替换）。
func (s *Service) stockInStore() doc.Store[*model.StockIn, model.StockInItem] {
	return newRepoDocStore(s.repo, s.businessNow, stockInOps)
}

// stockInOps 把 repository 方法装配成内核 Tx 面（nil 行翻译为 doc.ErrNotFound）。
func stockInOps(repo *repository.Repository) repoDocOps[*model.StockIn, model.StockInItem] {
	notFound := func(row *model.StockIn, err error) (*model.StockIn, error) {
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, doc.ErrNotFound
		}
		return row, nil
	}
	return repoDocOps[*model.StockIn, model.StockInItem]{
		Find: func(ctx context.Context, id int64) (*model.StockIn, error) {
			return notFound(repo.FindStockInByID(ctx, id))
		},
		FindWithItems: func(ctx context.Context, id int64) (*model.StockIn, error) {
			return notFound(repo.FindStockInWithItems(ctx, id))
		},
		Lock: func(ctx context.Context, id int64) (*model.StockIn, error) {
			return notFound(repo.FindStockInByIDForUpdate(ctx, id))
		},
		Create:      repo.CreateStockIn,
		Save:        repo.SaveStockIn,
		CreateItems: repo.CreateStockInItems,
		DeleteItems: repo.DeleteStockInItems,
		DeleteIfStatus: func(ctx context.Context, id int64, status int8) (int64, error) {
			// 内核只删未审核（status 恒为 10）；仓储方法已把谓词绑定到 DELETE。
			return repo.DeleteStockInIfUnaudited(ctx, id)
		},
		UpdateStatusIf: repo.UpdateStockInStatusIf,
	}
}

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

// CreateStockIn 创建其它入库单（仅未审核）：明细/引用/时间校验 → 内核事务内
// 发号、落主表与明细。不动库存（spec §4.1：只有审核动作落账）。
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
	return s.stockInKernel().Create(ctx,
		func(ctx context.Context) (*model.StockIn, error) {
			return &model.StockIn{
				WarehouseID: req.WarehouseId,
				InTime:      inTime,
				Type:        docType,
				Remark:      req.Remark,
			}, nil
		},
		func(ctx context.Context, d *model.StockIn) ([]model.StockInItem, error) {
			return buildStockInItemRows(d.ID, items), nil
		},
	)
}

// UpdateStockIn 修改其它入库单（仅未审核，已审核 409，spec §4.1）：
// 字段全量覆盖；单号不采纳（保持原号）；明细整体替换（软删旧明细）。
func (s *Service) UpdateStockIn(ctx context.Context, req *dto.StockInUpdateReq) error {
	var items []docItemDraft
	var inTime time.Time
	var docType int8
	return s.stockInKernel().Update(ctx, req.ID,
		func(ctx context.Context, d *model.StockIn) error {
			var err error
			items, err = buildDocItems(req.Items, "入库明细")
			if err != nil {
				return err
			}
			if err := s.checkDocRefsForWrite(ctx, req.WarehouseId, items); err != nil {
				return err
			}
			inTime, err = s.parseDocTime(req.InTime, "入库时间")
			if err != nil {
				return err
			}
			docType, err = validateDocType(req.Type, model.ValidStockInType, "入库单")
			return err
		},
		func(ctx context.Context, d *model.StockIn) error {
			d.WarehouseID = req.WarehouseId
			d.InTime = inTime
			d.Type = docType
			d.Remark = req.Remark
			return nil
		},
		func(ctx context.Context, d *model.StockIn) ([]model.StockInItem, error) {
			return buildStockInItemRows(d.ID, items), nil
		},
	)
}

// DeleteStockIn 删除其它入库单（仅未审核，已审核 409，spec §4.1）：
// 主表 + 明细一并软删除；不动库存（未审核未落账）。
func (s *Service) DeleteStockIn(ctx context.Context, id int64) error {
	return s.stockInKernel().Delete(ctx, id)
}

// UpdateStockInStatus 审核（10→20）/反审核（20→10），幂等目标态（spec §4.1/§5）。
func (s *Service) UpdateStockInStatus(ctx context.Context, req *dto.StockDocUpdateStatusReq) error {
	return s.stockInKernel().SetStatus(ctx, req.ID, req.Status)
}

// approveStockInDoc 审核落账（内核已认领 status=10→20 并装载明细）：
// 逐行经唯一入账入口入账（biz_type=stock_in，正入）。
func (s *Service) approveStockInDoc(ctx context.Context, tx doc.Tx[*model.StockIn, model.StockInItem], d *model.StockIn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.WarehouseID})
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, stockInItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.StockInItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         item.Count,
			BizType:       model.StockRecordBizStockIn,
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

// unapproveStockInDoc 反审核回滚（内核已认领 status=20→10 并装载明细）：
// 逐行经唯一入账入口新增 _CANCEL 反向流水（count 取负，原流水保留）→
// 余额恢复。
func (s *Service) unapproveStockInDoc(ctx context.Context, tx doc.Tx[*model.StockIn, model.StockInItem], d *model.StockIn) error {
	repo, err := docRepo(tx)
	if err != nil {
		return err
	}
	warehouseNames, err := repo.FindWarehouseNamesIn(ctx, []int64{d.WarehouseID})
	if err != nil {
		return err
	}
	productNames, err := s.productNamesForItems(ctx, repo, stockInItemProducts(d.Items))
	if err != nil {
		return err
	}
	for _, item := range sortItemsByID(d.Items, func(it model.StockInItem) int64 { return it.ID }) {
		if err := s.ApplyStockChange(ctx, repo, StockChange{
			ProductID:     item.ProductID,
			WarehouseID:   d.WarehouseID,
			Count:         negQty(item.Count),
			BizType:       model.StockRecordBizStockInCancel,
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
	return s.stockInKernel().Find(ctx, id)
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
