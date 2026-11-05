package money_test

import (
	"database/sql/driver"
	"encoding"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 编译期断言：Money 满足各编码边界要求的接口。
var (
	_ json.Marshaler           = money.Money{}
	_ json.Unmarshaler         = (*money.Money)(nil)
	_ encoding.TextMarshaler   = money.Money{}
	_ encoding.TextUnmarshaler = (*money.Money)(nil)
	_ driver.Valuer            = money.Money{}
	_ sqlScanner               = (*money.Money)(nil)
)

// sqlScanner 与 database/sql 的 Scanner 签名一致，避免直接依赖驱动的类型断言噪音。
type sqlScanner = interface {
	Scan(src any) error
}

type moneyJSON struct {
	Amount money.Money `json:"amount"`
}

func TestMoneyMarshalJSON(t *testing.T) {
	m, err := money.ParseMoney("1234.56")
	if err != nil {
		t.Fatalf("ParseMoney 返回错误：%v", err)
	}
	data, err := json.Marshal(moneyJSON{Amount: m})
	if err != nil {
		t.Fatalf("json.Marshal 返回错误：%v", err)
	}
	if got, want := string(data), `{"amount":"1234.56"}`; got != want {
		t.Fatalf("json.Marshal = %s, 期望 %s", got, want)
	}
	zero, err := json.Marshal(money.Money{})
	if err != nil {
		t.Fatalf("json.Marshal 返回错误：%v", err)
	}
	if got, want := string(zero), `"0.00"`; got != want {
		t.Fatalf("零值 json.Marshal = %s, 期望 %s", got, want)
	}
}

func TestMoneyUnmarshalJSON(t *testing.T) {
	t.Run("接受十进制字符串", func(t *testing.T) {
		var payload moneyJSON
		if err := json.Unmarshal([]byte(`{"amount":"1234.56"}`), &payload); err != nil {
			t.Fatalf("json.Unmarshal 返回错误：%v", err)
		}
		if payload.Amount.String() != "1234.56" {
			t.Fatalf("解析结果 = %q, 期望 1234.56", payload.Amount.String())
		}
	})

	t.Run("null 归零", func(t *testing.T) {
		m := money.FromCents(123456)
		if err := json.Unmarshal([]byte(`null`), &m); err != nil {
			t.Fatalf("json.Unmarshal 返回错误：%v", err)
		}
		if m.String() != "0.00" {
			t.Fatalf("null 应归零, 得到 %q", m.String())
		}
	})

	t.Run("拒绝 JSON 数字字面量", func(t *testing.T) {
		var m money.Money
		err := json.Unmarshal([]byte(`1234.56`), &m)
		if !errors.Is(err, money.ErrInvalidFormat) {
			t.Fatalf("JSON 数字应被拒绝, 得到 %v", err)
		}
	})

	t.Run("拒绝不可表示的字符串", func(t *testing.T) {
		tests := []struct {
			text string
			err  error
		}{
			{`"1234.567"`, money.ErrInvalidFormat},
			{`"abc"`, money.ErrInvalidFormat},
			{`""`, money.ErrInvalidFormat},
			{`"10000000000.00"`, money.ErrOutOfRange},
		}
		for _, tt := range tests {
			var m money.Money
			if err := json.Unmarshal([]byte(tt.text), &m); !errors.Is(err, tt.err) {
				t.Fatalf("json.Unmarshal(%s) 应返回 %v, 得到 %v", tt.text, tt.err, err)
			}
		}
	})

	t.Run("拒绝其他类型", func(t *testing.T) {
		var m money.Money
		if err := json.Unmarshal([]byte(`false`), &m); !errors.Is(err, money.ErrInvalidFormat) {
			t.Fatalf("JSON 布尔值应被拒绝, 得到 %v", err)
		}
	})
}

func TestMoneyMarshalJSONNotFloat(t *testing.T) {
	// 大额输出必须是精确的十进制字符串，不含科学计数法或浮点尾数。
	m := money.FromCents(999999999999)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal 返回错误：%v", err)
	}
	if got, want := string(data), `"9999999999.99"`; got != want {
		t.Fatalf("json.Marshal = %s, 期望 %s", got, want)
	}
	if strings.ContainsAny(string(data), "eE") {
		t.Fatalf("输出不应包含科学计数法：%s", data)
	}
}

