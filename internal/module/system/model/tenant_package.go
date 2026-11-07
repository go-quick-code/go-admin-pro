package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Int64List 是存为 JSON 文本的 int64 数组列（如套餐的 menu_ids）。
// 扫描时接受 []byte/string/nil；写入时序列化为 JSON 字符串。
type Int64List []int64

func (l *Int64List) Scan(value interface{}) error {
	if value == nil {
		*l = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("Int64List: 不支持扫描类型 %T", value)
	}
	if len(raw) == 0 {
		*l = nil
		return nil
	}
	var out []int64
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("Int64List: 解析 JSON 失败: %w", err)
	}
	*l = out
	return nil
}

func (l Int64List) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	raw, err := json.Marshal([]int64(l))
	if err != nil {
		return nil, fmt.Errorf("Int64List: 序列化失败: %w", err)
	}
	return string(raw), nil
}

// TenantPackage 是租户套餐（平台级资源，不参与租户过滤）。
// MenuIDs 只保存目录/菜单 ID，按钮在绑定租户时按父菜单展开。
type TenantPackage struct {
	BaseEntity
	Name           string    `gorm:"size:100;not null" json:"name"`
	Status         int8      `gorm:"not null;default:0" json:"status"`
	MenuIDs        Int64List `gorm:"column:menu_ids;type:text;not null" json:"menuIds"`
	MaxUsers       int64     `gorm:"not null;default:0" json:"maxUsers"`
	MaxStorageMB   int64     `gorm:"not null;default:0" json:"maxStorageMb"`
	MaxSMSMonthly  int64     `gorm:"not null;default:0" json:"maxSmsMonthly"`
	MaxMailMonthly int64     `gorm:"not null;default:0" json:"maxMailMonthly"`
	Remark         string    `gorm:"size:500" json:"remark"`
}

func (TenantPackage) TableName() string { return "system_tenant_package" }
