package qty

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
)

// MarshalJSON 输出十进制字符串，如 "0.500"。
// 全程不经过 float64：直接写 String() 的字符（数字、小数点、负号）。
func (q Qty) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(`"-9223372036854775.808"`))
	b = append(b, '"')
	b = append(b, q.String()...)
	b = append(b, '"')
	return b, nil
}

// UnmarshalJSON 只接受 JSON 字符串字面量（保守做法）：JSON 数字在 JavaScript
// 端会退化成 double，写入端以字符串承载十进制才能保住精度，因此数字字面量
// 一律拒绝，避免隐式的 float 往返。null 归零。
func (q *Qty) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*q = Qty{}
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("%w: 数量必须是十进制字符串（如 \"0.5\"），不接受数字或其他类型", ErrInvalidFormat)
	}
	parsed, err := ParseQty(text)
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}

// MarshalText 输出固定三位小数的十进制字符串，配合 internal/pkg/excel
// 的导出开箱即用。
func (q Qty) MarshalText() ([]byte, error) {
	return []byte(q.String()), nil
}

// UnmarshalText 解析十进制数量文本，拒绝空串、超三位小数与越界值。
func (q *Qty) UnmarshalText(text []byte) error {
	parsed, err := ParseQty(string(text))
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}

// Value 实现 driver.Valuer：写入 decimal(24,3) 列时输出十进制字符串。
func (q Qty) Value() (driver.Value, error) {
	return q.String(), nil
}

// Scan 实现 sql.Scanner：读取 decimal(24,3) 列的驱动返回值。
//
// 常规路径只处理 []byte / string（MySQL 驱动默认把 DECIMAL 读成字节串），
// 不经过 float64。float64 分支是少数驱动/配置（如把列建成了 float、
// 或 DSN 关闭 decimal-as-bytes）的兼容入口：立即转成精确的十进制字符串
// 再走常规解析，float64 不参与任何数量运算；有损值（如 1.2345 的
// 最近似浮点）会被三位小数校验拒绝。
func (q *Qty) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*q = Qty{}
		return nil
	case []byte:
		return q.scanText(string(value), "字节串")
	case string:
		return q.scanText(value, "字符串")
	case float64:
		// 驱动兼容入口，见函数注释。
		return q.scanText(strconv.FormatFloat(value, 'f', -1, 64), "浮点数")
	default:
		return fmt.Errorf("%w: 不支持的列类型 %T（期望字符串或字节串）", ErrInvalidFormat, src)
	}
}

func (q *Qty) scanText(text, kind string) error {
	parsed, err := ParseQty(text)
	if err != nil {
		return fmt.Errorf("数量 %q（%s）无法读取：%w", text, kind, err)
	}
	*q = parsed
	return nil
}
