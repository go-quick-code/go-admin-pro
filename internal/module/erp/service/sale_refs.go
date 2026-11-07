// sale_refs.go 是销售链路的删除引用校验实现（issue #92，docs/erp-mvp-spec.md
// §4.9）：#89 预留的 CustomerRefChecker 在本票落地，app 装配处注册后 ERP 客户
// 删除约束自动生效（spec §2：模块内自洽）。产品引用的三类销售单据明细计数
// 经 stock_refs.go 的 CheckProductRefs 追加（RefChecker 语义：软删行不算引用）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// CheckCustomerRefs 实现 CustomerRefChecker（party_refs.go 扩展点）：
// ERP 客户被销售订单/出库/退货任何一单引用即禁删（对芋道的收紧，差异记清单）。
func (s *Service) CheckCustomerRefs(ctx context.Context, customerID int64) error {
	orderRows, err := s.repo.CountSaleOrdersByCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if orderRows > 0 {
		return xerr.Conflict("客户被销售订单引用，禁止删除")
	}
	outRows, err := s.repo.CountSaleOutsByCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if outRows > 0 {
		return xerr.Conflict("客户被销售出库单引用，禁止删除")
	}
	retRows, err := s.repo.CountSaleReturnsByCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if retRows > 0 {
		return xerr.Conflict("客户被销售退货单引用，禁止删除")
	}
	return nil
}

// countSaleProductRefs 销售三单明细的产品引用行数合计（0 表示无引用）。
// #93+ 的调拨/盘点明细引用类型继续以同样方式追加。
func (s *Service) countSaleProductRefs(ctx context.Context, productID int64) (int64, error) {
	orderRows, err := s.repo.CountSaleOrderItemsByProduct(ctx, productID)
	if err != nil {
		return 0, err
	}
	outRows, err := s.repo.CountSaleOutItemsByProduct(ctx, productID)
	if err != nil {
		return 0, err
	}
	retRows, err := s.repo.CountSaleReturnItemsByProduct(ctx, productID)
	if err != nil {
		return 0, err
	}
	return orderRows + outRows + retRows, nil
}
