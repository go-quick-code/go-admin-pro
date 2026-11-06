package service

import (
	"context"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/dto"
)

// TestChannel 渠道测试发送（POST /message/channel/:id/test，issue #73）：
// 平台租户用指定渠道向收件人投递一条真实消息，验证渠道配置是否可用。
//
// 与普通发送的差异：
//   - 渠道按 ID 显式指定，且不做「启用」检查——管理员可以在渠道停用状态
//     下先行验证配置，其余规则（模板启用、参数一致、超时、写日志）与
//     spec §4.3.2 保持一致；
//   - 仅平台租户可用（渠道是平台级配置，密钥只对平台运营方开放）。
func (s *Service) TestChannel(ctx context.Context, id int64, req *dto.ChannelTestReq) (*dto.SendResult, error) {
	if err := s.requirePlatform(ctx); err != nil {
		return nil, err
	}
	channel, err := s.findChannel(ctx, id)
	if err != nil {
		return nil, err
	}

	recipient := strings.TrimSpace(req.Recipient)
	if err := validateRecipient(channel.ChannelType, recipient); err != nil {
		return nil, err
	}

	template, err := s.findEnabledTemplate(ctx, channel.ChannelType, strings.TrimSpace(req.TemplateCode))
	if err != nil {
		return nil, err
	}

	return s.deliver(ctx, channel, template, recipient, req.Params)
}
