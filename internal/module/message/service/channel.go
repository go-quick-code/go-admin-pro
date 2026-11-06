package service

import (
	"context"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListChannels 分页查询渠道（仅平台租户）。返回的 config 已脱敏。
func (s *Service) ListChannels(ctx context.Context, req *dto.ChannelPageReq) ([]model.Channel, int64, error) {
	if err := s.requirePlatform(ctx); err != nil {
		return nil, 0, err
	}
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
	list, total, err := s.repo.ListChannels(ctx, repository.ChannelFilter{
		ChannelType: req.ChannelType,
		Code:        req.Code,
		Name:        req.Name,
		Status:      req.Status,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range list {
		list[i].Config = maskConfig(list[i].Config)
	}
	return list, total, nil
}

// CreateChannel 新建渠道（仅平台租户）。编码全局唯一（软删不释放）。
func (s *Service) CreateChannel(ctx context.Context, req *dto.ChannelCreateReq) (*model.Channel, error) {
	if err := s.requirePlatform(ctx); err != nil {
		return nil, err
	}
	channelType := strings.TrimSpace(req.ChannelType)
	if err := validateChannelType(channelType); err != nil {
		return nil, err
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	provider := strings.TrimSpace(req.Provider)
	if code == "" || name == "" || provider == "" {
		return nil, xerr.BadRequest("渠道编码、名称与 provider 不能为空")
	}
	status := int8(model.StatusEnable)
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	config, err := normalizeConfig(req.Config)
	if err != nil {
		return nil, configValidationError(err)
	}
	if err := s.checkChannelCodeFree(ctx, code, 0); err != nil {
		return nil, err
	}

	row := &model.Channel{
		ChannelType: channelType,
		Code:        code,
		Name:        name,
		Provider:    provider,
		Config:      config,
		Status:      status,
		Remark:      req.Remark,
	}
	if err := s.repo.CreateChannel(ctx, row); err != nil {
		if repository.IsDuplicateEntry(err) {
			return nil, xerr.BadRequest("渠道编码已存在")
		}
		return nil, err
	}
	// 创建响应同样脱敏：密钥只进不出。
	row.Config = maskConfig(row.Config)
	return row, nil
}

// UpdateChannel 修改渠道（仅平台租户）。config 中的 ****** 保留原密钥值。
func (s *Service) UpdateChannel(ctx context.Context, req *dto.ChannelUpdateReq) error {
	if err := s.requirePlatform(ctx); err != nil {
		return err
	}
	row, err := s.findChannel(ctx, req.ID)
	if err != nil {
		return err
	}
	channelType := strings.TrimSpace(req.ChannelType)
	if err := validateChannelType(channelType); err != nil {
		return err
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	provider := strings.TrimSpace(req.Provider)
	if code == "" || name == "" || provider == "" {
		return xerr.BadRequest("渠道编码、名称与 provider 不能为空")
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return err
		}
	}
	config, err := mergeSecretConfig(row.Config, req.Config)
	if err != nil {
		return configValidationError(err)
	}
	if err := s.checkChannelCodeFree(ctx, code, row.ID); err != nil {
		return err
	}

	fields := map[string]any{
		"channel_type": channelType,
		"code":         code,
		"name":         name,
		"provider":     provider,
		"config":       config,
		"remark":       req.Remark,
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if err := s.repo.UpdateChannelFields(ctx, row.ID, fields); err != nil {
		if repository.IsDuplicateEntry(err) {
			return xerr.BadRequest("渠道编码已存在")
		}
		return err
	}
	return nil
}

// UpdateChannelStatus 只修改渠道状态（0 启用 / 1 停用）。
func (s *Service) UpdateChannelStatus(ctx context.Context, id int64, req *dto.StatusReq) error {
	if err := s.requirePlatform(ctx); err != nil {
		return err
	}
	if req.Status == nil {
		return xerr.BadRequest("状态不能为空：期望 0 启用 / 1 停用")
	}
	if err := validateStatus(*req.Status); err != nil {
		return err
	}
	row, err := s.findChannel(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.UpdateChannelStatus(ctx, row.ID, *req.Status)
}

// DeleteChannel 软删除渠道（仅平台租户）。历史发送日志保留 channel_id，不受影响。
func (s *Service) DeleteChannel(ctx context.Context, id int64) error {
	if err := s.requirePlatform(ctx); err != nil {
		return err
	}
	row, err := s.findChannel(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteChannel(ctx, row.ID)
}

// findChannel 取渠道并做存在性校验（不存在/已删除统一 404）。
func (s *Service) findChannel(ctx context.Context, id int64) (*model.Channel, error) {
	row, err := s.repo.FindChannelByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("消息渠道不存在")
	}
	return row, nil
}

// checkChannelCodeFree 校验渠道编码未被占用（selfID 非 0 时排除自身）。
// 预检看不到软删行，软删行占码由唯一键兜底。
func (s *Service) checkChannelCodeFree(ctx context.Context, code string, selfID int64) error {
	row, err := s.repo.FindChannelByCode(ctx, code)
	if err != nil {
		return err
	}
	if row != nil && row.ID != selfID {
		return xerr.BadRequest("渠道编码已存在：" + code)
	}
	return nil
}
