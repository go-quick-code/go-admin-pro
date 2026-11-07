package money_test

import (
	"errors"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// TestMoneyMulQty 明细金额 = 单价(Money) × 数量(qty)（ADR 0017）：
// 毫 × 分 的精确乘积经 128 位中间量计算，按绝对值四舍五入到分。
func TestMoneyMulQty(t *testing.T) {
	tests := []struct {
		name      string
		price     money.Money
		count     qty.Qty
		wantCents int64
		wantErr   error
	}{
		{name: "整数量", price: money.FromCents(1000), count: qty.FromMilli(3000), wantCents: 3000},
		{name: "半单位", price: money.FromCents(1000), count: qty.FromMilli(500), wantCents: 500},
		{name: "三位小数舍去", price: money.FromCents(100), count: qty.FromMilli(333), wantCents: 33},
		{name: "三位小数半入", price: money.FromCents(100), count: qty.FromMilli(665), wantCents: 67},
		{name: "零数量", price: money.FromCents(1000), count: qty.FromMilli(0), wantCents: 0},
		{name: "零单价", price: money.FromCents(0), count: qty.FromMilli(999), wantCents: 0},
		{name: "负单价", price: money.FromCents(-1000), count: qty.FromMilli(500), wantCents: -500},
		{name: "负数量", price: money.FromCents(1000), count: qty.FromMilli(-500), wantCents: -500},
		{name: "负负得正", price: money.FromCents(-1000), count: qty.FromMilli(-500), wantCents: 500},
		{name: "上界乘积越金额域", price: money.FromCents(999999999999), count: qty.FromMilli(9223372036854775807), wantErr: money.ErrOutOfRange},
		{name: "两倍上界越金额域", price: money.FromCents(999999999999), count: qty.FromMilli(2000), wantErr: money.ErrOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.price.MulQty(tc.count)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v，期望 %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got.Cents() != tc.wantCents {
				t.Fatalf("结果 %s（%d 分），期望 %d 分", got.String(), got.Cents(), tc.wantCents)
			}
		})
	}
}
