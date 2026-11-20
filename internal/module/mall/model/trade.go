package model

import (
	"encoding/json"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// trade.go 是交易域实体（issue #179，docs/mall-mvp-spec.md §3.3）。
//
// 三表全部租户级数据（TenantTables，ADR 0019）：查询/写入由租户插件自动
// 追加 tenant_id 条件，跨租户/跨会员业务 ID 一律不可见（404，不泄露存在性）。
// 唯一键（不含 deleted_at，照 member_user/mobile 先例）：
//   - trade_cart：UNIQUE(tenant_id, user_id, sku_id)——重复加购合并数量；
//   - trade_order：UNIQUE(tenant_id, no)——SO 单号租户内唯一（mall_sequence
//     事务内发放）。
//
// 状态机五态数值对齐芋道 PayOrderStatusEnum/TradeOrderStatusEnum（spec
// §3.3/§3.4）；迁移合法性纯函数见 service/trade_rules.go，状态翻转只经
// 条件 UPDATE（service 层收口，任何路径不得直改 status）。
//
// 金额不变量（spec §3.4）：订单项 subtotal = price × count；订单
// item_money = Σ subtotal、pay_money = item_money − discount_money +
// postage（服务端定，客户端金额不采纳）；discount_money/postage 预留恒 0
//（spec §7 裁决 8）。

// 订单状态（spec §3.3「0 待支付 / 10 已支付 / 20 已完成 / 30 已取消 /
// 40 已关闭」）。
const (
	// TradeOrderStatusPending 待支付（创建订单初始态）。
	TradeOrderStatusPending int8 = 0
	// TradeOrderStatusPaid 已支付（T7 支付联动置入）。
	TradeOrderStatusPaid int8 = 10
	// TradeOrderStatusCompleted 已完成（T3 管理员完成置入）。
	TradeOrderStatusCompleted int8 = 20
	// TradeOrderStatusCanceled 已取消（用户取消，本票 C 端；终态）。
	TradeOrderStatusCanceled int8 = 30
	// TradeOrderStatusClosed 已关闭（T9 超时关单置入；终态）。
	TradeOrderStatusClosed int8 = 40
)

// TradeOrderStatuses 是全部订单状态集合（分页过滤的合法值域）。
var TradeOrderStatuses = []int8{
	TradeOrderStatusPending, TradeOrderStatusPaid, TradeOrderStatusCompleted,
	TradeOrderStatusCanceled, TradeOrderStatusClosed,
}

// IsTradeOrderStatus 报告 status 是否为合法订单状态。
func IsTradeOrderStatus(status int8) bool {
	for _, s := range TradeOrderStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// TradeCart 是购物车行（租户级；同 (user, sku) 唯一，重复加购合并数量）。
type TradeCart struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// UserID 是归属会员 ID（越权谓词：会员只读写自己的购物车行）。
	UserID int64 `gorm:"not null" json:"userId"`
	// SPUID 是商品 SPU ID（与 SKUID 同 SPU，service 层加购时校验一致；
	// 显式 column 标签照 ProductSKU：gorm 默认命名拆 sp_uid 与迁移列名不符）。
	SPUID int64 `gorm:"column:spu_id;not null" json:"spuId"`
	SKUID int64 `gorm:"column:sku_id;not null" json:"skuId"`
	// Count 是加购数量（正整数，纯函数校验）。
	Count int `gorm:"not null" json:"count"`
	// Selected 是结算选中位：0 未选 / 1 选中（试算与下单只结算选中项）。
	Selected int8 `gorm:"not null;default:1" json:"selected"`

	// Sku 是加购行的 SKU 现值（我的购物车返回：现价/库存/规格/图，非持久化列）。
	Sku *ProductSKU `gorm:"-" json:"sku,omitempty"`
	// SpuName 是 SPU 名称现值（展示列，非持久化）。
	SpuName string `gorm:"-" json:"spuName,omitempty"`
	// SpuStatus 是 SPU 上下架现值（展示列：下架项加购/结算拦截，非持久化）。
	SpuStatus int8 `gorm:"-" json:"spuStatus,omitempty"`
}

func (TradeCart) TableName() string { return "trade_cart" }

// TradeOrder 是商城订单（租户级；金额/收货人/状态全部服务端定）。
type TradeOrder struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// No 是订单号（SO 前缀 + yyyyMMdd + 序号，mall_sequence 事务内发放，
	// 服务端定——客户端提交的 no 一律不采纳，照 pay 单号口径）。
	No string `gorm:"size:64;not null" json:"no"`
	// UserID 是下单会员 ID（越权谓词）。
	UserID int64 `gorm:"not null" json:"userId"`
	// Status 是订单状态机（五态常量，翻转只经条件 UPDATE）。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// Type 是订单类型（预留 0 正常）。
	Type           int8       `gorm:"not null;default:0" json:"type"`
	UserRemark     string     `gorm:"size:200" json:"userRemark"`
	AdminRemark    string     `gorm:"size:500" json:"adminRemark"`
	PayOrderID     int64      `gorm:"not null;default:0" json:"payOrderId"`
	PayOrderNo     string     `gorm:"size:64;not null;default:''" json:"payOrderNo"`
	PayChannelCode string     `gorm:"size:32;not null;default:''" json:"payChannelCode"`
	PayTime        *time.Time `json:"payTime"`
	CancelTime     *time.Time `json:"cancelTime"`
	CancelReason   string     `gorm:"size:200" json:"cancelReason"`
	CloseTime      *time.Time `json:"closeTime"`
	CloseReason    string     `gorm:"size:200" json:"closeReason"`
	// TotalQuantity 是订单总件数（Σ 订单项 count，服务端定）。
	TotalQuantity int `gorm:"not null;default:0" json:"totalQuantity"`
	// ItemMoney 是商品金额（Σ subtotal）。
	ItemMoney money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"itemMoney"`
	// DiscountMoney 预留恒 0（spec §7 裁决 8）。
	DiscountMoney money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"discountMoney"`
	// Postage 预留恒 0（spec §7 裁决 8）。
	Postage money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"postage"`
	// PayMoney 是实付金额 = item_money − discount_money + postage（服务端定）。
	PayMoney money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"payMoney"`
	// 收货快照四列（下单时从 member_address 快照）。
	ReceiverName   string `gorm:"size:64;not null" json:"receiverName"`
	ReceiverMobile string `gorm:"size:20;not null" json:"receiverMobile"`
	ReceiverArea   string `gorm:"size:255;not null;default:''" json:"receiverArea"`
	ReceiverDetail string `gorm:"size:255;not null;default:''" json:"receiverDetail"`

	// Items 是订单项列表（详情返回；非持久化列）。
	Items []TradeOrderItem `gorm:"-" json:"items,omitempty"`
}

