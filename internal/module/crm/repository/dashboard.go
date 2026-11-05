package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// ---------- 仪表盘聚合（issue #19）----------
// 只读聚合，不新增表/迁移。数据权限与各列表同一套谓词（spec §4.1，ADR 0004）：
//   - 客户/商机/合同按自身 owner_dept_id / owner_user_id 过滤；
//   - 回款计划/记录 JOIN 合同后套用合同 owner_* 谓词（同 #18 列表口径）。
// 租户隔离：主表条件由租户插件追加；JOIN 的 crm_contract 与已收金额子查询
// 在这里显式带同值 tenant_id（插件只给主表追加条件，与 receivable.go 同款）。
//
// 时间口径（ADR 0006）：调用方（service）传入应用时区下的「今天」与月份区间；
// 本层只做日期比较，不在 SQL 里对无时区日期做时区换算。月份补齐、逾期判定
// 与金额合计的最终口径由 service 纯函数负责（dashboard_rules.go）。
//
// 注意：聚合查询统一用 Find 而不是 Scan。GORM 的 Rows/Scan 走 Row 回调，
// 不经过租户插件注册的 gorm:query 钩子——用 Scan 会漏掉自动追加的 tenant_id
// 条件，导致跨租户统计数据泄漏（dashboard 契约测试的租户隔离用例覆盖此点）。

// DashboardFilter 是仪表盘各指标的数据范围谓词，语义与 CustomerFilter/
// OpportunityFilter/ContractFilter 一致：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type DashboardFilter struct {
	OwnerDeptIDs    []int64
	SelfOwnerUserID int64

	// IncludePool 表示当前用户持 crm:customer:pool:view（spec §4.1）：
	// 客户计数把公海（owner_user_id IS NULL）计入，且公海不受数据范围过滤。
	// 其他指标不涉及公海，忽略该字段。
	IncludePool bool
}

// DashboardStageRow 是商机按阶段聚合的原始行。
type DashboardStageRow struct {
	Stage  int8        `gorm:"column:stage"`
	Count  int64       `gorm:"column:item_count"`
	Amount money.Money `gorm:"column:total_amount"`
}

// DashboardDatedRow 是按日期归集前的金额行（合同签订 / 回款到账）。
type DashboardDatedRow struct {
	Date   time.Time   `gorm:"column:event_date"`
	Amount money.Money `gorm:"column:amount"`
}

// DashboardDuePlanRow 是到期日早于「今天」的计划行（逾期与否由 service 复核）。
type DashboardDuePlanRow struct {
	Amount         money.Money `gorm:"column:amount"`
	ReceivedAmount money.Money `gorm:"column:received_amount"`
	DueDate        time.Time   `gorm:"column:due_date"`
}

// applyDashboardOwnerScope 把数据范围谓词翻译成 owner_* SQL 条件。
// deptColumn/userColumn 由调用方给出带表名的列（JOIN 路径用 crm_contract.*）。
func applyDashboardOwnerScope(q *gorm.DB, f DashboardFilter, deptColumn, userColumn string) *gorm.DB {
	if f.OwnerDeptIDs == nil && f.SelfOwnerUserID == 0 {
		return q
	}
	switch {
	case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
		return q.Where("("+deptColumn+" IN ? OR "+userColumn+" = ?)", f.OwnerDeptIDs, f.SelfOwnerUserID)
	case len(f.OwnerDeptIDs) > 0:
		return q.Where(deptColumn+" IN ?", f.OwnerDeptIDs)
	case f.SelfOwnerUserID > 0:
		return q.Where(userColumn+" = ?", f.SelfOwnerUserID)
	default:
		return q.Where("1 = 0")
	}
}

