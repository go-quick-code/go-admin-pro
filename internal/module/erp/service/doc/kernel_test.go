package doc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service/doc"
	pkgmodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// ---------- fake 单据类型 ----------

type fakeItem struct {
	ID        int64
	ProductID int64
}

type fakeHeader struct {
	pkgmodel.BaseEntity
	model.DocBase
	No      string
	BizTime time.Time
	Items   []fakeItem
}

func (h *fakeHeader) GetNo() string          { return h.No }
func (h *fakeHeader) SetNo(v string)         { h.No = v }
func (h *fakeHeader) GetBizTime() time.Time  { return h.BizTime }
func (h *fakeHeader) SetBizTime(v time.Time) { h.BizTime = v }

// ---------- fake store / tx ----------

type fakeStore struct {
	docs       map[int64]*fakeHeader
	nextID     int64
	seq        int64
	lockHook   func(*fakeHeader)
	claimHook  func() (int64, error)
	deleteHook func() (int64, error)
}

func newFakeStore() *fakeStore { return &fakeStore{docs: map[int64]*fakeHeader{}} }

func cloneDocs(src map[int64]*fakeHeader) map[int64]*fakeHeader {
	dst := make(map[int64]*fakeHeader, len(src))
	for id, d := range src {
		cp := *d
		cp.Items = append([]fakeItem(nil), d.Items...)
		dst[id] = &cp
	}
	return dst
}

func (s *fakeStore) Find(_ context.Context, id int64) (*fakeHeader, error) {
	d, ok := s.docs[id]
	if !ok {
		return nil, doc.ErrNotFound
	}
	cp := *d
	cp.Items = append([]fakeItem(nil), d.Items...)
	return &cp, nil
}

func (s *fakeStore) Transaction(_ context.Context, fn func(doc.Tx[*fakeHeader, fakeItem]) error) error {
	return s.run(fn)
}

func (s *fakeStore) TransactionReadCommitted(_ context.Context, fn func(doc.Tx[*fakeHeader, fakeItem]) error) error {
	return s.run(fn)
}

func (s *fakeStore) run(fn func(doc.Tx[*fakeHeader, fakeItem]) error) error {
	snapshot := cloneDocs(s.docs)
	if err := fn(&fakeTx{store: s}); err != nil {
		s.docs = snapshot
		return err
	}
	return nil
}

type fakeTx struct{ store *fakeStore }

func (t *fakeTx) NextNo(_ context.Context, _, prefix string) (string, error) {
	t.store.seq++
	return fmt.Sprintf("%s%04d", prefix, t.store.seq), nil
}

func (t *fakeTx) FindWithItems(ctx context.Context, id int64) (*fakeHeader, error) {
	return t.store.Find(ctx, id)
}

func (t *fakeTx) Lock(ctx context.Context, id int64) (*fakeHeader, error) {
	row, err := t.store.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.store.lockHook != nil {
		t.store.lockHook(row)
	}
	return row, nil
}

func (t *fakeTx) Create(_ context.Context, d *fakeHeader) error {
	t.store.nextID++
	d.ID = t.store.nextID
	cp := *d
	cp.Items = append([]fakeItem(nil), d.Items...)
	t.store.docs[d.ID] = &cp
	return nil
}

func (t *fakeTx) Save(_ context.Context, d *fakeHeader) error {
	cp := *d
	cp.Items = append([]fakeItem(nil), d.Items...)
	t.store.docs[d.ID] = &cp
	return nil
}

func (t *fakeTx) CreateItems(_ context.Context, docID int64, items []fakeItem) error {
	d, ok := t.store.docs[docID]
	if !ok {
		return fmt.Errorf("doc %d not found", docID)
	}
	next := int64(len(d.Items))
	for i := range items {
		next++
		items[i].ID = next
	}
	d.Items = append(d.Items, items...)
	return nil
}

func (t *fakeTx) DeleteItems(_ context.Context, docID int64) error {
	if d, ok := t.store.docs[docID]; ok {
		d.Items = nil
	}
	return nil
}

func (t *fakeTx) DeleteIfStatus(_ context.Context, id int64, status int8) (int64, error) {
	if t.store.deleteHook != nil {
		return t.store.deleteHook()
	}
	d, ok := t.store.docs[id]
	if !ok || d.Base().Status != status {
		return 0, nil
	}
	delete(t.store.docs, id)
	return 1, nil
}

