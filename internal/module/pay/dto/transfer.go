package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 转账单（租户级，issue #137，spec §5/§4.8） ----------

// TransferCreateReq 创建转账单（POST /pay/transfer/create，登录）。
//
// 调用方契约（同 #133 下单 / #135 退款）：本端点的调用方是「消费方业务服务端」
// （示例提现审批转发，或测试直调）——price/subject 由消费方服务端传入并自负
// 其责；channelCode 按请求选择（wallet/mock，spec §4.8「渠道 per request」；
// 不校验应用渠道绑定——spec §4.8 未定义转账的绑定约束，绑定守卫是收银台
// submit 的支付语义，见 service 层决策记录）；wallet 渠道 userAccount 必须
// 是合法的目标用户标识（409，spec §4.8），mock 渠道自由文本；user_name 可选
// 快照；notifyUrl 缺省取应用 transfer_notify_url 快照；ZZ 单号服务端发放
// （请求不收 no，spec §4.9）。
type TransferCreateReq struct {
	AppKey             string      `json:"appKey" binding:"required,max=64"`
	MerchantTransferID string      `json:"merchantTransferId" binding:"required,max=64"`
	ChannelCode        string      `json:"channelCode" binding:"required,max=32"`
	Subject            string      `json:"subject" binding:"required,max=100"`
	Price              money.Money `json:"price"`
	UserAccount        string      `json:"userAccount" binding:"max=64"`
	UserName           string      `json:"userName" binding:"max=64"`
	NotifyURL          string      `json:"notifyUrl" binding:"max=500"`
}

// TransferPageReq 转账单分页筛选（管理端 GET /pay/transfer/page，权限
// pay:transfer:query）。导出按钮随 #138 export-excel 补种（spec §7）。
type TransferPageReq struct {
	PageNo             int    `form:"pageNo"`
	PageSize           int    `form:"pageSize"`
	No                 string `form:"no"`
	MerchantTransferID string `form:"merchantTransferId"`
	AppID              int64  `form:"appId"`
	ChannelCode        string `form:"channelCode"`
	Status             *int8  `form:"status"`
}

// TransferDetail 转账单详情（GET /pay/transfer/:id）：转账单 + 关联支付应用
// （前端详情 drawer 展示应用定位）。
type TransferDetail struct {
	*model.Transfer
	App *model.App `json:"app"`
}
