// pool.go 承载公海纵切（issue #13）的数据访问：
// 释放/认领/转移的条件更新（乐观并发）、负责人历史写入与分页查询。
// 所有语句经租户插件自动带 tenant_id 条件；NULL 语义说明见各方法注释。
package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
)

// CountCustomersByOwner 统计负责人名下未删除客户总数（认领上限口径，
// 规格 §4.2：owner_user_id = 用户 的客户总数，不区分是否成交）。
func (r *Repository) CountCustomersByOwner(ctx context.Context, ownerUserID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Customer{}).
		Where("owner_user_id = ?", ownerUserID).
		Count(&total).Error
	return total, err
}

// ReleaseCustomerToPool 把客户释放回公海（issue #13）。
// 条件更新即乐观并发：仅当负责人仍为 fromOwnerUserID 时生效，返回受影响行数，
// 0 表示客户不存在（含租户外）或负责人已被并发操作变更，由 service 层翻译为冲突。
// owner/部门快照以 map 显式写 NULL（结构体更新会跳过零值，无法表达 NULL），
// 同时落池字段 pool_enter_time 与 release_reason。
func (r *Repository) ReleaseCustomerToPool(ctx context.Context, id, fromOwnerUserID int64, enterTime time.Time, reason string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Customer{}).
		Where("id = ? AND owner_user_id = ?", id, fromOwnerUserID).
		Updates(map[string]any{
			"owner_user_id":   nil,
			"owner_dept_id":   nil,
			"pool_enter_time": enterTime,
			"release_reason":  reason,
		})
	return res.RowsAffected, res.Error
}

// ClaimCustomerFromPool 从公海认领客户（issue #13）。
// 条件 owner_user_id IS NULL 保证并发认领只成功一个（行锁串行化，其余拿到 0 行），
// 写入新负责人与部门快照并清空池字段，返回受影响行数。
func (r *Repository) ClaimCustomerFromPool(ctx context.Context, id, ownerUserID, ownerDeptID int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Customer{}).
		Where("id = ? AND owner_user_id IS NULL", id).
		Updates(map[string]any{
			"owner_user_id":   ownerUserID,
			"owner_dept_id":   ownerDeptID,
			"pool_enter_time": nil,
			"release_reason":  "",
		})
	return res.RowsAffected, res.Error
}

// TransferCustomerOwner 把客户负责人转移给新用户（issue #13）。
// 条件更新同 Release：仅当负责人仍为 fromOwnerUserID 时生效；不改池字段
// （公海客户不允许转移，service 层已拦截）。
func (r *Repository) TransferCustomerOwner(ctx context.Context, id, fromOwnerUserID, toOwnerUserID, toOwnerDeptID int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Customer{}).
		Where("id = ? AND owner_user_id = ?", id, fromOwnerUserID).
		Updates(map[string]any{
			"owner_user_id": toOwnerUserID,
			"owner_dept_id": toOwnerDeptID,
		})
	return res.RowsAffected, res.Error
}

// CreateOwnerHistory 追加一条负责人历史（追加型表，只写不改不删）。
func (r *Repository) CreateOwnerHistory(ctx context.Context, h *model.CustomerOwnerHistory) error {
	return r.db.WithContext(ctx).Create(h).Error
}

// ListOwnerHistory 按客户分页查负责人历史，新→旧（id DESC）。
func (r *Repository) ListOwnerHistory(ctx context.Context, customerID int64, offset, limit int) ([]model.CustomerOwnerHistory, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.CustomerOwnerHistory{}).
		Where("customer_id = ?", customerID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.CustomerOwnerHistory
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
