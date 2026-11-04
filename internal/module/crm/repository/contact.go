package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
)

// ContactFilter 是联系人列表查询条件。
// 可见性跟随父客户（docs/crm-mvp-spec.md §4.1：联系人 join 父记录后套用父谓词），
// OwnerDeptIDs 与 SelfOwnerUserID 是 datascope.Filter 到父客户 owner_dept_id /
// owner_user_id 列的翻译，语义与 CustomerFilter 一致：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type ContactFilter struct {
	CustomerID int64
	Name       string
	Mobile     string

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindContactByID 按 ID 查联系人（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindContactByID(ctx context.Context, id int64) (*model.Contact, error) {
	var c model.Contact
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// ListContacts 按筛选与数据范围谓词分页查询联系人。
// join 父客户套父谓词：客户不可见（范围外或已软删除）的联系人一并不可见。
// 租户条件说明：插件只给主表 crm_contact 追加 tenant_id，被关联的 crm_customer
// 在这里显式带上同值条件（fail-closed：无租户上下文时插件先拦截，本条件取到 0 值也查不出数据）。
func (r *Repository) ListContacts(ctx context.Context, f ContactFilter, offset, limit int) ([]model.Contact, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Contact{}).
		Joins("JOIN crm_customer ON crm_customer.id = crm_contact.customer_id"+
			" AND crm_customer.deleted_at IS NULL"+
			" AND crm_customer.tenant_id = ?", tenant.ID(ctx))
	if f.CustomerID > 0 {
		q = q.Where("crm_contact.customer_id = ?", f.CustomerID)
	}
	if f.Name != "" {
		q = q.Where("crm_contact.name LIKE ?", "%"+f.Name+"%")
	}
	if f.Mobile != "" {
		q = q.Where("crm_contact.mobile LIKE ?", "%"+f.Mobile+"%")
	}
	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(crm_customer.owner_dept_id IN ? OR crm_customer.owner_user_id = ?)", f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("crm_customer.owner_dept_id IN ?", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("crm_customer.owner_user_id = ?", f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Contact
	if err := q.Order("crm_contact.id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateContact 创建联系人。
func (r *Repository) CreateContact(ctx context.Context, c *model.Contact) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// SaveContact 全量保存联系人（更新路径），所属客户列由 service 层保证不改写。
func (r *Repository) SaveContact(ctx context.Context, c *model.Contact) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// DeleteContact 按 ID 软删除联系人（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteContact(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Contact{}, id).Error
}

// FindCustomersByIDs 批量按 ID 查客户（租户插件自动过滤），供列表补客户名称。
func (r *Repository) FindCustomersByIDs(ctx context.Context, ids []int64) ([]model.Customer, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Customer
	if err := r.db.WithContext(ctx).Find(&list, ids).Error; err != nil {
		return nil, err
	}
	return list, nil
}
