// Package model 定义 CRM 模块的持久化模型。
package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// 产品分类状态枚举（docs/crm-mvp-spec.md §3：0 启用 / 1 停用，芋道 CommonStatus 口径；
// 注意与产品状态的 1 上架 / 0 下架语义不同）。
const (
	CategoryStatusEnable  int8 = 0
	CategoryStatusDisable int8 = 1
)

// ValidProductCategoryStatus 报告分类状态枚举是否合法。
func ValidProductCategoryStatus(s int8) bool {
	return s == CategoryStatusEnable || s == CategoryStatusDisable
}

// ProductCategory 是产品分类（树形结构，parent_id 0 为根）。
// 分类是租户级共享资源，无 owner 列、无数据范围（同产品）。
type ProductCategory struct {
	model.BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	ParentID int64  `gorm:"not null;default:0" json:"parentId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Sort     int    `gorm:"not null;default:0" json:"sort"`
	Status   int8   `gorm:"not null;default:0" json:"status"`

	// 非持久化字段：子分类，由 service 层组装树时填充。
	Children []*ProductCategory `gorm:"-" json:"children,omitempty"`
}

func (ProductCategory) TableName() string { return "crm_product_category" }
