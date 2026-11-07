// party_refs.go 是 ERP 仓库/客户/供应商的删除引用检查骨架（issue #89，
// docs/erp-mvp-spec.md §4.9 删除与引用约束）：主数据被引用禁删（409）。
//
// 与 #88 的 ProductRefChecker（product_refs.go）同构：本票三实体当前无引用源——
// 客户/供应商被「任何单据」引用（#91 采购 / #92 销售）、仓库被 erp_stock 非零
// 记录或任何流水引用（#90 库存）——因此这里只落可注册的扩展点，单据域实现
// Checker 后在 app 装配处注册即自动生效（spec §2：模块间零依赖）。
//
// 未注册实现时退回空实现放行（当前无引用可查）；正常装配路径随 #90–#92 落地
// 显式注册，不会静默放行。
package service

import (
	"context"
)

// WarehouseRefChecker 校验仓库是否被业务引用（引用时返回业务错误，nil 表示可删）。
// 实现方注意：引用计数只统计未删除的行（软删行不算引用）。
type WarehouseRefChecker interface {
	// CheckWarehouseRefs 返回仓库被引用时的业务错误（如 xerr.Conflict("仓库被库存记录引用，禁止删除")）。
	CheckWarehouseRefs(ctx context.Context, warehouseID int64) error
}

// CustomerRefChecker 校验 ERP 客户是否被业务引用（引用时返回业务错误，nil 表示可删）。
type CustomerRefChecker interface {
	// CheckCustomerRefs 返回客户被引用时的业务错误（如 xerr.Conflict("客户被单据引用，禁止删除")）。
	CheckCustomerRefs(ctx context.Context, customerID int64) error
}

// SupplierRefChecker 校验 ERP 供应商是否被业务引用（引用时返回业务错误，nil 表示可删）。
type SupplierRefChecker interface {
	// CheckSupplierRefs 返回供应商被引用时的业务错误（如 xerr.Conflict("供应商被单据引用，禁止删除")）。
	CheckSupplierRefs(ctx context.Context, supplierID int64) error
}

// noopRefsChecker 是三类引用校验的空实现：未注册校验器时无引用可查。
type noopRefsChecker struct{}

func (noopRefsChecker) CheckWarehouseRefs(context.Context, int64) error { return nil }
func (noopRefsChecker) CheckCustomerRefs(context.Context, int64) error  { return nil }
func (noopRefsChecker) CheckSupplierRefs(context.Context, int64) error  { return nil }

// SetWarehouseRefChecker 注册仓库删除引用校验实现（幂等，后注册覆盖先注册）。
// 装配点：internal/app；#90 库存（erp_stock/流水）实现 WarehouseRefChecker 后注册。
func (s *Service) SetWarehouseRefChecker(checker WarehouseRefChecker) {
	s.warehouseRefs = checker
}

// SetCustomerRefChecker 注册 ERP 客户删除引用校验实现。装配点：internal/app；
// #92 销售（销售订单/出库/退货）实现 CustomerRefChecker 后注册。
func (s *Service) SetCustomerRefChecker(checker CustomerRefChecker) {
	s.customerRefs = checker
}

// SetSupplierRefChecker 注册 ERP 供应商删除引用校验实现。装配点：internal/app；
// #91 采购（采购订单/入库/退货）实现 SupplierRefChecker 后注册。
func (s *Service) SetSupplierRefChecker(checker SupplierRefChecker) {
	s.supplierRefs = checker
}

// checkWarehouseRefs 在删除仓库前运行引用校验；未注册实现时空实现放行。
func (s *Service) checkWarehouseRefs(ctx context.Context, warehouseID int64) error {
	if s.warehouseRefs == nil {
		return noopRefsChecker{}.CheckWarehouseRefs(ctx, warehouseID)
	}
	return s.warehouseRefs.CheckWarehouseRefs(ctx, warehouseID)
}

// checkCustomerRefs 在删除 ERP 客户前运行引用校验；未注册实现时空实现放行。
func (s *Service) checkCustomerRefs(ctx context.Context, customerID int64) error {
	if s.customerRefs == nil {
		return noopRefsChecker{}.CheckCustomerRefs(ctx, customerID)
	}
	return s.customerRefs.CheckCustomerRefs(ctx, customerID)
}

// checkSupplierRefs 在删除供应商前运行引用校验；未注册实现时空实现放行。
func (s *Service) checkSupplierRefs(ctx context.Context, supplierID int64) error {
	if s.supplierRefs == nil {
		return noopRefsChecker{}.CheckSupplierRefs(ctx, supplierID)
	}
	return s.supplierRefs.CheckSupplierRefs(ctx, supplierID)
}
