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