func (t *fakeTx) UpdateStatusIf(_ context.Context, id int64, from, to int8) (int64, error) {
	if t.store.claimHook != nil {
		return t.store.claimHook()
	}
	d, ok := t.store.docs[id]
	if !ok || d.Base().Status != from {
		return 0, nil
	}
	d.Base().SetStatus(to)
	return 1, nil
}

func (t *fakeTx) Handle() any { return nil }

// ---------- fake env / hooks ----------

type fakeEnv struct {
	userID     int64
	deptID     int64
	ownerErr   error
	visibleErr error
	lastLabel  string
}

func (e *fakeEnv) CurrentUserID(context.Context) int64 { return e.userID }

func (e *fakeEnv) OwnerDeptSnapshot(context.Context, int64) (int64, error) {
	return e.deptID, e.ownerErr
}

func (e *fakeEnv) CheckRowVisible(_ context.Context, label string, _, _ int64) error {
	e.lastLabel = label
	return e.visibleErr
}

type hookState struct {
	approveCalls      int
	unapproveCalls    int
	beforeDeleteCalls int
	approveErr        error
	unapproveErr      error
	beforeDeleteErr   error
	lastItems         int
}

func (h *hookState) approve(_ context.Context, _ doc.Tx[*fakeHeader, fakeItem], d *fakeHeader) error {
	h.approveCalls++
	h.lastItems = len(d.Items)
	return h.approveErr
}

func (h *hookState) unapprove(_ context.Context, _ doc.Tx[*fakeHeader, fakeItem], _ *fakeHeader) error {
	h.unapproveCalls++
	return h.unapproveErr
}

func (h *hookState) beforeDelete(_ context.Context, _ doc.Tx[*fakeHeader, fakeItem], _ *fakeHeader) error {
	h.beforeDeleteCalls++
	return h.beforeDeleteErr
}

func newTestKernel(t *testing.T) (*doc.Kernel[*fakeHeader, fakeItem], *fakeStore, *fakeEnv, *hookState) {
	t.Helper()
	store := newFakeStore()
	env := &fakeEnv{userID: 7, deptID: 8}
	hooks := &hookState{}
	spec := doc.Spec[*fakeHeader, fakeItem]{
		Labels:       doc.Labels{Doc: "测试单", Item: "测试明细"},
		NoPrefix:     "TS",
		SeqBizType:   "test",
		Approve:      hooks.approve,
		Unapprove:    hooks.unapprove,
		BeforeDelete: hooks.beforeDelete,
	}
	return doc.New[*fakeHeader, fakeItem](env, store, spec), store, env, hooks
}

func storedHeader(store *fakeStore, id int64) *fakeHeader {
	return store.docs[id]
}

func newUnaudited(id int64) *fakeHeader {
	return &fakeHeader{
		BaseEntity: pkgmodel.BaseEntity{ID: id},
		DocBase:    model.DocBase{Status: model.DocStatusUnaudited},
		No:         "TS0001",
	}
}

// ---------- tests ----------

