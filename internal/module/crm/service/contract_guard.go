// contract_guard.go 是作废/删除合同的守卫扩展点（issue #16 接缝，spec §4.5：
// 合同存在回款记录时禁止作废/删除）。回款实体由 #18 落地后，internal/app
// 装配处已注册真实实现（NewReceivableVoidGuard），作废与删除两条路径都经过本守卫；
// 未注册时放行，仅用于轻量测试/工具场景。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
)

// VoidContractGuard 是作废/删除合同的守卫扩展点（依赖接口而非实现）。
type VoidContractGuard interface {
	// CanVoidContract 报告合同能否作废或删除（作废与删除路径共用本守卫）：
	// 存在未删除回款记录的合同返回业务错误（用户可见 400）。
	CanVoidContract(ctx context.Context, contractID int64) error
}

// TxVoidContractGuard 是守卫的可选事务扩展：实现方在调用方已锁定合同行的
// 事务仓储上复核（PR #39 审查 P1：作废与登记回款统一「先合同、后计划」锁序，
// 守卫必须在锁内计数，否则计数与状态更新之间可插入新的回款记录）。
// 未实现本接口的守卫退化为无事务调用（仍有合同行锁保护，但不与守卫查询同事务）。
type TxVoidContractGuard interface {
	CanVoidContractTx(ctx context.Context, tx *repository.Repository, contractID int64) error
}

// VoidContractGuardFunc 是 VoidContractGuard 的函数适配器（单测与轻量装配用）。
type VoidContractGuardFunc func(ctx context.Context, contractID int64) error

// CanVoidContract 实现接口。
func (f VoidContractGuardFunc) CanVoidContract(ctx context.Context, contractID int64) error {
	return f(ctx, contractID)
}

// SetVoidContractGuard 注入守卫实现（app 装配回款侧实现，见文件头注释）。
func (s *Service) SetVoidContractGuard(g VoidContractGuard) {
	s.voidGuard = g
}

// canVoidContractTx 在合同行锁内运行守卫：实现 TxVoidContractGuard 的守卫与
// 作废/删除共享同一事务（回款记录计数不会被并发登记绕过）；其余实现退化为
// 普通调用（仍有合同行锁串行化并发登记）。
func (s *Service) canVoidContractTx(ctx context.Context, tx *repository.Repository, contractID int64) error {
	if s.voidGuard == nil {
		return nil
	}
	if guard, ok := s.voidGuard.(TxVoidContractGuard); ok {
		return guard.CanVoidContractTx(ctx, tx, contractID)
	}
	return s.voidGuard.CanVoidContract(ctx, contractID)
}
