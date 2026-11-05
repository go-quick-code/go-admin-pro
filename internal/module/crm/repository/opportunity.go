package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
)

// OpportunityFilter 是商机列表查询条件。
// OwnerDeptIDs 与 SelfOwnerUserID 共同表达数据范围谓词（internal/pkg/datascope.Filter
// 到 owner_dept_id / owner_user_id 列的翻译，语义与 CustomerFilter 一致）：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type OpportunityFilter struct {
	Name       string
	Stage      *int8
	CustomerID int64

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindOpportunityByID 按 ID 查商机（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindOpportunityByID(ctx context.Context, id int64) (*model.Opportunity, error) {
	var o model.Opportunity
	if err := r.db.WithContext(ctx).First(&o, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &o, nil
}

// ListOpportunities 按筛选与数据范围谓词分页查询商机。
func (r *Repository) ListOpportunities(ctx context.Context, f OpportunityFilter, offset, limit int) ([]model.Opportunity, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Opportunity{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Stage != nil {
		q = q.Where("stage = ?", *f.Stage)
	}
	if f.CustomerID > 0 {
		q = q.Where("customer_id = ?", f.CustomerID)
	}
	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(owner_dept_id IN ? OR owner_user_id = ?)", f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("owner_dept_id IN ?", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("owner_user_id = ?", f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Opportunity
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// FindOpportunitiesByIDs 批量查商机（跟进列表补商机名称用），保序不保证，缺行不报错。
func (r *Repository) FindOpportunitiesByIDs(ctx context.Context, ids []int64) ([]model.Opportunity, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Opportunity
	if err := r.db.WithContext(ctx).Find(&list, ids).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CreateOpportunity 创建商机。必须已在 opportunity_no 赋值后调用（见 service.CreateOpportunity）。
func (r *Repository) CreateOpportunity(ctx context.Context, o *model.Opportunity) error {
	return r.db.WithContext(ctx).Create(o).Error
}

// SaveOpportunity 全量保存商机（更新路径），编号列由 service 层保证不改写；
// 阶段不在编辑路径变更（阶段推进走条件更新 AdvanceOpportunityStage）。
func (r *Repository) SaveOpportunity(ctx context.Context, o *model.Opportunity) error {
	return r.db.WithContext(ctx).Save(o).Error
}

// DeleteOpportunity 按 ID 软删除商机（GORM deleted_at，租户插件自动带租户条件）。
// 行项目为关联表无软删除，随商机不可见；产品删除引用校验只统计未删除商机的行项目。
func (r *Repository) DeleteOpportunity(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Opportunity{}, id).Error
}

// AdvanceOpportunityStage 条件更新阶段（乐观并发：只在阶段仍为 from 时生效）。
// 返回受影响行数；0 表示阶段已被并发请求变更，调用方报冲突。
func (r *Repository) AdvanceOpportunityStage(ctx context.Context, id int64, from, to int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Opportunity{}).
		Where("id = ? AND stage = ?", id, from).
		Update("stage", to)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ReplaceOpportunityProducts 全量替换商机行项目（先删后插，必须在商机保存的
// 同一事务里调用，行项目变更随商机整体保存）。items 由 service 层校验并算好金额；
// 归属商机 ID 以本入参为准回填（创建路径商机 ID 落库后才产生）。
func (r *Repository) ReplaceOpportunityProducts(ctx context.Context, opportunityID int64, items []model.OpportunityProduct) error {
	if err := r.db.WithContext(ctx).
		Where("opportunity_id = ?", opportunityID).
		Delete(&model.OpportunityProduct{}).Error; err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		items[i].OpportunityID = opportunityID
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// ListOpportunityProducts 查商机行项目（按产品 ID 升序，稳定展示顺序）。
func (r *Repository) ListOpportunityProducts(ctx context.Context, opportunityID int64) ([]model.OpportunityProduct, error) {
	var list []model.OpportunityProduct
	if err := r.db.WithContext(ctx).
		Where("opportunity_id = ?", opportunityID).
		Order("product_id ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CountOpportunityProductsByProduct 统计引用某产品的未删除商机行项目数
// （ProductRefCountChecker 实现，issue #11/#15）：行项目表无软删除，
// 「未删除」以所属商机 deleted_at IS NULL 为准——商机软删后其行项目不再算引用。
// 租户条件说明：插件只给主表（Model 指定的 crm_opportunity_product）追加条件，
// 被关联的 crm_opportunity 在这里显式带上同值条件（fail-closed：无租户上下文时插件先拦截）。
func (r *Repository) CountOpportunityProductsByProduct(ctx context.Context, productID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.OpportunityProduct{}).
		Joins("JOIN crm_opportunity ON crm_opportunity.id = crm_opportunity_product.opportunity_id"+
			" AND crm_opportunity.deleted_at IS NULL"+
			" AND crm_opportunity.tenant_id = ?", tenant.ID(ctx)).
		Where("crm_opportunity_product.product_id = ?", productID).
		Count(&count).Error
	return count, err
}

// FindProductsByIDs 批量查产品（商机行项目校验产品存在与补名称用，issue #15），
// 缺行不报错，由调用方按存在性判断。放在商机侧文件而非 product.go：
// product.go 是 codegen 生成物（compile_check 用生成产物覆盖校验），不掺手写查询。
func (r *Repository) FindProductsByIDs(ctx context.Context, ids []int64) ([]model.Product, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Product
	if err := r.db.WithContext(ctx).Find(&list, ids).Error; err != nil {
		return nil, err
	}
	return list, nil
}
