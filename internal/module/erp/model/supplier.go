package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// Supplier 是 ERP 供应商（issue #89，spec §3.1）：字段同 erp_customer（往来
// 联系信息 + 税号/税率/开户信息全展示字段）。与 CRM 产品/供应商完全独立
// （ADR 0015）。税率 decimal(5,2) 纯展示不做税额计算（spec §1），用
// *money.Money 承载（两位小数定点、JSON 十进制字符串、NULL=未填写）。
// owner = 创建人（spec §4.10）。
type Supplier struct {
	model.BaseEntity
	TenantID  int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name      string `gorm:"size:128;not null" json:"name"`
	Contact   string `gorm:"size:64" json:"contact"`
	Mobile    string `gorm:"size:32" json:"mobile"`
	Telephone string `gorm:"size:32" json:"telephone"`
	Email     string `gorm:"size:128" json:"email"`
	Fax       string `gorm:"size:64" json:"fax"`
	Remark    string `gorm:"size:500" json:"remark"`
	// status 沿用主数据统一口径 0 启用 / 1 停用（迁移 00029 注释）。
	Status int8 `gorm:"not null" json:"status"`
	Sort   int  `gorm:"not null" json:"sort"`

	TaxNo       *string      `gorm:"column:tax_no;size:64" json:"taxNo"`
	TaxPercent  *money.Money `gorm:"column:tax_percent;type:decimal(5,2)" json:"taxPercent"`
	BankName    *string      `gorm:"column:bank_name;size:128" json:"bankName"`
	BankAccount *string      `gorm:"column:bank_account;size:64" json:"bankAccount"`
	BankAddress *string      `gorm:"column:bank_address;size:255" json:"bankAddress"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

func (Supplier) TableName() string { return "erp_supplier" }
