package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// Product 是 ERP 产品（spec §3.1）：条码/规格/三个 Money 参考价/保质期/重量
// 展示字段。参考价无逻辑（采购/销售单据价格在明细里另填）；保质期与重量是
// 纯展示字段，可为空（NULL），重量用 qty 毫单位定点承载（ADR 0017）。
// owner = 创建人（spec §4.10）。
type Product struct {
	model.BaseEntity
	TenantID   int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name       string `gorm:"size:128;not null" json:"name"`
	BarCode    string `gorm:"size:64;index" json:"barCode"`
	CategoryId int64  `gorm:"index;not null" json:"categoryId"`
	UnitId     int64  `gorm:"index;not null" json:"unitId"`
	// status 不用 gorm default 标签：0（启用）是合法值，与分类/单位同口径。
	Status   int8   `gorm:"not null" json:"status"`
	Standard string `gorm:"size:128" json:"standard"`
	// 保质期天数与重量为可空展示字段：指针类型保留 NULL 语义（未填写 ≠ 0）。
	ExpiryDay     *int32      `gorm:"column:expiry_day" json:"expiryDay"`
	Weight        *qty.Qty    `json:"weight"`
	PurchasePrice money.Money `gorm:"type:decimal(12,2);not null;default:'0.00'" json:"purchasePrice"`
	SalePrice     money.Money `gorm:"type:decimal(12,2);not null;default:'0.00'" json:"salePrice"`
	MinPrice      money.Money `gorm:"type:decimal(12,2);not null;default:'0.00'" json:"minPrice"`
	Remark        string      `gorm:"size:500" json:"remark"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

func (Product) TableName() string { return "erp_product" }
