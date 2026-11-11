package service

import (
	"context"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// channel_service.go 是平台级渠道管理（docs/pay-mvp-spec.md §3.1/§5/§4.10）：
// 14 行种子常驻、禁删只启停；凭证三段式（查询脱敏、更新占位符合并）。
// 「仅平台租户」在 requirePlatform 收口（非平台租户即使持 pay:channel:* 也 403），
// 照 message_channel 先例。

// ListChannels 平台侧分页查询渠道：返回的 config 已脱敏（密钥只进不出）。
func (s *Service) ListChannels(ctx context.Context, req *dto.ChannelPageReq) ([]model.Channel, int64, error) {
	if err := s.requirePlatform(ctx); err != nil {
		return nil, 0, err
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	list, total, err := s.repo.ListChannels(ctx, payrepo.ChannelFilter{
		Code:   req.Code,
		Name:   req.Name,
		Status: req.Status,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range list {
		list[i].Config = maskConfig(list[i].Config)
	}
	return list, total, nil
}

// UpdateChannel 修改渠道（仅平台租户）：只启停 + 凭证（占位符合并）+ 备注。
// 渠道码/名称是种子常量不可改，fee_rate 纯记录 v1 恒 0 不开放编辑。
func (s *Service) UpdateChannel(ctx context.Context, id int64, req *dto.ChannelUpdateReq) error {
	if err := s.requirePlatform(ctx); err != nil {
		return err
	}
	row, err := s.findChannel(ctx, id)
	if err != nil {
		return err
	}
	fields := map[string]any{"remark": req.Remark}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return err
		}
		fields["status"] = *req.Status
	}
	// config 语义：nil 表示不动；非 nil 时必须是合法 JSON 对象并做占位符合并。
	if req.Config != nil {
		config, err := mergeSecretConfig(row.Config, *req.Config)
		if err != nil {
			return configValidationError(err)
		}
		fields["config"] = config
	}
	return s.repo.UpdateChannelFields(ctx, row.ID, fields)
}

// ListChannelOptions 租户侧只读渠道清单（spec §5：code/名称/状态，无凭证）：
// 应用详情的渠道绑定 tab 数据源。登录 + pay:app:query 即可读，无凭证字段。
func (s *Service) ListChannelOptions(ctx context.Context) ([]dto.ChannelOption, error) {
	list, err := s.repo.ListChannelsByCodes(ctx, channel.AllCodes())
	if err != nil {
		return nil, err
	}
	options := make([]dto.ChannelOption, 0, len(list))
	for _, row := range list {
		options = append(options, dto.ChannelOption{
			Code:   row.Code,
			Name:   row.Name,
			Status: row.Status,
		})
	}
	return options, nil
}

// findChannel 取渠道并做存在性校验（不存在/已删除统一 404）。
func (s *Service) findChannel(ctx context.Context, id int64) (*model.Channel, error) {
	row, err := s.repo.FindChannelByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("支付渠道不存在")
	}
	return row, nil
}

// validateStatus 校验启停状态枚举（0 启用 / 1 停用）。
func validateStatus(status int8) error {
	if status != model.StatusEnable && status != model.StatusDisable {
		return xerr.BadRequest("状态不合法：期望 0 启用 / 1 停用")
	}
	return nil
}

// trimString 去首尾空白（nil 安全）。
func trimString(v string) string { return strings.TrimSpace(v) }
