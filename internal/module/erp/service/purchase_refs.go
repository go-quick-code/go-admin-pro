// purchase_refs.go 是采购域的删除引用校验实现（issue #91，docs/erp-mvp-spec.md
// §4.9）：#89 预留的 SupplierRefChecker 与 #88 预留的 ProductRefChecker 在本票
// 落地采购侧实现。app 装配处把本实现与 #90 的库存实现经 RefCheckerChain 组合
// 注册（SetProductRefChecker 后注册覆盖先注册），主数据删除约束自动生效
// （spec §2：模块内自洽，无跨模块依赖）。
//
//   - 产品：被采购订单/入库/退货任一单据明细引用禁删（软删行不算引用）；
//   - 供应商：被采购订单/入库/退货任一单据主表引用禁删（spec §4.9：
//     客户/供应商被任何单据引用，对芋道的收紧，差异记清单）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// PurchaseDocRefChecker 是采购域引用检查（实现 ProductRefChecker 与
// SupplierRefChecker 两个扩展点；与 #90 的 Service 自实现并列，经
// RefCheckerChain 组合注册）。
type PurchaseDocRefChecker struct {
	svc *Service
}

// NewPurchaseDocRefChecker 创建采购域引用检查器（装配点：internal/app）。
func NewPurchaseDocRefChecker(svc *Service) PurchaseDocRefChecker {
	return PurchaseDocRefChecker{svc: svc}
}

// CheckProductRefs 实现 ProductRefChecker（产品被采购单据明细引用禁删）。
func (c PurchaseDocRefChecker) CheckProductRefs(ctx context.Context, productID int64) error {
	orderRows, err := c.svc.repo.CountPurchaseOrderItemsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if orderRows > 0 {
		return xerr.Conflict("产品被采购订单明细引用，禁止删除")
	}
	inRows, err := c.svc.repo.CountPurchaseInItemsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if inRows > 0 {
		return xerr.Conflict("产品被采购入库单明细引用，禁止删除")
	}
	retRows, err := c.svc.repo.CountPurchaseReturnItemsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if retRows > 0 {
		return xerr.Conflict("产品被采购退货单明细引用，禁止删除")
	}
	return nil
}

// CheckSupplierRefs 实现 SupplierRefChecker（供应商被采购单据引用禁删）。
func (c PurchaseDocRefChecker) CheckSupplierRefs(ctx context.Context, supplierID int64) error {
	rows, err := c.svc.repo.CountPurchaseDocsBySupplier(ctx, supplierID)
	if err != nil {
		return err
	}
	if rows > 0 {
		return xerr.Conflict("供应商被采购单据引用，禁止删除")
	}
	return nil
}
