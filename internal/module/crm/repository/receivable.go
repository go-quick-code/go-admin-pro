package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- 回款计划 + 回款记录（issue #18）----------
// 数据权限（docs/crm-mvp-spec.md §4.1：回款 join 父记录后套用父谓词）：
// 计划/记录都 JOIN crm_contract，套用合同的 owner_dept_id / owner_user_id 谓词；
// 合同软删后其计划与记录一并不可见。租户条件说明：插件只给主表追加 tenant_id，
// 被关联的 crm_contract、crm_receivable_plan 与已收金额子查询在这里显式带上同值条件。
// 派生状态（待回款/部分回款/已回款/逾期）不落库：列表 SQL 用 SUM 子查询别名
// received_amount 扫出已收金额，状态筛选按与 service.DeriveReceivableStatus
// 相同的判定口径翻译；展示状态由 service 用纯函数重算，保证单一事实来源。

// ReceivablePlanFilter 是回款计划列表查询条件。
// OwnerDeptIDs / SelfOwnerUserID 语义与 ContractFilter 一致（join 合同后套父谓词）：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type ReceivablePlanFilter struct {
	ContractID int64
	Status     *int8
	Overdue    *bool
	// Today 是派生状态/逾期比较用的「今天」（yyyy-MM-dd，应用时区口径），
	// 由 service 以 time.Now() 生成并显式传入，不依赖数据库会话时区。
	Today string

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// ReceivableRecordFilter 是回款记录列表查询条件。
type ReceivableRecordFilter struct {
	ContractID int64
	PlanID     int64

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// receivablePlanBaseQuery 构造计划列表的公共查询：JOIN 合同（数据权限）+
// LEFT JOIN 已收金额聚合子查询，并投影 received_amount。
func (r *Repository) receivablePlanBaseQuery(ctx context.Context, f ReceivablePlanFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ReceivablePlan{}).
		Select("crm_receivable_plan.*, COALESCE(crm_receivable_record_sum.received, 0) AS received_amount").
		Joins("JOIN crm_contract ON crm_contract.id = crm_receivable_plan.contract_id"+
			" AND crm_contract.deleted_at IS NULL"+
			" AND crm_contract.tenant_id = ?", tenant.ID(ctx)).
		Joins("LEFT JOIN (SELECT plan_id, SUM(amount) AS received FROM crm_receivable_record"+
			" WHERE deleted_at IS NULL AND tenant_id = ? GROUP BY plan_id) crm_receivable_record_sum"+
			" ON crm_receivable_record_sum.plan_id = crm_receivable_plan.id", tenant.ID(ctx))
	if f.ContractID > 0 {
		q = q.Where("crm_receivable_plan.contract_id = ?", f.ContractID)
	}

	const received = "COALESCE(crm_receivable_record_sum.received, 0)"
	if f.Status != nil {
		switch *f.Status {
		case model.ReceivableStatusPending:
			q = q.Where(received+" = 0 AND crm_receivable_plan.due_date >= ?", f.Today)
		case model.ReceivableStatusPartial:
			q = q.Where(received+" > 0 AND "+received+" < crm_receivable_plan.amount"+
				" AND crm_receivable_plan.due_date >= ?", f.Today)
		case model.ReceivableStatusReceived:
			q = q.Where(received + " >= crm_receivable_plan.amount")
		case model.ReceivableStatusOverdue:
			q = q.Where(received+" < crm_receivable_plan.amount"+
				" AND crm_receivable_plan.due_date < ?", f.Today)
		}
	}
	if f.Overdue != nil {
		if *f.Overdue {
			q = q.Where(received+" < crm_receivable_plan.amount"+
				" AND crm_receivable_plan.due_date < ?", f.Today)
		} else {
			q = q.Where("("+received+" >= crm_receivable_plan.amount"+
				" OR crm_receivable_plan.due_date >= ?)", f.Today)
		}
	}
	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(crm_contract.owner_dept_id IN ? OR crm_contract.owner_user_id = ?)",
				f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("crm_contract.owner_dept_id IN ?", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("crm_contract.owner_user_id = ?", f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
	}
	return q
}

// ListReceivablePlans 按筛选与数据范围谓词分页查询回款计划，附带已收金额。
func (r *Repository) ListReceivablePlans(ctx context.Context, f ReceivablePlanFilter, offset, limit int) ([]model.ReceivablePlan, int64, error) {
	var total int64
	if err := r.receivablePlanBaseQuery(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ReceivablePlan
	if err := r.receivablePlanBaseQuery(ctx, f).
		Order("crm_receivable_plan.due_date ASC, crm_receivable_plan.id ASC").
		Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllReceivablePlans 按筛选与数据范围谓词查询全部回款计划（Excel 导出用，
// 不分页；排序与列表一致：到期日升序）。
func (r *Repository) ListAllReceivablePlans(ctx context.Context, f ReceivablePlanFilter) ([]model.ReceivablePlan, error) {
	var list []model.ReceivablePlan
	if err := r.receivablePlanBaseQuery(ctx, f).
		Order("crm_receivable_plan.due_date ASC, crm_receivable_plan.id ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FindReceivablePlanByID 按 ID 查计划（租户插件自动过滤），不存在返回 nil。
// 已收金额不在此投影：单条路径由 service 调 SumReceivableRecordsByPlanIDs 补齐。
func (r *Repository) FindReceivablePlanByID(ctx context.Context, id int64) (*model.ReceivablePlan, error) {
	var p model.ReceivablePlan
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// CreateReceivablePlan 创建回款计划。
func (r *Repository) CreateReceivablePlan(ctx context.Context, p *model.ReceivablePlan) error {
	return r.db.WithContext(ctx).Create(p).Error
}

// UpdateReceivablePlanFields 只更新请求字段（编辑接口）：期次/金额/到期日/备注。
// 合同归属不可经编辑改写（contract_id 不进入本方法），用 map 更新避免整行 Save
// 回写并发改动（如合同切换、已收金额派生列）。
func (r *Repository) UpdateReceivablePlanFields(ctx context.Context, id int64, fields map[string]any) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.ReceivablePlan{}).
		Where("id = ?", id).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// LockReceivablePlanByID 在事务内以 SELECT ... FOR UPDATE 锁定回款计划行并返回，
// 供回款记录金额不变量校验串行化使用（同一计划的并发登记在此排队）。
// 必须由调用方在 Transaction 内调用；租户插件自动带 tenant_id 条件。
func (r *Repository) LockReceivablePlanByID(ctx context.Context, id int64) (*model.ReceivablePlan, error) {
	var p model.ReceivablePlan
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		First(&p, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// DeleteReceivablePlan 按 ID 软删除计划（租户插件自动带租户条件）。
func (r *Repository) DeleteReceivablePlan(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ReceivablePlan{}, id).Error
}

// SumReceivablePlansByContract 求某合同下未删除计划的金额合计；
// excludePlanID > 0 时排除该计划（更新时排除自身，spec §4.3 不变量）。
func (r *Repository) SumReceivablePlansByContract(ctx context.Context, contractID, excludePlanID int64) (money.Money, error) {
	q := r.db.WithContext(ctx).Model(&model.ReceivablePlan{}).
		Where("contract_id = ?", contractID)
	if excludePlanID > 0 {
		q = q.Where("id <> ?", excludePlanID)
	}
	var row struct {
		Total money.Money
	}
	if err := q.Select("COALESCE(SUM(amount), 0) AS total").Scan(&row).Error; err != nil {
		return money.Money{}, err
	}
	return row.Total, nil
}

// receivableRecordBaseQuery 构造记录列表的公共查询：JOIN 计划（期次展示、计划软删
// 后记录不可见）与合同（数据权限），并投影 plan_period_no。
func (r *Repository) receivableRecordBaseQuery(ctx context.Context, f ReceivableRecordFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ReceivableRecord{}).
		Select("crm_receivable_record.*, crm_receivable_plan.period_no AS plan_period_no").
		Joins("JOIN crm_receivable_plan ON crm_receivable_plan.id = crm_receivable_record.plan_id"+
			" AND crm_receivable_plan.deleted_at IS NULL"+
			" AND crm_receivable_plan.tenant_id = ?", tenant.ID(ctx)).
		Joins("JOIN crm_contract ON crm_contract.id = crm_receivable_record.contract_id"+
			" AND crm_contract.deleted_at IS NULL"+
			" AND crm_contract.tenant_id = ?", tenant.ID(ctx))
	if f.ContractID > 0 {
		q = q.Where("crm_receivable_record.contract_id = ?", f.ContractID)
	}
	if f.PlanID > 0 {
		q = q.Where("crm_receivable_record.plan_id = ?", f.PlanID)
	}
	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(crm_contract.owner_dept_id IN ? OR crm_contract.owner_user_id = ?)",
				f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("crm_contract.owner_dept_id IN ?", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("crm_contract.owner_user_id = ?", f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
	}
	return q
}

// ListReceivableRecords 按筛选与数据范围谓词分页查询回款记录（新→旧）。
func (r *Repository) ListReceivableRecords(ctx context.Context, f ReceivableRecordFilter, offset, limit int) ([]model.ReceivableRecord, int64, error) {
	var total int64
	if err := r.receivableRecordBaseQuery(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ReceivableRecord
	if err := r.receivableRecordBaseQuery(ctx, f).
		Order("crm_receivable_record.id DESC").
		Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// FindReceivableRecordByID 按 ID 查记录（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindReceivableRecordByID(ctx context.Context, id int64) (*model.ReceivableRecord, error) {
	var row model.ReceivableRecord
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// CreateReceivableRecord 创建回款记录（必须已在 record_no 赋值后调用，
// 见 service.CreateReceivableRecord：编号与落库同事务）。
func (r *Repository) CreateReceivableRecord(ctx context.Context, row *model.ReceivableRecord) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// DeleteReceivableRecord 按 ID 软删除回款记录（租户插件自动带租户条件）。
func (r *Repository) DeleteReceivableRecord(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ReceivableRecord{}, id).Error
}

// SumReceivableRecordsByPlan 求某计划下未删除回款记录的金额合计
// （创建记录时与其他记录合计比较，spec §4.3）。
func (r *Repository) SumReceivableRecordsByPlan(ctx context.Context, planID int64) (money.Money, error) {
	var row struct {
		Total money.Money
	}
	err := r.db.WithContext(ctx).Model(&model.ReceivableRecord{}).
		Where("plan_id = ?", planID).
		Select("COALESCE(SUM(amount), 0) AS total").Scan(&row).Error
	if err != nil {
		return money.Money{}, err
	}
	return row.Total, nil
}

// SumReceivableRecordsByPlanIDs 批量求各计划的已收金额（单条计划响应/详情补齐用），
// 缺失计划不进 map（调用方按零值处理）。
func (r *Repository) SumReceivableRecordsByPlanIDs(ctx context.Context, planIDs []int64) (map[int64]money.Money, error) {
	result := make(map[int64]money.Money, len(planIDs))
	if len(planIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		PlanID int64       `gorm:"column:plan_id"`
		Total  money.Money `gorm:"column:total"`
	}
	if err := r.db.WithContext(ctx).Model(&model.ReceivableRecord{}).
		Where("plan_id IN ?", planIDs).
		Select("plan_id, COALESCE(SUM(amount), 0) AS total").
		Group("plan_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.PlanID] = row.Total
	}
	return result, nil
}

// CountReceivableRecordsByPlan 统计某计划的未删除回款记录数
// （计划删除约束：存在记录时禁止删除，spec §4.5）。
func (r *Repository) CountReceivableRecordsByPlan(ctx context.Context, planID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ReceivableRecord{}).
		Where("plan_id = ?", planID).Count(&count).Error
	return count, err
}

// CountReceivableRecordsByContract 统计某合同的未删除回款记录数
// （VoidContractGuard 真实实现：存在回款记录时禁止作废/删除合同，spec §4.5）。
func (r *Repository) CountReceivableRecordsByContract(ctx context.Context, contractID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ReceivableRecord{}).
		Where("contract_id = ?", contractID).Count(&count).Error
	return count, err
}
