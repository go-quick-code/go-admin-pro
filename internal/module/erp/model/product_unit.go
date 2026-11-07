package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// ProductUnit 是 ERP 产品单位（仅名称字典，无换算率，spec §3.1；
// 多单位换算明确不做，见 spec §1）。owner = 创建人（spec §4.10）。
type ProductUnit struct {
	model.BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	// status 不用 gorm default 标签：0（启用）是合法值，与分类同口径。
	Status      int8  `gorm:"not null" json:"status"`
	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

func (ProductUnit) TableName() string { return "erp_product_unit" }
