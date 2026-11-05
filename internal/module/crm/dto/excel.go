package dto

import (
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// ---------- Excel 导入行与导出列（issue #20，docs/crm-mvp-spec.md §7）----------
// excel 标签即模板/导入/导出的表头契约；Row 记录 Excel 实际行号（表头为第 1 行），
// 由解析回调回填，供「先全量校验、有错整批拒绝」时定位行级错误。
// 金额字段一律 money.Money（十进制字符串，internal/pkg/money），日期由 excel helper
// 按应用时区解析（ADR 0006），导出统一启用 NativeDates（#32）。

// CustomerImportRow 是客户导入行：列与 CustomerCreateReq 对齐，编号由服务端发放，
// 负责人缺省为当前登录用户。
type CustomerImportRow struct {
	Row      int    `excel:"-"`
	Name     string `excel:"客户名称,required,width=28"`
	Industry string `excel:"行业,width=20"`
	Level    string `excel:"级别,width=12"`
	Source   string `excel:"来源,width=16"`
	Phone    string `excel:"联系电话,width=20"`
	Website  string `excel:"网址,width=28"`
	Address  string `excel:"地址,width=40"`
	Status   *int8  `excel:"状态,width=12"`
	Remark   string `excel:"备注,width=36"`
}

// ContactImportRow 是联系人导入行：用客户编号关联所属客户（须存在且对当前用户可见）。
type ContactImportRow struct {
	Row          int    `excel:"-"`
	CustomerNo   string `excel:"客户编号,required,width=22"`
	Name         string `excel:"姓名,required,width=16"`
	Position     string `excel:"职位,width=16"`
	Mobile       string `excel:"手机,width=18"`
	Email        string `excel:"邮箱,width=26"`
	Gender       *int8  `excel:"性别,width=10"`
	DecisionRole string `excel:"决策角色,width=16"`
	Remark       string `excel:"备注,width=36"`
}

// ProductImportRow 是产品导入行：分类按名称关联（可空），编码租户内唯一，
// 价格只接受十进制字符串（Money 约束）。
type ProductImportRow struct {
	Row          int         `excel:"-"`
	Name         string      `excel:"产品名称,required,width=26"`
	Code         string      `excel:"产品编码,required,width=20"`
	CategoryName string      `excel:"分类名称,width=18"`
	Unit         string      `excel:"单位,width=12"`
	Price        money.Money `excel:"价格,required,width=14"`
	Status       *int8       `excel:"状态,width=10"`
	Description  string      `excel:"描述,width=36"`
}

// CustomerExportRow 是客户导出列：覆盖列表关键字段与负责人/部门/创建时间。
type CustomerExportRow struct {
	CustomerNo    string    `excel:"客户编号,width=22"`
	Name          string    `excel:"客户名称,width=28"`
	Status        string    `excel:"状态,width=10"`
	Industry      string    `excel:"行业,width=16"`
	Level         string    `excel:"级别,width=10"`
	Source        string    `excel:"来源,width=14"`
	Phone         string    `excel:"联系电话,width=18"`
	Website       string    `excel:"网址,width=26"`
	Address       string    `excel:"地址,width=36"`
	OwnerUserName string    `excel:"负责人,width=14"`
	OwnerDeptName string    `excel:"部门,width=14"`
	Remark        string    `excel:"备注,width=36"`
	CreateTime    time.Time `excel:"创建时间,width=20"`
}

// ProductExportRow 是产品导出列：分类名称由服务端解析，价格输出十进制字符串。
type ProductExportRow struct {
	Name        string      `excel:"产品名称,width=26"`
	Code        string      `excel:"产品编码,width=20"`
	Category    string      `excel:"分类名称,width=18"`
	Unit        string      `excel:"单位,width=12"`
	Price       money.Money `excel:"价格,width=14"`
	Status      string      `excel:"状态,width=10"`
	Description string      `excel:"描述,width=36"`
	CreateTime  time.Time   `excel:"创建时间,width=20"`
}

// ContractExportRow 是合同导出列：金额与签订/起止日期是财务对账的关键字段。
type ContractExportRow struct {
	ContractNo    string      `excel:"合同编号,width=22"`
	Name          string      `excel:"合同名称,width=28"`
	CustomerName  string      `excel:"所属客户,width=24"`
	Amount        money.Money `excel:"合同金额,width=16"`
	Status        string      `excel:"状态,width=10"`
	SignDate      *time.Time  `excel:"签订日期,width=14"`
	StartDate     *time.Time  `excel:"生效日期,width=14"`
	EndDate       *time.Time  `excel:"失效日期,width=14"`
	OwnerUserName string      `excel:"负责人,width=14"`
	OwnerDeptName string      `excel:"部门,width=14"`
	Remark        string      `excel:"备注,width=36"`
	CreateTime    time.Time   `excel:"创建时间,width=20"`
}

// ReceivablePlanExportRow 是回款计划导出列：含计划/已收金额、到期日与派生状态
// （状态与逾期不落库，由 service 按列表同一判定口径计算）。
type ReceivablePlanExportRow struct {
	ContractNo     string      `excel:"合同编号,width=22"`
	ContractName   string      `excel:"合同名称,width=28"`
	CustomerName   string      `excel:"所属客户,width=24"`
	PeriodNo       int         `excel:"期次,width=8"`
	ContractAmount money.Money `excel:"合同金额,width=16"`
	Amount         money.Money `excel:"计划金额,width=16"`
	ReceivedAmount money.Money `excel:"已收金额,width=16"`
	DueDate        time.Time   `excel:"到期日,width=14"`
	Status         string      `excel:"状态,width=12"`
	Overdue        string      `excel:"逾期,width=8"`
	Remark         string      `excel:"备注,width=36"`
}
