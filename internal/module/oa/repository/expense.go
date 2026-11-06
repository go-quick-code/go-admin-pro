// expense.go 承载报销单与明细的数据访问（issue #54，spec §3.6/§4.9）：
// 表头 CRUD、可见范围分页、明细行集读取与「整体替换」（软删旧行 + 批量插入）。
// 两表已登记 TenantTables，租户条件由插件自动追加；草稿保存与删除在事务内
// 完成，保证「表头金额 = 明细合计」与「删除级联明细」原子一致。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm/clause"
)

// CreateExpenseClaim 落一条报销单（草稿）；tenant_id 由插件回填。
func (r *Repository) CreateExpenseClaim(ctx context.Context, row *model.ExpenseClaim) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建报销单: %w", err)
	}
	return nil
}

// SaveExpenseClaim 更新报销单的全部可变字段（合计、状态、驳回原因、实例 ID）。
func (r *Repository) SaveExpenseClaim(ctx context.Context, row *model.ExpenseClaim) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新报销单: %w", err)
	}
	return nil
}

// FindExpenseClaim 按 ID 读报销单；未命中（含跨租户）返回 nil。
func (r *Repository) FindExpenseClaim(ctx context.Context, id int64) (*model.ExpenseClaim, error) {
	var row model.ExpenseClaim
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindExpenseClaimForUpdate 行锁读报销单（草稿保存/删除的写入口，串行化
// 同一单据的并发保存）。
func (r *Repository) FindExpenseClaimForUpdate(ctx context.Context, id int64) (*model.ExpenseClaim, error) {
	var row model.ExpenseClaim
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// DeleteExpenseClaim 软删报销单（service 已校验归属与状态）。
func (r *Repository) DeleteExpenseClaim(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.ExpenseClaim{}, id)
	if result.Error != nil {
		return false, fmt.Errorf("删除报销单: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// PageExpenseClaimsByScope 按可见范围分页（创建时间倒序，spec §4.6）：
// userIDs 为 nil 表示全租户（oa:expense:manage）；非 nil 时按 user_id IN
// 过滤（部门负责人子树 ∪ 本人），空集合直接返回空页（fail-closed）。
// status 可选精确过滤。租户条件由插件自动追加，跨租户行不会命中。
func (r *Repository) PageExpenseClaimsByScope(ctx context.Context, userIDs []int64, status *int8, offset, limit int) ([]model.ExpenseClaim, int64, error) {
	if userIDs != nil && len(userIDs) == 0 {
		return []model.ExpenseClaim{}, 0, nil
	}
	q := r.db.WithContext(ctx).Model(&model.ExpenseClaim{})
	if userIDs != nil {
		q = q.Where("`user_id` IN ?", userIDs)
	}
	if status != nil {
		q = q.Where("`status` = ?", *status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计报销单: %w", err)
	}
	var rows []model.ExpenseClaim
	if err := q.Order("`id` DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询报销单: %w", err)
	}
	return rows, total, nil
}

// ListExpenseItemsByClaim 读一张报销单的明细（sort ASC, id ASC）。
func (r *Repository) ListExpenseItemsByClaim(ctx context.Context, claimID int64) ([]model.ExpenseItem, error) {
	var rows []model.ExpenseItem
	if err := r.db.WithContext(ctx).
		Where("`claim_id` = ?", claimID).
		Order("`sort` ASC, `id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询报销明细: %w", err)
	}
	return rows, nil
}

// ListExpenseItemsByClaimIDs 批量读多张报销单的明细（列表页展示明细数），
// 按 claim_id、sort 排序；空入参返回空切片。
func (r *Repository) ListExpenseItemsByClaimIDs(ctx context.Context, claimIDs []int64) ([]model.ExpenseItem, error) {
	if len(claimIDs) == 0 {
		return []model.ExpenseItem{}, nil
	}
	var rows []model.ExpenseItem
	if err := r.db.WithContext(ctx).
		Where("`claim_id` IN ?", claimIDs).
		Order("`claim_id` ASC, `sort` ASC, `id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("批量查询报销明细: %w", err)
	}
	return rows, nil
}

// ReplaceExpenseItems 整体替换一张报销单的明细行（草稿保存语义）：
// 先软删该单现有明细，再批量插入新行。调用方须在同一事务内调用。
func (r *Repository) ReplaceExpenseItems(ctx context.Context, claimID int64, items []model.ExpenseItem) error {
	if err := r.SoftDeleteExpenseItemsByClaim(ctx, claimID); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		items[i].ClaimID = claimID
	}
	if err := r.db.WithContext(ctx).Create(&items).Error; err != nil {
		return fmt.Errorf("写入报销明细: %w", err)
	}
	return nil
}

// SoftDeleteExpenseItemsByClaim 软删一张报销单的全部明细（删除单据级联）。
func (r *Repository) SoftDeleteExpenseItemsByClaim(ctx context.Context, claimID int64) error {
	if err := r.db.WithContext(ctx).
		Where("`claim_id` = ?", claimID).
		Delete(&model.ExpenseItem{}).Error; err != nil {
		return fmt.Errorf("删除报销明细: %w", err)
	}
	return nil
}
