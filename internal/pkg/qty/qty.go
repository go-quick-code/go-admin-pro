// Package qty 提供 ERP 数量值类型 Qty，以毫单位（10⁻³）为单位的 int64 定点值（ADR 0017）。
//
// 最小用法：
//
//	q, err := qty.ParseQty("0.5") // 500 毫
//	total, err := q.Add(other)
//	fmt.Println(q.String())       // "0.500"
//
// Qty 贯通 Excel（encoding.TextMarshaler/TextUnmarshaler）、JSON（十进制字符串）
// 与 GORM（decimal(24,3) 列），数量换算全程不经过 float64；
// ERP 全部单据明细数量与 3 位小数展示字段（如重量）都用它承载。
//
// 范围：decimal(24,3) 的完整列域超出 int64，Go 侧的实际约束是 int64 毫单位全域
// （最大 ±9223372036854775.807）；解析与运算入口（ParseQty、Scan、
// UnmarshalText/UnmarshalJSON、Add/Sub）都保证结果落在该域内。
package qty

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// milliPerUnit 是单位与毫单位的换算系数（10⁻³ 定点）。
const milliPerUnit int64 = 1000

// 预定义的错误类别，调用方可用 errors.Is 判断。
var (
	// ErrInvalidFormat 表示数量不是合法的十进制字符串，或小数超过三位。
	ErrInvalidFormat = errors.New("qty: 数量格式不合法")
	// ErrOutOfRange 表示数量超出 int64 毫单位范围。
	ErrOutOfRange = errors.New("qty: 数量超出范围")
)

// Qty 是以毫单位（10⁻³）为单位的数量值类型。零值表示 0.000。
type Qty struct {
	milli int64
}

// FromMilli 用已知来源的毫值构造 Qty，不做范围校验。
// 范围校验属于解析与运算入口；int64 毫值本身即全域，传入任意 int64 都是合法值。
func FromMilli(milli int64) Qty {
	return Qty{milli: milli}
}

// Milli 返回以毫单位为单位的数值。
func (q Qty) Milli() int64 {
	return q.milli
}

// ParseQty 解析十进制数量字符串，如 "0.5"、"123"、"-1.234"。
// 小数最多三位（不四舍五入），结果须落在 int64 毫单位范围内；
// 空串、非数字与越界输入返回错误，可用 errors.Is 匹配
// ErrInvalidFormat / ErrOutOfRange。
func ParseQty(text string) (Qty, error) {
	body := text
	negative := false
	if strings.HasPrefix(body, "-") {
		negative = true
		body = body[1:]
	}
	intPart, fracPart, hasPoint := strings.Cut(body, ".")
	if intPart == "" || (hasPoint && fracPart == "") {
		// "12."、".5"、""、"-"：整数或小数部分缺失。
		return Qty{}, invalid(text)
	}
	units, err := parseDigits(intPart)
	if err != nil {
		return Qty{}, invalid(text)
	}
	var frac uint64
	if hasPoint {
		if len(fracPart) > 3 {
			return Qty{}, fmt.Errorf("%w: %q 的小数超过三位（最多三位，如 0.001）", ErrInvalidFormat, text)
		}
		if frac, err = parseFraction(fracPart); err != nil {
			return Qty{}, invalid(text)
		}
	}
	// units × 1000 必然溢出的先拒绝，避免 uint64 回绕。
	if units > math.MaxUint64/uint64(milliPerUnit) {
		return Qty{}, outOfRange(text)
	}
	magnitude := units*uint64(milliPerUnit) + frac
	if negative {
		// int64 负域只多容纳 |MinInt64|（...808），比正域大 1。
		if magnitude > 1<<63 {
			return Qty{}, outOfRange(text)
		}
		return Qty{milli: -int64(magnitude-1) - 1}, nil // magnitude == 1<<63 时安全落到 MinInt64
	}
	if magnitude > math.MaxInt64 {
		return Qty{}, outOfRange(text)
	}
	return Qty{milli: int64(magnitude)}, nil
}

func invalid(text string) error {
	return fmt.Errorf("%w: %q 不是合法的数量（示例：0.5，最多三位小数）", ErrInvalidFormat, text)
}

func outOfRange(text string) error {
	return fmt.Errorf("%w: %q 超出数量范围（最大 9223372036854775.807）", ErrOutOfRange, text)
}

// parseDigits 解析纯数字的整数部分，拒绝空串与任何非数字字符。
func parseDigits(s string) (uint64, error) {
	if !allDigits(s) {
		return 0, errors.New("整数部分不是纯数字")
	}
	return strconv.ParseUint(s, 10, 64)
}

// parseFraction 解析 1–3 位小数部分，右侧补齐到毫位。
func parseFraction(s string) (uint64, error) {
	if !allDigits(s) {
		return 0, errors.New("小数部分不是纯数字")
	}
	value, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	switch len(s) {
	case 1:
		return value * 100, nil
	case 2:
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

// String 按 decimal(24,3) 语义输出固定三位小数，如 "0.500"、"-1.234"。
func (q Qty) String() string {
	negative := q.milli < 0
	absolute := uint64(q.milli)
	if negative {
		absolute = -absolute // uint64 取负，MinInt64 也安全
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%03d", sign, absolute/uint64(milliPerUnit), absolute%uint64(milliPerUnit))
}

// Add 返回两数量之和，结果超出 int64 毫单位范围时返回 ErrOutOfRange。
func (q Qty) Add(other Qty) (Qty, error) {
	sum := q.milli + other.milli
	// 同号相加才可能溢出：int64 环绕后符号翻转（混合符号不会溢出）。
	if (q.milli > 0 && other.milli > 0 && sum < 0) || (q.milli < 0 && other.milli < 0 && sum >= 0) {
		return Qty{}, fmt.Errorf("%w: 加法结果超出 int64 范围", ErrOutOfRange)
	}
	return Qty{milli: sum}, nil
}

// Sub 返回两数量之差（q - other），结果超出 int64 毫单位范围时返回 ErrOutOfRange。
func (q Qty) Sub(other Qty) (Qty, error) {
	diff := q.milli - other.milli
	// 异号相减才可能溢出：环绕后符号与被减数相反。
	if (q.milli >= 0 && other.milli < 0 && diff < 0) || (q.milli < 0 && other.milli >= 0 && diff > 0) {
		return Qty{}, fmt.Errorf("%w: 减法结果超出 int64 范围", ErrOutOfRange)
	}
	return Qty{milli: diff}, nil
}
