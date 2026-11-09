// doc_store.go 是候选 02a 的泛型单据存储（ADR 0018 / 候选 02）：
// 用 schema 描述符 + GORM 泛型实现 doc.Store/doc.Tx 面，替换 01a 的
// 手写 repoDocOps 装配。事务内全部读写走同一 GORM 句柄（含租户/审计插件）。
package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

// DocSchema 描述一类单据的泛型存储差异（表名/明细关联名由模型自身提供，
// 全部单据主表都叫 Items；这里只声明主表零值工厂与明细外键列）。
type DocSchema[H doc.Header, I any] struct {
	// New 返回主表零值指针（H 为指针类型，如 *model.StockIn）。
	New func() H
	// ItemsFK 是明细表指向主表的外键列（如 stock_in_id）。
	ItemsFK string
	// SetItemDocID 把明细外键指向主表。由 store 在插入前统一写入，避免 adapter
	// 漏写 buildItems 的 FK 时静默产生 xxx_id=0 的孤儿明细。
	SetItemDocID func(item *I, docID int64)
}

// genericDocStore 实现 doc.Store：事务外 finder + 事务内 Tx 面。
type genericDocStore[H doc.Header, I any] struct {
	repo   *repository.Repository
	now    func() time.Time
	schema DocSchema[H, I]
}

// newGenericDocStore 创建一类单据的泛型存储（now 提供业务时区日期键）。
func newGenericDocStore[H doc.Header, I any](
	repo *repository.Repository,
	now func() time.Time,
	schema DocSchema[H, I],
) doc.Store[H, I] {
	return &genericDocStore[H, I]{repo: repo, now: now, schema: schema}
}

// Find 事务外读取单据主表。
func (s *genericDocStore[H, I]) Find(ctx context.Context, id int64) (H, error) {
	return s.find(ctx, id, false)
}

// FindWithItems 事务外读取单据主表并预载明细（详情页用）。
func (s *genericDocStore[H, I]) FindWithItems(ctx context.Context, id int64) (H, error) {
	return s.find(ctx, id, true)
}

func (s *genericDocStore[H, I]) find(ctx context.Context, id int64, withItems bool) (H, error) {
	row := s.schema.New()
	q := s.repo.DB().WithContext(ctx)
	if withItems {
		q = q.Preload("Items")
	}
	if err := q.First(row, id).Error; err != nil {
		return notFoundDoc[H](err)
	}
	return row, nil
}

// Transaction 在默认隔离级别事务内执行内核生命周期。
func (s *genericDocStore[H, I]) Transaction(ctx context.Context, fn func(doc.Tx[H, I]) error) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		return fn(&genericDocTx[H, I]{repo: tx, now: s.now, schema: s.schema})
	})
}

// TransactionReadCommitted 以 READ COMMITTED 执行内核生命周期（财务核销）。
func (s *genericDocStore[H, I]) TransactionReadCommitted(ctx context.Context, fn func(doc.Tx[H, I]) error) error {
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		return fn(&genericDocTx[H, I]{repo: tx, now: s.now, schema: s.schema})
	})
}

// genericDocTx 实现 doc.Tx：全部操作绑定同一事务句柄。
type genericDocTx[H doc.Header, I any] struct {
	repo   *repository.Repository
	now    func() time.Time
	schema DocSchema[H, I]
}

func (t *genericDocTx[H, I]) NextNo(ctx context.Context, seqBizType, prefix string) (string, error) {
	return nextDocNo(ctx, t.repo, t.now(), seqBizType, prefix)
}

func (t *genericDocTx[H, I]) FindWithItems(ctx context.Context, id int64) (H, error) {
	row := t.schema.New()
	if err := t.repo.DB().WithContext(ctx).Preload("Items").First(row, id).Error; err != nil {
		return notFoundDoc[H](err)
	}
	return row, nil
}

func (t *genericDocTx[H, I]) Lock(ctx context.Context, id int64) (H, error) {
	row := t.schema.New()
	if err := t.repo.DB().WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(row, id).Error; err != nil {
		return notFoundDoc[H](err)
	}
	return row, nil
}

func (t *genericDocTx[H, I]) Create(ctx context.Context, d H) error {
	return t.repo.DB().WithContext(ctx).Create(d).Error
}

func (t *genericDocTx[H, I]) Save(ctx context.Context, d H) error {
	return t.repo.DB().WithContext(ctx).Save(d).Error
}

func (t *genericDocTx[H, I]) CreateItems(ctx context.Context, docID int64, items []I) error {
	if len(items) == 0 {
		return nil
	}
	if t.schema.SetItemDocID != nil {
		for i := range items {
			t.schema.SetItemDocID(&items[i], docID)
		}
	}
	return t.repo.DB().WithContext(ctx).Create(&items).Error
}

func (t *genericDocTx[H, I]) DeleteItems(ctx context.Context, docID int64) error {
	var item I
	return t.repo.DB().WithContext(ctx).Where(t.schema.ItemsFK+" = ?", docID).Delete(&item).Error
}

func (t *genericDocTx[H, I]) DeleteIfStatus(ctx context.Context, id int64, status int8) (int64, error) {
	res := t.repo.DB().WithContext(ctx).
		Where("id = ? AND status = ?", id, status).
		Delete(t.schema.New())
	return res.RowsAffected, res.Error
}

func (t *genericDocTx[H, I]) UpdateStatusIf(ctx context.Context, id int64, from, to int8) (int64, error) {
	res := t.repo.DB().WithContext(ctx).
		Model(t.schema.New()).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	return res.RowsAffected, res.Error
}

// Handle 返回底层事务句柄（适配器钩子专用）。
func (t *genericDocTx[H, I]) Handle() any { return t.repo }

// notFoundDoc 把 GORM 的 ErrRecordNotFound 翻译为 doc.ErrNotFound。
func notFoundDoc[H doc.Header](err error) (H, error) {
	var zero H
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return zero, doc.ErrNotFound
	}
	return zero, err
}
