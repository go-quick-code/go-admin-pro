package money_test

import (
	"errors"
	"testing"

	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

func TestParseMoney(t *testing.T) {
	tests := []struct {
		name string
		text string
		want money.Money
		str  string // 解析后固定两位小数的格式化结果
	}{
		{name: "整数分", text: "0.01", want: money.FromCents(1), str: "0.01"},
		{name: "常规两位小数", text: "1234.56", want: money.FromCents(123456), str: "1234.56"},
		{name: "负数", text: "-1234.56", want: money.FromCents(-123456), str: "-1234.56"},
		{name: "一位小数补齐", text: "1234.5", want: money.FromCents(123450), str: "1234.50"},
		{name: "纯整数", text: "1234", want: money.FromCents(123400), str: "1234.00"},
		{name: "零", text: "0", want: money.FromCents(0), str: "0.00"},
		{name: "零点零零", text: "0.00", want: money.FromCents(0), str: "0.00"},
		{name: "负的负五分", text: "-0.05", want: money.FromCents(-5), str: "-0.05"},
		{name: "最大值", text: "9999999999.99", want: money.FromCents(999999999999), str: "9999999999.99"},
		{name: "最小值", text: "-9999999999.99", want: money.FromCents(-999999999999), str: "-9999999999.99"},
		{name: "前导零", text: "0001234.56", want: money.FromCents(123456), str: "1234.56"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.ParseMoney(tt.text)
			if err != nil {
				t.Fatalf("ParseMoney(%q) 返回错误：%v", tt.text, err)
			}
			if got != tt.want {
				t.Fatalf("ParseMoney(%q) = %v, 期望 %v", tt.text, got, tt.want)
			}
			if got.String() != tt.str {
				t.Fatalf("ParseMoney(%q).String() = %q, 期望 %q", tt.text, got.String(), tt.str)
			}
		})
	}
}

func TestParseMoneyRejects(t *testing.T) {
	tests := []struct {
		name string
		text string
		err  error
	}{
		{name: "空串", text: "", err: money.ErrInvalidFormat},
		{name: "三位小数", text: "1234.567", err: money.ErrInvalidFormat},
		{name: "三位小数需四舍五入", text: "0.005", err: money.ErrInvalidFormat},
		{name: "四位小数", text: "1234.5600", err: money.ErrInvalidFormat},
		{name: "非数字", text: "abc", err: money.ErrInvalidFormat},
		{name: "空白", text: " ", err: money.ErrInvalidFormat},
		{name: "千分位", text: "1,234.56", err: money.ErrInvalidFormat},
		{name: "科学计数法", text: "1e5", err: money.ErrInvalidFormat},
		{name: "正号", text: "+1234.56", err: money.ErrInvalidFormat},
		{name: "两个点", text: "1.2.3", err: money.ErrInvalidFormat},
		{name: "缺整数部分", text: ".56", err: money.ErrInvalidFormat},
		{name: "缺小数部分", text: "1234.", err: money.ErrInvalidFormat},
		{name: "只有负号", text: "-", err: money.ErrInvalidFormat},
		{name: "超过整数上限", text: "10000000000", err: money.ErrOutOfRange},
		{name: "最大值加一分", text: "10000000000.00", err: money.ErrOutOfRange},
		{name: "负向越界", text: "-10000000000.00", err: money.ErrOutOfRange},
		{name: "整数溢出 int64", text: "99999999999999999999", err: money.ErrInvalidFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.ParseMoney(tt.text)
			if err == nil {
				t.Fatalf("ParseMoney(%q) 应被拒绝，得到 %v", tt.text, got)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("ParseMoney(%q) 错误 = %v, 期望可匹配 %v", tt.text, err, tt.err)
			}
			if got != (money.Money{}) {
				t.Fatalf("ParseMoney(%q) 失败时应返回零值，得到 %v", tt.text, got)
			}
		})
	}
}

func TestMoneyString(t *testing.T) {
	tests := []struct {
		name  string
		cents int64
		want  string
	}{
		{name: "正数两位小数", cents: 123456, want: "1234.56"},
		{name: "负数两位小数", cents: -123456, want: "-1234.56"},
		{name: "一分", cents: 1, want: "0.01"},
		{name: "负一分", cents: -1, want: "-0.01"},
		{name: "十分", cents: 10, want: "0.10"},
		{name: "零", cents: 0, want: "0.00"},
		{name: "最大值", cents: 999999999999, want: "9999999999.99"},
		{name: "最小值", cents: -999999999999, want: "-9999999999.99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := money.FromCents(tt.cents).String(); got != tt.want {
				t.Fatalf("FromCents(%d).String() = %q, 期望 %q", tt.cents, got, tt.want)
			}
		})
	}
}

func TestMoneyCents(t *testing.T) {
	m, err := money.ParseMoney("-1234.56")
	if err != nil {
		t.Fatalf("ParseMoney 返回错误：%v", err)
	}
	if got := m.Cents(); got != -123456 {
		t.Fatalf("Cents() = %d, 期望 -123456", got)
	}
}

