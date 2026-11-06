package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// sendTimeout 是 provider 投递超时（spec §4.3.2：超时 10s）。
const sendTimeout = 10 * time.Second

// SendSMS 发送短信（POST /message/sms/send）。
func (s *Service) SendSMS(ctx context.Context, req *dto.SendReq) (*dto.SendResult, error) {
	return s.send(ctx, model.ChannelTypeSMS, req)
}

// SendMail 发送邮件（POST /message/mail/send）。
func (s *Service) SendMail(ctx context.Context, req *dto.SendReq) (*dto.SendResult, error) {
	return s.send(ctx, model.ChannelTypeMail, req)
}

// send 是短信/邮件共用的发送流程（spec §4.3.2）：
// 模板启用 → 渠道启用 → 参数与声明一致 → 渲染 → provider 发送 → 写日志。
// 失败（未知 provider / provider 错误 / 超时）返回 502，并写失败日志。
func (s *Service) send(ctx context.Context, channelType string, req *dto.SendReq) (*dto.SendResult, error) {
	recipient := strings.TrimSpace(req.Recipient)
	if err := validateRecipient(channelType, recipient); err != nil {
		return nil, err
	}

	template, err := s.repo.FindTemplateByCode(ctx, channelType, req.TemplateCode)
	if err != nil {
		return nil, err
	}
	if template == nil || template.Status != model.StatusEnable {
		return nil, xerr.BadRequest("消息模板不存在或已停用")
	}

	channel, err := s.resolveChannel(ctx, channelType, req.ChannelCode)
	if err != nil {
		return nil, err
	}

	declared, err := decodeTemplateParams(template.Params)
	if err != nil {
		return nil, xerr.BadRequest("模板参数声明格式错误")
	}
	if err := validateSendParams(declared, req.Params); err != nil {
		return nil, err
	}

	title, err := renderTemplate(template.Title, req.Params)
	if err != nil {
		return nil, err
	}
	content, err := renderTemplate(template.Content, req.Params)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	row := &model.Log{
		TenantID:     tenant.ID(ctx),
		ChannelType:  channelType,
		ChannelID:    channel.ID,
		TemplateCode: template.Code,
		Recipient:    recipient,
		Title:        truncateRunes(title, 200),
		Content:      truncateRunes(content, 4000),
		RetryCount:   0,
		SentAt:       &now,
	}

	sender, lookupErr := s.registry.Lookup(channel.Provider)
	if lookupErr != nil {
		s.recordSendResult(ctx, row, SendResult{}, lookupErr)
		slog.Error("message provider unavailable",
			"channelId", channel.ID, "provider", channel.Provider, "err", lookupErr)
		return nil, badGateway(sendFailureMessage(channelType))
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	result, sendErr := sender.Send(sendCtx, SendRequest{
		Channel:      *channel,
		TemplateCode: template.Code,
		Recipient:    recipient,
		Title:        title,
		Content:      content,
		Params:       req.Params,
	})
	s.recordSendResult(ctx, row, result, sendErr)
	if sendErr != nil {
		// 详情只落 message_log 与 slog，不回显给调用方（spec §4.3.5）。
		slog.Error("message send failed",
			"channelId", channel.ID, "provider", channel.Provider,
			"channelType", channelType, "err", sendErr)
		return nil, badGateway(sendFailureMessage(channelType))
	}
	return &dto.SendResult{LogID: row.ID}, nil
}

// resolveChannel 选择发送渠道：指定 channelCode 时按编码取；否则取该类型
// 第一个启用的渠道。渠道必须存在、启用且类型匹配，否则 400。
func (s *Service) resolveChannel(ctx context.Context, channelType, code string) (*model.Channel, error) {
	if code != "" {
		row, err := s.repo.FindChannelByCode(ctx, code)
		if err != nil {
			return nil, err
		}
		if row == nil || row.Status != model.StatusEnable || row.ChannelType != channelType {
			return nil, xerr.BadRequest("消息渠道不存在或已停用")
		}
		return row, nil
	}
	list, err := s.repo.ListEnabledChannelsByType(ctx, channelType)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, xerr.BadRequest("消息渠道不存在或已停用")
	}
	return &list[0], nil
}

// recordSendResult 写入一条发送日志（成功与失败都写，spec §4.2.6 / §4.3.2）。
// 日志写入失败只告警：消息可能已经投递，不能因此让调用方重试造成重复发送。
func (s *Service) recordSendResult(ctx context.Context, row *model.Log, result SendResult, sendErr error) {
	row.ProviderMessageID = truncateRunes(result.ProviderMessageID, 128)
	if sendErr != nil {
		row.Status = model.LogStatusFailed
		row.ErrorMessage = truncateRunes(sendErr.Error(), 1000)
	} else {
		row.Status = model.LogStatusSuccess
	}
	if err := s.repo.CreateLog(ctx, row); err != nil {
		slog.Error("write message log failed",
			"channelId", row.ChannelID, "recipient", row.Recipient, "err", err)
	}
}

// sendFailureMessage 是 provider 失败的对外文案（不含内部错误与密钥）。
func sendFailureMessage(channelType string) string {
	switch channelType {
	case model.ChannelTypeSMS:
		return "短信发送失败，请稍后重试"
	case model.ChannelTypeMail:
		return "邮件发送失败，请稍后重试"
	default:
		return "消息发送失败，请稍后重试"
	}
}
