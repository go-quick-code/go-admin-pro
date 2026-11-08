// Package doc 是 ERP 单据内核（ADR 0018）：12 类单据共同的写侧生命周期。
//
// 内核拥有事务边界、状态认领、条件软删、状态机、单号发放与 finder；每类
// 单据只留适配器（元数据 + 访问器 + 校验/落账钩子）。内核不依赖 repository/
// GORM，通过 Store/Tx 取数，便于 fake store 单测；候选 02 的泛型 store 实现
// 同一 Tx 面。
package doc

import (
	"context"
	"errors"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// ErrNotFound 表示单据不存在（适配器 store 在 nil 行时返回，内核翻译为
// 「{单据名}不存在」404）。
var ErrNotFound = errors.New("doc: not found")

// Header 是单据主表对内核暴露的最小能力：DocBase 提供状态/归属/租户，
// 各单据类型补单号与业务时间访问器（业务时间列因类型而异）。
// H 取指针类型（如 *model.StockIn），方法集才包含 DocBase 的指针接收者。
type Header interface {
	GetID() int64
	Base() *model.DocBase
	GetNo() string
	SetNo(string)
	GetBizTime() time.Time
	SetBizTime(time.Time)
}

// Store 是内核的存储入口：事务外 finder + 两种隔离级别的事务。
type Store[H Header, I any] interface {
	Find(ctx context.Context, id int64) (H, error)
	// FindWithItems 事务外读取主表并预载明细（详情页用，可见性由内核校验）。
	FindWithItems(ctx context.Context, id int64) (H, error)
	Transaction(ctx context.Context, fn func(Tx[H, I]) error) error
	TransactionReadCommitted(ctx context.Context, fn func(Tx[H, I]) error) error
}

// Tx 是事务内的单据存储面；由适配器实现（tracer 手写，候选 02 泛型 store）。
type Tx[H Header, I any] interface {
	// NextNo 在事务内发放单号（前缀 + 业务日期 + 序号），与单据落库同事务。
	NextNo(ctx context.Context, seqBizType, prefix string) (string, error)
	FindWithItems(ctx context.Context, id int64) (H, error)
	Lock(ctx context.Context, id int64) (H, error)
	Create(ctx context.Context, doc H) error
	Save(ctx context.Context, doc H) error
	CreateItems(ctx context.Context, docID int64, items []I) error
	DeleteItems(ctx context.Context, docID int64) error
	DeleteIfStatus(ctx context.Context, id int64, status int8) (int64, error)
	UpdateStatusIf(ctx context.Context, id int64, from, to int8) (int64, error)
	// Handle 返回底层事务句柄，仅供适配器钩子使用（内核自身不调用）。
	Handle() any
}

// Env 是内核依赖的宿主能力（由 erp Service 实现）。
type Env interface {
	CurrentUserID(ctx context.Context) int64
	OwnerDeptSnapshot(ctx context.Context, userID int64) (int64, error)
	CheckRowVisible(ctx context.Context, label string, ownerUserID, ownerDeptID int64) error
}

// Labels 是内核标准文案的单据/明细名。
type Labels struct {
	Doc  string
	Item string
}

// Isolation 是审核/反审核事务的隔离级别。
type Isolation int

const (
	// IsolationDefault 使用仓储默认隔离级别（REPEATABLE READ）。
	IsolationDefault Isolation = iota
	// IsolationReadCommitted 使用 READ COMMITTED（财务核销上限的当前读）。
	IsolationReadCommitted
)

// Spec 是一类单据对内核的声明。
type Spec[H Header, I any] struct {
	Labels Labels
	// ApproveIsolation/UnapproveIsolation 分别控制审核/反审核事务的隔离级别；
	// 零值 IsolationDefault（仓储默认，REPEATABLE READ）。
	ApproveIsolation   Isolation
	UnapproveIsolation Isolation
	NoPrefix           string
	SeqBizType         string

	// Approve/Unapprove 在事务内、状态认领成功后执行落账/回滚。
	Approve   func(ctx context.Context, tx Tx[H, I], doc H) error
	Unapprove func(ctx context.Context, tx Tx[H, I], doc H) error
	// BeforeDelete 在事务内、条件软删前执行下游引用禁令（可为 nil）。
	BeforeDelete func(ctx context.Context, tx Tx[H, I], doc H) error
}

// Kernel 是一类单据的内核实例。
type Kernel[H Header, I any] struct {
	env   Env
	store Store[H, I]
	spec  Spec[H, I]
}

// New 创建内核实例。
func New[H Header, I any](env Env, store Store[H, I], spec Spec[H, I]) *Kernel[H, I] {
	return &Kernel[H, I]{env: env, store: store, spec: spec}
}
