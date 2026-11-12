package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// transfer.go 是转账单实体（issue #137，docs/pay-mvp-spec.md §3.2/§4.8）：
// 消费方服务端发起（merchant_transfer_id 幂等键，UNIQUE(tenant_id, app_id,
// merchant_transfer_id)），wallet 渠道同事务入账目标钱包（biz=6）、mock 渠道
// 模拟成功；no 为 ZZ 单号（pay_sequence 事务内发放，客户端提交的 no 一律
// 不采纳，spec §4.9）。转账为平台出账，v1 无平台资金账本（差异清单）。
// user_id/user_type 可为 0（外部账户无归属，spec §3.2——wallet 渠道要求
// 合法目标，mock 渠道不要求）。

// 转账单状态（status，spec §3.2，对齐芋道枚举）。
const (
	TransferStatusWaiting int8 = 0  // 等待
	TransferStatusDoing   int8 = 5  // 进行中（异步渠道预留：等渠道回调）
	TransferStatusSuccess int8 = 10 // 成功（wallet/mock 同步完成）
	TransferStatusClosed  int8 = 20 // 关闭
)

// Transfer 是转账单（租户级，append-only 资金事实）：user_account/user_name
// 为收款人快照（wallet 渠道 user_account = 目标用户标识）；success_time 转账
// 成功时间；channel_error_*/channel_notify_data 失败分支与回调原文列。
type Transfer struct {
	BaseEntity
	TenantID           int64       `gorm:"not null;default:1" json:"tenantId"`
	No                 string      `gorm:"size:32;not null" json:"no"`
	AppID              int64       `gorm:"not null" json:"appId"`
	MerchantTransferID string      `gorm:"size:64;not null" json:"merchantTransferId"`
	UserID             int64       `gorm:"not null;default:0" json:"userId"`
	UserType           int8        `gorm:"not null;default:0" json:"userType"`
	ChannelCode        string      `gorm:"size:32;not null" json:"channelCode"`
	Subject            string      `gorm:"size:100;not null" json:"subject"`
	Price              money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	UserAccount        string      `gorm:"size:64" json:"userAccount"`
	UserName           string      `gorm:"size:64" json:"userName"`
	Status             int8        `gorm:"not null;default:0" json:"status"`
	SuccessTime        *time.Time  `gorm:"" json:"successTime"`
	NotifyURL          string      `gorm:"size:500" json:"notifyUrl"`
	UserIP             string      `gorm:"size:64" json:"userIp"`
	ChannelExtras      string      `gorm:"type:text" json:"channelExtras"`
	ChannelTransferNo  string      `gorm:"size:64" json:"channelTransferNo"`
	ChannelErrorCode   string      `gorm:"size:64" json:"channelErrorCode"`
	ChannelErrorMsg    string      `gorm:"size:255" json:"channelErrorMsg"`
	ChannelNotifyData  string      `gorm:"type:text" json:"channelNotifyData"`
}

func (Transfer) TableName() string { return "pay_transfer" }
