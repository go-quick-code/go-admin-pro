// contract_refs.go 承载合同侧的引用校验与依赖面实现（issue #16）：
//   - ContractProductRefChecker：合同行项目引用的产品禁删（ProductRefCountChecker
//     的合同侧实现，#11 扩展点，与 #15 商机侧实现经 RefCheckerChain 组合）；
//   - RefCheckerChain：组合多个 ProductRefCountChecker（依次调用、首个非 nil 错误返回）；
//   - ContractChecker：公海自动回收的合同排除（#14 接点，spec §4.2
//     「无未作废合同」条件）——未作废合同的客户不被回收，作废合同不拦截。
package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ContractProductRefChecker 持有 crm 仓储，校验产品是否被合同行项目引用。
type ContractProductRefChecker struct {
	repo *repository.Repository
}

// NewContractProductRefChecker 创建合同侧引用校验器（app 装配时注入 crm 仓储）。
func NewContractProductRefChecker(repo *repository.Repository) ContractProductRefChecker {
	return ContractProductRefChecker{repo: repo}
}

// CheckProductRefs 实现接口：被未删除合同的行项目引用时返回业务错误，nil 表示可删。
// 错误原文即为用户可见文案，由产品删除路径透传（xerr 语义保持 400）。
func (c ContractProductRefChecker) CheckProductRefs(ctx context.Context, productID int64) error {
	count, err := c.repo.CountContractProductsByProduct(ctx, productID)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.BadRequest("产品被合同行项目引用，禁止删除")
	}
	return nil
}

// RefCheckerChain 组合多个产品引用校验器：依次调用，首个非 nil 错误即返回
// （全部通过才放行删除）。#16 起注册商机 + 合同两侧实现（app 装配处）。
type RefCheckerChain []ProductRefCountChecker

// CheckProductRefs 实现接口：链式调用各校验器。
func (chain RefCheckerChain) CheckProductRefs(ctx context.Context, productID int64) error {
	for _, checker := range chain {
		if err := checker.CheckProductRefs(ctx, productID); err != nil {
			return err
		}
	}
	return nil
}

// liveContractChecker 是公海自动回收合同排除的真实实现（#14 接点，
// 替换 pool_recycle.go 的 NoopContractChecker）：有未作废合同的客户不被回收。
type liveContractChecker struct {
	repo *repository.Repository
}

// NewContractChecker 创建合同排除检查器，返回 ContractChecker 接口
// （app 装配处 SetContractChecker 用；实现保持非导出，依赖面只有接口）。
func NewContractChecker(repo *repository.Repository) ContractChecker {
	return liveContractChecker{repo: repo}
}

// ActiveContractCustomerIDs 实现接口：作废合同与无合同都不进集合（批量查询，
// 避免逐客户 N+1，见 ContractChecker 接口注释）。
func (c liveContractChecker) ActiveContractCustomerIDs(ctx context.Context, customerIDs []int64) (map[int64]bool, error) {
	return c.repo.ActiveContractCustomerIDs(ctx, customerIDs)
}
