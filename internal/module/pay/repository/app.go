package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// app.go 是租户级支付应用与渠道绑定（pay_app / pay_app_channel）的数据访问。
// 查询/写入由租户插件自动追加 tenant_id 条件：跨租户业务 ID 一律不可见（404）。

// AppFilter 是应用列表查询条件。
type AppFilter struct {
	AppKey string
	Name   string
	Status *int8
}

// FindAppByID 按 ID 查应用（租户级），不存在/跨租户返回 nil。
func (r *Repository) FindAppByID(ctx context.Context, id int64) (*model.App, error) {
	var row model.App
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindAppByKey 按 app_key 查应用（租户内唯一），不存在返回 nil。
func (r *Repository) FindAppByKey(ctx context.Context, appKey string) (*model.App, error) {
	var row model.App
	if err := r.db.WithContext(ctx).Where("app_key = ?", appKey).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListApps 按筛选分页查询应用（租户级，固定 id DESC）。
func (r *Repository) ListApps(ctx context.Context, f AppFilter, page web.PageQuery) ([]model.App, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.App{})
	if f.AppKey != "" {
		q = q.Where("app_key LIKE ?", "%"+f.AppKey+"%")
	}
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.App
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateApp 创建应用；撞 uk_pay_app_tenant_key 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) CreateApp(ctx context.Context, row *model.App) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateAppFields 只更新请求列。
func (r *Repository) UpdateAppFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.App{}).
		Where("id = ?", id).Updates(fields).Error)
}

// DeleteApp 软删除应用（引用禁删校验在 service 层）。
func (r *Repository) DeleteApp(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.App{}, id).Error
}

// ---------- 渠道绑定（纯绑定关系，全量替换） ----------

// ListAppChannels 返回应用的绑定码（按 ID 升序）。
func (r *Repository) ListAppChannels(ctx context.Context, appID int64) ([]string, error) {
	var rows []model.AppChannel
	if err := r.db.WithContext(ctx).Where("app_id = ?", appID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		codes = append(codes, row.ChannelCode)
	}
	return codes, nil
}

// CountAppChannels 统计应用的绑定行数（删除前「须先解绑」校验用）。
func (r *Repository) CountAppChannels(ctx context.Context, appID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.AppChannel{}).Where("app_id = ?", appID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// ReplaceAppChannels 全量替换应用的绑定码：先删后插，调用方保证事务包裹
// （校验通过后一次提交，避免半替换状态）。撞唯一键返回 IsDuplicateEntry 可识别的错误。
//
// 删除是硬删（Unscoped）：绑定是纯关系行，唯一键 (tenant_id, app_id,
// channel_code) 不含 deleted_at——软删行会占住唯一键，重绑同码必撞 1062。
func (r *Repository) ReplaceAppChannels(ctx context.Context, appID int64, codes []string) error {
	if err := r.db.WithContext(ctx).Unscoped().Where("app_id = ?", appID).Delete(&model.AppChannel{}).Error; err != nil {
		return err
	}
	if len(codes) == 0 {
		return nil
	}
	rows := make([]model.AppChannel, 0, len(codes))
	for _, code := range codes {
		rows = append(rows, model.AppChannel{AppID: appID, ChannelCode: code})
	}
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(&rows).Error)
}
