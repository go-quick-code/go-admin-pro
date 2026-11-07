// stock_refs.go 是库存/单据域的删除引用校验实现（issue #90，docs/erp-mvp-spec.md
// §4.9）：#89 预留的 WarehouseRefChecker 与 #88 预留的 ProductRefChecker 在本票
// 落地实现，app 装配处注册后主数据删除约束自动生效（spec §2：模块内自洽）。
//
//   - 仓库：被 erp_stock 非零记录或任何流水引用禁删（append-only 流水即零余额
//     行也构成历史引用）；
//   - 产品：被任何单据明细引用禁删（本票为其它入库/出库明细；#91+ 的引用类型
//     以 RefCheckerChain 追加组合）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// CheckWarehouseRefs 实现 WarehouseRefChecker（party_refs.go 扩展点）。
func (s *Service) CheckWarehouseRefs(ctx context.Context, warehouseID int64) error {
	zeroRows, err := s.repo.CheckWarehouseZeroStock(ctx, warehouseID)
	if err != nil {
		return err
	}
	if zeroRows > 0 {
		return xerr.Conflict("仓库存在非零库存记录，禁止删除")
	}
	recordRows, err := s.repo.CheckWarehouseStockRecordRef(ctx, warehouseID)
	if err != nil {
		return err
	}
	if recordRows > 0 {
		return xerr.Conflict("仓库存在出入库流水，禁止删除")
	}
	return nil
}

// CheckProductRefs 实现 ProductRefChecker（product_refs.go 扩展点）。
// #91+ 的单据明细引用类型经 RefCheckerChain 组合追加。
func (s *Service) CheckProductRefs(ctx context.Context, productID int64) error {
	inRows, err := s.repo.CountStockInItemsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if inRows > 0 {
		return xerr.Conflict("产品被其它入库单明细引用，禁止删除")
	}
	outRows, err := s.repo.CountStockOutItemsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if outRows > 0 {
		return xerr.Conflict("产品被其它出库单明细引用，禁止删除")
	}
	return nil
}
