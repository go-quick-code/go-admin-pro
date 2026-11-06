package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// ListLogs 分页查询当前租户的发送日志（租户插件自动隔离）。
func (s *Service) ListLogs(ctx context.Context, req *dto.LogPageReq) ([]model.Log, int64, error) {
	if req.ChannelType != "" {
		if err := validateChannelType(req.ChannelType); err != nil {
			return nil, 0, err
		}
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListLogs(ctx, repository.LogFilter{
		ChannelType:  req.ChannelType,
		TemplateCode: req.TemplateCode,
		Recipient:    req.Recipient,
		Status:       req.Status,
	}, page.Offset(), page.PageSize)
}

// ResendLog 手动重发失败日志（spec §4.3.7）：只允许失败行，重发更新原行
// 的 status/error_message/provider_message_id/sent_at/retry_count，不新增行。
func (s *Service) ResendLog(ctx context.Context, id int64) error {
	row, err := s.repo.FindLogByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("发送日志不存在")
	}
	if row.Status != model.LogStatusFailed {
		return xerr.BadRequest("只有发送失败的日志可以重发")
	}

	channel, err := s.repo.FindChannelByID(ctx, row.ChannelID)
	if err != nil {
		return err
	}
	if channel == nil || channel.Status != model.StatusEnable || channel.ChannelType != row.ChannelType {
		attemptErr := errors.New("原发送渠道不存在或已停用")
		s.recordRetry(ctx, row, SendResult{}, attemptErr)
		slog.Error("message resend channel unavailable", "logId", row.ID, "channelId", row.ChannelID)
		return badGateway(sendFailureMessage(row.ChannelType))
	}

	sender, lookupErr := s.registry.Lookup(channel.Provider)
	if lookupErr != nil {
		s.recordRetry(ctx, row, SendResult{}, lookupErr)
		slog.Error("message resend provider unavailable",
			"logId", row.ID, "provider", channel.Provider, "err", lookupErr)
		return badGateway(sendFailureMessage(row.ChannelType))
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	result, sendErr := sender.Send(sendCtx, SendRequest{
		Channel:      *channel,
		TemplateCode: row.TemplateCode,
		Recipient:    row.Recipient,
		Title:        row.Title,
		Content:      row.Content,
	})
	s.recordRetry(ctx, row, result, sendErr)
	if sendErr != nil {
		slog.Error("message resend failed",
			"logId", row.ID, "provider", channel.Provider, "err", sendErr)
		return badGateway(sendFailureMessage(row.ChannelType))
	}
	return nil
}

// recordRetry 把一次重发结果写回原日志行（retry_count 自增）。
// 日志更新失败只告警：消息可能已经投递，不能因此让调用方重试造成重复发送。
func (s *Service) recordRetry(ctx context.Context, row *model.Log, result SendResult, sendErr error) {
	now := time.Now()
	status := int8(model.LogStatusSuccess)
	errorMessage := ""
	if sendErr != nil {
		status = model.LogStatusFailed
		errorMessage = truncateRunes(sendErr.Error(), 1000)
	}
	fields := map[string]any{
		"status":              status,
		"error_message":       errorMessage,
		"provider_message_id": truncateRunes(result.ProviderMessageID, 128),
		"sent_at":             &now,
		"retry_count":         gorm.Expr("retry_count + 1"),
	}
	if err := s.repo.UpdateLogAttempt(ctx, row.ID, fields); err != nil {
		slog.Error("update message log retry failed", "logId", row.ID, "err", err)
	}
}

// DeleteLog 删除当前租户的一条发送日志（跨租户/不存在 404）。
func (s *Service) DeleteLog(ctx context.Context, id int64) error {
	row, err := s.repo.FindLogByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("发送日志不存在")
	}
	return s.repo.DeleteLog(ctx, row.ID)
}

// ClearLogs 清空当前租户的全部发送日志。
func (s *Service) ClearLogs(ctx context.Context) error {
	return s.repo.ClearLogs(ctx)
}
