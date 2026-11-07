package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// purchase_rules_test.go 锁定采购占用/剩余计算的纯函数（次 seam，spec §8.1：
// 占用/剩余计算表驱动）。期望值全部来自 spec §4.4 的公式手算样例，不与实现
// 复用同一套代码（非同义反复）。

func draftItem(productID int64, count string) docItemDraft {
	q, err := qty.ParseQty(count)
	if err != nil {
		panic(err)
	}
	return docItemDraft{
		ProductID:    productID,
		Count:        q,
		ProductPrice: money.FromCents(0),
		TotalPrice:   money.FromCents(0),
	}
}

func orderItem(productID int64, count string) model.PurchaseOrderItem {
	q, _ := qty.ParseQty(count)
	return model.PurchaseOrderItem{ProductID: productID, Count: q}
}

func inItem(productID int64, count string) model.PurchaseInItem {
	q, _ := qty.ParseQty(count)
	return model.PurchaseInItem{ProductID: productID, Count: q}
}

// assertConflict 断言错误为 409 冲突（占用超量的唯一合法失败形态）。
func assertConflict(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 409 冲突，得到 nil")
	}
	var xe *xerr.Error
	if !errors.As(err, &xe) || xe.Status != http.StatusConflict {
		t.Fatalf("期望 409 冲突，得到 %v", err)
	}
}

// TestCheckPurchaseInOccupancy 订单余量占用校验（spec §4.4 公式）。
func TestCheckPurchaseInOccupancy(t *testing.T) {
	// 订单两行同产品聚合（7 + 3 = 10），另有一行产品 2（5）。
	orderItems := []model.PurchaseOrderItem{
		orderItem(1, "7.000"),
		orderItem(1, "3.000"),
		orderItem(2, "5.000"),
	}
	cases := []struct {
		name         string
		occupiedIn   map[int64]qty.Qty
		occupiedRet  map[int64]qty.Qty
		drafts       []docItemDraft
		wantConflict bool
	}{
		{
			name:   "空余量全量可入",
			drafts: []docItemDraft{draftItem(1, "10.000")},
		},
		{
			name:         "超量 409（10 已占 9 再入 2）",
			occupiedIn:   map[int64]qty.Qty{1: draftItem(0, "9.000").Count},
			drafts:       []docItemDraft{draftItem(1, "2.000")},
			wantConflict: true,
		},
		{
			name:         "恰好占满放行（边界等号）",
			occupiedIn:   map[int64]qty.Qty{1: draftItem(0, "9.000").Count},
			drafts:       []docItemDraft{draftItem(1, "1.000")},
			wantConflict: false,
		},
		{
			name:         "退货也占订单余量（已入 8 已退 3 余量 10 再入 0.5 超量）",
			occupiedIn:   map[int64]qty.Qty{1: draftItem(0, "8.000").Count},
			occupiedRet:  map[int64]qty.Qty{1: draftItem(0, "3.000").Count},
			drafts:       []docItemDraft{draftItem(1, "0.500")},
			wantConflict: true,
		},
		{
			name:         "未审核入库也占量（在途 4.5 再入 0.5 恰好放行）",
			occupiedIn:   map[int64]qty.Qty{2: draftItem(0, "4.500").Count},
			drafts:       []docItemDraft{draftItem(2, "0.500")},
			wantConflict: false,
		},
		{
			name:         "未审核入库占满后 409",
			occupiedIn:   map[int64]qty.Qty{2: draftItem(0, "4.500").Count},
			drafts:       []docItemDraft{draftItem(2, "0.501")},
			wantConflict: true,
		},
		{
			name:         "产品不在订单中 409（余量为 0）",
			drafts:       []docItemDraft{draftItem(3, "1.000")},
			wantConflict: true,
		},
		{
			name:         "同产品多行草稿合并后超量（3×4 > 10）",
			drafts:       []docItemDraft{draftItem(1, "3.000"), draftItem(1, "3.000"), draftItem(1, "3.000"), draftItem(1, "3.000")},
			wantConflict: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPurchaseInOccupancy(orderItems, tc.occupiedIn, tc.occupiedRet, tc.drafts, nil)
			if tc.wantConflict {
				assertConflict(t, err)
				return
			}
			if err != nil {
				t.Fatalf("期望放行，得到 %v", err)
			}
		})
	}
}

// TestCheckPurchaseReturnOccupancy 入库单余量退货占用校验。
func TestCheckPurchaseReturnOccupancy(t *testing.T) {
	inItems := []model.PurchaseInItem{
		inItem(1, "6.000"),
		inItem(2, "4.000"),
	}
	cases := []struct {
		name         string
		occupiedRet  map[int64]qty.Qty
		drafts       []docItemDraft
		wantConflict bool
	}{
		{
			name:   "全额可退",
			drafts: []docItemDraft{draftItem(1, "6.000")},
		},
		{
			name:         "超入库量 409",
			drafts:       []docItemDraft{draftItem(1, "6.001")},
			wantConflict: true,
		},
		{
			name:         "既有退货占量后再退超量（已退 4 再退 3 > 6）",
			occupiedRet:  map[int64]qty.Qty{1: draftItem(0, "4.000").Count},
			drafts:       []docItemDraft{draftItem(1, "3.000")},
			wantConflict: true,
		},
		{
			name:         "未审核退货也占量（在途 5.5 再退 0.5 恰好放行）",
			occupiedRet:  map[int64]qty.Qty{1: draftItem(0, "5.500").Count},
			drafts:       []docItemDraft{draftItem(1, "0.500")},
			wantConflict: false,
		},
		{
			name:         "退货产品不在入库单 409",
			drafts:       []docItemDraft{draftItem(9, "1.000")},
			wantConflict: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPurchaseReturnOccupancy(inItems, tc.occupiedRet, tc.drafts, nil)
			if tc.wantConflict {
				assertConflict(t, err)
				return
			}
			if err != nil {
				t.Fatalf("期望放行，得到 %v", err)
			}
		})
	}
}

// TestSumDocItemQuantities 同产品多行合并。
func TestSumDocItemQuantities(t *testing.T) {
	got, err := sumDocItemQuantities([]docItemDraft{draftItem(1, "1.500"), draftItem(1, "2.250"), draftItem(2, "3.000")})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if got[1].String() != "3.750" || got[2].String() != "3.000" {
		t.Fatalf("合并结果不符预期: %v", got)
	}
}
