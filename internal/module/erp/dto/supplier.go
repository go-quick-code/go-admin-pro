package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// SupplierCreateReq 新建供应商（issue #89，spec §3.1：字段同 ERP 客户）。
// 税率以十进制字符串提交（如 "13.00"），服务层校验 0–999.99（decimal(5,2) 列域）。
type SupplierCreateReq struct {
	Name        string       `json:"name" binding:"required,max=128"`
	Contact     string       `json:"contact" binding:"max=64"`
	Mobile      string       `json:"mobile" binding:"max=32"`
	Telephone   string       `json:"telephone" binding:"max=32"`
	Email       string       `json:"email" binding:"max=128"`
	Fax         string       `json:"fax" binding:"max=64"`
	Remark      string       `json:"remark" binding:"max=500"`
	Status      *int8        `json:"status"`
	Sort        *int         `json:"sort"`
	TaxNo       *string      `json:"taxNo" binding:"omitempty,max=64"`
	TaxPercent  *money.Money `json:"taxPercent"`
	BankName    *string      `json:"bankName" binding:"omitempty,max=128"`
	BankAccount *string      `json:"bankAccount" binding:"omitempty,max=64"`
	BankAddress *string      `json:"bankAddress" binding:"omitempty,max=255"`
}

// SupplierUpdateReq 修改供应商：字段全量覆盖（可空字段未提交即清空）。
type SupplierUpdateReq struct {
	ID          int64        `json:"id" binding:"required"`
	Name        string       `json:"name" binding:"required,max=128"`
	Contact     string       `json:"contact" binding:"max=64"`
	Mobile      string       `json:"mobile" binding:"max=32"`
	Telephone   string       `json:"telephone" binding:"max=32"`
	Email       string       `json:"email" binding:"max=128"`
	Fax         string       `json:"fax" binding:"max=64"`
	Remark      string       `json:"remark" binding:"max=500"`
	Status      *int8        `json:"status"`
	Sort        *int         `json:"sort"`
	TaxNo       *string      `json:"taxNo" binding:"omitempty,max=64"`
	TaxPercent  *money.Money `json:"taxPercent"`
	BankName    *string      `json:"bankName" binding:"omitempty,max=128"`
	BankAccount *string      `json:"bankAccount" binding:"omitempty,max=64"`
	BankAddress *string      `json:"bankAddress" binding:"omitempty,max=255"`
}

// SupplierPageReq 供应商分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
type SupplierPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Mobile   string `form:"mobile"`
	Status   *int8  `form:"status"`
}
