package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListTemplates 分页查询模板（仅平台租户）。
func (s *Service) ListTemplates(ctx context.Context, req *dto.TemplatePageReq) ([]dto.TemplateVO, int64, error) {
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
	list, total, err := s.repo.ListTemplates(ctx, repository.TemplateFilter{
		ChannelType: req.ChannelType,
		Code:        req.Code,
		Name:        req.Name,
		Status:      req.Status,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	vos := make([]dto.TemplateVO, 0, len(list))
	for _, row := range list {
		vos = append(vos, templateVO(row))
	}
	return vos, total, nil
}

// CreateTemplate 新建模板（仅平台租户）。内容中的占位符必须在参数声明中。
func (s *Service) CreateTemplate(ctx context.Context, req *dto.TemplateCreateReq) (*dto.TemplateVO, error) {
	if err := s.requirePlatform(ctx); err != nil {
		return nil, err
	}
	channelType := strings.TrimSpace(req.ChannelType)
	if err := validateChannelType(channelType); err != nil {
		return nil, err
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		return nil, xerr.BadRequest("模板编码与名称不能为空")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, xerr.BadRequest("模板内容不能为空")
	}
	params, err := prepareTemplateParams(channelType, req.Title, req.Content, req.Params)
	if err != nil {
		return nil, err
	}
	status := int8(model.StatusEnable)
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	if err := s.checkTemplateCodeFree(ctx, channelType, code, 0); err != nil {
		return nil, err
	}

	row := &model.Template{
		ChannelType: channelType,
		Code:        code,
		Name:        name,
		Title:       req.Title,
		Content:     req.Content,
		Params:      params,
		Status:      status,
		Remark:      req.Remark,
	}
	if err := s.repo.CreateTemplate(ctx, row); err != nil {
		if repository.IsDuplicateEntry(err) {
			return nil, xerr.BadRequest("同类型下的模板编码已存在")
		}
		return nil, err
	}
	vo := templateVO(*row)
	return &vo, nil
}

// UpdateTemplate 修改模板（全量覆盖，仅平台租户）。
func (s *Service) UpdateTemplate(ctx context.Context, req *dto.TemplateUpdateReq) error {
	if err := s.requirePlatform(ctx); err != nil {
		return err
	}
	row, err := s.findTemplate(ctx, req.ID)
	if err != nil {
		return err
	}
	channelType := strings.TrimSpace(req.ChannelType)
	if err := validateChannelType(channelType); err != nil {
		return err
	}
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		return xerr.BadRequest("模板编码与名称不能为空")
	}
	if strings.TrimSpace(req.Content) == "" {
		return xerr.BadRequest("模板内容不能为空")
	}
	params, err := prepareTemplateParams(channelType, req.Title, req.Content, req.Params)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return err
		}
	}
	if err := s.checkTemplateCodeFree(ctx, channelType, code, row.ID); err != nil {
		return err
	}

	fields := map[string]any{
		"channel_type": channelType,
		"code":         code,
		"name":         name,
		"title":        req.Title,
		"content":      req.Content,
		"params":       params,
		"remark":       req.Remark,
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if err := s.repo.UpdateTemplateFields(ctx, row.ID, fields); err != nil {
		if repository.IsDuplicateEntry(err) {
			return xerr.BadRequest("同类型下的模板编码已存在")
		}
		return err
	}
	return nil
}

// DeleteTemplate 软删除模板（仅平台租户）。
func (s *Service) DeleteTemplate(ctx context.Context, id int64) error {
	if err := s.requirePlatform(ctx); err != nil {
		return err
	}
	row, err := s.findTemplate(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteTemplate(ctx, row.ID)
}

// findTemplate 取模板并做存在性校验（不存在/已删除统一 404）。
func (s *Service) findTemplate(ctx context.Context, id int64) (*model.Template, error) {
	row, err := s.repo.FindTemplateByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("消息模板不存在")
	}
	return row, nil
}

// checkTemplateCodeFree 校验 (channelType, code) 未被占用（selfID 非 0 时排除自身）。
func (s *Service) checkTemplateCodeFree(ctx context.Context, channelType, code string, selfID int64) error {
	row, err := s.repo.FindTemplateByCode(ctx, channelType, code)
	if err != nil {
		return err
	}
	if row != nil && row.ID != selfID {
		return xerr.BadRequest("同类型下的模板编码已存在：" + code)
	}
	return nil
}

// prepareTemplateParams 校验参数声明与内容占位符，返回落库的 JSON 文本。
// 短信模板不允许 title（spec §3.3：邮件主题，短信为空）。
func prepareTemplateParams(channelType, title, content string, params []string) (string, error) {
	normalized, err := validateTemplateParams(params)
	if err != nil {
		return "", err
	}
	if channelType == model.ChannelTypeSMS && strings.TrimSpace(title) != "" {
		return "", xerr.BadRequest("短信模板不支持标题，请留空")
	}
	if err := validateTemplateContent(normalized, title, content); err != nil {
		return "", err
	}
	return encodeTemplateParams(normalized)
}

// templateVO 把实体转换为 API 结构：参数声明从 JSON 文本还原为数组。
func templateVO(row model.Template) dto.TemplateVO {
	params, err := decodeTemplateParams(row.Params)
	if err != nil {
		// 库内数据异常不应让整页 500：降级为空数组并告警。
		slog.Warn("message template params invalid", "templateId", row.ID, "err", err)
		params = []string{}
	}
	return dto.TemplateVO{
		ID:          row.ID,
		ChannelType: row.ChannelType,
		Code:        row.Code,
		Name:        row.Name,
		Title:       row.Title,
		Content:     row.Content,
		Params:      params,
		Status:      row.Status,
		Remark:      row.Remark,
		CreateTime:  row.CreatedAt,
		UpdateTime:  row.UpdatedAt,
	}
}
