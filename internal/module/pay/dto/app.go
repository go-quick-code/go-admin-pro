package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
)

// ---------- 支付应用（租户级） ----------

// AppPageReq 应用分页筛选。
type AppPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	AppKey   string `form:"appKey"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// AppSaveReq 创建应用（POST /pay/app）：app_key 租户内唯一，撞键 400。
type AppSaveReq struct {
	AppKey            string `json:"appKey" binding:"required,max=64"`
	Name              string `json:"name" binding:"required,max=100"`
	Status            *int8  `json:"status"`
	Remark            string `json:"remark" binding:"max=500"`
	OrderNotifyURL    string `json:"orderNotifyUrl" binding:"max=500"`
	RefundNotifyURL   string `json:"refundNotifyUrl" binding:"max=500"`
	TransferNotifyURL string `json:"transferNotifyUrl" binding:"max=500"`
}

// AppUpdateReq 修改应用（PUT /pay/app，id 在 body）。
type AppUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	AppSaveReq
}

// AppDetail 应用详情 + 已绑定渠道码（GET /pay/app/:id）。
type AppDetail struct {
	*model.App
	ChannelCodes []string `json:"channelCodes"`
}
