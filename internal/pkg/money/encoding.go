package money

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
)

// MarshalJSON 输出十进制字符串，如 "1234.56"。
// 全程不经过 float64：直接写 String() 的字符（数字、小数点、负号）。
func (m Money) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(`"-1234567890.99"`))
	b = append(b, '"')
	b = append(b, m.String()...)
	b = append(b, '"')
	return b, nil
}

// UnmarshalJSON 只接受 JSON 字符串字面量（保守做法）：JSON 数字在 JavaScript
// 端会退化成 double，写入端以字符串承载十进制才能保住精度，因此数字字面量
// 一律拒绝，避免隐式的 float 往返。null 归零。
func (m *Money) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = Money{}
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("%w: 金额必须是十进制字符串（如 \"1234.56\"），不接受数字或其他类型", ErrInvalidFormat)
	}
	parsed, err := ParseMoney(text)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// MarshalText 输出固定两位小数的十进制字符串，配合 internal/pkg/excel
// 的导入导出与模板开箱即用。
func (m Money) MarshalText() ([]byte, error) {
	return []byte(m.String()), nil
}

// UnmarshalText 解析十进制金额文本，拒绝空串、超两位小数与越界值。
func (m *Money) UnmarshalText(text []byte) error {
	parsed, err := ParseMoney(string(text))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// Value 实现 driver.Valuer：写入 decimal(12,2) 列时输出十进制字符串。
func (m Money) Value() (driver.Value, error) {
	return m.String(), nil
}

// Scan 实现 sql.Scanner：读取 decimal(12,2) 列的驱动返回值。
//
// 常规路径只处理 []byte / string（MySQL 驱动默认把 DECIMAL 读成字节串），
// 不经过 float64。float64 分支是少数驱动/配置（如把列建成了 float、
// 或 DSN 关闭 decimal-as-bytes）的兼容入口：立即转成精确的十进制字符串
// 再走常规解析，float64 不参与任何金额运算；有损值（如 1234.567 的
// 最近似浮点）会被两位小数校验拒绝。
func (m *Money) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*m = Money{}
		return nil
	case []byte:
		return m.scanText(string(value), "字节串")
	case string:
		return m.scanText(value, "字符串")
	case float64:
		// 驱动兼容入口，见函数注释。
		return m.scanText(strconv.FormatFloat(value, 'f', -1, 64), "浮点数")
	default:
		return fmt.Errorf("%w: 不支持的列类型 %T（期望字符串或字节串）", ErrInvalidFormat, src)
	}
}

func (m *Money) scanText(text, kind string) error {
	parsed, err := ParseMoney(text)
	if err != nil {
		return fmt.Errorf("金额 %q（%s）无法读取：%w", text, kind, err)
	}
	*m = parsed
	return nil
}
