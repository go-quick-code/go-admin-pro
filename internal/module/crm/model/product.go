package model

import (
	"github.com/lautung/go-admin-pro/internal/pkg/model"
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// 产品状态枚举（ProductStatus，生成自字段元信息：状态）。
const (
	ProductStatusOffShelf int8 = 0
	ProductStatusOnSale   int8 = 1
)

// ValidProductStatus 报告产品状态枚举是否合法。
func ValidProductStatus(v int8) bool {
	return v >= ProductStatusOffShelf && v <= ProductStatusOnSale
}

// Product 是产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）。
type Product struct {
	model.BaseEntity
	TenantID   int64       `gorm:"index;not null;default:1" json:"tenantId"`
	CategoryId int64       `gorm:"index" json:"categoryId"`
	Name       string      `gorm:"size:128;not null" json:"name"`
	Code       string      `gorm:"size:64;not null" json:"code"`
	Unit       string      `gorm:"size:32" json:"unit"`
	Price      money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	// status 不用 gorm default 标签：0（下架）是合法值，带 default 会让 GORM
	// 在插入时把零值替换成库默认值 1，新建/导入的「下架」产品被静默改成上架。
	Status      int8   `gorm:"not null" json:"status"`
	Description string `gorm:"size:500" json:"description"`
}

func (Product) TableName() string { return "crm_product" }
