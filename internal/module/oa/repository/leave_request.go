// leave_request.go 承载请假申请的数据访问（issue #52，spec §3.5/§4.8）：
// 草稿的创建/更新/软删、本人分页、行锁读与「同用户待审/已通过」区间查询
// （槽位重叠判定的取数入口）。oa_leave_request 已登记 TenantTables，租户
// 条件由插件自动追加；提交与审批回调的写路径用事务内行锁串行化。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateLeaveRequest 落一条请假申请（草稿）；tenant_id 由插件回填。
func (r *Repository) CreateLeaveRequest(ctx context.Context, row *model.LeaveRequest) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建请假申请: %w", err)
	}
	return nil
}

// SaveLeaveRequest 更新申请的全部可变字段（状态、时长、驳回原因、实例 ID）。
func (r *Repository) SaveLeaveRequest(ctx context.Context, row *model.LeaveRequest) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新请假申请: %w", err)
	}
	return nil
}

// FindLeaveRequest 按 ID 读申请；未命中（含跨租户）返回 nil。
func (r *Repository) FindLeaveRequest(ctx context.Context, id int64) (*model.LeaveRequest, error) {
	var row model.LeaveRequest
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindLeaveRequestForUpdate 行锁读申请（提交/审批回调的状态迁移入口）。
func (r *Repository) FindLeaveRequestForUpdate(ctx context.Context, id int64) (*model.LeaveRequest, error) {
	var row model.LeaveRequest
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// DeleteLeaveRequest 软删申请（service 已校验仅草稿/已驳回可删）。
func (r *Repository) DeleteLeaveRequest(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.LeaveRequest{}, id)
	if result.Error != nil {
		return false, fmt.Errorf("删除请假申请: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// PageLeaveRequestsByScope 按可见范围分页（start_date 倒序，issue #53）：
// userIDs 为 nil 表示全租户（oa:leave:manage）；非 nil 时按 user_id IN
// 过滤（部门负责人子树 ∪ 本人），空集合直接返回空页（fail-closed）。
// status 可选精确过滤。租户条件由插件自动追加，跨租户行不会命中。
func (r *Repository) PageLeaveRequestsByScope(ctx context.Context, userIDs []int64, status *int8, offset, limit int) ([]model.LeaveRequest, int64, error) {
	if userIDs != nil && len(userIDs) == 0 {
		return []model.LeaveRequest{}, 0, nil
	}
	q := r.db.WithContext(ctx).Model(&model.LeaveRequest{})
	if userIDs != nil {
		q = q.Where("`user_id` IN ?", userIDs)
	}
	if status != nil {
		q = q.Where("`status` = ?", *status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计请假申请: %w", err)
	}
	var rows []model.LeaveRequest
	if err := q.Order("`start_date` DESC, `id` DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询请假申请: %w", err)
	}
	return rows, total, nil
}

// ListActiveLeaveRequestsInRange 读同用户与 [startKey, endKey] 相交的待审/
// 已通过申请（日期闭区间相交：start_date <= end AND end_date >= start）。
// 槽位级重叠由纯函数 SlotsOverlap 判定；excludeID 用于重提交时排除自身。
func (r *Repository) ListActiveLeaveRequestsInRange(ctx context.Context, userID int64, startKey, endKey string, excludeID int64) ([]model.LeaveRequest, error) {
	q := r.db.WithContext(ctx).
		Where("`user_id` = ? AND `status` IN ?", userID, []int8{model.LeaveRequestStatusPending, model.LeaveRequestStatusApproved}).
		Where("`start_date` <= ? AND `end_date` >= ?", endKey, startKey)
	if excludeID > 0 {
		q = q.Where("`id` <> ?", excludeID)
	}
	var rows []model.LeaveRequest
	if err := q.Order("`start_date` ASC, `id` ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询同一用户请假时段: %w", err)
	}
	return rows, nil
}

// DB 返回底层 gorm 句柄：业务服务把事务句柄交给 bpm（StartInstanceInTx）与
// 事务内回调（BusinessHandler 的 tx 入参）时使用，保证业务写与审批同事务。
func (r *Repository) DB() *gorm.DB { return r.db }

// ListApprovedLeaveRequestsCoveringDate 读某用户覆盖指定日期的**已通过**请假
// 申请（issue #62，考勤结果派生的请假天数取数）：日期闭区间相交
// （start_date <= date AND end_date >= date）。同一天可有多条（不同半天严格
// 不重叠），逐日天数由纯函数 LeaveDaysOnDate 裁剪；租户条件由插件自动追加。
func (r *Repository) ListApprovedLeaveRequestsCoveringDate(ctx context.Context, userID int64, dateKey string) ([]model.LeaveRequest, error) {
	var rows []model.LeaveRequest
	if err := r.db.WithContext(ctx).
		Where("`user_id` = ? AND `status` = ?", userID, model.LeaveRequestStatusApproved).
		Where("`start_date` <= ? AND `end_date` >= ?", dateKey, dateKey).
		Order("`start_date` ASC, `id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询已通过请假: %w", err)
	}
	return rows, nil
}
