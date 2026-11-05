// contract_guard.go 是作废/删除合同的守卫扩展点（issue #16 接缝，spec §4.5：
// 合同存在回款记录时禁止作废/删除）。回款实体由 #18 落地后，internal/app
// 装配处已注册真实实现（NewReceivableVoidGuard），作废与删除两条路径都经过本守卫；
// 未注册时空实现放行，仅用于轻量测试/工具场景。
package service

import (
	"context"
)

// VoidContractGuard 是作废/删除合同的守卫扩展点（依赖接口而非实现）。
type VoidContractGuard interface {
	// CanVoidContract 报告合同能否作废或删除（作废与删除路径共用本守卫）：
	// 存在未删除回款记录的合同返回业务错误（用户可见 400）。
	CanVoidContract(ctx context.Context, contractID int64) error
}

// VoidContractGuardFunc 是 VoidContractGuard 的函数适配器（单测与轻量装配用）。
type VoidContractGuardFunc func(ctx context.Context, contractID int64) error

// CanVoidContract 实现接口。
func (f VoidContractGuardFunc) CanVoidContract(ctx context.Context, contractID int64) error {
	return f(ctx, contractID)
}

// noopVoidContractGuard 是空实现：未注册守卫时无约束可查；
// 正常装配会注册回款侧真实实现（internal/app），生产路径不会静默放行。
type noopVoidContractGuard struct{}

func (noopVoidContractGuard) CanVoidContract(context.Context, int64) error { return nil }

// SetVoidContractGuard 注入守卫实现（app 装配回款侧实现，见文件头注释）。
func (s *Service) SetVoidContractGuard(g VoidContractGuard) {
	s.voidGuard = g
}

// canVoidContract 在作废/删除合同前运行守卫；未注册实现时空实现放行。
func (s *Service) canVoidContract(ctx context.Context, contractID int64) error {
	if s.voidGuard == nil {
		return noopVoidContractGuard{}.CanVoidContract(ctx, contractID)
	}
	return s.voidGuard.CanVoidContract(ctx, contractID)
}
