package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
)

// SendStats 是消息发送结果统计（分渠道的成功/失败计数，issue #84）。
// 中性类型：internal/app 的监控统计适配器把它映射为 infra 侧的响应 DTO，
// message 不感知 infra。
type SendStats struct {
	SMSSuccess  int64
	SMSFailed   int64
	MailSuccess int64
	MailFailed  int64
}

// SendStatsSince 返回 since 以来全部租户的消息发送成功/失败数
// （spec §4.10.1：近 24h 短信/邮件发送成功/失败数）。供 internal/app 的
// 监控统计适配器调用；时间下界由调用方按应用时区计算后传入。
// 未知渠道（如后续新增渠道）不进这四个字段，当前监控面板只覆盖短信/邮件。
func (s *Service) SendStatsSince(ctx context.Context, since time.Time) (SendStats, error) {
	rows, err := s.repo.CountLogsByStatusSince(ctx, since)
	if err != nil {
		return SendStats{}, err
	}
	var stats SendStats
	for _, row := range rows {
		switch {
		case row.ChannelType == model.ChannelTypeSMS && row.Status == model.LogStatusSuccess:
			stats.SMSSuccess = row.Total
		case row.ChannelType == model.ChannelTypeSMS && row.Status == model.LogStatusFailed:
			stats.SMSFailed = row.Total
		case row.ChannelType == model.ChannelTypeMail && row.Status == model.LogStatusSuccess:
			stats.MailSuccess = row.Total
		case row.ChannelType == model.ChannelTypeMail && row.Status == model.LogStatusFailed:
			stats.MailFailed = row.Total
		}
	}
	return stats, nil
}
