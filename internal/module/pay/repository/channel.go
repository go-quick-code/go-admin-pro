package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
)

// channel.go 是平台级支付渠道（pay_channel）的数据访问。
// 渠道 14 行种子常驻、禁删只启停：刻意不提供 Create/Delete——
// 渠道码是编译期常量，平台不可新建渠道（spec §4.10）。

// ChannelFilter 是渠道列表查询条件。
type ChannelFilter struct {
	Code   string
	Name   string
	Status *int8
}

// FindChannelByID 按 ID 查渠道（平台级），不存在返回 nil。
func (r *Repository) FindChannelByID(ctx context.Context, id int64) (*model.Channel, error) {
	var row model.Channel
	if err := r.platform(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindChannelByCode 按编码查渠道（平台级），不存在返回 nil。
func (r *Repository) FindChannelByCode(ctx context.Context, code string) (*model.Channel, error) {
	var row model.Channel
	if err := r.platform(ctx).Where("code = ?", code).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListChannels 按筛选分页查询渠道（平台级，固定 id ASC：14 行种子顺序稳定）。
func (r *Repository) ListChannels(ctx context.Context, f ChannelFilter, offset, limit int) ([]model.Channel, int64, error) {
	q := r.platform(ctx).Model(&model.Channel{})
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
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
	var list []model.Channel
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListChannelsByCodes 按渠道码集合查渠道（绑定校验用，平台级）。
func (r *Repository) ListChannelsByCodes(ctx context.Context, codes []string) ([]model.Channel, error) {
	if len(codes) == 0 {
		return nil, nil
	}
	var list []model.Channel
	if err := r.platform(ctx).Where("code IN ?", codes).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateChannelFields 只更新请求列（不覆盖并发状态变更）。
func (r *Repository) UpdateChannelFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.platform(ctx).Model(&model.Channel{}).
		Where("id = ?", id).Updates(fields).Error)
}
