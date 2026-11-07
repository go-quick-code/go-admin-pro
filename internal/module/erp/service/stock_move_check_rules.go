// stock_move_check_rules.go 是库存调拨/盘点的共享规则（issue #93，
// docs/erp-mvp-spec.md §4.2/§4.6/§4.8）：单号前缀与序列 biz_type、明细校验
// （调拨数量 > 0；盘点实际清点数 ≥ 0）、盘点差异计算、多仓库加锁键排序。
// 库存在入账入口 ApplyStockChange（spec §4.2 唯一入口），本文件不触碰余额。
package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 单号前缀（spec §4.8：调拨 DB / 盘点 PD，2 字母拼音风格对齐 CRM）。
const (
	StockMoveNoPrefix  = "DB"
	StockCheckNoPrefix = "PD"
)

// erp_sequence 的 biz_type（spec §4.8：与单据表名一致）。
const (
	SeqBizTypeStockMove  = "stock_move"
	SeqBizTypeStockCheck = "stock_check"
)

// moveItemDraft 是调拨明细草稿（审核入账的权威明细）。
type moveItemDraft struct {
	ProductID int64
	Count     qty.Qty
}

// checkItemDraft 是盘点明细草稿（ActualCount 为客户端实际清点数，BaseCount 为
// 创建时账面快照——仅记录，审核不使用，spec §4.6）。
type checkItemDraft struct {
	ProductID   int64
	ActualCount qty.Qty
	BaseCount   qty.Qty
}

// buildMoveItemDrafts 校验并换算调拨明细（创建/编辑共用）：
//   - 至少一条明细；
//   - 产品 ID 必填；
//   - 数量 > 0（spec §4.2 明细不变量；出仓不足由审核入账 409）。
func buildMoveItemDrafts(items []dto.StockMoveItemPayload) ([]moveItemDraft, error) {
	if len(items) == 0 {
		return nil, xerr.BadRequest("调拨明细至少一条")
	}
	drafts := make([]moveItemDraft, 0, len(items))
	for i, item := range items {
		if item.ProductId <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("调拨明细第 %d 行缺少产品", i+1))
		}
		if item.Count.Milli() <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("调拨明细第 %d 行数量必须大于 0", i+1))
		}
		drafts = append(drafts, moveItemDraft{ProductID: item.ProductId, Count: item.Count})
	}
	return drafts, nil
}

// buildCheckItemDrafts 校验并换算盘点明细（创建/编辑共用）：
//   - 至少一条明细；
//   - 产品 ID 必填；
//   - 实际清点数 ≥ 0（spec §3.5/§4.6）；
//   - base_count 取当前账面快照（baseCounts 缺省 0，仅记录）。
func buildCheckItemDrafts(items []dto.StockCheckItemPayload, baseCounts map[int64]qty.Qty) ([]checkItemDraft, error) {
	if len(items) == 0 {
		return nil, xerr.BadRequest("盘点明细至少一条")
	}
	drafts := make([]checkItemDraft, 0, len(items))
	for i, item := range items {
		if item.ProductId <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("盘点明细第 %d 行缺少产品", i+1))
		}
		if item.ActualCount.Milli() < 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("盘点明细第 %d 行实际清点数不能为负数", i+1))
		}
		drafts = append(drafts, checkItemDraft{
			ProductID:   item.ProductId,
			ActualCount: item.ActualCount,
			BaseCount:   baseCounts[item.ProductId],
		})
	}
	return drafts, nil
}

// checkDiff 计算盘点差异：actual − book（spec §4.6：差正盘盈入、差负盘亏出；
// 差零账实一致、不产生流水）。越界返回 400。
func checkDiff(actual, book qty.Qty) (qty.Qty, error) {
	diff, err := actual.Sub(book)
	if err != nil {
		return qty.Qty{}, xerr.BadRequest(fmt.Sprintf("盘点差异超出范围：实际 %s − 账面 %s", actual, book))
	}
	return diff, nil
}

// stockLockKey 是库存行锁键（仓库×产品）。多仓库调拨按仓库 ID 升序加锁
// （spec §4.2），同仓库内按产品 ID 升序，保证任意两笔交叉调拨的取锁次序一致。
type stockLockKey struct {
	WarehouseID int64
	ProductID   int64
}

// sortStockLockKeys 对加锁键去重并按（仓库 ID, 产品 ID）升序排序（纯函数）。
func sortStockLockKeys(keys []stockLockKey) []stockLockKey {
	seen := make(map[stockLockKey]struct{}, len(keys))
	unique := make([]stockLockKey, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].WarehouseID != unique[j].WarehouseID {
			return unique[i].WarehouseID < unique[j].WarehouseID
		}
		return unique[i].ProductID < unique[j].ProductID
	})
	return unique
}

// lockStockRows 按升序对库存行先物化再取行锁（spec §4.2：事务内先取仓库×
// 产品行锁，多仓库调拨按仓库 ID 升序防死锁）。锁被本事务持有后再经唯一入账
// 入口 ApplyStockChange 更新余额——入口内部重复取同一行锁是安全的。
func (s *Service) lockStockRows(ctx context.Context, tx *repository.Repository, keys []stockLockKey) error {
	tenantID := erpTenantID(ctx)
	for _, key := range sortStockLockKeys(keys) {
		if err := tx.EnsureStockRow(ctx, tenantID, key.ProductID, key.WarehouseID); err != nil {
			return err
		}
		if _, err := tx.LockStock(ctx, tenantID, key.ProductID, key.WarehouseID); err != nil {
			return err
		}
	}
	return nil
}

// ensureDistinctWarehouses 校验调出/调入仓库必须不同（spec §4.6：同仓 409）。
func ensureDistinctWarehouses(fromWarehouseID, toWarehouseID int64) error {
	if fromWarehouseID == toWarehouseID {
		return xerr.Conflict("调拨的调出仓库与调入仓库不能相同")
	}
	return nil
}

// checkProductsExist 校验一批产品在租户内存在（跨租户/不存在一律 400，
// 不泄露存在性）。
func (s *Service) checkProductsExist(ctx context.Context, ids []int64) error {
	names, err := s.repo.FindProductNamesIn(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := names[id]; !ok {
			return xerr.BadRequest("产品不存在")
		}
	}
	return nil
}
