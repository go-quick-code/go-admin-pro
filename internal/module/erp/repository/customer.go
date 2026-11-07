package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// customer.go 是 ERP 客户的数据访问（issue #89，spec §3.1/§4.10）。

// CustomerFilter 是客户列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type CustomerFilter struct {
	Name   string
	Mobile string
	Status *int8

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindCustomerByID 按 ID 查客户（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindCustomerByID(ctx context.Context, id int64) (*model.Customer, error) {
	var row model.Customer
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListCustomers 按筛选与数据范围谓词分页查询客户。
func (r *Repository) ListCustomers(ctx context.Context, f CustomerFilter, offset, limit int) ([]model.Customer, int64, error) {
	q := r.customerQuery(ctx, f)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Customer
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllCustomers 按筛选与数据范围谓词查询全部客户（Excel 导出用，不分页）。
func (r *Repository) ListAllCustomers(ctx context.Context, f CustomerFilter) ([]model.Customer, error) {
	var list []model.Customer
	if err := r.customerQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// customerQuery 构造客户列表/导出的公共查询（筛选 + 数据范围谓词，见 scope.go）。
func (r *Repository) customerQuery(ctx context.Context, f CustomerFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Customer{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Mobile != "" {
		q = q.Where("mobile LIKE ?", "%"+f.Mobile+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CreateCustomer 创建客户。
func (r *Repository) CreateCustomer(ctx context.Context, row *model.Customer) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveCustomer 全量保存客户（更新路径；可空税务/开户字段 nil 落 NULL）。
func (r *Repository) SaveCustomer(ctx context.Context, row *model.Customer) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteCustomer 按 ID 软删除客户（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteCustomer(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Customer{}, id).Error
}