func TestMoneyMarshalText(t *testing.T) {
	m, err := money.ParseMoney("-0.05")
	if err != nil {
		t.Fatalf("ParseMoney 返回错误：%v", err)
	}
	text, err := m.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText 返回错误：%v", err)
	}
	if got, want := string(text), "-0.05"; got != want {
		t.Fatalf("MarshalText = %q, 期望 %q", got, want)
	}
}

func TestMoneyUnmarshalText(t *testing.T) {
	t.Run("往返", func(t *testing.T) {
		var m money.Money
		if err := m.UnmarshalText([]byte("1234.56")); err != nil {
			t.Fatalf("UnmarshalText 返回错误：%v", err)
		}
		if m.String() != "1234.56" {
			t.Fatalf("往返结果 = %q, 期望 1234.56", m.String())
		}
	})

	t.Run("空串拒绝", func(t *testing.T) {
		var m money.Money
		if err := m.UnmarshalText([]byte("")); !errors.Is(err, money.ErrInvalidFormat) {
			t.Fatalf("空串应返回 ErrInvalidFormat, 得到 %v", err)
		}
	})
}

func TestMoneyValue(t *testing.T) {
	m, err := money.ParseMoney("1234.56")
	if err != nil {
		t.Fatalf("ParseMoney 返回错误：%v", err)
	}
	value, err := m.Value()
	if err != nil {
		t.Fatalf("Value 返回错误：%v", err)
	}
	if got, ok := value.(string); !ok || got != "1234.56" {
		t.Fatalf("Value = (%T)%v, 期望 string \"1234.56\"", value, value)
	}
}

func TestMoneyScan(t *testing.T) {
	t.Run("接受字符串", func(t *testing.T) {
		var m money.Money
		if err := m.Scan("1234.56"); err != nil {
			t.Fatalf("Scan 返回错误：%v", err)
		}
		if m.String() != "1234.56" {
			t.Fatalf("Scan 结果 = %q, 期望 1234.56", m.String())
		}
	})

	t.Run("接受字节串", func(t *testing.T) {
		var m money.Money
		if err := m.Scan([]byte("-0.05")); err != nil {
			t.Fatalf("Scan 返回错误：%v", err)
		}
		if m.String() != "-0.05" {
			t.Fatalf("Scan 结果 = %q, 期望 -0.05", m.String())
		}
	})

	t.Run("nil 归零", func(t *testing.T) {
		m := money.FromCents(123456)
		if err := m.Scan(nil); err != nil {
			t.Fatalf("Scan(nil) 返回错误：%v", err)
		}
		if m.String() != "0.00" {
			t.Fatalf("Scan(nil) 应归零, 得到 %q", m.String())
		}
	})

	t.Run("float64 驱动兼容入口无损转换", func(t *testing.T) {
		var m money.Money
		if err := m.Scan(float64(1234.56)); err != nil {
			t.Fatalf("Scan 返回错误：%v", err)
		}
		if m.String() != "1234.56" {
			t.Fatalf("Scan 结果 = %q, 期望 1234.56", m.String())
		}
	})

	t.Run("float64 有损值拒绝", func(t *testing.T) {
		var m money.Money
		if err := m.Scan(float64(1234.567)); !errors.Is(err, money.ErrInvalidFormat) {
			t.Fatalf("float64(1234.567) 应被拒绝, 得到 %v", err)
		}
	})

	t.Run("非法输入拒绝", func(t *testing.T) {
		for _, src := range []any{"abc", []byte("1234.567"), true, int64(123)} {
			var m money.Money
			if err := m.Scan(src); err == nil {
				t.Fatalf("Scan(%v) 应被拒绝", src)
			}
		}
	})
}

func TestMoneyScanValueRoundTrip(t *testing.T) {
	want, err := money.ParseMoney("9999999999.99")
	if err != nil {
		t.Fatalf("ParseMoney 返回错误：%v", err)
	}
	value, err := want.Value()
	if err != nil {
		t.Fatalf("Value 返回错误：%v", err)
	}
	var got money.Money
	if err := got.Scan(value); err != nil {
		t.Fatalf("Scan 返回错误：%v", err)
	}
	if got != want {
		t.Fatalf("Scan(Value(m)) = %v, 期望 %v", got, want)
	}
}
