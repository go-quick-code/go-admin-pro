package service

import (
	"context"
	"errors"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// party_refs_test.go 钉死删除引用扩展点契约（issue #89，spec §4.9）：三类引用
// 类型 erp_warehouse/erp_customer/erp_supplier 可注册、未注册空实现放行、注册
// 实现的错误原样透传（删除路径 409 的来源）。扩展点由 #90–#92 单据票实现后
// 在 app 装配处注册（spec §2：模块间零依赖）；落库路径由 HTTP 集成测试覆盖。

// stubWarehouseRefs/stubCustomerRefs/stubSupplierRefs 是可编程桩实现。
type stubWarehouseRefs struct{ err error }

func (s stubWarehouseRefs) CheckWarehouseRefs(context.Context, int64) error { return s.err }

type stubCustomerRefs struct{ err error }

func (s stubCustomerRefs) CheckCustomerRefs(context.Context, int64) error { return s.err }

type stubSupplierRefs struct{ err error }

func (s stubSupplierRefs) CheckSupplierRefs(context.Context, int64) error { return s.err }

func TestWarehouseRefCheckerExtension(t *testing.T) {
	s := New(nil, nil)

	// 未注册：空实现放行（当前无引用源）。
	if err := s.checkWarehouseRefs(context.Background(), 1); err != nil {
		t.Fatalf("未注册时应放行，实际 %v", err)
	}

	// 注册后：错误原样透传（删除路径以此返回 409）。
	want := xerr.Conflict("仓库被库存记录引用，禁止删除")
	s.SetWarehouseRefChecker(stubWarehouseRefs{err: want})
	if err := s.checkWarehouseRefs(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("注册实现错误应透传，实际 %v", err)
	}

	// 注册放行实现：nil。
	s.SetWarehouseRefChecker(stubWarehouseRefs{})
	if err := s.checkWarehouseRefs(context.Background(), 1); err != nil {
		t.Fatalf("注册放行实现应通过，实际 %v", err)
	}

	// 后注册覆盖先注册（幂等语义）。
	s.SetWarehouseRefChecker(stubWarehouseRefs{err: want})
	if err := s.checkWarehouseRefs(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("后注册应覆盖先注册，实际 %v", err)
	}
}

func TestCustomerRefCheckerExtension(t *testing.T) {
	s := New(nil, nil)

	if err := s.checkCustomerRefs(context.Background(), 1); err != nil {
		t.Fatalf("未注册时应放行，实际 %v", err)
	}

	want := xerr.Conflict("客户被单据引用，禁止删除")
	s.SetCustomerRefChecker(stubCustomerRefs{err: want})
	if err := s.checkCustomerRefs(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("注册实现错误应透传，实际 %v", err)
	}

	s.SetCustomerRefChecker(stubCustomerRefs{})
	if err := s.checkCustomerRefs(context.Background(), 1); err != nil {
		t.Fatalf("注册放行实现应通过，实际 %v", err)
	}
}

func TestSupplierRefCheckerExtension(t *testing.T) {
	s := New(nil, nil)

	if err := s.checkSupplierRefs(context.Background(), 1); err != nil {
		t.Fatalf("未注册时应放行，实际 %v", err)
	}

	want := xerr.Conflict("供应商被单据引用，禁止删除")
	s.SetSupplierRefChecker(stubSupplierRefs{err: want})
	if err := s.checkSupplierRefs(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("注册实现错误应透传，实际 %v", err)
	}

	s.SetSupplierRefChecker(stubSupplierRefs{})
	if err := s.checkSupplierRefs(context.Background(), 1); err != nil {
		t.Fatalf("注册放行实现应通过，实际 %v", err)
	}
}
