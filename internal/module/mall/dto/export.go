package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// export.go 是管理列表 Excel 导出列（docs/mall-mvp-spec.md §1 第 8 步「Excel
// 导出」，PR #184 双轴审查补票）：会员/商品 SPU/交易订单三个管理列表导出，
// 复用各自列表的筛选 DTO 与租户隔离口径（导出内容与权限一致，spec 硬约束）。
// 照 pay dto/excel.go 先例：金额一律 money.Money（TextMarshaler 输出两位小数
// 十进制字符串，无科学计数/浮点尾差）；日期按业务时区（ADR 0006，导出启用
// NativeDates）；凭证/配置类内部列不进导出（password_hash/avatar 等排除）。
//
// 行数无上限：与 pay/CRM/ERP ListAll 同口径，由筛选条件控制结果规模。

// MemberExportRow 是会员导出列：ID/手机号/昵称/等级/积分余额/状态/注册时间。
// 等级为等级名（服务端批量解析；未分配「-」，等级已删回落「等级 #ID」，与
// 会员管理页 levelName 同口径）；状态为启用/停用文案。
type MemberExportRow struct {
	ID           int64     `excel:"ID,width=10"`
	Mobile       string    `excel:"手机号,width=16"`
	Nickname     string    `excel:"昵称,width=16"`
	Level        string    `excel:"等级,width=14"`
	PointBalance int       `excel:"积分余额,width=10"`
	Status       string    `excel:"状态,width=8"`
	CreateTime   time.Time `excel:"注册时间,width=20"`
}

// SPUExportRow 是商品 SPU 导出列：ID/名称/分类/品牌/规格类型/市场价/状态/
// 时间。分类/品牌名为服务端批量解析（product_spu 只存 ID 引用，与 SPU 管理页
// categoryName/brandName 展示列同口径）；品牌未关联（brand_id=0）输出空。
// product_spu 无独立上架时间列（上下架是状态位非事件），时间列对齐 SPU 管理
// 页的「创建时间」列。
type SPUExportRow struct {
	ID          int64       `excel:"ID,width=10"`
	Name        string      `excel:"商品名称,width=24"`
	Category    string      `excel:"分类,width=16"`
	Brand       string      `excel:"品牌,width=16"`
	SpecType    string      `excel:"规格类型,width=10"`
	MarketPrice money.Money `excel:"市场价,width=12"`
	Status      string      `excel:"状态,width=8"`
	CreateTime  time.Time   `excel:"创建时间,width=20"`
}

// TradeOrderExportRow 是交易订单导出列：单号/会员/状态文案/总件数/商品总额/
// 实付/支付渠道/下单时间/支付时间。会员为「昵称(手机号)」（服务端按 user_id
// 批量解析，缺失回落 #ID）；状态为五态中文文案（与订单管理页 statusLabels
// 同一套）；金额一律 Money 十进制字符串；收货快照/备注等明细列不进导出。
type TradeOrderExportRow struct {
	No             string      `excel:"订单号,width=24"`
	Member         string      `excel:"会员,width=20"`
	Status         string      `excel:"状态,width=10"`
	TotalQuantity  int         `excel:"总件数,width=10"`
	ItemMoney      money.Money `excel:"商品总额,width=14"`
	PayMoney       money.Money `excel:"实付金额,width=14"`
	PayChannelCode string      `excel:"支付渠道,width=12"`
	CreateTime     time.Time   `excel:"下单时间,width=20"`
	PayTime        *time.Time  `excel:"支付时间,width=20"`
}
