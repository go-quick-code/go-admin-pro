// Package model 是 message 模块的实体定义。
//
// 渠道（Channel）与模板（Template）是平台级配置（不登记 TenantTables），
// 只有平台租户可管理；发送日志（Log）是租户级 append-only 记录，
// 由租户插件自动追加 tenant_id 条件（spec §3.2–§3.4 / §4.3）。
package model

import (
	"time"

	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座，定义见 internal/pkg/model。
type BaseEntity = basemodel.BaseEntity

// 渠道类型（spec §3.2）。
const (
	ChannelTypeSMS  = "sms"
	ChannelTypeMail = "mail"
)

// 启用状态：0 启用 / 1 停用，与系统模块保持一致。
const (
	StatusEnable  = 0
	StatusDisable = 1
)

// 发送结果：0 成功 / 1 失败（spec §3.4）。
const (
	LogStatusSuccess = 0
	LogStatusFailed  = 1
)

// 内置 provider 编码（ADR 0010）：
//   - log：开发/演示用，只写应用日志不真正投递（#72）；
//   - smtp：stdlib net/smtp + STARTTLS 邮件通道（#73）；
//   - aliyun_sms：stdlib HTTP + RPC 签名阿里云短信通道（#74）。
const (
	ProviderLog       = "log"
	ProviderSMTP      = "smtp"
	ProviderAliyunSMS = "aliyun_sms"
)

// Channel 是消息渠道（平台级）：provider 配置以 JSON 文本存储，
// 密钥字段（password/secret/accessKeySecret）在查询时由 service 脱敏。
type Channel struct {
	BaseEntity
	ChannelType string `gorm:"size:16;not null" json:"channelType"`
	Code        string `gorm:"size:64;not null" json:"code"`
	Name        string `gorm:"size:100;not null" json:"name"`
	Provider    string `gorm:"size:64;not null" json:"provider"`
	Config      string `gorm:"type:text" json:"config"`
	Status      int8   `gorm:"not null" json:"status"`
	Remark      string `gorm:"size:500" json:"remark"`
}

func (Channel) TableName() string { return "message_channel" }

// Template 是消息模板（平台级）。Params 以 JSON 数组文本落库（varchar(1000)），
// API 层转换为 []string；Content/Title 仅支持 {{name}} 占位（spec §4.3.3）。
type Template struct {
	BaseEntity
	ChannelType string `gorm:"size:16;not null" json:"channelType"`
	Code        string `gorm:"size:64;not null" json:"code"`
	Name        string `gorm:"size:100;not null" json:"name"`
	Title       string `gorm:"size:200" json:"title"`
	Content     string `gorm:"type:text;not null" json:"content"`
	Params      string `gorm:"size:1000;not null;default:'[]'" json:"-"`
	Status      int8   `gorm:"not null" json:"status"`
	Remark      string `gorm:"size:500" json:"remark"`
}

func (Template) TableName() string { return "message_template" }

// Log 是发送日志（租户级 append-only）：不设 deleted_at/updated_at/
// creator/updater。手动重发更新原行，不新增行（spec §4.3.7）。
type Log struct {
	ID                int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt         time.Time  `json:"createTime"`
	TenantID          int64      `gorm:"not null;default:1" json:"tenantId"`
	ChannelType       string     `gorm:"size:16;not null" json:"channelType"`
	ChannelID         int64      `gorm:"not null" json:"channelId"`
	TemplateCode      string     `gorm:"size:64;not null" json:"templateCode"`
	Recipient         string     `gorm:"size:255;not null" json:"recipient"`
	Title             string     `gorm:"size:200" json:"title"`
	Content           string     `gorm:"type:text" json:"content"`
	Status            int8       `gorm:"not null" json:"status"`
	ErrorMessage      string     `gorm:"size:1000" json:"errorMessage"`
	ProviderMessageID string     `gorm:"size:128" json:"providerMessageId"`
	RetryCount        int        `gorm:"not null;default:0" json:"retryCount"`
	SentAt            *time.Time `json:"sentAt"`
}

func (Log) TableName() string { return "message_log" }
