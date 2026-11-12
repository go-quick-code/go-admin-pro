package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- Excel 导出列（issue #138，docs/pay-mvp-spec.md §4.12）----------
// 导出列对齐各管理列表页关键字段（凭证/配置列一律不进导出：渠道 config 不落
// 导出行，notifyUrl/userIp/extension/channel_notify_data 等回调与发起内部列
// 排除，spec §7 安全审查「日志与 Excel 不落凭证」）；金额一律 money.Money
//（TextMarshaler 输出两位小数十进制字符串，无科学计数/浮点尾差）；日期按业务
// 时区（ADR 0006，导出启用 NativeDates）。

// OrderExportRow 是支付订单导出列：单号/金额/状态/渠道回执与关键时间。
type OrderExportRow struct {
	No             string      `excel:"订单号,width=24"`
	MerchantID     string      `excel:"消费方单号,width=24"`
	Subject        string      `excel:"商品标题,width=24"`
	Price          money.Money `excel:"实付金额,width=14"`
	RefundPrice    money.Money `excel:"已退金额,width=14"`
	Status         string      `excel:"状态,width=10"`
	ChannelCode    string      `excel:"支付渠道,width=12"`
	ChannelOrderNo string      `excel:"渠道单号,width=24"`
	Owner          string      `excel:"归属用户,width=14"`
	SuccessTime    *time.Time  `excel:"支付成功时间,width=20"`
	CreateTime     time.Time   `excel:"创建时间,width=20"`
}

// RefundExportRow 是退款单导出列：原单快照与退款金额/原因。
type RefundExportRow struct {
	No               string      `excel:"退款单号,width=24"`
	MerchantRefundID string      `excel:"消费方退款单号,width=24"`
	OrderNo          string      `excel:"支付订单号,width=24"`
	MerchantOrderID  string      `excel:"消费方订单号,width=24"`
	PayPrice         money.Money `excel:"原单实付,width=14"`
	RefundPrice      money.Money `excel:"退款金额,width=14"`
	Status           string      `excel:"状态,width=10"`
	ChannelCode      string      `excel:"支付渠道,width=12"`
	ChannelRefundNo  string      `excel:"渠道退款单号,width=24"`
	Reason           string      `excel:"退款原因,width=24"`
	SuccessTime      *time.Time  `excel:"退款成功时间,width=20"`
	CreateTime       time.Time   `excel:"创建时间,width=20"`
}

// TransferExportRow 是转账单导出列：收款人快照与金额/状态。
type TransferExportRow struct {
	No                 string      `excel:"转账单号,width=24"`
	MerchantTransferID string      `excel:"消费方转账单号,width=24"`
	Subject            string      `excel:"转账标题,width=24"`
	Price              money.Money `excel:"转账金额,width=14"`
	UserAccount        string      `excel:"收款账户,width=20"`
	UserName           string      `excel:"收款人,width=14"`
	Status             string      `excel:"状态,width=10"`
	ChannelCode        string      `excel:"支付渠道,width=12"`
	ChannelTransferNo  string      `excel:"渠道单号,width=24"`
	SuccessTime        *time.Time  `excel:"转账成功时间,width=20"`
	CreateTime         time.Time   `excel:"创建时间,width=20"`
}

// WalletTransactionExportRow 是钱包流水导出列：变动金额正入负出、结存快照
// 可按行重放（唯一入账入口保证）。
type WalletTransactionExportRow struct {
	No         string      `excel:"流水号,width=24"`
	WalletID   int64       `excel:"钱包ID,width=10"`
	BizType    string      `excel:"业务类型,width=10"`
	BizID      int64       `excel:"业务单据ID,width=12"`
	Title      string      `excel:"标题,width=24"`
	Price      money.Money `excel:"变动金额,width=14"`
	Balance    money.Money `excel:"结存快照,width=14"`
	CreateTime time.Time   `excel:"入账时间,width=20"`
}

// RechargeExportRow 是充值单导出列：套餐快照（自定义充值套餐列显示「自定义」）
// 与支付/退款回执（挂单订单号由服务端回填）。
type RechargeExportRow struct {
	WalletID         int64       `excel:"钱包ID,width=10"`
	TotalPrice       money.Money `excel:"充值金额,width=14"`
	PayPrice         money.Money `excel:"实付金额,width=14"`
	BonusPrice       money.Money `excel:"赠送金额,width=14"`
	Package          string      `excel:"套餐,width=22"`
	PayStatus        string      `excel:"支付状态,width=10"`
	PayChannelCode   string      `excel:"支付渠道,width=12"`
	OrderNo          string      `excel:"支付订单号,width=24"`
	PayTime          *time.Time  `excel:"支付时间,width=20"`
	RefundStatus     string      `excel:"退款状态,width=10"`
	RefundTotalPrice money.Money `excel:"已退金额,width=14"`
	CreateTime       time.Time   `excel:"创建时间,width=20"`
}
