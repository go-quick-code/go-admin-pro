// attendance_result.go 承载考勤结果的读写（issue #61，spec §3.9/§4.10）：
// 一人一天一条（唯一键 tenant_id+user_id+work_date），重算走 upsert 幂等；
// 管理员校正只写 override_*，不覆盖派生字段与原始打卡。表已登记
// TenantTables（迁移 00022），租户条件由插件自动追加，跨租户 ID 读不到。
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertAttendanceResult 写入/覆盖一条派生结果（幂等）：命中唯一键
// (tenant_id, user_id, work_date) 时更新派生字段与 computed_at，**不触碰**
// override_*/corrected_*——重算不得冲掉管理员校正（spec §4.10）。
// tenant_id 由租户插件在 Create 回调回填，ON CONFLICT 的 VALUES() 取该值。
func (r *Repository) UpsertAttendanceResult(ctx context.Context, row *model.AttendanceResult) error {
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoUpdates: clause.Assignments(map[string]any{
			"shift_id":      gorm.Expr("VALUES(`shift_id`)"),
			"in_time":       gorm.Expr("VALUES(`in_time`)"),
			"out_time":      gorm.Expr("VALUES(`out_time`)"),
			"late_minutes":  gorm.Expr("VALUES(`late_minutes`)"),
			"early_minutes": gorm.Expr("VALUES(`early_minutes`)"),
			"missing_in":    gorm.Expr("VALUES(`missing_in`)"),
			"missing_out":   gorm.Expr("VALUES(`missing_out`)"),
			"leave_days":    gorm.Expr("VALUES(`leave_days`)"),
			"status":        gorm.Expr("VALUES(`status`)"),
			"computed_at":   gorm.Expr("VALUES(`computed_at`)"),
			"updater":       gorm.Expr("VALUES(`updater`)"),
			"updated_at":    gorm.Expr("VALUES(`updated_at`)"),
			"deleted_at":    nil,
		}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("写入考勤结果: %w", err)
	}
	return nil
}

// FindAttendanceResultByID 按 ID 读结果行；未命中（含跨租户/软删）返回 nil。
func (r *Repository) FindAttendanceResultByID(ctx context.Context, id int64) (*model.AttendanceResult, error) {
	var row model.AttendanceResult
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindAttendanceResultByUserDate 按 (user, work_date) 读结果行；未命中返回 nil。
// date 传 YYYY-MM-DD（MySQL DATE 列比较）。
func (r *Repository) FindAttendanceResultByUserDate(ctx context.Context, userID int64, date string) (*model.AttendanceResult, error) {
	var row model.AttendanceResult
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND `work_date` = ?", userID, date).
		First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// AttendanceResultFilter 是结果列表条件（全部可选）：
//   - UserID 精确单个用户；UserIDs 非空时取集合（可见范围过滤）；
//   - StartDate/EndDate 为 YYYY-MM-DD 闭区间；
//   - Status 过滤**生效状态**（override_status 优先于派生 status，COALESCE）。
type AttendanceResultFilter struct {
	UserID    int64
	UserIDs   []int64
	StartDate string
	EndDate   string
	Status    *int8
}

// PageAttendanceResults 分页查询结果（work_date 降序、user_id 升序稳定序）。
func (r *Repository) PageAttendanceResults(ctx context.Context, filter AttendanceResultFilter, offset, limit int) ([]model.AttendanceResult, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.AttendanceResult{})
	if filter.UserID > 0 {
		q = q.Where("user_id = ?", filter.UserID)
	} else if filter.UserIDs != nil {
		if len(filter.UserIDs) == 0 {
			return []model.AttendanceResult{}, 0, nil
		}
		q = q.Where("user_id IN ?", filter.UserIDs)
	}
	if filter.StartDate != "" {
		q = q.Where("`work_date` >= ?", filter.StartDate)
	}
	if filter.EndDate != "" {
		q = q.Where("`work_date` <= ?", filter.EndDate)
	}
	if filter.Status != nil {
		q = q.Where("COALESCE(override_status, status) = ?", *filter.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计考勤结果: %w", err)
	}
	var list []model.AttendanceResult
	err := q.Order("`work_date` DESC, `user_id` ASC, `id` ASC").
		Offset(offset).Limit(limit).Find(&list).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询考勤结果: %w", err)
	}
	return list, total, nil
}

// SetAttendanceCorrection 写入管理员校正（override 状态 + 备注 + 校正人/时间）。
// 不修改派生字段与原始打卡；命中返回 true。
func (r *Repository) SetAttendanceCorrection(ctx context.Context, id int64, status int8, remark string, correctedBy int64, at time.Time) (bool, error) {
	result := r.db.WithContext(ctx).Model(&model.AttendanceResult{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"override_status": status,
			"override_remark": remark,
			"corrected_by":    correctedBy,
			"corrected_at":    at,
		})
	if result.Error != nil {
		return false, fmt.Errorf("写入考勤校正: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ClearAttendanceCorrection 撤销管理员校正（override_* 归 NULL/空串），
// 结果回落到派生状态；命中返回 true。
func (r *Repository) ClearAttendanceCorrection(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Model(&model.AttendanceResult{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"override_status": nil,
			"override_remark": "",
			"corrected_by":    nil,
			"corrected_at":    nil,
		})
	if result.Error != nil {
		return false, fmt.Errorf("清除考勤校正: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ListAllGroups 读取租户内全部考勤组（含停用；每日任务按租户批量派生）。
func (r *Repository) ListAllGroups(ctx context.Context) ([]model.AttendanceGroup, error) {
	var rows []model.AttendanceGroup
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询考勤组: %w", err)
	}
	return rows, nil
}

// ListAllGroupUsers 读取租户内全部组员关系（含停用组的成员；每日任务以
// 组员关系为派生的用户集合，按 user_id 升序）。
func (r *Repository) ListAllGroupUsers(ctx context.Context) ([]model.AttendanceGroupUser, error) {
	var rows []model.AttendanceGroupUser
	if err := r.db.WithContext(ctx).Order("user_id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询考勤组成员: %w", err)
	}
	return rows, nil
}
