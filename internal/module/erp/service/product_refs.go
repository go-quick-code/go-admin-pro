// product_refs.go 是 ERP 引用检查骨架（issue #88，docs/erp-mvp-spec.md §4.9
// 删除与引用约束）：主数据被引用禁删（409）。
//
// 本票（#88）落地「分类/单位被产品引用」的模块内直查实现（见
// product_service 的 checkProductCategoryRefs/checkProductUnitRefs，随产品域
// 切片接线）；「产品被单据引用」（库存/采购/销售/财务，#90–#94）经本文件
// 的扩展点注册——后续票各自实现 ProductRefChecker，app 装配处以 RefCheckerChain
// 组合注册，与本票不产生代码耦合（spec §2：模块间零依赖，唯一跨域共享是
// 平台底座）。
//
// 未注册实现时退回空实现放行（产品删除当前无引用可查），仅用于轻量测试/工具
// 场景；正常装配路径随单据域落地显式注册，不会静默放行。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ProductRefChecker 校验产品是否被业务引用（引用时返回业务错误，nil 表示可删）。
// 实现方注意：引用计数只统计未删除的行（软删行不算引用）。
type ProductRefChecker interface {
	// CheckProductRefs 返回产品被引用时的业务错误（如 xerr.Conflict("产品被单据明细引用，禁止删除")）。
	CheckProductRefs(ctx context.Context, productID int64) error
}

// noopProductRefChecker 是空实现：未注册校验器时无引用可查。
type noopProductRefChecker struct{}

func (noopProductRefChecker) CheckProductRefs(context.Context, int64) error { return nil }

// RefCheckerChain 依次调用多个实现，首个非 nil 错误即返回
// （与 crm service.RefCheckerChain 同构；组合说明见各票装配注释）。
type RefCheckerChain []ProductRefChecker

// CheckProductRefs 实现 ProductRefChecker。
func (chain RefCheckerChain) CheckProductRefs(ctx context.Context, productID int64) error {
	for _, checker := range chain {
		if err := checker.CheckProductRefs(ctx, productID); err != nil {
			return err
		}
	}
	return nil
}

// SetProductRefChecker 注册产品删除引用校验实现（幂等，后注册覆盖先注册）。
// 装配点：internal/app；后续票（#90 库存、#91 采购、#92 销售…）的引用类型
// 实现 ProductRefChecker 后在此组合注册。
func (s *Service) SetProductRefChecker(checker ProductRefChecker) {
	s.productRefs = checker
}

// checkProductRefs 在删除产品前运行引用校验；未注册实现时空实现放行。
func (s *Service) checkProductRefs(ctx context.Context, productID int64) error {
	if s.productRefs == nil {
		return noopProductRefChecker{}.CheckProductRefs(ctx, productID)
	}
	return s.productRefs.CheckProductRefs(ctx, productID)
}

// checkProductCategoryRefs 分类删除引用校验（spec §4.9：分类被产品引用禁删 409；
// 与 crm 的「分类下存在产品」同语义，但按 ERP 差异收紧为 HTTP 409）。
func (s *Service) checkProductCategoryRefs(ctx context.Context, categoryID int64) error {
	count, err := s.repo.CountProductsByCategory(ctx, categoryID)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.Conflict("分类下存在产品，无法删除")
	}
	return nil
}

// checkProductUnitRefs 单位删除引用校验（spec §4.9：单位被产品引用禁删 409）。
func (s *Service) checkProductUnitRefs(ctx context.Context, unitID int64) error {
	count, err := s.repo.CountProductsByUnit(ctx, unitID)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.Conflict("单位下存在产品，无法删除")
	}
	return nil
}
