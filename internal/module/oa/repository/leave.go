// leave.go 承载请假域的数据访问（issue #51，spec §3.5）：
// 类型 CRUD 与引用计数、额度的行级读写与锁读、申请表的引用计数读取
// （写入由 #52 承接，本文件不提供任何申请表写路径）。三表均已登记
// TenantTables，租户条件由插件追加；额度行级串行化用 SELECT ... FOR UPDATE。
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- 请假类型 ----------

// CreateLeaveType 落一条类型；tenant_id 由插件回填。并发撞租户内唯一键
// （code/name）时经 wrapDuplicateEntry 规整为 gorm.ErrDuplicatedKey，service 翻译 409。
func (r *Repository) CreateLeaveType(ctx context.Context, row *model.LeaveType) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return wrapDuplicateEntry(err)
	}
	return nil
}

// SaveLeaveType 更新类型的可变字段（name/balance_managed/annual_quota/status/
// sort/remark；code 创建后不可变，本方法不涉及）。
func (r *Repository) SaveLeaveType(ctx context.Context, row *model.LeaveType) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return wrapDuplicateEntry(err)
	}
	return nil
}

// FindLeaveType 按 ID 读类型；未命中（含跨租户）返回 nil。
func (r *Repository) FindLeaveType(ctx context.Context, id int64) (*model.LeaveType, error) {
	var row model.LeaveType
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindLeaveTypeByCodeOrName 读同租户内编码或名称任一命中的现存行（排除
// excludeID，更新路径排除自身）；未命中返回 nil。service 用它做唯一性预检，
// 软删行占用的唯一键由数据库兜底（wrapDuplicateEntry → 409）。
func (r *Repository) FindLeaveTypeByCodeOrName(ctx context.Context, code, name string, excludeID int64) (*model.LeaveType, error) {
	var row model.LeaveType
	q := r.db.WithContext(ctx).
		Where("`code` = ? OR `name` = ?", code, name)
	if excludeID > 0 {
		q = q.Where("`id` <> ?", excludeID)
	}
	if err := q.First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListLeaveTypes 全量列表（管理页与员工发起请假共用）：sort 升序、id 升序，
// 可选 status 精确过滤与名称/编码模糊关键字。
func (r *Repository) ListLeaveTypes(ctx context.Context, status *int8, keyword string) ([]model.LeaveType, error) {
	q := r.db.WithContext(ctx).Model(&model.LeaveType{})
	if status != nil {
		q = q.Where("`status` = ?", *status)
	}
	if keyword != "" {
		q = q.Where("`name` LIKE ? OR `code` LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	var rows []model.LeaveType
	if err := q.Order("`sort` ASC, `id` ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询请假类型: %w", err)
	}
	return rows, nil
}

// ListLeaveTypesByIDs 按 ID 集合读启用中的类型（额度初始化按类型取年度额度
// 快照用）；顺序按 sort/id，与列表语义一致。
func (r *Repository) ListLeaveTypesByIDs(ctx context.Context, ids []int64) ([]model.LeaveType, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []model.LeaveType
	if err := r.db.WithContext(ctx).
		Where("`id` IN ? AND `status` = ?", ids, model.LeaveTypeStatusEnabled).
		Order("`sort` ASC, `id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询请假类型: %w", err)
	}
	return rows, nil
}

// DeleteLeaveType 软删类型（service 已校验未被申请引用）；返回是否删到行。
func (r *Repository) DeleteLeaveType(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.LeaveType{}, id)
	if result.Error != nil {
		return false, fmt.Errorf("删除请假类型: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// CountLeaveRequestsByType 统计引用某类型的申请行数（任意状态，含草稿——
// 删除类型不能让引用它的草稿悬空，spec §4.14「被引用时只能停用」）。
func (r *Repository) CountLeaveRequestsByType(ctx context.Context, typeID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.LeaveRequest{}).
		Where("`leave_type_id` = ?", typeID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("统计类型引用: %w", err)
	}
	return count, nil
}

// ---------- 请假额度 ----------

// CreateLeaveBalance 落一条额度行（唯一键 (tenant, user, type, year) 冲突
// 由 wrapDuplicateEntry 规整，service 在事务里锁读后补行，不会走到）。
func (r *Repository) CreateLeaveBalance(ctx context.Context, row *model.LeaveBalance) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return wrapDuplicateEntry(err)
	}
	return nil
}

// FindBalance 按 (userID, typeID, year) 读额度行；未命中返回 nil。
func (r *Repository) FindBalance(ctx context.Context, userID, typeID int64, year int) (*model.LeaveBalance, error) {
	var row model.LeaveBalance
	if err := r.db.WithContext(ctx).
		Where("`user_id` = ? AND `leave_type_id` = ? AND `year` = ?", userID, typeID, year).
		First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindBalanceForUpdate 锁读额度行（调整/扣减/返还的串行化入口，事务内调用）。
func (r *Repository) FindBalanceForUpdate(ctx context.Context, userID, typeID int64, year int) (*model.LeaveBalance, error) {
	var row model.LeaveBalance
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("`user_id` = ? AND `leave_type_id` = ? AND `year` = ?", userID, typeID, year).
		First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListBalancesCross 读 (year, users × types) 交叉范围内的已有额度行
// （初始化幂等计划的 existing 输入：wanted 组合 = userIDs × typeIDs × year，
// 交叉外的行与初始化无关）。AND 条件不与租户插件的条件产生优先级问题。
// userIDs/typeIDs 为空或 year 非法时返回空切片。
func (r *Repository) ListBalancesCross(ctx context.Context, year int, userIDs, typeIDs []int64) ([]model.LeaveBalance, error) {
	if year <= 0 || len(userIDs) == 0 || len(typeIDs) == 0 {
		return nil, nil
	}
	var rows []model.LeaveBalance
	if err := r.db.WithContext(ctx).
		Where("`year` = ? AND `user_id` IN ? AND `leave_type_id` IN ?", year, userIDs, typeIDs).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询额度行: %w", err)
	}
	return rows, nil
}

// PageBalances 管理端分页（tenant_id 由插件追加）：userId/typeId/year 可选
// 精确过滤，按 id 升序稳定分页。
func (r *Repository) PageBalances(ctx context.Context, userID, typeID int64, year int, offset, limit int) ([]model.LeaveBalance, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.LeaveBalance{})
	if userID > 0 {
		q = q.Where("`user_id` = ?", userID)
	}
	if typeID > 0 {
		q = q.Where("`leave_type_id` = ?", typeID)
	}
	if year > 0 {
		q = q.Where("`year` = ?", year)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计额度: %w", err)
	}
	var rows []model.LeaveBalance
	if err := q.Order("`id` ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询额度: %w", err)
	}
	return rows, total, nil
}

// SaveLeaveBalance 落额度行的调整结果（adjustment/adjust_reason；审计字段
// updater/updated_at 由插件回填）。事务内锁读后调用。
func (r *Repository) SaveLeaveBalance(ctx context.Context, row *model.LeaveBalance) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("保存额度: %w", err)
	}
	return nil
}

// wrapDuplicateEntry 把 MySQL 1062 重复键错误规整为 gorm.ErrDuplicatedKey 链
// （与 crm/bpm repository 同款兜底：gorm 默认不开错误翻译），service 层翻译为 409。
func wrapDuplicateEntry(err error) error {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return errors.Join(gorm.ErrDuplicatedKey, err)
	}
	return err
}
