// stock.go 是库存账的业务层（issue #90，docs/erp-mvp-spec.md §3.2/§4.2/§4.3/§5）：
// ApplyStockChange 是全 ERP 唯一的库存入账入口——任何库存数量变动（本票两类
// 单据的审核/反审核，后续票的采购/销售/调拨/盘点/收付款驱动的一切变动）只允许
// 经它在事务内完成「校验 → 更新余额 → 插流水」，任何代码不得直改 erp_stock。
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

// StockChange 是一次库存变动请求（入账方向的完整业务描述）。
type StockChange struct {
	ProductID   int64
	WarehouseID int64
	// Count 是变动量：正入负出（qty 定点，非零）。
	Count qty.Qty
	// BizType/BizID/BizItemID/BizNo 是流水上的单据引用（spec §3.2/§4.3）。
	BizType   string
	BizID     int64
	BizItemID int64
	BizNo     string
	// ProductName/WarehouseName 仅供 409 文案（余额不足时拼进提示）；
	// 可为空（回退展示 ID）。
	ProductName   string
	WarehouseName string
}

// negQty 返回数量的相反数（单据数量 > 0 校验在单据规则层，负号取反安全）。
func negQty(q qty.Qty) qty.Qty { return qty.FromMilli(-q.Milli()) }

// ApplyStockChange 唯一入账入口（spec §4.2）：必须在数据库事务内调用（tx 为
// 绑定同一事务的 Repository，见 repository.Transaction）。步骤：
//
//  1. 参数校验：产品/仓库 ID 与 biz 引用非零、变动量非零；
//  2. 先锁后写：EnsureStockRow 物化余额行（唯一键 no-op upsert）→
//     LockStock 取行锁读当前余额（409 文案数据源）；
//  3. 更新余额：入账方向 count + ?；出账方向 SQL 带 `count >= ?` 条件
//     （spec §4.2 应用层 + SQL 双保险的 SQL 侧），影响行数为 0 → 409
//     （提示产品/仓库/当前库存/变更量）；
//  4. 插入流水：count 正入负出 + total_count 变动后结存快照 + 单据引用
//     （spec §4.3：余额 = 初始 + 流水代数和，可审计重放）。
//
// 并发约定：本函数只锁单一仓库×产品行；将来跨多仓库的调用方（调拨 #93）
// 必须按仓库 ID 升序逐仓加锁后再入账（本票单仓，规则先行成文，spec §4.2）。
func (s *Service) ApplyStockChange(ctx context.Context, tx *repository.Repository, ch StockChange) error {
	if ch.ProductID <= 0 {
		return xerr.BadRequest("库存入账缺少产品 ID")
	}
	if ch.WarehouseID <= 0 {
		return xerr.BadRequest("库存入账缺少仓库 ID")
	}
	if ch.BizType == "" || ch.BizID <= 0 {
		return xerr.BadRequest("库存入账缺少业务单据引用")
	}
	if ch.Count.Milli() == 0 {
		return xerr.BadRequest("库存变动量不能为 0")
	}
	tenantID := erpTenantID(ctx)

	// 1) 物化余额行（幂等；唯一键 upsert 在行锁内完成，避免 LockStock 落 gap 锁）。
	if err := tx.EnsureStockRow(ctx, tenantID, ch.ProductID, ch.WarehouseID); err != nil {
		return err
	}
	// 2) 行锁 + 当前余额（先锁后写，spec §4.2）。
	current, err := tx.LockStock(ctx, tenantID, ch.ProductID, ch.WarehouseID)
	if err != nil {
		return err
	}
	// 3) 更新余额。SQL 条件是负库存禁令的最终裁决：出账影响 0 行 → 409。
	var after qty.Qty
	if ch.Count.Milli() > 0 {
		after, err = current.Add(ch.Count)
		if err != nil {
			return xerr.BadRequest(fmt.Sprintf("库存累计超出范围：当前 %s + 变动 %s", current, ch.Count))
		}
		if _, err := tx.IncreaseStock(ctx, tenantID, ch.ProductID, ch.WarehouseID, ch.Count); err != nil {
			return err
		}
	} else {
		rows, err := tx.DecreaseStock(ctx, tenantID, ch.ProductID, ch.WarehouseID, negQty(ch.Count))
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict(stockShortMessage(ch, current))
		}
		after, err = current.Sub(negQty(ch.Count))
		if err != nil {
			return xerr.BadRequest(fmt.Sprintf("库存累计超出范围：当前 %s − 变动 %s", current, ch.Count))
		}
	}
	// 4) 流水（append-only）：正入负出 + 结存快照 + 单据引用。
	return tx.InsertStockRecord(ctx, &model.StockRecord{
		TenantID:    tenantID,
		ProductID:   ch.ProductID,
		WarehouseID: ch.WarehouseID,
		Count:       ch.Count,
		TotalCount:  after,
		BizType:     ch.BizType,
		BizID:       ch.BizID,
		BizItemID:   ch.BizItemID,
		BizNo:       ch.BizNo,
	})
}

