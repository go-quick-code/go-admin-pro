// Package dto 是 message 模块的 HTTP 契约。
package dto

import "time"

// ---------- 渠道 ----------

// ChannelPageReq 渠道分页筛选：channelType 限定 sms/mail（短信/邮件页面各传一个）。
type ChannelPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	ChannelType string `form:"channelType"`
	Code        string `form:"code"`
	Name        string `form:"name"`
	Status      *int8  `form:"status"`
}

// ChannelCreateReq 新建渠道（仅平台租户）。config 为 provider 配置 JSON 文本，
// 密钥字段以明文提交，查询时脱敏。
type ChannelCreateReq struct {
	ChannelType string `json:"channelType" binding:"required,max=16"`
	Code        string `json:"code" binding:"required,max=64"`
	Name        string `json:"name" binding:"required,max=100"`
	Provider    string `json:"provider" binding:"required,max=64"`
	Config      string `json:"config"`
	Status      *int8  `json:"status"`
	Remark      string `json:"remark" binding:"max=500"`
}

// ChannelUpdateReq 修改渠道；config 中提交 ****** 的密钥字段保留原值（spec §4.3.6）。
type ChannelUpdateReq struct {
	ID          int64  `json:"id" binding:"required"`
	ChannelType string `json:"channelType" binding:"required,max=16"`
	Code        string `json:"code" binding:"required,max=64"`
	Name        string `json:"name" binding:"required,max=100"`
	Provider    string `json:"provider" binding:"required,max=64"`
	Config      string `json:"config"`
	Status      *int8  `json:"status"`
	Remark      string `json:"remark" binding:"max=500"`
}

// StatusReq 修改渠道状态：0 启用 / 1 停用。status 用指针接收，0 是合法值。
type StatusReq struct {
	Status *int8 `json:"status" binding:"required"`
}

// ChannelTestReq 渠道测试发送请求（POST /message/channel/:id/test）：
// 用指定渠道向 recipient 投递 templateCode 渲染后的消息，验证渠道配置。
// 仅平台租户可用（service 层校验，spec §5.2）。
type ChannelTestReq struct {
	Recipient    string            `json:"recipient" binding:"required,max=255"`
	TemplateCode string            `json:"templateCode" binding:"required,max=64"`
	Params       map[string]string `json:"params"`
}

// ---------- 模板 ----------

// TemplatePageReq 模板分页筛选。
type TemplatePageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	ChannelType string `form:"channelType"`
	Code        string `form:"code"`
	Name        string `form:"name"`
	Status      *int8  `form:"status"`
}

// TemplateCreateReq 新建模板。params 声明模板参数名（JSON 数组），
// 内容中的 {{name}} 占位必须全部在声明中（spec §4.3.3）。
type TemplateCreateReq struct {
	ChannelType string   `json:"channelType" binding:"required,max=16"`
	Code        string   `json:"code" binding:"required,max=64"`
	Name        string   `json:"name" binding:"required,max=100"`
	Title       string   `json:"title" binding:"max=200"`
	Content     string   `json:"content" binding:"required"`
	Params      []string `json:"params"`
	Status      *int8    `json:"status"`
	Remark      string   `json:"remark" binding:"max=500"`
}

// TemplateUpdateReq 修改模板（全量覆盖）。
type TemplateUpdateReq struct {
	ID          int64    `json:"id" binding:"required"`
	ChannelType string   `json:"channelType" binding:"required,max=16"`
	Code        string   `json:"code" binding:"required,max=64"`
	Name        string   `json:"name" binding:"required,max=100"`
	Title       string   `json:"title" binding:"max=200"`
	Content     string   `json:"content" binding:"required"`
	Params      []string `json:"params"`
	Status      *int8    `json:"status"`
	Remark      string   `json:"remark" binding:"max=500"`
}

// TemplateVO 是模板的返回结构：Params 以数组返回（库内是 JSON 文本）。
type TemplateVO struct {
	ID          int64     `json:"id"`
	ChannelType string    `json:"channelType"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Params      []string  `json:"params"`
	Status      int8      `json:"status"`
	Remark      string    `json:"remark"`
	CreateTime  time.Time `json:"createTime"`
	UpdateTime  time.Time `json:"updateTime"`
}

// ---------- 发送 ----------

// SendReq 是短信/邮件发送请求。channelCode 为空时使用该类型第一个启用的渠道；
// params 的键必须与模板声明的参数完全一致（缺失或多余都返回 400）。
type SendReq struct {
	ChannelCode  string            `json:"channelCode" binding:"max=64"`
	TemplateCode string            `json:"templateCode" binding:"required,max=64"`
	Recipient    string            `json:"recipient" binding:"required,max=255"`
	Params       map[string]string `json:"params"`
}

// SendResult 返回本次发送写入的日志 ID。
type SendResult struct {
	LogID int64 `json:"logId"`
}

// ---------- 发送日志 ----------

// LogPageReq 发送日志分页筛选（租户插件自动限定当前租户）。
type LogPageReq struct {
	PageNo       int    `form:"pageNo"`
	PageSize     int    `form:"pageSize"`
	ChannelType  string `form:"channelType"`
	TemplateCode string `form:"templateCode"`
	Recipient    string `form:"recipient"`
	Status       *int8  `form:"status"`
}
