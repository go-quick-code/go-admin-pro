package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// LogStatusCount 是按渠道×状态聚合的消息日志计数行（issue #84）。
type LogStatusCount struct {
	ChannelType string `gorm:"column:channel_type"`
	Status      int8   `gorm:"column:status"`
	Total       int64  `gorm:"column:total"`
}

// CountLogsByStatusSince 统计 since 以来全部租户的消息发送日志，按
// 渠道×状态聚合。监控面板是平台级视角，跨租户显式 tenant.Skip；
// 时间下界由调用方按应用时区计算后传入（与配额查询同口径）。
func (r *Repository) CountLogsByStatusSince(ctx context.Context, since time.Time) ([]LogStatusCount, error) {
	var rows []LogStatusCount
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&model.Log{}).
		Select("channel_type, status, COUNT(*) AS total").
		Where("created_at >= ?", since).
		Group("channel_type, status").
		Scan(&rows).Error
	return rows, err
}
