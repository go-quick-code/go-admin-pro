// Package model 是 pay 模块的实体定义。
//
// 渠道（Channel）是平台级配置（登记 PlatformTables，不登记 TenantTables），
// 14 行种子常驻禁删只启停；应用（App）与绑定（AppChannel）是租户级数据，
// 由租户插件自动隔离（docs/pay-mvp-spec.md §3.1 / ADR 0021）。
// 序列表（pay_sequence）无模型：发放走裸 SQL（repository.sequence.go，
// 机制复制 erp_sequence）。
package model

import (
	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座，定义见 internal/pkg/model。
type BaseEntity = basemodel.BaseEntity

// 启用状态：0 启用 / 1 停用，与 system/message 保持一致。
const (
	StatusEnable  = 0
	StatusDisable = 1
)

// Channel 是支付渠道（平台级）：14 个渠道码常量逐一预插（mock/wallet 启用、
// 其余停用且 config={}），禁删只启停。config 为渠道凭证 JSON 明文落库，
// 查询时由 service 层脱敏（照 message_channel 三段式）。
type Channel struct {
	BaseEntity
	Code    string `gorm:"size:32;not null" json:"code"`
	Name    string `gorm:"size:100;not null" json:"name"`
	Status  int8   `gorm:"not null" json:"status"`
	FeeRate string `gorm:"type:decimal(5,2);not null;default:0.00" json:"feeRate"`
	Config  string `gorm:"type:text" json:"config"`
	Remark  string `gorm:"size:500" json:"remark"`
}

func (Channel) TableName() string { return "pay_channel" }

// App 是支付应用（租户级）：app_key 租户内唯一，回调地址在下单时快照。
// Status 不带 default 标签（同 Channel：GORM 零值省略带 default 的字段，
// 0 启用会被 DB 默认值覆盖的坑）。
type App struct {
	BaseEntity
	TenantID          int64  `gorm:"not null;default:1" json:"tenantId"`
	AppKey            string `gorm:"size:64;not null" json:"appKey"`
	Name              string `gorm:"size:100;not null" json:"name"`
	Status            int8   `gorm:"not null" json:"status"`
	Remark            string `gorm:"size:500" json:"remark"`
	OrderNotifyURL    string `gorm:"size:500" json:"orderNotifyUrl"`
	RefundNotifyURL   string `gorm:"size:500" json:"refundNotifyUrl"`
	TransferNotifyURL string `gorm:"size:500" json:"transferNotifyUrl"`
}

func (App) TableName() string { return "pay_app" }

// AppChannel 是应用渠道绑定（租户级，纯绑定关系）：tenant_id 入唯一键，
// 全量替换更新（spec §5）。channel_code ∈ 14 渠道码常量 ∧ 平台渠道启用
// 才可绑（校验在 service 层，spec §4.1）。
type AppChannel struct {
	BaseEntity
	TenantID    int64  `gorm:"not null;default:1" json:"tenantId"`
	AppID       int64  `gorm:"index;not null" json:"appId"`
	ChannelCode string `gorm:"size:32;not null" json:"channelCode"`
}

func (AppChannel) TableName() string { return "pay_app_channel" }
