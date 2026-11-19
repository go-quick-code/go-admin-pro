package dto

import (
	"encoding/json"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// trade_admin.go 是交易域管理端的请求/响应 DTO（issue #180，T7；
// docs/mall-mvp-spec.md §4「交易：订单分页（状态/编号/会员/时间过滤）/
// 详情（含 items）/管理员备注/取消（待支付）/完成（已支付）」）。
// 与 C 端 DTO 分立（trade_app.go 收窄口径）：管理端全量带管理面字段
//（payOrderId/payOrderNo/payChannelCode/adminRemark/type），金额一律
// Money 十进制字符串序列化。

// AdminOrderPageReq 管理端订单分页筛选（GET /mall/trade/order/page，权限
// mall:trade:query）：状态/编号模糊/会员 ID/下单时间范围四类过滤，均可选。
// 时间文本布局 YYYY-MM-DD [HH:mm:ss]，end 为纯日期按「含当日」语义由
// service 层外推（repo 上界排他）。
type AdminOrderPageReq struct {
	PageNo    int    `form:"pageNo"`
	PageSize  int    `form:"pageSize"`
	Status    *int8  `form:"status"`
	No        string `form:"no"`
	UserID    int64  `form:"userId"`
	StartTime string `form:"startTime"`
	EndTime   string `form:"endTime"`
}

// AdminOrderItemResp 是管理端订单项视图（成交快照全量，与 C 端
// AppOrderItemResp 同形——快照列管理/C 端一致）。
type AdminOrderItemResp struct {
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

// AdminOrderResp 是管理端订单视图（详情含 items；分页复用主行不含 items）：
// 管理面字段全量（payOrderId/payOrderNo/payChannelCode/adminRemark/type），
// 与 C 端 AppOrderResp 的收窄口径相对。
type AdminOrderResp struct {
	ID             int64                `json:"id"`
	No             string               `json:"no"`
	UserID         int64                `json:"userId"`
	Status         int8                 `json:"status"`
	Type           int8                 `json:"type"`
	UserRemark     string               `json:"userRemark"`
	AdminRemark    string               `json:"adminRemark"`
	PayOrderID     int64                `json:"payOrderId"`
	PayOrderNo     string               `json:"payOrderNo"`
	PayChannelCode string               `json:"payChannelCode"`
	PayTime        *time.Time           `json:"payTime"`
	CancelTime     *time.Time           `json:"cancelTime"`
	CancelReason   string               `json:"cancelReason"`
	CloseTime      *time.Time           `json:"closeTime"`
	CloseReason    string               `json:"closeReason"`
	TotalQuantity  int                  `json:"totalQuantity"`
	ItemMoney      money.Money          `json:"itemMoney"`
	DiscountMoney  money.Money          `json:"discountMoney"`
	Postage        money.Money          `json:"postage"`
	PayMoney       money.Money          `json:"payMoney"`
	ReceiverName   string               `json:"receiverName"`
	ReceiverMobile string               `json:"receiverMobile"`
	ReceiverArea   string               `json:"receiverArea"`
	ReceiverDetail string               `json:"receiverDetail"`
	CreateTime     time.Time            `json:"createTime"`
	Items          []AdminOrderItemResp `json:"items"`
}

// AdminOrderRemarkReq 管理员备注（PUT /mall/trade/order/:id/remark，权限
// mall:trade:update）：任意状态可补注（终态也允许），≤500 字对齐
// trade_order.admin_remark 列宽。
type AdminOrderRemarkReq struct {
	Remark string `json:"remark" binding:"max=500"`
}
