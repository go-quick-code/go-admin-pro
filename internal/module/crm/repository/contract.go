package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// ContractFilter 是合同列表查询条件。
// OwnerDeptIDs 与 SelfOwnerUserID 共同表达数据范围谓词（internal/pkg/datascope.Filter
// 到 owner_dept_id / owner_user_id 列的翻译，语义与 CustomerFilter/OpportunityFilter 一致）：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type ContractFilter struct {
	Name       string
	Status     *int8
	CustomerID int64

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindContractByID 按 ID 查合同（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindContractByID(ctx context.Context, id int64) (*model.Contract, error) {
	var c model.Contract
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// ListContracts 按筛选与数据范围谓词分页查询合同。
func (r *Repository) ListContracts(ctx context.Context, f ContractFilter, offset, limit int) ([]model.Contract, int64, error) {
	q := r.contractQuery(ctx, f)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Contract
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllContracts 按筛选与数据范围谓词查询全部合同（Excel 导出用，不分页）。
func (r *Repository) ListAllContracts(ctx context.Context, f ContractFilter) ([]model.Contract, error) {
	var list []model.Contract
	if err := r.contractQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// contractQuery 构造合同列表/导出的公共查询（筛选 + 数据范围谓词）。
func (r *Repository) contractQuery(ctx context.Context, f ContractFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Contract{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
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
	return q
}

// FindContractsByIDs 批量查合同（跟进列表补合同名称用），缺行不报错。
// 放在合同侧文件而非 product.go：product.go 是 codegen 生成物，不掺手写查询。
func (r *Repository) FindContractsByIDs(ctx context.Context, ids []int64) ([]model.Contract, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Contract
	if err := r.db.WithContext(ctx).Find(&list, ids).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CreateContract 创建合同。必须已在 contract_no 赋值后调用（见 service.CreateContract）。
func (r *Repository) CreateContract(ctx context.Context, c *model.Contract) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// SaveContract 全量保存合同（更新路径），编号列由 service 层保证不改写；
// 状态与驳回原因不在编辑路径变更（状态流转走条件更新 TransitionContractStatus）。
func (r *Repository) SaveContract(ctx context.Context, c *model.Contract) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// DeleteContract 按 ID 软删除合同（GORM deleted_at，租户插件自动带租户条件）。
// 行项目为关联表无软删除，随合同不可见；产品删除引用校验只统计未删除合同的行项目。
func (r *Repository) DeleteContract(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Contract{}, id).Error
}

// TransitionContractStatus 条件更新状态（乐观并发：只在状态仍为 from 时生效），
// 返回受影响行数；0 表示状态已被并发请求变更，调用方报冲突。
// 驳回（to=草稿）时写入驳回原因；提交审批（to=待审）时清空上一轮驳回原因。
func (r *Repository) TransitionContractStatus(ctx context.Context, id int64, from, to int8, reason string) (int64, error) {
	updates := map[string]any{"status": to}
	switch to {
	case model.ContractStatusDraft:
		updates["reject_reason"] = reason
	case model.ContractStatusPending:
		updates["reject_reason"] = ""
	}
	res := r.db.WithContext(ctx).Model(&model.Contract{}).
		Where("id = ? AND status = ?", id, from).
		Updates(updates)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ReplaceContractProducts 全量替换合同行项目（先删后插，必须在合同保存的
// 同一事务里调用，行项目变更随合同整体保存）。items 由 service 层校验并算好金额；
// 归属合同 ID 以本入参为准回填（创建路径合同 ID 落库后才产生）。
func (r *Repository) ReplaceContractProducts(ctx context.Context, contractID int64, items []model.ContractProduct) error {
	if err := r.db.WithContext(ctx).
		Where("contract_id = ?", contractID).
		Delete(&model.ContractProduct{}).Error; err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		items[i].ContractID = contractID
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// ListContractProducts 查合同行项目（按产品 ID 升序，稳定展示顺序）。
func (r *Repository) ListContractProducts(ctx context.Context, contractID int64) ([]model.ContractProduct, error) {
	var list []model.ContractProduct
	if err := r.db.WithContext(ctx).
		Where("contract_id = ?", contractID).
		Order("product_id ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CountContractProductsByProduct 统计引用某产品的未删除合同行项目数
// （ProductRefCountChecker 的合同侧实现，issue #11/#16）：行项目表无软删除，
// 「未删除」以所属合同 deleted_at IS NULL 为准——合同软删后其行项目不再算引用。
// 租户条件说明：插件只给主表（Model 指定的 crm_contract_product）追加条件，
// 被关联的 crm_contract 在这里显式带上同值条件（fail-closed：无租户上下文时插件先拦截）。
func (r *Repository) CountContractProductsByProduct(ctx context.Context, productID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ContractProduct{}).
		Joins("JOIN crm_contract ON crm_contract.id = crm_contract_product.contract_id"+
			" AND crm_contract.deleted_at IS NULL"+
			" AND crm_contract.tenant_id = ?", tenant.ID(ctx)).
		Where("crm_contract_product.product_id = ?", productID).
		Count(&count).Error
	return count, err
}

// ActiveContractCustomerIDs 返回给定客户中存在「未作废合同」的客户 ID 集合
// （ContractChecker 实现，issue #14/#16，spec §4.2 自动回收排除）：
// 作废合同与无合同都不进集合；行级软删合同不算（deleted_at IS NULL）。
// ids 为空直接返回空集。租户条件由插件按主表 crm_contract 自动追加。
func (r *Repository) ActiveContractCustomerIDs(ctx context.Context, customerIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(customerIDs))
	if len(customerIDs) == 0 {
		return result, nil
	}
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.Contract{}).
		Where("customer_id IN ? AND status <> ?", customerIDs, model.ContractStatusVoid).
		Distinct("customer_id").
		Pluck("customer_id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}