func TestMoneyAddSub(t *testing.T) {
	t.Run("0.01 累加一百次不丢分", func(t *testing.T) {
		total, err := money.ParseMoney("0")
		if err != nil {
			t.Fatalf("ParseMoney 返回错误：%v", err)
		}
		oneCent, err := money.ParseMoney("0.01")
		if err != nil {
			t.Fatalf("ParseMoney 返回错误：%v", err)
		}
		for i := 0; i < 100; i++ {
			total, err = total.Add(oneCent)
			if err != nil {
				t.Fatalf("第 %d 次累加失败：%v", i+1, err)
			}
		}
		if total.String() != "1.00" {
			t.Fatalf("累加结果 = %q, 期望 1.00", total.String())
		}
		if total.Cents() != 100 {
			t.Fatalf("累加分值 = %d, 期望 100", total.Cents())
		}
	})

	t.Run("减法", func(t *testing.T) {
		a, _ := money.ParseMoney("1234.56")
		b, _ := money.ParseMoney("1234.55")
		got, err := a.Sub(b)
		if err != nil {
			t.Fatalf("Sub 返回错误：%v", err)
		}
		if got.String() != "0.01" {
			t.Fatalf("Sub 结果 = %q, 期望 0.01", got.String())
		}
	})

	t.Run("加法组合", func(t *testing.T) {
		a, _ := money.ParseMoney("1234.56")
		b, _ := money.ParseMoney("-1234.56")
		got, err := a.Add(b)
		if err != nil {
			t.Fatalf("Add 返回错误：%v", err)
		}
		if got.String() != "0.00" {
			t.Fatalf("Add 结果 = %q, 期望 0.00", got.String())
		}
	})

	t.Run("正向越界拒绝", func(t *testing.T) {
		max := money.FromCents(999999999999)
		oneCent := money.FromCents(1)
		got, err := max.Add(oneCent)
		if !errors.Is(err, money.ErrOutOfRange) {
			t.Fatalf("最大值加一分应返回 ErrOutOfRange, 得到 %v", err)
		}
		if got != (money.Money{}) {
			t.Fatalf("失败时应返回零值, 得到 %v", got)
		}
	})

	t.Run("负向越界拒绝", func(t *testing.T) {
		min := money.FromCents(-999999999999)
		oneCent := money.FromCents(1)
		got, err := min.Sub(oneCent)
		if !errors.Is(err, money.ErrOutOfRange) {
			t.Fatalf("最小值减一分应返回 ErrOutOfRange, 得到 %v", err)
		}
		if got != (money.Money{}) {
			t.Fatalf("失败时应返回零值, 得到 %v", got)
		}
	})
}

// TestMoneyMul 金额乘数量（issue #15 行项目金额 = 数量 × 单价；#16 合同行项目同规则复用）：
// 精确十进制运算、符号语义正确、int64 与 decimal(12,2) 双重越界拒绝。
func TestMoneyMul(t *testing.T) {
	parse := func(t *testing.T, s string) money.Money {
		t.Helper()
		m, err := money.ParseMoney(s)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", s, err)
		}
		return m
	}

	t.Run("常规乘法", func(t *testing.T) {
		cases := []struct {
			price string
			qty   int64
			want  string
		}{
			{"1234.56", 3, "3703.68"},
			{"0.01", 1, "0.01"},
			{"99.99", 100, "9999.00"},
			{"0.00", 5, "0.00"},
			{"12.34", 0, "0.00"},
		}
		for _, tc := range cases {
			got, err := parse(t, tc.price).Mul(tc.qty)
			if err != nil {
				t.Fatalf("Mul(%s, %d) 返回错误：%v", tc.price, tc.qty, err)
			}
			if got.String() != tc.want {
				t.Fatalf("Mul(%s, %d) = %q, 期望 %q", tc.price, tc.qty, got.String(), tc.want)
			}
		}
	})

	t.Run("负数量语义正确（校验正整数是 service 层职责）", func(t *testing.T) {
		got, err := parse(t, "12.50").Mul(-2)
		if err != nil {
			t.Fatalf("Mul 返回错误：%v", err)
		}
		if got.String() != "-25.00" {
			t.Fatalf("Mul(12.50, -2) = %q, 期望 -25.00", got.String())
		}
	})

	t.Run("decimal 越界拒绝", func(t *testing.T) {
		got, err := parse(t, "9999999999.99").Mul(100000)
		if !errors.Is(err, money.ErrOutOfRange) {
			t.Fatalf("乘积超 decimal(12,2) 应返回 ErrOutOfRange, 得到 %v", err)
		}
		if got != (money.Money{}) {
			t.Fatalf("失败时应返回零值, 得到 %v", got)
		}
	})

	t.Run("int64 溢出拒绝", func(t *testing.T) {
		// 999999999999 分 × 10^12 分已超 int64；数学上必溢出，不得环绕。
		got, err := money.FromCents(999999999999).Mul(1000000000000)
		if !errors.Is(err, money.ErrOutOfRange) {
			t.Fatalf("int64 溢出应返回 ErrOutOfRange, 得到 %v", err)
		}
		if got != (money.Money{}) {
			t.Fatalf("失败时应返回零值, 得到 %v", got)
		}
	})
}
