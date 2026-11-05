// workday.go 承载工作日历显式记录的数据访问（issue #50，spec §3.3）：
// 按月区间读、批量 upsert、按日清除。表已登记 TenantTables，租户条件由
// 插件追加；date 列为 MySQL DATE，区间条件统一传 YYYY-MM-DD 字符串。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListWorkdaysByRange 读 [startDate, endDate]（含两端，YYYY-MM-DD）内的
// 显式记录，按日升序。tenant_id 由租户插件追加。
func (r *Repository) ListWorkdaysByRange(ctx context.Context, startDate, endDate string) ([]model.WorkdayCalendar, error) {
	var rows []model.WorkdayCalendar
	err := r.db.WithContext(ctx).
		Where("`date` >= ? AND `date` <= ?", startDate, endDate).
		Order("`date` ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询工作日历: %w", err)
	}
	return rows, nil
}

// FindWorkdayByDate 读单日显式记录；未命中返回 nil（含软删行）。
func (r *Repository) FindWorkdayByDate(ctx context.Context, date string) (*model.WorkdayCalendar, error) {
	var row model.WorkdayCalendar
	err := r.db.WithContext(ctx).
		Where("`date` = ?", date).
		First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// UpsertWorkdays 批量写入显式记录（命中唯一键 (tenant_id, date) 时改写
// type/remark/审计字段并复活软删行 deleted_at=NULL）。tenant_id 由租户插件
// 在 Create 回调回填；同批重复日期由 service 先去重。
func (r *Repository) UpsertWorkdays(ctx context.Context, rows []model.WorkdayCalendar) error {
	if len(rows) == 0 {
		return nil
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoUpdates: clause.Assignments(map[string]any{
			"`type`":       gorm.Expr("VALUES(`type`)"),
			"`remark`":     gorm.Expr("VALUES(`remark`)"),
			"`updater`":    gorm.Expr("VALUES(`updater`)"),
			"`updated_at`": gorm.Expr("VALUES(`updated_at`)"),
			"`deleted_at`": nil,
		}),
	}).Create(&rows).Error
	if err != nil {
		return fmt.Errorf("写入工作日历: %w", err)
	}
	return nil
}

// DeleteWorkdayByDate 清除单日显式记录（软删，恢复默认规则）。
// 未命中（无记录或已清除）返回 false，由 service 翻译为 404。
func (r *Repository) DeleteWorkdayByDate(ctx context.Context, date string) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("`date` = ?", date).
		Delete(&model.WorkdayCalendar{})
	if result.Error != nil {
		return false, fmt.Errorf("清除工作日历记录: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}
