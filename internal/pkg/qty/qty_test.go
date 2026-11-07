package qty_test

import (
	"errors"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

func TestParseQty(t *testing.T) {
	tests := []struct {
		name string
		text string
		want qty.Qty
		str  string // 解析后固定三位小数的格式化结果
	}{
		{name: "零", text: "0", want: qty.FromMilli(0), str: "0.000"},
		{name: "半千克", text: "0.5", want: qty.FromMilli(500), str: "0.500"},
		{name: "三位小数", text: "1.234", want: qty.FromMilli(1234), str: "1.234"},
		{name: "整数数量", text: "123", want: qty.FromMilli(123000), str: "123.000"},
		{name: "一位小数补齐", text: "0.05", want: qty.FromMilli(50), str: "0.050"},
		{name: "两位小数补齐", text: "0.005", want: qty.FromMilli(5), str: "0.005"},
		{name: "负数", text: "-1.234", want: qty.FromMilli(-1234), str: "-1.234"},
		{name: "int64 上界", text: "9223372036854775.807", want: qty.FromMilli(9223372036854775807), str: "9223372036854775.807"},
		{name: "int64 下界", text: "-9223372036854775.808", want: qty.FromMilli(-9223372036854775808), str: "-9223372036854775.808"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := qty.ParseQty(tc.text)
			if err != nil {
				t.Fatalf("ParseQty(%q): %v", tc.text, err)
			}
			if got != tc.want {
				t.Fatalf("ParseQty(%q) = %v（%d 毫），期望 %d 毫", tc.text, got.String(), got.Milli(), tc.want.Milli())
			}
			if got.String() != tc.str {
				t.Fatalf("String() = %q，期望 %q", got.String(), tc.str)
			}
		})
	}
}

func TestParseQtyInvalid(t *testing.T) {
	tests := []struct {
		name string
		text string
		want error
	}{
		{name: "空串", text: "", want: qty.ErrInvalidFormat},
		{name: "只有负号", text: "-", want: qty.ErrInvalidFormat},
		{name: "整数部分缺失", text: ".5", want: qty.ErrInvalidFormat},
		{name: "小数部分缺失", text: "12.", want: qty.ErrInvalidFormat},
		{name: "非数字", text: "abc", want: qty.ErrInvalidFormat},
		{name: "混入字母", text: "1a.5", want: qty.ErrInvalidFormat},
		{name: "超过三位小数", text: "1.2345", want: qty.ErrInvalidFormat},
		{name: "数字字面量不接受加号", text: "+1.5", want: qty.ErrInvalidFormat},
		{name: "正数超 int64 毫单位", text: "9223372036854775.808", want: qty.ErrOutOfRange},
		{name: "负数超 int64 毫单位", text: "-9223372036854775.809", want: qty.ErrOutOfRange},
		{name: "17 位整数超界", text: "10000000000000000", want: qty.ErrOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := qty.ParseQty(tc.text)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseQty(%q) err = %v，期望 %v", tc.text, err, tc.want)
			}
		})
	}
}

func TestQtyAddSub(t *testing.T) {
	tests := []struct {
		name    string
		a, b    qty.Qty
		op      func(qty.Qty, qty.Qty) (qty.Qty, error)
		want    qty.Qty
		wantErr error
	}{
		{name: "相加", a: qty.FromMilli(500), b: qty.FromMilli(500),
			op: qty.Qty.Add, want: qty.FromMilli(1000)},
		{name: "相减", a: qty.FromMilli(1000), b: qty.FromMilli(500),
			op: qty.Qty.Sub, want: qty.FromMilli(500)},
		{name: "负数相减", a: qty.FromMilli(0), b: qty.FromMilli(500),
			op: qty.Qty.Sub, want: qty.FromMilli(-500)},
		{name: "加法溢出", a: qty.FromMilli(9223372036854775807), b: qty.FromMilli(1),
			op: qty.Qty.Add, wantErr: qty.ErrOutOfRange},
		{name: "减法下溢", a: qty.FromMilli(-9223372036854775808), b: qty.FromMilli(1),
			op: qty.Qty.Sub, wantErr: qty.ErrOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.op(tc.a, tc.b)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v，期望 %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got != tc.want {
				t.Fatalf("结果 %d 毫，期望 %d 毫", got.Milli(), tc.want.Milli())
			}
		})
	}
}
