package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 产品状态枚举（ProductStatus，生成自字段元信息：状态）。
const (
	ProductStatusOnSale   int8 = 1
	ProductStatusOffShelf int8 = 2
)

// ValidProductStatus 报告产品状态枚举是否合法。
func ValidProductStatus(v int8) bool {
	return v >= ProductStatusOnSale && v <= ProductStatusOffShelf
}

// Product 是产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）。
// owner-scoped：owner_dept_id 是负责人部门的快照，随 owner 写入同步（ADR 0004）。
type Product struct {
	model.BaseEntity
	TenantID int64       `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string      `gorm:"size:128;not null" json:"name"`
	Category string      `gorm:"size:64" json:"category"`
	Unit     string      `gorm:"size:32" json:"unit"`
	Price    money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	Status   int8        `gorm:"not null;default:1" json:"status"`
	Remark   string      `gorm:"size:500" json:"remark"`
	// OwnerUserID 为空（0）表示无负责人；快照列同理，无负责人时不落部门。
	OwnerUserID int64 `gorm:"index" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index" json:"ownerDeptId"`

	// 非持久化字段：负责人昵称与部门名称，由 service 层批量补齐供展示。
	OwnerUserName string `gorm:"-" json:"ownerUserName,omitempty"`
	OwnerDeptName string `gorm:"-" json:"ownerDeptName,omitempty"`
}

func (Product) TableName() string { return "crm_product" }
