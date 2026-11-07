package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- Excel 导出列（issue #88，docs/erp-mvp-spec.md §4.11）----------
// ERP 只做导出、不做导入（spec §1 明确不做；差异记清单）。excel 标签即导出表头
// 契约；金额 money.Money 与数量/重量经 TextMarshaler 输出十进制字符串（开箱即用），
// 日期按业务时区导出（NativeDates，ADR 0006）。

// ProductExportRow 是产品导出列：分类/单位名称由服务端解析，重量输出 qty 定点字符串。
type ProductExportRow struct {
	Name          string      `excel:"产品名称,width=26"`
	BarCode       string      `excel:"商品条码,width=20"`
	Category      string      `excel:"产品分类,width=18"`
	Unit          string      `excel:"单位,width=12"`
	Standard      string      `excel:"规格,width=18"`
	ExpiryDay     *int32      `excel:"保质期(天),width=12"`
	Weight        string      `excel:"重量,width=12"`
	PurchasePrice money.Money `excel:"采购参考价,width=14"`
	SalePrice     money.Money `excel:"销售参考价,width=14"`
	MinPrice      money.Money `excel:"最低参考价,width=14"`
	Status        string      `excel:"状态,width=10"`
	Remark        string      `excel:"备注,width=36"`
	CreateTime    time.Time   `excel:"创建时间,width=20"`
}

// ProductCategoryExportRow 是分类导出列（全量列表口径，spec §5）。
type ProductCategoryExportRow struct {
	Name       string    `excel:"分类名称,width=24"`
	Code       string    `excel:"分类编码,width=20"`
	Sort       int       `excel:"排序,width=8"`
	Status     string    `excel:"状态,width=10"`
	CreateTime time.Time `excel:"创建时间,width=20"`
}

// ProductUnitExportRow 是单位导出列。
type ProductUnitExportRow struct {
	Name       string    `excel:"单位名称,width=20"`
	Status     string    `excel:"状态,width=10"`
	CreateTime time.Time `excel:"创建时间,width=20"`
}

// ---------- 仓库/客户/供应商导出列（issue #89，spec §4.11）----------

// WarehouseExportRow 是仓库导出列。
type WarehouseExportRow struct {
	Name       string    `excel:"仓库名称,width=24"`
	Address    string    `excel:"仓库地址,width=32"`
	Principal  string    `excel:"负责人,width=12"`
	Sort       int       `excel:"排序,width=8"`
	Status     string    `excel:"状态,width=10"`
	CreateTime time.Time `excel:"创建时间,width=20"`
}

// CustomerExportRow 是 ERP 客户导出列：税率经 TextMarshaler 输出十进制字符串。
type CustomerExportRow struct {
	Name        string       `excel:"客户名称,width=24"`
	Contact     string       `excel:"联系人,width=12"`
	Mobile      string       `excel:"手机号码,width=16"`
	Telephone   string       `excel:"联系电话,width=16"`
	Email       string       `excel:"电子邮箱,width=24"`
	Fax         string       `excel:"传真,width=16"`
	TaxNo       string       `excel:"税号,width=24"`
	TaxPercent  *money.Money `excel:"税率(%),width=10"`
	BankName    string       `excel:"开户行,width=24"`
	BankAccount string       `excel:"开户账号,width=24"`
	BankAddress string       `excel:"开户行地址,width=32"`
	Status      string       `excel:"状态,width=10"`
	Sort        int          `excel:"排序,width=8"`
	Remark      string       `excel:"备注,width=36"`
	CreateTime  time.Time    `excel:"创建时间,width=20"`
}

// SupplierExportRow 是供应商导出列（字段同 ERP 客户，spec §3.1）。
type SupplierExportRow struct {
	Name        string       `excel:"供应商名称,width=24"`
	Contact     string       `excel:"联系人,width=12"`
	Mobile      string       `excel:"手机号码,width=16"`
	Telephone   string       `excel:"联系电话,width=16"`
	Email       string       `excel:"电子邮箱,width=24"`
	Fax         string       `excel:"传真,width=16"`
	TaxNo       string       `excel:"税号,width=24"`
	TaxPercent  *money.Money `excel:"税率(%),width=10"`
	BankName    string       `excel:"开户行,width=24"`
	BankAccount string       `excel:"开户账号,width=24"`
	BankAddress string       `excel:"开户行地址,width=32"`
	Status      string       `excel:"状态,width=10"`
	Sort        int          `excel:"排序,width=8"`
	Remark      string       `excel:"备注,width=36"`
	CreateTime  time.Time    `excel:"创建时间,width=20"`
}
