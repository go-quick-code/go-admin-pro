package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// FollowUpFilter 是跟进列表查询条件。
// 可见性跟随父记录（docs/crm-mvp-spec.md §4.1：跟进 join 父记录后套用父谓词）：
// 挂客户跟随客户 owner_*，挂商机/合同跟随各自父记录 owner_*（#15/#16）。
// OwnerDeptIDs 与 SelfOwnerUserID 是 datascope.Filter 到父记录 owner_dept_id /
// owner_user_id 列的翻译，语义与 CustomerFilter 一致：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type FollowUpFilter struct {
	CustomerID    int64
	OpportunityID int64
	ContractID    int64
	Type          *int8

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindFollowUpByID 按 ID 查跟进（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindFollowUpByID(ctx context.Context, id int64) (*model.FollowUp, error) {
	var f model.FollowUp
	if err := r.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

// ListFollowUps 按筛选与数据范围谓词分页查询跟进。
// LEFT JOIN 三个父记录（客户/商机/合同，三选一）套父谓词：父记录不可见（范围外或
// 已软删除）的跟进一并不可见。每条跟进恰有一个父记录，COALESCE 取到存在一侧
// 的 owner_*（其余侧 join 不中为 NULL）；全部不存在（父已删）则整行不可见。
// 租户条件说明：插件只给主表 crm_follow_up 追加 tenant_id，被关联的
// crm_customer / crm_opportunity / crm_contract 在这里显式带上同值条件（fail-closed）。
func (r *Repository) ListFollowUps(ctx context.Context, f FollowUpFilter, offset, limit int) ([]model.FollowUp, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.FollowUp{}).
		Joins("LEFT JOIN crm_customer ON crm_customer.id = crm_follow_up.customer_id"+
			" AND crm_customer.deleted_at IS NULL"+
			" AND crm_customer.tenant_id = ?", tenant.ID(ctx)).
		Joins("LEFT JOIN crm_opportunity ON crm_opportunity.id = crm_follow_up.opportunity_id"+
			" AND crm_opportunity.deleted_at IS NULL"+
			" AND crm_opportunity.tenant_id = ?", tenant.ID(ctx)).
		Joins("LEFT JOIN crm_contract ON crm_contract.id = crm_follow_up.contract_id"+
			" AND crm_contract.deleted_at IS NULL"+
			" AND crm_contract.tenant_id = ?", tenant.ID(ctx)).
		// 父记录必须存在：客户/商机/合同删除后其跟进随之不可见（spec §4.5）。
		Where("COALESCE(crm_customer.id, crm_opportunity.id, crm_contract.id) IS NOT NULL")
	if f.CustomerID > 0 {
		q = q.Where("crm_follow_up.customer_id = ?", f.CustomerID)
	}
	if f.OpportunityID > 0 {
		q = q.Where("crm_follow_up.opportunity_id = ?", f.OpportunityID)
	}
	if f.ContractID > 0 {
		q = q.Where("crm_follow_up.contract_id = ?", f.ContractID)
	}
	if f.Type != nil {
		q = q.Where("crm_follow_up.type = ?", *f.Type)
	}
	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		deptExpr := "COALESCE(crm_customer.owner_dept_id, crm_opportunity.owner_dept_id, crm_contract.owner_dept_id)"
		userExpr := "COALESCE(crm_customer.owner_user_id, crm_opportunity.owner_user_id, crm_contract.owner_user_id)"
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where(fmt.Sprintf("(%s IN ? OR %s = ?)", deptExpr, userExpr), f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where(fmt.Sprintf("%s IN ?", deptExpr), f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where(fmt.Sprintf("%s = ?", userExpr), f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.FollowUp
	if err := q.Order("crm_follow_up.id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateFollowUp 创建跟进并在同一事务里刷新所属客户 last_follow_time
// （docs/crm-mvp-spec.md §4.2：新增跟进刷新；编辑/删除不回退——
// last_follow_time 语义是「最后一次跟进时刻」，改内容或删记录不改变「联系过」的事实）。
// refreshCustomerID 是要刷新 last_follow_time 的客户：挂客户的跟进传 CustomerID；
// 挂商机的跟进（issue #15）传商机所属客户——规格 §4.2「跟进任一子记录都刷新
// 客户的 last_follow_time」，商机是客户的子记录。传 0 不刷新（预留）。
// 事务保证：跟进写失败或客户刷新失败时整体回滚，不会出现有跟进但时间没刷新的中间态。
func (r *Repository) CreateFollowUp(ctx context.Context, f *model.FollowUp, refreshCustomerID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(f).Error; err != nil {
			return err
		}
		if refreshCustomerID <= 0 {
			return nil
		}
		// 租户插件对 Update 也按主模型 crm_customer 自动追加 tenant_id 条件。
		return tx.Model(&model.Customer{}).
			Where("id = ?", refreshCustomerID).
			Update("last_follow_time", time.Now()).Error
	})
}

// SaveFollowUp 全量保存跟进（更新路径），所属客户列由 service 层保证不改写。
// 编辑不刷新 last_follow_time（取舍见 CreateFollowUp 注释）。
func (r *Repository) SaveFollowUp(ctx context.Context, f *model.FollowUp) error {
	return r.db.WithContext(ctx).Save(f).Error
}

// DeleteFollowUp 按 ID 软删除跟进（GORM deleted_at，租户插件自动带租户条件）。
// 删除不回退 last_follow_time（取舍见 CreateFollowUp 注释）。
func (r *Repository) DeleteFollowUp(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.FollowUp{}, id).Error
}
