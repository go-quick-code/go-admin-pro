// Package money 提供 CRM 金额值类型 Money，以 int64 分为单位。
//
// 最小用法：
//
//	m, err := money.ParseMoney("1234.56") // 123456 分
//	total, err := m.Add(other)
//	fmt.Println(m.String())               // "1234.56"
//
// Money 贯通 Excel（encoding.TextMarshaler/TextUnmarshaler）、JSON（十进制字符串）
// 与 GORM（decimal(12,2) 列），金额换算全程不经过 float64。
// 解析与运算都做 decimal(12,2) 范围校验：最大 9,999,999,999.99（含负对称）。
package money

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// decimal(12,2) 的分值上下限：9,999,999,999.99 = 999999999999 分。
const (
	maxCents int64 = 999_999_999_999
	minCents int64 = -999_999_999_999
)

// 预定义的错误类别，调用方可用 errors.Is 判断。
var (
	// ErrInvalidFormat 表示金额不是合法的十进制字符串，或小数超过两位。
	ErrInvalidFormat = errors.New("money: 金额格式不合法")
	// ErrOutOfRange 表示金额超出 decimal(12,2) 范围。
	ErrOutOfRange = errors.New("money: 金额超出 decimal(12,2) 范围（最大 9999999999.99）")
)

// Money 是以分为单位的金额值类型。零值表示 0.00。
//
// 解析与运算入口（ParseMoney、Scan、UnmarshalText/UnmarshalJSON、Add/Sub）
// 都保证结果落在 decimal(12,2) 范围内；字段不导出，外部无法构造非法值，
// FromCents 仅供已知来源的分值使用。
type Money struct {
	cents int64
}

// FromCents 用已知来源的分值构造 Money，不做范围校验。
// 范围校验属于解析与运算入口；传入超出 decimal(12,2) 的分值时，
// String/Value 等输出仍正确，但后续运算可能返回 ErrOutOfRange。
func FromCents(cents int64) Money {
	return Money{cents: cents}
}

// Cents 返回以分为单位的数值。
func (m Money) Cents() int64 {
	return m.cents
}

// ParseMoney 解析十进制金额字符串，如 "1234.56"、"-1234.56"、"0.01"。
// 小数最多两位（不四舍五入），结果须在 decimal(12,2) 范围内；
// 空串、非数字与越界输入返回错误，可用 errors.Is 匹配
// ErrInvalidFormat / ErrOutOfRange。
func ParseMoney(text string) (Money, error) {
	body := text
	negative := false
	if strings.HasPrefix(body, "-") {
		negative = true
		body = body[1:]
	}
	intPart, fracPart, hasPoint := strings.Cut(body, ".")
	if intPart == "" || (hasPoint && fracPart == "") {
		// "1234."、".56"、""、"-"：整数或小数部分缺失。
		return Money{}, invalid(text)
	}
	units, err := parseDigits(intPart)
	if err != nil {
		return Money{}, invalid(text)
	}
	// 整数部分超过 10 位时乘 100 必然越界，先拒绝，避免 int64 溢出。
	if units > maxCents/100 {
		return Money{}, fmt.Errorf("%w: %q 超出 decimal(12,2) 范围（最大 9999999999.99）", ErrOutOfRange, text)
	}
	var frac int64
	if hasPoint {
		if frac, err = parseFraction(fracPart); err != nil {
			return Money{}, invalid(text)
		}
		if len(fracPart) > 2 {
			return Money{}, fmt.Errorf("%w: %q 的小数超过两位（最多两位，如 1234.56）", ErrInvalidFormat, text)
		}
	}
	cents := units*100 + frac
	if negative {
		cents = -cents
	}
	return checked(cents)
}

func invalid(text string) error {
	return fmt.Errorf("%w: %q 不是合法的金额（示例：1234.56，最多两位小数）", ErrInvalidFormat, text)
}

// parseDigits 解析纯数字的整数部分，拒绝空串、符号与任何非数字字符。
func parseDigits(s string) (int64, error) {
	if !allDigits(s) {
		return 0, errors.New("整数部分不是纯数字")
	}
	return strconv.ParseInt(s, 10, 64)
}

// parseFraction 解析 1–2 位小数部分，右侧补齐到百分位。
func parseFraction(s string) (int64, error) {
	if !allDigits(s) {
		return 0, errors.New("小数部分不是纯数字")
	}
	value, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if len(s) == 1 {
		return value * 10, nil
	}
	return value, nil
}

// allDigits 判断非空字符串是否只含数字字符。
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String 按 decimal(12,2) 语义输出固定两位小数，如 "1234.56"、"-0.05"。
func (m Money) String() string {
	negative := m.cents < 0
	absolute := uint64(m.cents)
	if negative {
		absolute = -absolute // uint64 取负，极端值也安全
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%02d", sign, absolute/100, absolute%100)
}

// Add 返回两金额之和，结果超出 decimal(12,2) 范围时返回 ErrOutOfRange。
func (m Money) Add(other Money) (Money, error) {
	sum := m.cents + other.cents
	// 同号相加才可能溢出：int64 环绕后符号翻转（混合符号不会溢出）。
	if (m.cents > 0 && other.cents > 0 && sum < 0) || (m.cents < 0 && other.cents < 0 && sum >= 0) {
		return Money{}, fmt.Errorf("%w: 加法结果超出 int64 范围", ErrOutOfRange)
	}
	return checked(sum)
}

// Sub 返回两金额之差（m - other），结果超出 decimal(12,2) 范围时返回 ErrOutOfRange。
func (m Money) Sub(other Money) (Money, error) {
	diff := m.cents - other.cents
	// 异号相减才可能溢出：环绕后符号与被减数相反。
	if (m.cents >= 0 && other.cents < 0 && diff < 0) || (m.cents < 0 && other.cents >= 0 && diff > 0) {
		return Money{}, fmt.Errorf("%w: 减法结果超出 int64 范围", ErrOutOfRange)
	}
	return checked(diff)
}

// Mul 返回 m × qty（行项目金额 = 数量 × 单价，issue #15/#16 同规则）。
// 数量的正负语义由调用方决定（行项目的「数量正整数」校验在 service 层），
// 乘法用无符号 128 位中间结果防 int64 溢出，结果须落在 decimal(12,2) 范围内。
func (m Money) Mul(qty int64) (Money, error) {
	hi, lo := bits.Mul64(uabs(m.cents), uabs(qty))
	// 无符号乘积超出 MaxInt64（含 MinInt64 的量级）必然越界：
	// decimal(12,2) 上限远小于 int64 极值，这里无需为 MinInt64 保留特例。
	if hi != 0 || lo > math.MaxInt64 {
		return Money{}, fmt.Errorf("%w: 乘法结果超出范围", ErrOutOfRange)
	}
	product := int64(lo)
	if (m.cents < 0) != (qty < 0) {
		product = -product
	}
	return checked(product)
}

// uabs 返回 int64 的绝对值位型；MinInt64 的相反数会溢出，按无符号取幅值。
func uabs(x int64) uint64 {
	if x < 0 {
		return uint64(^x) + 1
	}
	return uint64(x)
}

// checked 校验分值落在 decimal(12,2) 范围内。
func checked(cents int64) (Money, error) {
	if cents > maxCents || cents < minCents {
		return Money{}, fmt.Errorf("%w: 结果 %d 分超出 decimal(12,2) 范围", ErrOutOfRange, cents)
	}
	return Money{cents: cents}, nil
}
