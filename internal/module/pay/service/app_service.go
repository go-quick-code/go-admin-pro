package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// app_service.go 是租户级支付应用与渠道绑定（docs/pay-mvp-spec.md §3.1/§4.1/
// §4.10/§5）：应用 CRUD；绑定全量替换（校验「码 ∈ 14 渠道码常量 ∧ 平台渠道
// 启用」）；删除守卫（绑定须先解绑 + 引用检查扩展点）。

// ListApps 分页查询应用（租户级，固定 id DESC）。
func (s *Service) ListApps(ctx context.Context, req *dto.AppPageReq) ([]model.App, int64, error) {
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListApps(ctx, payrepo.AppFilter{
		AppKey: req.AppKey,
		Name:   req.Name,
		Status: req.Status,
	}, page)
}

// GetApp 应用详情（附绑定码清单）。
func (s *Service) GetApp(ctx context.Context, id int64) (*dto.AppDetail, error) {
	row, err := s.findApp(ctx, id)
	if err != nil {
		return nil, err
	}
	codes, err := s.repo.ListAppChannels(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return &dto.AppDetail{App: row, ChannelCodes: codes}, nil
}

// CreateApp 创建应用：app_key 租户内唯一（撞唯一键 400，照 message 渠道编码先例）。
// 客户端提交的 no/ID 等服务端字段不存在，DTO 不收。
func (s *Service) CreateApp(ctx context.Context, req *dto.AppSaveReq) (*model.App, error) {
	appKey, name, err := validateAppReq(req)
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
	row := &model.App{
		AppKey:            appKey,
		Name:              name,
		Status:            status,
		Remark:            req.Remark,
		OrderNotifyURL:    req.OrderNotifyURL,
		RefundNotifyURL:   req.RefundNotifyURL,
		TransferNotifyURL: req.TransferNotifyURL,
	}
	if err := s.repo.CreateApp(ctx, row); err != nil {
		if payrepo.IsDuplicateEntry(err) {
			return nil, xerr.BadRequest("应用标识已存在：" + appKey)
		}
		return nil, err
	}
	return row, nil
}

// UpdateApp 修改应用（id 在 body，与 CRM/ProTable 一致）。app_key 可改，
// 唯一键兜底并发冲突。
func (s *Service) UpdateApp(ctx context.Context, req *dto.AppUpdateReq) error {
	appKey, name, err := validateAppReq(&req.AppSaveReq)
	if err != nil {
		return err
	}
	row, err := s.findApp(ctx, req.ID)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"app_key":             appKey,
		"name":                name,
		"remark":              req.Remark,
		"order_notify_url":    req.OrderNotifyURL,
		"refund_notify_url":   req.RefundNotifyURL,
		"transfer_notify_url": req.TransferNotifyURL,
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return err
		}
		fields["status"] = *req.Status
	}
	if err := s.repo.UpdateAppFields(ctx, row.ID, fields); err != nil {
		if payrepo.IsDuplicateEntry(err) {
			return xerr.BadRequest("应用标识已存在：" + appKey)
		}
		return err
	}
	return nil
}

// DeleteApp 删除应用（软删）：有渠道绑定须先解绑（409）；业务引用禁删的
// 检查经 AppRefChecker 扩展点（#133+ 注册，spec §4.10——本票骨架见 app_ref.go）。
func (s *Service) DeleteApp(ctx context.Context, id int64) error {
	row, err := s.findApp(ctx, id)
	if err != nil {
		return err
	}
	bindings, err := s.repo.CountAppChannels(ctx, row.ID)
	if err != nil {
		return err
	}
	if bindings > 0 {
		return xerr.Conflict("应用存在渠道绑定，请先解绑再删除")
	}
	if s.appRefCheckers != nil {
		referenced, ref, err := s.appRefCheckers.AppHasReference(ctx, row.ID)
		if err != nil {
			return err
		}
		if referenced {
			return xerr.Conflict("应用存在" + ref + "引用，禁止删除")
		}
	}
	return s.repo.DeleteApp(ctx, row.ID)
}

// ListAppChannels 返回应用已绑定的渠道码。
func (s *Service) ListAppChannels(ctx context.Context, appID int64) ([]string, error) {
	if _, err := s.findApp(ctx, appID); err != nil {
		return nil, err
	}
	return s.repo.ListAppChannels(ctx, appID)
}

// ReplaceAppChannels 全量替换应用的渠道绑定（spec §5：PUT /pay/app/:id/channels）。
// 校验（spec §4.1）：码 ∈ 14 渠道码常量（未知码 409）∧ 平台渠道启用（停用 409）
// ∧ 渠道已实现（注册表无 Client 409 渠道未实现，#133）。
func (s *Service) ReplaceAppChannels(ctx context.Context, appID int64, codes []string) ([]string, error) {
	row, err := s.findApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	normalized, err := s.validateBindingCodes(ctx, codes)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceAppChannels(ctx, row.ID, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// validateBindingCodes 校验并规整绑定码：去重保序、逐个查常量与平台启停。
func (s *Service) validateBindingCodes(ctx context.Context, codes []string) ([]string, error) {
	seen := make(map[string]struct{}, len(codes))
	normalized := make([]string, 0, len(codes))
	for _, code := range codes {
		code = trimString(code)
		if code == "" {
			continue
		}
		if !channel.IsKnownCode(code) {
			return nil, xerr.Conflict("未知支付渠道码：" + code)
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		normalized = append(normalized, code)
	}
	if len(normalized) == 0 {
		return normalized, nil
	}
	rows, err := s.repo.ListChannelsByCodes(ctx, normalized)
	if err != nil {
		return nil, err
	}
	statusByCode := make(map[string]int8, len(rows))
	for _, row := range rows {
		statusByCode[row.Code] = row.Status
	}
	for _, code := range normalized {
		status, ok := statusByCode[code]
		if !ok {
			// 14 个常量码由种子预插；查询不到说明种子缺失，按停用拦截。
			return nil, xerr.Conflict("支付渠道已停用：" + code)
		}
		if status != model.StatusEnable {
			return nil, xerr.Conflict("支付渠道已停用：" + channel.Name(code) + "（" + code + "）")
		}
		if !s.channelRegistry().Known(code) {
			// 平台启用但未实现（注册表无 Client）：绑定即拒绝（spec §4.1，
			// #133——渠道码常量仍是唯一码源，实现与否由注册表判定）。
			return nil, xerr.Conflict("渠道未实现：" + channel.Name(code) + "（" + code + "）")
		}
	}
	return normalized, nil
}

// findApp 取应用并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findApp(ctx context.Context, id int64) (*model.App, error) {
	row, err := s.repo.FindAppByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("支付应用不存在")
	}
	return row, nil
}

// findAppByKey 按 app_key 取应用（下单/收银台的消费方凭据，spec §4.2）；
// 不存在/跨租户统一 404。
func (s *Service) findAppByKey(ctx context.Context, appKey string) (*model.App, error) {
	row, err := s.repo.FindAppByKey(ctx, appKey)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("支付应用不存在")
	}
	return row, nil
}

// validateAppReq 校验应用公共字段：app_key/name 必填（去首尾空白）。
func validateAppReq(req *dto.AppSaveReq) (appKey, name string, err error) {
	appKey = trimString(req.AppKey)
	name = trimString(req.Name)
	if appKey == "" || name == "" {
		return "", "", xerr.BadRequest("应用标识与应用名称不能为空")
	}
	return appKey, name, nil
}
