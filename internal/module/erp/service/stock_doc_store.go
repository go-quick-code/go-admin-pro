// stock_doc_store.go 是 tracer 阶段的手写单据存储（ADR 0018 决策 5）：
// 把一类单据的 repository 方法装配成 doc.Store/doc.Tx 面。候选 02 用泛型
// store 实现同一 Tx 面后替换本文件的 repoDocOps 装配。
package service

import (
	"context"
	"errors"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service/doc"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// docEnv 把 erp Service 的宿主能力适配给内核。
type docEnv struct{ s *Service }

// CurrentUserID 返回当前登录用户（owner 快照与可见性判断的唯一来源）。
func (e docEnv) CurrentUserID(ctx context.Context) int64 { return CurrentUserID(ctx) }

// OwnerDeptSnapshot 取 owner 的部门快照（与既有 ownerDeptSnapshot 同口径）。
func (e docEnv) OwnerDeptSnapshot(ctx context.Context, userID int64) (int64, error) {
	return e.s.ownerDeptSnapshot(ctx, userID)
}

// CheckRowVisible 做数据范围可见性校验（范围外 404）。
func (e docEnv) CheckRowVisible(ctx context.Context, label string, ownerUserID, ownerDeptID int64) error {
	return e.s.checkRowVisible(ctx, label, ownerUserID, ownerDeptID)
}

// docRepo 取内核事务的底层 repository（仅供适配器钩子：库存入账/进度回写等）。
func docRepo[H doc.Header, I any](tx doc.Tx[H, I]) (*repository.Repository, error) {
	repo, ok := tx.Handle().(*repository.Repository)
	if !ok {
		return nil, xerr.Internal(errors.New("单据内核事务句柄类型错误"))
	}
	return repo, nil
}

// repoDocOps 是一类单据的仓储操作集合；工厂按事务句柄创建，确保事务内
// 全部读写走同一连接。Find/FindWithItems/Lock 在 nil 行时返回 doc.ErrNotFound。
type repoDocOps[H doc.Header, I any] struct {
	Find           func(ctx context.Context, id int64) (H, error)
	FindWithItems  func(ctx context.Context, id int64) (H, error)
	Lock           func(ctx context.Context, id int64) (H, error)
	Create         func(ctx context.Context, doc H) error
	Save           func(ctx context.Context, doc H) error
	CreateItems    func(ctx context.Context, items []I) error
	DeleteItems    func(ctx context.Context, docID int64) error
	DeleteIfStatus func(ctx context.Context, id int64, status int8) (int64, error)
	UpdateStatusIf func(ctx context.Context, id int64, from, to int8) (int64, error)
}

// repoDocStore 实现 doc.Store：事务外 finder + 事务内 Tx 面。
type repoDocStore[H doc.Header, I any] struct {
	repo *repository.Repository
	now  func() time.Time
	ops  func(repo *repository.Repository) repoDocOps[H, I]
}

// newRepoDocStore 创建一类单据的存储（now 提供业务时区，用于单号日期键）。
func newRepoDocStore[H doc.Header, I any](
	repo *repository.Repository,
	now func() time.Time,
	ops func(repo *repository.Repository) repoDocOps[H, I],
) *repoDocStore[H, I] {
	return &repoDocStore[H, I]{repo: repo, now: now, ops: ops}
}

// Find 事务外读取单据主表（finder 的 404/可见性由内核统一处理）。
func (s *repoDocStore[H, I]) Find(ctx context.Context, id int64) (H, error) {
	return s.ops(s.repo).Find(ctx, id)
}

// Transaction 在默认隔离级别事务内执行内核生命周期。
func (s *repoDocStore[H, I]) Transaction(ctx context.Context, fn func(doc.Tx[H, I]) error) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		return fn(&repoDocTx[H, I]{repo: tx, now: s.now, ops: s.ops(tx)})
	})
}

// TransactionReadCommitted 以 READ COMMITTED 执行内核生命周期（财务核销）。
func (s *repoDocStore[H, I]) TransactionReadCommitted(ctx context.Context, fn func(doc.Tx[H, I]) error) error {
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		return fn(&repoDocTx[H, I]{repo: tx, now: s.now, ops: s.ops(tx)})
	})
}

// repoDocTx 实现 doc.Tx：全部操作绑定同一事务句柄。
type repoDocTx[H doc.Header, I any] struct {
	repo *repository.Repository
	now  func() time.Time
	ops  repoDocOps[H, I]
}

func (t *repoDocTx[H, I]) NextNo(ctx context.Context, seqBizType, prefix string) (string, error) {
	return nextDocNo(ctx, t.repo, t.now(), seqBizType, prefix)
}

func (t *repoDocTx[H, I]) FindWithItems(ctx context.Context, id int64) (H, error) {
	return t.ops.FindWithItems(ctx, id)
}

func (t *repoDocTx[H, I]) Lock(ctx context.Context, id int64) (H, error) {
	return t.ops.Lock(ctx, id)
}

func (t *repoDocTx[H, I]) Create(ctx context.Context, d H) error {
	return t.ops.Create(ctx, d)
}

func (t *repoDocTx[H, I]) Save(ctx context.Context, d H) error {
	return t.ops.Save(ctx, d)
}

func (t *repoDocTx[H, I]) CreateItems(ctx context.Context, docID int64, items []I) error {
	return t.ops.CreateItems(ctx, items)
}

func (t *repoDocTx[H, I]) DeleteItems(ctx context.Context, docID int64) error {
	return t.ops.DeleteItems(ctx, docID)
}

func (t *repoDocTx[H, I]) DeleteIfStatus(ctx context.Context, id int64, status int8) (int64, error) {
	return t.ops.DeleteIfStatus(ctx, id, status)
}

func (t *repoDocTx[H, I]) UpdateStatusIf(ctx context.Context, id int64, from, to int8) (int64, error) {
	return t.ops.UpdateStatusIf(ctx, id, from, to)
}

// Handle 返回底层事务句柄（适配器钩子专用）。
func (t *repoDocTx[H, I]) Handle() any { return t.repo }
