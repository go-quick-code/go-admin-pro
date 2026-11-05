// receivable_guard.go 是 VoidContractGuard 的真实实现（issue #18 接入 #16 预留的
// 作废/删除合同守卫，docs/crm-mvp-spec.md §4.5：合同存在回款记录时禁止作废/删除）。
// 只统计未删除的回款记录：计划本身不拦截（计划可以调整），有实际到账才锁合同。
package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ReceivableVoidGuard 以回款记录拦截合同作废/删除。
type ReceivableVoidGuard struct {
	repo *repository.Repository
}

// NewReceivableVoidGuard 创建作废/删除合同守卫（app 装配处
// crmSvc.SetVoidContractGuard 注册，替换 v1 空实现）。
func NewReceivableVoidGuard(repo *repository.Repository) VoidContractGuard {
	return ReceivableVoidGuard{repo: repo}
}

// CanVoidContract 实现 VoidContractGuard：存在未删除回款记录时返回用户可读 400。
func (g ReceivableVoidGuard) CanVoidContract(ctx context.Context, contractID int64) error {
	count, err := g.repo.CountReceivableRecordsByContract(ctx, contractID)
	if err != nil {
		return err
	}
	return rejectVoidWithRecords(count)
}

// CanVoidContractTx 实现 TxVoidContractGuard：回款记录计数与合同作废/删除共用
// 同一事务（调用方已锁定合同行），PR #39 审查 P1 的锁内复核口径。
func (g ReceivableVoidGuard) CanVoidContractTx(ctx context.Context, tx *repository.Repository, contractID int64) error {
	count, err := tx.CountReceivableRecordsByContract(ctx, contractID)
	if err != nil {
		return err
	}
	return rejectVoidWithRecords(count)
}

// rejectVoidWithRecords 把「存在回款记录」翻译成用户可读 400（作废/删除共用）。
func rejectVoidWithRecords(count int64) error {
	if count > 0 {
		return xerr.BadRequest("合同存在回款记录，禁止作废或删除：请先删除相关回款记录")
	}
	return nil
}
