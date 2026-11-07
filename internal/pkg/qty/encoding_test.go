package qty_test

import (
	"encoding"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

var _ encoding.TextMarshaler = qty.Qty{}
var _ encoding.TextUnmarshaler = (*qty.Qty)(nil)

func TestQtyMarshalJSON(t *testing.T) {
	got, err := json.Marshal(qty.FromMilli(1234))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `"1.234"` {
		t.Fatalf("JSON = %s，期望十进制字符串 \"1.234\"", got)
	}
}

func TestQtyUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    qty.Qty
		wantErr error
	}{
		{name: "十进制字符串", text: `"0.5"`, want: qty.FromMilli(500)},
		{name: "null 归零", text: `null`, want: qty.Qty{}},
		{name: "数字字面量拒绝", text: `0.5`, wantErr: qty.ErrInvalidFormat},
		{name: "越界字符串拒绝", text: `"10000000000000000"`, wantErr: qty.ErrOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got qty.Qty
			err := json.Unmarshal([]byte(tc.text), &got)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Unmarshal(%s) err = %v，期望 %v", tc.text, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.text, err)
			}
			if got != tc.want {
				t.Fatalf("结果 %d 毫，期望 %d 毫", got.Milli(), tc.want.Milli())
			}
		})
	}
}

func TestQtyMarshalText(t *testing.T) {
	m, ok := any(qty.FromMilli(-500)).(encoding.TextMarshaler)
	if !ok {
		t.Fatal("Qty 应实现 TextMarshaler")
	}
	got, err := m.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(got) != "-0.500" {
		t.Fatalf("Text = %q，期望 \"-0.500\"", got)
	}
}

func TestQtyUnmarshalText(t *testing.T) {
	var q qty.Qty
	if err := q.UnmarshalText([]byte("12.345")); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if q != qty.FromMilli(12345) {
		t.Fatalf("结果 %d 毫，期望 12345 毫", q.Milli())
	}
	if err := q.UnmarshalText([]byte("1.2345")); err == nil || !strings.Contains(err.Error(), "小数") {
		t.Fatalf("四位小数应拒绝，err = %v", err)
	}
}

func TestQtyValueScan(t *testing.T) {
	v, err := qty.FromMilli(1234).Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if v != "1.234" {
		t.Fatalf("Value = %v，期望十进制字符串 \"1.234\"", v)
	}

	var q qty.Qty
	if err := q.Scan([]byte("1.234")); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if q != qty.FromMilli(1234) {
		t.Fatalf("Scan 结果 %d 毫，期望 1234 毫", q.Milli())
	}
	if err := q.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if q != (qty.Qty{}) {
		t.Fatal("Scan(nil) 应归零")
	}
	// 驱动兼容入口：float64 先转精确十进制字符串再解析，float64 不参与运算。
	if err := q.Scan(1.234); err != nil {
		t.Fatalf("Scan(float64): %v", err)
	}
	if q != qty.FromMilli(1234) {
		t.Fatalf("Scan(float64) 结果 %d 毫，期望 1234 毫", q.Milli())
	}
}
