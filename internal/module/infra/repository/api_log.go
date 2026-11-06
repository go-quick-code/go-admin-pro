package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// ---------- API 访问日志（issue #80，spec §3.7/§4.6，ADR 0014）----------

// APILogFilter 是访问日志分页过滤条件。
type APILogFilter struct {
	// Path 按路由模板模糊匹配（LIKE %path%）。
	Path string
	// Status 精确匹配响应状态码；nil 表示不过滤。
	Status   *int
	Username string
	// UserID 精确匹配登录用户；0 表示不过滤。
	UserID int64
	// BeginTime/EndTime 过滤 created_at 区间（左闭右闭）；nil 表示不限制。
	BeginTime *time.Time
	EndTime   *time.Time
}

// BatchCreateAPILogs 批量写入访问日志。
//
// 后台落库 goroutine 不携带请求上下文（租户插件 fail-closed 会拦截无上下文
// 的租户表写入），而同一批条目可能来自不同租户，因此显式 tenant.Skip +
// 行内 tenant_id（中间件在采集时已从 Auth 上下文定格）。写入失败由调用方
// 记日志，绝不影响请求。
func (r *Repository) BatchCreateAPILogs(ctx context.Context, logs []model.APILog) error {
	if len(logs) == 0 {
		return nil
	}
	return tenant.Skip(r.db.WithContext(ctx)).Create(&logs).Error
}

// ListAPILogs 按筛选分页查询当前租户的访问日志（租户插件自动限定）。
func (r *Repository) ListAPILogs(ctx context.Context, f APILogFilter, offset, limit int) ([]model.APILog, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.APILog{})
	if f.Path != "" {
		q = q.Where("request_path LIKE ?", "%"+f.Path+"%")
	}
	if f.Status != nil {
		q = q.Where("response_status = ?", *f.Status)
	}
	if f.Username != "" {
		q = q.Where("username LIKE ?", "%"+f.Username+"%")
	}
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.BeginTime != nil {
		q = q.Where("created_at >= ?", *f.BeginTime)
	}
	if f.EndTime != nil {
		q = q.Where("created_at <= ?", *f.EndTime)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.APILog
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CountAPILogsByIDs 统计 ids 中属于当前租户的行数：删除前置校验，
// count != len(ids) 即存在跨租户/不存在的 ID，服务层据此返回 404，
// 不泄露具体哪个 ID 越权（spec §4.6：跨租户业务 ID 一律 404）。
func (r *Repository) CountAPILogsByIDs(ctx context.Context, ids []int64) (int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.APILog{}).
		Where("id IN ?", ids).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// DeleteAPILogs 按 IDs 硬删除当前租户的访问日志（租户插件限定）。
func (r *Repository) DeleteAPILogs(ctx context.Context, ids []int64) error {
	return r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.APILog{}).Error
}

// ClearAPILogs 清空当前租户的全部访问日志（硬删除，租户插件限定）。
func (r *Repository) ClearAPILogs(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&model.APILog{}).Error
}

// CleanAPILogsBefore 清理指定时间之前的访问日志，供 cleanAccessLogs 定时任务
// 使用。系统级维护操作：跨租户清理，显式 tenant.Skip。
func (r *Repository) CleanAPILogsBefore(ctx context.Context, before time.Time) (int64, error) {
	res := tenant.Skip(r.db.WithContext(ctx)).
		Where("created_at < ?", before).
		Delete(&model.APILog{})
	return res.RowsAffected, res.Error
}
