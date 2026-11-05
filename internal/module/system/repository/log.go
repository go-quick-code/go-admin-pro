package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

type LoginLogFilter struct {
	Username string
	LoginIP  string
	Result   *int8
}

func (r *Repository) CreateLoginLog(ctx context.Context, log *model.LoginLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *Repository) ListLoginLogs(ctx context.Context, f LoginLogFilter, offset, limit int) ([]model.LoginLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.LoginLog{})
	if f.Username != "" {
		q = q.Where("username LIKE ?", "%"+f.Username+"%")
	}
	if f.LoginIP != "" {
		q = q.Where("login_ip LIKE ?", "%"+f.LoginIP+"%")
	}
	if f.Result != nil {
		q = q.Where("result = ?", *f.Result)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.LoginLog
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) ClearLoginLogs(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&model.LoginLog{}).Error
}

type OperateLogFilter struct {
	Username string
	Module   string
	Status   *int8
}

func (r *Repository) CreateOperateLog(ctx context.Context, log *model.OperateLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *Repository) ListOperateLogs(ctx context.Context, f OperateLogFilter, offset, limit int) ([]model.OperateLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.OperateLog{})
	if f.Username != "" {
		q = q.Where("username LIKE ?", "%"+f.Username+"%")
	}
	if f.Module != "" {
		q = q.Where("module LIKE ?", "%"+f.Module+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.OperateLog
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) DeleteOperateLogs(ctx context.Context, ids []int64) error {
	return r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.OperateLog{}).Error
}

func (r *Repository) ClearOperateLogs(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&model.OperateLog{}).Error
}

// CleanLogsBefore 清理指定时间之前的日志，供 cleanLogs 定时任务使用。
// 这是系统级维护操作，需要跨租户清理，这里显式 tenant.Skip。
func (r *Repository) CleanLogsBefore(ctx context.Context, before time.Time) error {
	if err := tenant.Skip(r.db.WithContext(ctx)).Where("login_time < ?", before).Delete(&model.LoginLog{}).Error; err != nil {
		return err
	}
	return tenant.Skip(r.db.WithContext(ctx)).Where("operate_time < ?", before).Delete(&model.OperateLog{}).Error
}
