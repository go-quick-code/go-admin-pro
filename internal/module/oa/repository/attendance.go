// attendance.go 承载考勤基础四张写入表的数据访问（issue #60，spec §3.9/§4.10）：
// 班次、考勤组、组员、打卡。结果表（oa_attendance_result）的读写由 #61 承接。
// 表已登记 TenantTables（迁移 00022），租户条件由插件自动追加；组员唯一键
// (tenant_id, user_id) 的 upsert（复活改绑）见 UpsertGroupUsers。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- 班次 ----------

// ShiftFilter 是班次列表查询条件（name 模糊、status 精确；nil 表示不过滤）。
type ShiftFilter struct {
	Name   string
	Status *int8
}

// CreateShift 落一条班次（tenant_id 由租户插件回填）。
func (r *Repository) CreateShift(ctx context.Context, row *model.AttendanceShift) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建班次: %w", err)
	}
	return nil
}

// FindShiftByID 按 ID 读班次；未命中（含跨租户）返回 nil。
func (r *Repository) FindShiftByID(ctx context.Context, id int64) (*model.AttendanceShift, error) {
	var row model.AttendanceShift
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// UpdateShift 更新班次的可编辑字段（名称/时段/宽限/排序）。
func (r *Repository) UpdateShift(ctx context.Context, id int64, name, startTime, endTime string, graceMinutes, sort int) error {
	return r.db.WithContext(ctx).Model(&model.AttendanceShift{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"name":          name,
			"start_time":    startTime,
			"end_time":      endTime,
			"grace_minutes": graceMinutes,
			"sort":          sort,
		}).Error
}

// SetShiftStatus 启停班次。
func (r *Repository) SetShiftStatus(ctx context.Context, id int64, status int8) error {
	return r.db.WithContext(ctx).Model(&model.AttendanceShift{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// DeleteShift 软删班次（删除约束「被考勤组绑定不可删」由 service 以
// CountGroupsByShift 把关）。
func (r *Repository) DeleteShift(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.AttendanceShift{}, id)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// PageShifts 分页查询班次（tenant_id 由插件追加；排序 sort 升序、id 升序稳定序）。
func (r *Repository) PageShifts(ctx context.Context, filter ShiftFilter, offset, limit int) ([]model.AttendanceShift, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.AttendanceShift{})
	if filter.Name != "" {
		q = q.Where("name LIKE ?", "%"+filter.Name+"%")
	}
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计班次: %w", err)
	}
	var list []model.AttendanceShift
	err := q.Order("sort ASC, id ASC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// CountGroupsByShift 统计绑定某班次的考勤组数（删除约束：有组绑定的班次
// 不能删除，只能先调整考勤组；spec §4.10 管理约束）。
func (r *Repository) CountGroupsByShift(ctx context.Context, shiftID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.AttendanceGroup{}).
		Where("shift_id = ?", shiftID).
		Count(&count).Error
	return count, err
}

// ---------- 考勤组 ----------

// GroupFilter 是考勤组列表查询条件（name 模糊、status 精确；nil 不过滤）。
type GroupFilter struct {
	Name   string
	Status *int8
}

// CreateGroup 落一条考勤组（tenant_id 由租户插件回填）。
func (r *Repository) CreateGroup(ctx context.Context, row *model.AttendanceGroup) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建考勤组: %w", err)
	}
	return nil
}

// FindGroupByID 按 ID 读考勤组；未命中（含跨租户）返回 nil。
func (r *Repository) FindGroupByID(ctx context.Context, id int64) (*model.AttendanceGroup, error) {
	var row model.AttendanceGroup
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// UpdateGroup 更新考勤组可编辑字段（名称/绑定班次）。
func (r *Repository) UpdateGroup(ctx context.Context, id int64, name string, shiftID int64) error {
	return r.db.WithContext(ctx).Model(&model.AttendanceGroup{}).
		Where("id = ?", id).
		Updates(map[string]any{"name": name, "shift_id": shiftID}).Error
}

// SetGroupStatus 启停考勤组（停用后组员打卡 400，spec §4.10）。
func (r *Repository) SetGroupStatus(ctx context.Context, id int64, status int8) error {
	return r.db.WithContext(ctx).Model(&model.AttendanceGroup{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// DeleteGroup 软删考勤组。
func (r *Repository) DeleteGroup(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.AttendanceGroup{}, id)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// PageGroups 分页查询考勤组（tenant_id 由插件追加；id 升序稳定序）。
func (r *Repository) PageGroups(ctx context.Context, filter GroupFilter, offset, limit int) ([]model.AttendanceGroup, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.AttendanceGroup{})
	if filter.Name != "" {
		q = q.Where("name LIKE ?", "%"+filter.Name+"%")
	}
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计考勤组: %w", err)
	}
	var list []model.AttendanceGroup
	err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// ---------- 组员 ----------

// UpsertGroupUsers 把用户物化入组（命中唯一键 (tenant_id, user_id) 时改绑
// group_id 并复活软删行 deleted_at=NULL——用户从 A 组移出后加入 B 组不产生
// 第二行，一个用户只属于一个组）。tenant_id 由租户插件在 Create 回调回填。
func (r *Repository) UpsertGroupUsers(ctx context.Context, groupID int64, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	rows := make([]model.AttendanceGroupUser, 0, len(userIDs))
	for _, uid := range userIDs {
		rows = append(rows, model.AttendanceGroupUser{GroupID: groupID, UserID: uid})
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoUpdates: clause.Assignments(map[string]any{
			"group_id":   gorm.Expr("VALUES(`group_id`)"),
			"updater":    gorm.Expr("VALUES(`updater`)"),
			"updated_at": gorm.Expr("VALUES(`updated_at`)"),
			"deleted_at": nil,
		}),
	}).Create(&rows).Error
	if err != nil {
		return fmt.Errorf("写入考勤组成员: %w", err)
	}
	return nil
}

// ListGroupUsersByGroup 读考勤组的组员关系行（按 user_id 升序）。
func (r *Repository) ListGroupUsersByGroup(ctx context.Context, groupID int64) ([]model.AttendanceGroupUser, error) {
	var rows []model.AttendanceGroupUser
	err := r.db.WithContext(ctx).
		Where("group_id = ?", groupID).
		Order("user_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询考勤组成员: %w", err)
	}
	return rows, nil
}

// CountGroupUsers 统计考勤组当前组员数（软删行不计）：分页列表只要计数，
// 不拉组员明细。
func (r *Repository) CountGroupUsers(ctx context.Context, groupID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.AttendanceGroupUser{}).
		Where("group_id = ?", groupID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("统计考勤组成员: %w", err)
	}
	return count, nil
}

// FindGroupUserByUser 读用户当前唯一的组员关系行（含已停用组；未入组返回 nil）。
// 唯一键 (tenant_id, user_id) 保证至多一行。
func (r *Repository) FindGroupUserByUser(ctx context.Context, userID int64) (*model.AttendanceGroupUser, error) {
	var row model.AttendanceGroupUser
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// DeleteGroupUser 软删一条组员关系（移出组）；命中返回 true。
func (r *Repository) DeleteGroupUser(ctx context.Context, groupID, userID int64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Delete(&model.AttendanceGroupUser{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// DeleteGroupUsersByGroup 软删考勤组下的全部组员关系（删组级联解除成员关系）。
func (r *Repository) DeleteGroupUsersByGroup(ctx context.Context, groupID int64) error {
	return r.db.WithContext(ctx).
		Where("group_id = ?", groupID).
		Delete(&model.AttendanceGroupUser{}).Error
}

// ---------- 打卡 ----------

// CreatePunch 落一条打卡记录（同日多次均保留，无唯一约束；tenant_id 由插件回填）。
func (r *Repository) CreatePunch(ctx context.Context, row *model.AttendancePunch) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("写入打卡记录: %w", err)
	}
	return nil
}

// ListPunchesByUserDate 读用户某打卡日的全部记录（按打卡时刻升序）。
// date 传 YYYY-MM-DD 字符串（MySQL DATE 列比较）。
func (r *Repository) ListPunchesByUserDate(ctx context.Context, userID int64, date string) ([]model.AttendancePunch, error) {
	var rows []model.AttendancePunch
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND `punch_date` = ?", userID, date).
		Order("`punch_time` ASC, `id` ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询当日打卡记录: %w", err)
	}
	return rows, nil
}

// PunchFilter 是打卡记录查询条件（日期区间可选）。
type PunchFilter struct {
	StartDate string
	EndDate   string
}

// PagePunchesByUser 分页查询用户打卡记录（punch_time 降序；日期区间含两端）。
func (r *Repository) PagePunchesByUser(ctx context.Context, userID int64, filter PunchFilter, offset, limit int) ([]model.AttendancePunch, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.AttendancePunch{}).
		Where("user_id = ?", userID)
	if filter.StartDate != "" {
		q = q.Where("`punch_date` >= ?", filter.StartDate)
	}
	if filter.EndDate != "" {
		q = q.Where("`punch_date` <= ?", filter.EndDate)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计打卡记录: %w", err)
	}
	var list []model.AttendancePunch
	err := q.Order("`punch_time` DESC, `id` DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}
