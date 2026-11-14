// Package repository 是 crm 模块的 DAL 层。
// 所有查询/写入通过租户插件自动带 tenant_id 条件（crm_customer、crm_contact、
// crm_sequence 已登记 TenantTables），本层不手工拼主表的租户条件；
// 两条例外路径都显式说明：序列发放的裸 SQL（租户值显式入参，见
// NextSequenceValue）与联系人 join 父客户时对被关联表的手工租户条件
// （插件只给主表追加条件，见 ListContacts）。
package repository

import (
	"context"
	"errors"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/sequence"
	"gorm.io/gorm"
)

// Repository 是 crm 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 crm Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 返回底层 gorm 句柄（事务内调用跨模块服务时需要传递同一事务，
// issue #47：合同提交/回调复用 bpm 的 StartInstanceInTx/CancelInstanceInTx）。
func (r *Repository) DB() *gorm.DB { return r.db }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// CustomerFilter 是客户列表查询条件。
// OwnerDeptIDs 与 SelfOwnerUserID 共同表达数据范围谓词（internal/pkg/datascope.Filter
// 到 CRM 列名 owner_dept_id / owner_user_id 的翻译，与 system 模块 UserFilter 同构）：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
//
// 公海归属过滤（issue #13，两者互斥；皆假时不过滤归属，保持 #8 行为）：
//   - PoolOnly：只看公海客户（owner IS NULL）；公海列表不受数据范围过滤，
//     调用方 service 不再拼范围谓词（规格 §4.1）。
//   - ExcludePool：普通客户列表排除公海客户（公海在独立页面按 pool:view 展示）。
type CustomerFilter struct {
	Name        string
	Status      *int8
	OwnerUserID int64
	OwnerDeptID int64

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64

	PoolOnly    bool
	ExcludePool bool
}

// FindCustomerByID 按 ID 查客户（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindCustomerByID(ctx context.Context, id int64) (*model.Customer, error) {
	var c model.Customer
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// FindCustomersByNos 按客户编号批量查客户（联系人导入解析所属客户用；租户插件
// 自动过滤租户与软删除行）。编号为空的入参被忽略，缺号的不返回。
func (r *Repository) FindCustomersByNos(ctx context.Context, nos []string) ([]model.Customer, error) {
	unique := make([]string, 0, len(nos))
	seen := make(map[string]bool, len(nos))
	for _, no := range nos {
		if no == "" || seen[no] {
			continue
		}
		seen[no] = true
		unique = append(unique, no)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	var list []model.Customer
	if err := r.db.WithContext(ctx).Where("customer_no IN ?", unique).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
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

// customerQuery 构造客户列表/导出的公共查询（筛选 + 数据范围谓词）。
func (r *Repository) customerQuery(ctx context.Context, f CustomerFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Customer{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.OwnerUserID > 0 {
		q = q.Where("owner_user_id = ?", f.OwnerUserID)
	}
	if f.OwnerDeptID > 0 {
		q = q.Where("owner_dept_id = ?", f.OwnerDeptID)
	}
	switch {
	case f.PoolOnly:
		q = q.Where("owner_user_id IS NULL")
	case f.ExcludePool:
		q = q.Where("owner_user_id IS NOT NULL")
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

// CreateCustomer 创建客户。必须已在 customer_no 赋值后调用（见 service.CreateCustomer）。
func (r *Repository) CreateCustomer(ctx context.Context, c *model.Customer) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// UpdateCustomerFields 只更新请求字段（编辑接口），不写编号、status（由调用方
// 按请求是否携带决定）、owner_*（转移走 TransferCustomerOwner）、池字段与
// last_follow_time 等派生/归属列。用 map 而非结构体：GORM 结构体更新会跳过
// 零值，无法把字段清空，也会把读到的整行回写、覆盖并发改动。
// 返回受影响行数（0 表示行不存在或已被删除），租户插件自动带租户条件。
func (r *Repository) UpdateCustomerFields(ctx context.Context, id int64, fields map[string]any) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Customer{}).
		Where("id = ?", id).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// DeleteCustomer 按 ID 软删除客户（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteCustomer(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Customer{}, id).Error
}

// NextSequenceValue 在 (tenantID, bizType, dateKey) 上原子发放下一个序号；机制与
// 并发说明见 internal/pkg/sequence（唯一实现，本方法只是表名固定的薄包装）。
func (r *Repository) NextSequenceValue(ctx context.Context, tenantID int64, bizType, dateKey string) (int64, error) {
	return sequence.Next(ctx, r.db, "crm_sequence", tenantID, bizType, dateKey)
}

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository。
// 编号发放与客户落库必须同事务：任一步失败整体回滚，不消耗序号、不留脏行。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}