// stockShortMessage 生成余额不足的 409 文案（spec §4.2：提示产品/仓库/当前库存/变更量）。
func stockShortMessage(ch StockChange, current qty.Qty) string {
	product := ch.ProductName
	if product == "" {
		product = fmt.Sprintf("产品 %d", ch.ProductID)
	}
	warehouse := ch.WarehouseName
	if warehouse == "" {
		warehouse = fmt.Sprintf("仓库 %d", ch.WarehouseID)
	}
	return fmt.Sprintf("%s在%s库存不足：当前库存 %s，需要出库 %s",
		product, warehouse, current, negQty(ch.Count))
}

// ---------- 库存页（仓库×产品余额）与全仓合计 ----------

// ListStocks 库存余额分页（仓库×产品，spec §5）：库存行无 owner 语义
// （仓库不做独立权限，spec §4.10），租户内全量可见，产品/仓库名称服务端解析。
func (s *Service) ListStocks(ctx context.Context, req *dto.StockPageReq) ([]dto.StockRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.StockFilter{
		WarehouseId: req.WarehouseId,
		ProductId:   req.ProductId,
	}
	list, total, err := s.repo.ListStocks(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.assembleStockRows(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ExportStocks 导出库存余额（筛选与列表一致，spec §4.11）。
func (s *Service) ExportStocks(ctx context.Context, req *dto.StockPageReq) ([]byte, error) {
	filter := repository.StockFilter{
		WarehouseId: req.WarehouseId,
		ProductId:   req.ProductId,
	}
	list, err := s.repo.ListAllStocks(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.assembleStockRows(ctx, list)
	if err != nil {
		return nil, err
	}
	out := make([]dto.StockExportRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dto.StockExportRow{
			ProductName:   row.ProductName,
			WarehouseName: row.WarehouseName,
			Count:         row.Count,
			UpdateTime:    row.UpdateTime,
		})
	}
	return excel.Export(out, excel.ExportOptions{NativeDates: true})
}

// assembleStockRows 批量解析产品/仓库名称并组装展示行。
func (s *Service) assembleStockRows(ctx context.Context, list []model.Stock) ([]dto.StockRow, error) {
	productIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	for _, row := range list {
		productIDs = append(productIDs, row.ProductID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
	}
	productNames, err := s.repo.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.StockRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockRow{
			ID:            row.ID,
			ProductId:     row.ProductID,
			WarehouseId:   row.WarehouseID,
			ProductName:   productNames[row.ProductID],
			WarehouseName: warehouseNames[row.WarehouseID],
			Count:         row.Count,
			UpdateTime:    row.UpdatedAt,
		})
	}
	return rows, nil
}

// GetStockCount 产品库存合计：warehouseId > 0 时返回单仓余额，否则全仓合计
// （spec §5：get-count）。无余额行返回 0。
func (s *Service) GetStockCount(ctx context.Context, productID, warehouseID int64) (qty.Qty, error) {
	if productID <= 0 {
		return qty.Qty{}, xerr.BadRequest("缺少产品 ID")
	}
	return s.repo.SumStockCount(ctx, productID, warehouseID)
}

// ---------- 出入库流水页 ----------

// ListStockRecords 流水分页（按 biz_type/仓库/产品/时间筛选，spec §5）。
func (s *Service) ListStockRecords(ctx context.Context, req *dto.StockRecordPageReq) ([]dto.StockRecordRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.stockRecordFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListStockRecords(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.assembleStockRecordRows(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ExportStockRecords 导出流水（筛选与列表一致，spec §4.11）。
func (s *Service) ExportStockRecords(ctx context.Context, req *dto.StockRecordPageReq) ([]byte, error) {
	filter, err := s.stockRecordFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllStockRecords(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.assembleStockRecordRows(ctx, list)
	if err != nil {
		return nil, err
	}
	out := make([]dto.StockRecordExportRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dto.StockRecordExportRow{
			BizType:       stockRecordBizNames[row.BizType],
			BizNo:         row.BizNo,
			ProductName:   row.ProductName,
			WarehouseName: row.WarehouseName,
			Count:         row.Count,
			TotalCount:    row.TotalCount,
			CreateTime:    row.CreateTime,
		})
	}
	return excel.Export(out, excel.ExportOptions{NativeDates: true})
}

// stockRecordFilter 构造流水筛选（时间按业务时区解析，非法 400）。
func (s *Service) stockRecordFilter(ctx context.Context, req *dto.StockRecordPageReq) (repository.StockRecordFilter, error) {
	filter := repository.StockRecordFilter{
		BizType:     req.BizType,
		WarehouseId: req.WarehouseId,
		ProductId:   req.ProductId,
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.StockRecordFilter{}, err
	}
	filter.StartTime = start
	filter.EndTime = end
	return filter, nil
}

// assembleStockRecordRows 批量解析名称并组装流水展示行。
func (s *Service) assembleStockRecordRows(ctx context.Context, list []model.StockRecord) ([]dto.StockRecordRow, error) {
	productIDs := make([]int64, 0, len(list))
	warehouseIDs := make([]int64, 0, len(list))
	for _, row := range list {
		productIDs = append(productIDs, row.ProductID)
		warehouseIDs = append(warehouseIDs, row.WarehouseID)
	}
	productNames, err := s.repo.FindProductNamesIn(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	warehouseNames, err := s.repo.FindWarehouseNamesIn(ctx, warehouseIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.StockRecordRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.StockRecordRow{
			ID:            row.ID,
			BizType:       row.BizType,
			BizNo:         row.BizNo,
			ProductId:     row.ProductID,
			WarehouseId:   row.WarehouseID,
			ProductName:   productNames[row.ProductID],
			WarehouseName: warehouseNames[row.WarehouseID],
			Count:         row.Count,
			TotalCount:    row.TotalCount,
			CreateTime:    row.CreatedAt,
		})
	}
	return rows, nil
}

// stockRecordBizNames 是流水 biz_type 的中文展示（导出/前端标签用）。
// #91+ 扩采购/销售/调拨/盘点对时在此追加。
var stockRecordBizNames = map[string]string{
	model.StockRecordBizStockIn:        "其它入库",
	model.StockRecordBizStockInCancel:  "其它入库（反审核）",
	model.StockRecordBizStockOut:       "其它出库",
	model.StockRecordBizStockOutCancel: "其它出库（反审核）",
	// 采购三单（issue #91，spec §4.3：biz_type 与表名一致 + _CANCEL 对）。
	model.StockRecordBizPurchaseIn:           "采购入库",
	model.StockRecordBizPurchaseInCancel:     "采购入库（反审核）",
	model.StockRecordBizPurchaseReturn:       "采购退货",
	model.StockRecordBizPurchaseReturnCancel: "采购退货（反审核）",
	// 库存调拨/盘点（issue #93，spec §4.3/§4.6）：调拨出入成对、盘点差异调整，
	// 各配 _CANCEL 对。
	model.StockRecordBizStockMove:        "库存调拨",
	model.StockRecordBizStockMoveCancel:  "库存调拨（反审核）",
	model.StockRecordBizStockCheck:       "盘点调整",
	model.StockRecordBizStockCheckCancel: "盘点调整（反审核）",
}