// CountDashboardCustomers 统计当前用户可见的客户数（spec §4.1）：
//   - 无 crm:customer:pool:view（IncludePool=false）：公海不可见，只统计
//     owner_user_id IS NOT NULL，再套普通数据范围谓词；
//   - 有该权限（IncludePool=true）：公海计入，且公海不受数据范围过滤——
//     结果为「数据范围谓词 OR owner_user_id IS NULL」；数据范围为「全部」时
//     不过滤（含公海）。
func (r *Repository) CountDashboardCustomers(ctx context.Context, f DashboardFilter) (int64, error) {
	var count int64
	q := r.db.WithContext(ctx).Model(&model.Customer{})
	switch {
	case !f.IncludePool:
		q = q.Where("crm_customer.owner_user_id IS NOT NULL")
		q = applyDashboardOwnerScope(q, f, "crm_customer.owner_dept_id", "crm_customer.owner_user_id")
	case f.OwnerDeptIDs == nil && f.SelfOwnerUserID == 0:
		// 全部数据范围 + 公海可见：不过滤。
	default:
		// 公海与数据范围取并集：范围外的有负责人客户仍不可见。
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(crm_customer.owner_dept_id IN ? OR crm_customer.owner_user_id = ?"+
				" OR crm_customer.owner_user_id IS NULL)", f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("(crm_customer.owner_dept_id IN ? OR crm_customer.owner_user_id IS NULL)", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("(crm_customer.owner_user_id = ? OR crm_customer.owner_user_id IS NULL)", f.SelfOwnerUserID)
		default:
			// 无可见部门且不含本人：只剩公海。
			q = q.Where("crm_customer.owner_user_id IS NULL")
		}
	}
	err := q.Count(&count).Error
	return count, err
}

// SumDashboardOpportunityAmountsByStage 按阶段聚合当前数据范围内可见商机的
// 数量与预计金额（stage 由调用方限定 1–6，金额列是 expected_amount）。
func (r *Repository) SumDashboardOpportunityAmountsByStage(ctx context.Context, f DashboardFilter, stages []int8) ([]DashboardStageRow, error) {
	q := r.db.WithContext(ctx).Model(&model.Opportunity{}).
		Select("stage, COUNT(*) AS item_count, COALESCE(SUM(expected_amount), 0) AS total_amount").
		Where("stage IN ?", stages).
		Group("stage").
		Order("stage ASC")
	q = applyDashboardOwnerScope(q, f, "crm_opportunity.owner_dept_id", "crm_opportunity.owner_user_id")
	var rows []DashboardStageRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// SumDashboardSignedContractAmount 求当前数据范围内已签合同（status=3）的金额合计。
func (r *Repository) SumDashboardSignedContractAmount(ctx context.Context, f DashboardFilter) (money.Money, error) {
	var row struct {
		Total money.Money `gorm:"column:total"`
	}
	q := r.db.WithContext(ctx).Model(&model.Contract{}).
		Select("COALESCE(SUM(amount), 0) AS total").
		Where("status = ?", model.ContractStatusSigned)
	q = applyDashboardOwnerScope(q, f, "crm_contract.owner_dept_id", "crm_contract.owner_user_id")
	if err := q.Find(&row).Error; err != nil {
		return money.Money{}, err
	}
	return row.Total, nil
}

// dashboardDuePlanBaseQuery 构造到期计划查询：JOIN 合同（数据权限）+
// LEFT JOIN 已收金额聚合子查询，投影 received_amount。
func (r *Repository) dashboardDuePlanBaseQuery(ctx context.Context, f DashboardFilter, today string) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ReceivablePlan{}).
		Select("crm_receivable_plan.amount, COALESCE(crm_receivable_record_sum.received, 0) AS received_amount,"+
			" crm_receivable_plan.due_date").
		Joins("JOIN crm_contract ON crm_contract.id = crm_receivable_plan.contract_id"+
			" AND crm_contract.deleted_at IS NULL"+
			" AND crm_contract.tenant_id = ?", tenant.ID(ctx)).
		Joins("LEFT JOIN (SELECT plan_id, SUM(amount) AS received FROM crm_receivable_record"+
			" WHERE deleted_at IS NULL AND tenant_id = ? GROUP BY plan_id) crm_receivable_record_sum"+
			" ON crm_receivable_record_sum.plan_id = crm_receivable_plan.id", tenant.ID(ctx)).
		Where("crm_receivable_plan.due_date < ?", today)
	return applyDashboardOwnerScope(q, f, "crm_contract.owner_dept_id", "crm_contract.owner_user_id")
}

// ListDashboardDuePlans 返回到期日严格早于 today（yyyy-MM-dd，应用时区口径）的
// 可见回款计划快照；是否逾期（已收 < 计划额）由 service 纯函数复核。
func (r *Repository) ListDashboardDuePlans(ctx context.Context, f DashboardFilter, today string) ([]DashboardDuePlanRow, error) {
	var rows []DashboardDuePlanRow
	if err := r.dashboardDuePlanBaseQuery(ctx, f, today).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListDashboardSignedContractAmounts 返回 [from, until) 内已签合同的签订日期与
// 金额（from/until 为 yyyy-MM-dd，应用时区口径）；归月与补零由 service 完成。
func (r *Repository) ListDashboardSignedContractAmounts(ctx context.Context, f DashboardFilter, from, until string) ([]DashboardDatedRow, error) {
	q := r.db.WithContext(ctx).Model(&model.Contract{}).
		Select("sign_date AS event_date, amount").
		Where("status = ? AND sign_date IS NOT NULL AND sign_date >= ? AND sign_date < ?",
			model.ContractStatusSigned, from, until)
	q = applyDashboardOwnerScope(q, f, "crm_contract.owner_dept_id", "crm_contract.owner_user_id")
	var rows []DashboardDatedRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListDashboardReceiptAmounts 返回 [from, until) 内回款记录的到账日期与金额，
// 可见性 JOIN 合同后套用合同 owner 谓词（spec §4.1）。
func (r *Repository) ListDashboardReceiptAmounts(ctx context.Context, f DashboardFilter, from, until string) ([]DashboardDatedRow, error) {
	q := r.db.WithContext(ctx).Model(&model.ReceivableRecord{}).
		Select("crm_receivable_record.receipt_date AS event_date, crm_receivable_record.amount").
		Joins("JOIN crm_contract ON crm_contract.id = crm_receivable_record.contract_id"+
			" AND crm_contract.deleted_at IS NULL"+
			" AND crm_contract.tenant_id = ?", tenant.ID(ctx)).
		Where("crm_receivable_record.receipt_date >= ? AND crm_receivable_record.receipt_date < ?", from, until)
	q = applyDashboardOwnerScope(q, f, "crm_contract.owner_dept_id", "crm_contract.owner_user_id")
	var rows []DashboardDatedRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
