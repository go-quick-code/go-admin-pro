package doc

import (
	"context"
	"errors"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 标准文案（与既有 12 类逐字一致；单据名由 Spec.Labels 提供）。
func errApprovedUpdate() error { return xerr.Conflict("已审核单据不可修改") }
func errApprovedDelete() error { return xerr.Conflict("已审核单据不可删除") }
func errStatusChanged() error  { return xerr.Conflict("单据状态已变化，请刷新后重试") }

func (k *Kernel[H, I]) notFound() error {
	return xerr.NotFound(k.spec.Labels.Doc + "不存在")
}

// mapNotFound 把 store 的 ErrNotFound 翻译成带单据名的 404。
func (k *Kernel[H, I]) mapNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return k.notFound()
	}
	return err
}

// Find 取单据并做可见性校验（数据范围外一律 404）。
func (k *Kernel[H, I]) Find(ctx context.Context, id int64) (H, error) {
	return k.findOne(ctx, id, false)
}

// FindWithItems 取单据（含明细）并做可见性校验（详情页用）。
func (k *Kernel[H, I]) FindWithItems(ctx context.Context, id int64) (H, error) {
	return k.findOne(ctx, id, true)
}

func (k *Kernel[H, I]) findOne(ctx context.Context, id int64, withItems bool) (H, error) {
	var zero H
	var (
		row H
		err error
	)
	if withItems {
		row, err = k.store.FindWithItems(ctx, id)
	} else {
		row, err = k.store.Find(ctx, id)
	}
	if err != nil {
		return zero, k.mapNotFound(err)
	}
	ownerUserID, ownerDeptID := row.Base().GetOwner()
	if err := k.env.CheckRowVisible(ctx, k.spec.Labels.Doc, ownerUserID, ownerDeptID); err != nil {
		return zero, err
	}
	return row, nil
}

// Create 创建单据：owner 快照（事务外）→ 事务内 prepare（挂单锁/配额等）
// → 发号 → 建头 → 建明细。buildHeader 只填类型特有字段；单号/状态/归属由
// 内核统一写入。prepare 可为 nil。
func (k *Kernel[H, I]) Create(
	ctx context.Context,
	prepare func(ctx context.Context, tx Tx[H, I]) error,
	buildHeader func(ctx context.Context) (H, error),
	buildItems func(ctx context.Context, doc H) ([]I, error),
) (H, error) {
	var zero H
	ownerUserID := k.env.CurrentUserID(ctx)
	ownerDeptID, err := k.env.OwnerDeptSnapshot(ctx, ownerUserID)
	if err != nil {
		return zero, err
	}
	var created H
	err = k.store.Transaction(ctx, func(tx Tx[H, I]) error {
		if prepare != nil {
			if err := prepare(ctx, tx); err != nil {
				return err
			}
		}
		no, err := tx.NextNo(ctx, k.spec.SeqBizType, k.spec.NoPrefix)
		if err != nil {
			return err
		}
		doc, err := buildHeader(ctx)
		if err != nil {
			return err
		}
		doc.SetNo(no)
		base := doc.Base()
		base.SetStatus(model.DocStatusUnaudited)
		base.SetOwner(ownerUserID, ownerDeptID)
		if err := tx.Create(ctx, doc); err != nil {
			return err
		}
		items, err := buildItems(ctx, doc)
		if err != nil {
			return err
		}
		if err := tx.CreateItems(ctx, doc.GetID(), items); err != nil {
			return err
		}
		created = doc
		return nil
	})
	if err != nil {
		return zero, err
	}
	return created, nil
}

// Update 编辑单据：finder/状态前置校验 → validate（事务外）→ 事务内锁重读
// 复核 status=10 → mutate（可做挂单锁/配额等事务内校验）→ 整体替换明细。
// validate/mutate 可为 nil。
func (k *Kernel[H, I]) Update(
	ctx context.Context,
	id int64,
	validate func(ctx context.Context, doc H) error,
	mutate func(ctx context.Context, tx Tx[H, I], doc H) error,
	buildItems func(ctx context.Context, doc H) ([]I, error),
) error {
	row, err := k.Find(ctx, id)
	if err != nil {
		return err
	}
	if row.Base().Status != model.DocStatusUnaudited {
		return errApprovedUpdate()
	}
	if validate != nil {
		if err := validate(ctx, row); err != nil {
			return err
		}
	}
	return k.store.Transaction(ctx, func(tx Tx[H, I]) error {
		if k.spec.BeforeUpdateLock != nil {
			if err := k.spec.BeforeUpdateLock(ctx, tx, row); err != nil {
				return err
			}
		}
		locked, err := tx.Lock(ctx, id)
		if err != nil {
			return k.mapNotFound(err)
		}
		if locked.Base().Status != model.DocStatusUnaudited {
			return errApprovedUpdate()
		}
		if mutate != nil {
			if err := mutate(ctx, tx, locked); err != nil {
				return err
			}
		}
		items, err := buildItems(ctx, locked)
		if err != nil {
			return err
		}
		if err := tx.Save(ctx, locked); err != nil {
			return err
		}
		if err := tx.DeleteItems(ctx, id); err != nil {
			return err
		}
		return tx.CreateItems(ctx, id, items)
	})
}

// Delete 删除单据：finder/状态前置校验 → 事务内下游禁令 → 条件软删 → 删明细。
func (k *Kernel[H, I]) Delete(ctx context.Context, id int64) error {
	row, err := k.Find(ctx, id)
	if err != nil {
		return err
	}
	if row.Base().Status != model.DocStatusUnaudited {
		return errApprovedDelete()
	}
	return k.store.Transaction(ctx, func(tx Tx[H, I]) error {
		if k.spec.BeforeDelete != nil {
			if err := k.spec.BeforeDelete(ctx, tx, row); err != nil {
				return err
			}
		}
		rows, err := tx.DeleteIfStatus(ctx, id, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if rows == 0 {
			return errApprovedDelete()
		}
		return tx.DeleteItems(ctx, id)
	})
}

// SetStatus 审核/反审核：幂等目标态 → 事务内乐观认领 → 落账/回滚钩子。
func (k *Kernel[H, I]) SetStatus(ctx context.Context, id int64, target int8) error {
	row, err := k.Find(ctx, id)
	if err != nil {
		return err
	}
	current := row.Base().Status
	idempotent, err := CheckStatusTransition(current, target)
	if err != nil {
		return err
	}
	if idempotent {
		return nil
	}
	isolation := k.spec.ApproveIsolation
	if target == model.DocStatusUnaudited {
		isolation = k.spec.UnapproveIsolation
	}
	run := k.store.Transaction
	if isolation == IsolationReadCommitted {
		run = k.store.TransactionReadCommitted
	}
	return run(ctx, func(tx Tx[H, I]) error {
		if k.spec.BeforeClaim != nil {
			if err := k.spec.BeforeClaim(ctx, tx, row); err != nil {
				return err
			}
		}
		rows, err := tx.UpdateStatusIf(ctx, id, current, target)
		if err != nil {
			return err
		}
		if rows == 0 {
			return errStatusChanged()
		}
		full, err := tx.FindWithItems(ctx, id)
		if err != nil {
			return k.mapNotFound(err)
		}
		if target == model.DocStatusApproved {
			if k.spec.Approve != nil {
				return k.spec.Approve(ctx, tx, full)
			}
			return nil
		}
		if k.spec.Unapprove != nil {
			return k.spec.Unapprove(ctx, tx, full)
		}
		return nil
	})
}