func TestKernelCreate(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	bizTime := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	created, err := k.Create(context.Background(),
		func(context.Context) (*fakeHeader, error) {
			return &fakeHeader{BizTime: bizTime}, nil
		},
		func(_ context.Context, d *fakeHeader) ([]fakeItem, error) {
			if d.ID == 0 {
				t.Fatal("buildItems 应拿到已落库的单据 ID")
			}
			return []fakeItem{{ProductID: 11}, {ProductID: 12}}, nil
		},
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.GetNo() != "TS0001" {
		t.Fatalf("单号 = %q，期望 TS0001", created.GetNo())
	}
	if created.Base().Status != model.DocStatusUnaudited {
		t.Fatalf("状态 = %d，期望 10", created.Base().Status)
	}
	ownerID, deptID := created.Base().GetOwner()
	if ownerID != 7 || deptID != 8 {
		t.Fatalf("owner = (%d,%d)，期望 (7,8)", ownerID, deptID)
	}
	stored := storedHeader(store, created.GetID())
	if stored == nil {
		t.Fatal("单据未落库")
	}
	if len(stored.Items) != 2 || stored.Items[0].ProductID != 11 || stored.Items[1].ProductID != 12 {
		t.Fatalf("明细 = %+v，期望 2 行 [11,12]", stored.Items)
	}
}

func TestKernelCreateRollsBackOnItemError(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	_, err := k.Create(context.Background(),
		func(context.Context) (*fakeHeader, error) { return &fakeHeader{}, nil },
		func(context.Context, *fakeHeader) ([]fakeItem, error) { return nil, errors.New("明细构造失败") },
	)
	if err == nil || err.Error() != "明细构造失败" {
		t.Fatalf("err = %v，期望 明细构造失败", err)
	}
	if len(store.docs) != 0 {
		t.Fatalf("回滚后不应有单据，实际 %d", len(store.docs))
	}
}

func TestKernelCreateOwnerError(t *testing.T) {
	k, store, env, _ := newTestKernel(t)
	env.ownerErr = errors.New("用户无部门")
	_, err := k.Create(context.Background(),
		func(context.Context) (*fakeHeader, error) { return &fakeHeader{}, nil },
		func(context.Context, *fakeHeader) ([]fakeItem, error) { return nil, nil },
	)
	if err == nil || err.Error() != "用户无部门" {
		t.Fatalf("err = %v，期望 用户无部门", err)
	}
	if len(store.docs) != 0 {
		t.Fatal("owner 失败不应落库")
	}
}

func TestKernelUpdateRejectsApprovedBeforeValidate(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Base().SetStatus(model.DocStatusApproved)
	store.docs[1] = doc
	validateCalls := 0
	err := k.Update(context.Background(), 1,
		func(context.Context, *fakeHeader) error { validateCalls++; return nil },
		func(context.Context, *fakeHeader) error { return nil },
		func(context.Context, *fakeHeader) ([]fakeItem, error) { return nil, nil },
	)
	if err == nil || err.Error() != "已审核单据不可修改" {
		t.Fatalf("err = %v，期望 已审核单据不可修改", err)
	}
	if validateCalls != 0 {
		t.Fatalf("已审核单据不应进入 validate，实际调用 %d 次", validateCalls)
	}
}

func TestKernelUpdateLockRecheck(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	store.lockHook = func(d *fakeHeader) { d.Base().SetStatus(model.DocStatusApproved) }
	mutateCalls := 0
	err := k.Update(context.Background(), 1, nil,
		func(context.Context, *fakeHeader) error { mutateCalls++; return nil },
		func(context.Context, *fakeHeader) ([]fakeItem, error) { return nil, nil },
	)
	if err == nil || err.Error() != "已审核单据不可修改" {
		t.Fatalf("err = %v，期望 已审核单据不可修改", err)
	}
	if mutateCalls != 0 {
		t.Fatalf("锁内复核失败不应 mutate，实际调用 %d 次", mutateCalls)
	}
}

func TestKernelUpdateReplacesItems(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Items = []fakeItem{{ID: 9, ProductID: 99}}
	store.docs[1] = doc
	err := k.Update(context.Background(), 1, nil,
		func(_ context.Context, d *fakeHeader) error {
			d.BizTime = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
		func(context.Context, *fakeHeader) ([]fakeItem, error) {
			return []fakeItem{{ProductID: 1}}, nil
		},
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	stored := storedHeader(store, 1)
	if len(stored.Items) != 1 || stored.Items[0].ProductID != 1 {
		t.Fatalf("明细 = %+v，期望整体替换为 [1]", stored.Items)
	}
}

func TestKernelDeleteRejectsApproved(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Base().SetStatus(model.DocStatusApproved)
	store.docs[1] = doc
	err := k.Delete(context.Background(), 1)
	if err == nil || err.Error() != "已审核单据不可删除" {
		t.Fatalf("err = %v，期望 已审核单据不可删除", err)
	}
	if hooks.beforeDeleteCalls != 0 {
		t.Fatalf("已审核单据不应执行下游禁令，实际 %d 次", hooks.beforeDeleteCalls)
	}
}

func TestKernelDeleteConditionalConflict(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Items = []fakeItem{{ID: 1, ProductID: 1}}
	store.docs[1] = doc
	store.deleteHook = func() (int64, error) { return 0, nil }
	err := k.Delete(context.Background(), 1)
	if err == nil || err.Error() != "已审核单据不可删除" {
		t.Fatalf("err = %v，期望 已审核单据不可删除", err)
	}
	if len(storedHeader(store, 1).Items) != 1 {
		t.Fatal("条件软删失败时不应删除明细")
	}
}

func TestKernelDeleteOK(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	if err := k.Delete(context.Background(), 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.docs[1] != nil {
		t.Fatal("单据应被删除")
	}
	if hooks.beforeDeleteCalls != 1 {
		t.Fatalf("下游禁令应执行 1 次，实际 %d", hooks.beforeDeleteCalls)
	}
}

func TestKernelDeleteBeforeDeleteError(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	hooks.beforeDeleteErr = errors.New("存在下游引用")
	if err := k.Delete(context.Background(), 1); err == nil || err.Error() != "存在下游引用" {
		t.Fatalf("err = %v，期望 存在下游引用", err)
	}
	if store.docs[1] == nil {
		t.Fatal("禁令失败时单据应保留")
	}
}

func TestKernelSetStatusApprove(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Items = []fakeItem{{ID: 1, ProductID: 1}, {ID: 2, ProductID: 2}}
	store.docs[1] = doc
	if err := k.SetStatus(context.Background(), 1, model.DocStatusApproved); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got := storedHeader(store, 1).Base().Status; got != model.DocStatusApproved {
		t.Fatalf("状态 = %d，期望 20", got)
	}
	if hooks.approveCalls != 1 || hooks.lastItems != 2 {
		t.Fatalf("approve 调用 %d 次、明细 %d 行，期望 1 次/2 行", hooks.approveCalls, hooks.lastItems)
	}
}

func TestKernelSetStatusIdempotent(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Base().SetStatus(model.DocStatusApproved)
	store.docs[1] = doc
	if err := k.SetStatus(context.Background(), 1, model.DocStatusApproved); err != nil {
		t.Fatalf("幂等目标态应放行: %v", err)
	}
	if hooks.approveCalls != 0 {
		t.Fatalf("幂等目标态不应触发 approve，实际 %d 次", hooks.approveCalls)
	}
}

func TestKernelSetStatusInvalidTarget(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	err := k.SetStatus(context.Background(), 1, 99)
	if err == nil || err.Error() != "单据审核状态 99 不合法：期望 10 未审核 / 20 已审核" {
		t.Fatalf("err = %v", err)
	}
}

func TestKernelSetStatusClaimConflict(t *testing.T) {
	k, store, _, _ := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	store.claimHook = func() (int64, error) { return 0, nil }
	err := k.SetStatus(context.Background(), 1, model.DocStatusApproved)
	if err == nil || err.Error() != "单据状态已变化，请刷新后重试" {
		t.Fatalf("err = %v，期望 单据状态已变化，请刷新后重试", err)
	}
}

func TestKernelSetStatusHookErrorRollsBack(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	hooks.approveErr = errors.New("库存不足")
	err := k.SetStatus(context.Background(), 1, model.DocStatusApproved)
	if err == nil || err.Error() != "库存不足" {
		t.Fatalf("err = %v，期望 库存不足", err)
	}
	if got := storedHeader(store, 1).Base().Status; got != model.DocStatusUnaudited {
		t.Fatalf("钩子失败应回滚状态到 10，实际 %d", got)
	}
}

func TestKernelSetStatusUnapprove(t *testing.T) {
	k, store, _, hooks := newTestKernel(t)
	doc := newUnaudited(1)
	doc.Base().SetStatus(model.DocStatusApproved)
	store.docs[1] = doc
	if err := k.SetStatus(context.Background(), 1, model.DocStatusUnaudited); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got := storedHeader(store, 1).Base().Status; got != model.DocStatusUnaudited {
		t.Fatalf("状态 = %d，期望 10", got)
	}
	if hooks.unapproveCalls != 1 {
		t.Fatalf("unapprove 调用 %d 次，期望 1", hooks.unapproveCalls)
	}
}

func TestKernelFindNotFound(t *testing.T) {
	k, _, _, _ := newTestKernel(t)
	_, err := k.Find(context.Background(), 42)
	if err == nil || err.Error() != "测试单不存在" {
		t.Fatalf("err = %v，期望 测试单不存在", err)
	}
}

func TestKernelFindVisibility(t *testing.T) {
	k, store, env, _ := newTestKernel(t)
	store.docs[1] = newUnaudited(1)
	env.visibleErr = errors.New("不可见")
	_, err := k.Find(context.Background(), 1)
	if err == nil || err.Error() != "不可见" {
		t.Fatalf("err = %v，期望 不可见", err)
	}
	if env.lastLabel != "测试单" {
		t.Fatalf("可见性文案标签 = %q，期望 测试单", env.lastLabel)
	}
}
