// opportunity_refs.go 是 ProductRefCountChecker 的商机侧实现（issue #15 接 #11 扩展点）：
// 商机行项目引用的产品不可删除（docs/crm-mvp-spec.md §4.5）。
// #16 合同行项目落地后在 app 装配处组合两份实现（见 app.go 装配注释与提交说明）。
package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// OpportunityProductRefChecker 持有 crm 仓储，校验产品是否被商机行项目引用。
type OpportunityProductRefChecker struct {
	repo *repository.Repository
}

// NewOpportunityProductRefChecker 创建商机侧引用校验器（app 装配时注入 crm 仓储）。
func NewOpportunityProductRefChecker(repo *repository.Repository) OpportunityProductRefChecker {
	return OpportunityProductRefChecker{repo: repo}
}

// CheckProductRefs 实现接口：被未删除商机的行项目引用时返回业务错误，nil 表示可删。
// 错误原文即为用户可见文案，由产品删除路径透传（xerr 语义保持 400）。
func (c OpportunityProductRefChecker) CheckProductRefs(ctx context.Context, productID int64) error {
	count, err := c.repo.CountOpportunityProductsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.BadRequest("产品被商机行项目引用，禁止删除")
	}
	return nil
}