func (TradeOrder) TableName() string { return "trade_order" }

// TradeOrderItem 是订单项（租户级；成交快照随订单一次落库不再变更）。
type TradeOrderItem struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	OrderID  int64 `gorm:"not null" json:"orderId"`
	UserID   int64 `gorm:"not null" json:"userId"`
	SPUID    int64 `gorm:"column:spu_id;not null" json:"spuId"`
	SKUID    int64 `gorm:"column:sku_id;not null" json:"skuId"`
	// SPUName 是 SPU 名称快照（成交时现值）。
	SPUName string `gorm:"size:128;not null" json:"spuName"`
	// SKUPic 是 SKU 图片快照（缺省回落 SPU 主图）。
	SKUPic string `gorm:"size:255" json:"skuPic"`
	// SKUProperties 是 SKU 规格 JSON 快照（[{propertyId,propertyName,valueId,
	// valueName}]，单规格 []；写入侧保证非 nil）。
	SKUProperties json.RawMessage `gorm:"type:json;not null" json:"skuProperties"`
	// Price 是成交单价（下单时 SKU 现价快照，服务端取现价）。
	Price money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	// Count 是购买数量（正整数）。
	Count int `gorm:"not null" json:"count"`
	// Subtotal 是小计 = price × count（服务端定）。
	Subtotal money.Money `gorm:"type:decimal(12,2);not null" json:"subtotal"`
}

func (TradeOrderItem) TableName() string { return "trade_order_item" }
