package dto

import (
	"encoding/json"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// trade_app.go 是交易域 C 端自助的请求/响应 DTO（issue #179，T6；
// docs/mall-mvp-spec.md §4「购物车：加购/数量修改/选中切换/删除/我的购物车；
// 订单：确认结算页（选中项+默认地址+金额试算）、创建订单（收货地址 id +
// 备注）、我的订单分页/详情/取消、我的积分流水」）。与管理端 DTO 分立：
// C 端请求字段更窄（金额/状态/单号/收货快照全部服务端定——照 pay_money
// 服务端定口径先例），订单响应不含 adminRemark/payOrderId 等管理面字段。

// AppCartAddReq 加购（POST /mall/app/cart，需会员令牌）：skuId + count。
// 同 SKU 重复加购合并数量（UNIQUE(tenant_id, user_id, sku_id) upsert）。
type AppCartAddReq struct {
	SKUID int64 `json:"skuId" binding:"required"`
	Count int   `json:"count" binding:"required"`
}

// AppCartCountReq 修改加购数量（PUT /mall/app/cart/:id/count）：count 为
// 绝对值覆盖（正整数，纯函数校验）。
type AppCartCountReq struct {
	Count int `json:"count" binding:"required"`
}

// AppCartSelectReq 结算选中切换（PUT /mall/app/cart/:id/selected）：selected
// 取 0/1（指针区分「未传」与 0，binding required 对指针即非 nil）。
type AppCartSelectReq struct {
	Selected *int8 `json:"selected" binding:"required"`
}

// AppCartDeleteReq 删除购物车行（POST /mall/app/cart/delete，批量）。
type AppCartDeleteReq struct {
	IDs []int64 `json:"ids" binding:"required,gt=0"`
}

// AppCartItem 购物车行（GET /mall/app/cart 返回项）：SKU 现价/现库存/规格/
// 图与 SPU 名称/上架态一并带出（spec §4「我的购物车（含 SKU 现价与库存
// 展示）」）。OnSale=false 表示 SPU 已下架（前端置灰，加购/结算服务端拦截）。
type AppCartItem struct {
	ID         int64           `json:"id"`
	SPUID      int64           `json:"spuId"`
	SKUID      int64           `json:"skuId"`
	Count      int             `json:"count"`
	Selected   int8            `json:"selected"`
	SPUName    string          `json:"spuName"`
	SPUPicURL  string          `json:"spuPicUrl"`
	SKUPicURL  string          `json:"skuPicUrl"`
	Properties json.RawMessage `json:"properties"`
	Price      money.Money     `json:"price"`
	Stock      int             `json:"stock"`
	Subtotal   money.Money     `json:"subtotal"`
	OnSale     bool            `json:"onSale"`
}

// AppCartSummary 是购物车汇总（选中项口径，与结算试算同源）。
type AppCartSummary struct {
	SelectedCount int         `json:"selectedCount"`
	TotalQuantity int         `json:"totalQuantity"`
	ItemMoney     money.Money `json:"itemMoney"`
}

// AppCartListResp 我的购物车（GET /mall/app/cart）。
type AppCartListResp struct {
	Items   []AppCartItem  `json:"items"`
	Summary AppCartSummary `json:"summary"`
}

// AppSettlementItem 结算页明细行（选中项 + 现价快照展示）。
type AppSettlementItem struct {
	SPUID      int64           `json:"spuId"`
	SKUID      int64           `json:"skuId"`
	SPUName    string          `json:"spuName"`
	SKUPicURL  string          `json:"skuPicUrl"`
	Properties json.RawMessage `json:"properties"`
	Price      money.Money     `json:"price"`
	Count      int             `json:"count"`
	Subtotal   money.Money     `json:"subtotal"`
}

// AppAddress 是 C 端收货地址视图（结算页默认地址；字段与 model.MemberAddress
// 对齐但不含 tenantId/userId 等归属字段）。
type AppAddress struct {
	ID             int64  `json:"id"`
	ReceiverName   string `json:"receiverName"`
	ReceiverMobile string `json:"receiverMobile"`
	Province       string `json:"province"`
	City           string `json:"city"`
	District       string `json:"district"`
	Detail         string `json:"detail"`
	IsDefault      int8   `json:"isDefault"`
}

// AppSettlementResp 结算试算（GET /mall/app/trade/settlement，不下单）：
// 选中项 + 默认地址（无默认地址时为 null）+ 金额三列（服务端定）。
type AppSettlementResp struct {
	Items         []AppSettlementItem `json:"items"`
	Address       *AppAddress         `json:"address"`
	TotalQuantity int                 `json:"totalQuantity"`
	ItemMoney     money.Money         `json:"itemMoney"`
	DiscountMoney money.Money         `json:"discountMoney"`
	Postage       money.Money         `json:"postage"`
	PayMoney      money.Money         `json:"payMoney"`
}

// AppCreateOrderReq 创建订单（POST /mall/app/trade/order，需会员令牌）：
// 收货地址 id + 备注，按选中购物车项结算（spec §4「创建订单（收货地址 id +
// 备注）」）。金额/状态/单号全部服务端定。
type AppCreateOrderReq struct {
	AddressID  int64  `json:"addressId" binding:"required"`
	UserRemark string `json:"userRemark" binding:"max=200"`
}

// AppCreateOrderResp 创建订单响应：订单 ID/单号/实付金额（跳支付页的数据）。
type AppCreateOrderResp struct {
	ID       int64       `json:"id"`
	No       string      `json:"no"`
	PayMoney money.Money `json:"payMoney"`
}

// AppOrderPageReq 我的订单分页（GET /mall/app/trade/order/page，需会员令牌）：
// user_id 恒取当前会员（越权谓词）；status 过滤选填（∈ 五态枚举，非法 400）。
type AppOrderPageReq struct {
	PageNo   int   `form:"pageNo"`
	PageSize int   `form:"pageSize"`
	Status   *int8 `form:"status"`
}

// AppOrderItemResp 是 C 端订单项视图（成交快照）。
type AppOrderItemResp struct {
	ID         int64           `json:"id"`
	SPUID      int64           `json:"spuId"`
	SKUID      int64           `json:"skuId"`
	SPUName    string          `json:"spuName"`
	SKUPicURL  string          `json:"skuPicUrl"`
	Properties json.RawMessage `json:"skuProperties"`
	Price      money.Money     `json:"price"`
	Count      int             `json:"count"`
	Subtotal   money.Money     `json:"subtotal"`
}

// AppOrderResp 是 C 端订单视图（含 items）：不含 adminRemark/payOrderId/
// payOrderNo/payChannelCode/creator 等管理面字段（照 member_app.go 收窄口径）。
type AppOrderResp struct {
	ID             int64              `json:"id"`
	No             string             `json:"no"`
	Status         int8               `json:"status"`
	UserRemark     string             `json:"userRemark"`
	TotalQuantity  int                `json:"totalQuantity"`
	ItemMoney      money.Money        `json:"itemMoney"`
	DiscountMoney  money.Money        `json:"discountMoney"`
	Postage        money.Money        `json:"postage"`
	PayMoney       money.Money        `json:"payMoney"`
	ReceiverName   string             `json:"receiverName"`
	ReceiverMobile string             `json:"receiverMobile"`
	ReceiverArea   string             `json:"receiverArea"`
	ReceiverDetail string             `json:"receiverDetail"`
	PayTime        *time.Time         `json:"payTime"`
	CancelTime     *time.Time         `json:"cancelTime"`
	CancelReason   string             `json:"cancelReason"`
	CloseTime      *time.Time         `json:"closeTime"`
	CloseReason    string             `json:"closeReason"`
	CreatedAt      time.Time          `json:"createTime"`
	Items          []AppOrderItemResp `json:"items"`
}

// AppCancelOrderReq 取消订单（POST /mall/app/trade/order/:id/cancel）：原因
// 选填 ≤200 字。
type AppCancelOrderReq struct {
	Reason string `json:"reason" binding:"max=200"`
}

// AppPointRecordPageReq 我的积分流水分页（GET /mall/app/trade/point-records，
// 需会员令牌）：user_id 恒取当前会员；biz_type 过滤选填（∈ 三类枚举，非法
// 400，与管理端 PointRecordPageReq 同口径）。响应直接复用
// model.MemberPointRecord 序列化（流水列表复用模型的管理端先例）。
type AppPointRecordPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	BizType  string `form:"bizType"`
}
