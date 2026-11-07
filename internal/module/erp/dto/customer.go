package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// CustomerCreateReq 新建 ERP 客户（issue #89，spec §3.1）：税务与开户字段可空
// （未提交保持 NULL）。税率以十进制字符串提交（如 "13.00"），换算不经过
// float64（internal/pkg/money）；服务层校验 0–999.99（decimal(5,2) 列域）。
type CustomerCreateReq struct {
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

// CustomerUpdateReq 修改 ERP 客户：字段全量覆盖（可空字段未提交即清空，
// 与表单整体提交契约一致）。
type CustomerUpdateReq struct {
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

// CustomerPageReq 客户分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
type CustomerPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Mobile   string `form:"mobile"`
	Status   *int8  `form:"status"`
}
