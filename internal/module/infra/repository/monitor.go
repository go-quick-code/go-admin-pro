package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// APILogHourAgg 是按小时分桶的访问日志聚合行（issue #84）：Hour 是
// MySQL DATE_FORMAT 产出的桶起点（存储时区即应用时区，DSN loc 由
// app.timezone 生成），AvgMs 是未取整的平均耗时毫秒值。
type APILogHourAgg struct {
	Hour  string
	Total int64
	S2xx  int64
	S3xx  int64
	S4xx  int64
	S5xx  int64
	AvgMs float64
	MaxMs int64
}

// CountJobs 统计全部租户的定时任务总数与启用数（status=0）。
// 监控面板是平台级视角，跨租户聚合显式 tenant.Skip。
func (r *Repository) CountJobs(ctx context.Context) (total, enabled int64, err error) {
	err = tenant.Skip(r.db.WithContext(ctx)).Model(&model.Job{}).Count(&total).Error
	if err != nil {
		return 0, 0, err
	}
	err = tenant.Skip(r.db.WithContext(ctx)).Model(&model.Job{}).
		Where("status = ?", model.JobStatusRunning).Count(&enabled).Error
	return total, enabled, err
}

// SummarizeAPILogs 汇总 [start,end) 内全部租户的访问日志：总数、4xx/5xx
// 计数、平均与最大耗时。空区间返回零值行（COUNT=0），不视为错误。
func (r *Repository) SummarizeAPILogs(ctx context.Context, start, end time.Time) (total, clientErr, serverErr, avgMs, maxMs int64, err error) {
	var row struct {
		Total     int64   `gorm:"column:total"`
		ClientErr int64   `gorm:"column:client_err"`
		ServerErr int64   `gorm:"column:server_err"`
		AvgMs     float64 `gorm:"column:avg_ms"`
		MaxMs     *int64  `gorm:"column:max_ms"`
	}
	err = tenant.Skip(r.db.WithContext(ctx)).Model(&model.APILog{}).
		Select(
			"COUNT(*) AS total, "+
				"SUM(response_status >= 400 AND response_status < 500) AS client_err, "+
				"SUM(response_status >= 500) AS server_err, "+
				"AVG(duration_ms) AS avg_ms, MAX(duration_ms) AS max_ms",
		).
		Where("created_at >= ? AND created_at < ?", start, end).
		Scan(&row).Error
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	avg := int64(0)
	if row.AvgMs > 0 {
		avg = int64(row.AvgMs + 0.5)
	}
	if row.MaxMs != nil {
		maxMs = *row.MaxMs
	}
	return row.Total, row.ClientErr, row.ServerErr, avg, maxMs, nil
}

// AggregateAPILogsByHour 按小时分桶聚合 [start,end) 内全部租户的访问日志
// （issue #84，spec §4.10.2）：桶按整点对齐，只返回有数据的小时，按时间
// 升序。空数据返回空切片。DATE_FORMAT 为 MySQL 方言（本项目仅支持 MySQL 8）。
func (r *Repository) AggregateAPILogsByHour(ctx context.Context, start, end time.Time) ([]APILogHourAgg, error) {
	bucket := "DATE_FORMAT(created_at, '%Y-%m-%d %H:00:00')"
	var rows []APILogHourAgg
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&model.APILog{}).
		Select(
			bucket+" AS hour, COUNT(*) AS total, "+
				"SUM(response_status >= 200 AND response_status < 300) AS s2xx, "+
				"SUM(response_status >= 300 AND response_status < 400) AS s3xx, "+
				"SUM(response_status >= 400 AND response_status < 500) AS s4xx, "+
				"SUM(response_status >= 500) AS s5xx, "+
				"AVG(duration_ms) AS avg_ms, MAX(duration_ms) AS max_ms",
		).
		Where("created_at >= ? AND created_at < ?", start, end).
		Group(bucket).
		Order("hour").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
