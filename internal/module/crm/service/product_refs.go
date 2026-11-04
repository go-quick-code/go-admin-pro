// product_refs.go 是产品删除约束的扩展点（issue #11，docs/crm-mvp-spec.md §4.1：
// 产品被商机/合同行项目引用时禁止删除）。本工单只落产品侧接口与 v1 空实现；
// 商机（#15）/合同（#16）落地行项目后，各自的行项目仓储提供实现并在 app 装配处注册。
package service

import (
	"context"
)

// ProductRefCountChecker 校验产品是否被业务引用（引用时返回业务错误，nil 表示可删）。
// 实现方注意：引用计数只统计未删除的行项目（软删行不算引用）。
type ProductRefCountChecker interface {
	// CheckProductRefs 返回产品被引用时的业务错误（如 xerr.BadRequest("产品被商机/合同行项目引用，禁止删除")）。
	CheckProductRefs(ctx context.Context, productID int64) error
}

// noopProductRefCountChecker 是 v1 空实现：商机/合同行项目（#15/#16）尚未落地，无引用可查。
type noopProductRefCountChecker struct{}

func (noopProductRefCountChecker) CheckProductRefs(context.Context, int64) error { return nil }

// SetProductRefCountChecker 注册删除引用校验实现（幂等，后注册覆盖先注册）。
// 装配点：internal/app 在 #15/#16 落地后传入商机/合同侧实现；v1 不调用即用空实现。
func (s *Service) SetProductRefCountChecker(checker ProductRefCountChecker) {
	s.productRefs = checker
}

// checkProductRefs 在删除产品前运行引用校验；未注册实现时空实现放行。
func (s *Service) checkProductRefs(ctx context.Context, productID int64) error {
	if s.productRefs == nil {
		return noopProductRefCountChecker{}.CheckProductRefs(ctx, productID)
	}
	return s.productRefs.CheckProductRefs(ctx, productID)
}
