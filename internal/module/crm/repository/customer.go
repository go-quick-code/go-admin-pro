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

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"gorm.io/gorm"
)

// Repository 是 crm 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 crm Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

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
type CustomerFilter struct {
	Name        string
	Status      *int8
	OwnerUserID int64
	OwnerDeptID int64

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindCustomerByID 按 ID 查客户（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindCustomerByID(ctx context.Context, id int64) (*model.Customer, error) {
	var c model.Customer
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// ListCustomers 按筛选与数据范围谓词分页查询客户。
func (r *Repository) ListCustomers(ctx context.Context, f CustomerFilter, offset, limit int) ([]model.Customer, int64, error) {
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
	var list []model.Customer
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateCustomer 创建客户。必须已在 customer_no 赋值后调用（见 service.CreateCustomer）。
func (r *Repository) CreateCustomer(ctx context.Context, c *model.Customer) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// SaveCustomer 全量保存客户（更新路径），编号列由 service 层保证不改写。
func (r *Repository) SaveCustomer(ctx context.Context, c *model.Customer) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// DeleteCustomer 按 ID 软删除客户（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteCustomer(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Customer{}, id).Error
}

// NextSequenceValue 在 (tenantID, bizType, dateKey) 上原子发放下一个序号。
//
// 并发安全方案（issue #8：并发下不重号）：单条 INSERT ... ON DUPLICATE KEY UPDATE
// 在唯一键（即主键）的行锁内完成「首行插入或自增」，并发请求在同一行上串行化；
// 表只有这一个唯一索引，不存在多索引交叉加锁，且所有事务先取序列锁再写客户行，
// 加锁顺序一致，无死锁环。随后在同一事务内回读该行的 last_value：
// 本事务持有的行锁保证读到的就是本事务发放的值，不会读入并发方已发放的更大值。
//
// 必须包在事务里：autocommit 下 upsert 与回读之间允许并发发放，会把同一序号
// 发给两个请求。租户值为显式入参：裸 SQL 不经租户插件。
// 标识符统一加反引号：last_value 与 MySQL 8 窗口函数关键字 LAST_VALUE 同形，
// 裸 SQL 的列清单里不加引号会被解析器拒绝（GORM 生成的语句自带引号，不受影响）。
func (r *Repository) NextSequenceValue(ctx context.Context, tenantID int64, bizType, dateKey string) (int64, error) {
	var value int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"INSERT INTO `crm_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, 1) "+
				"ON DUPLICATE KEY UPDATE `last_value` = `last_value` + 1",
			tenantID, bizType, dateKey,
		).Error; err != nil {
			return err
		}
		return tx.Raw(
			"SELECT `last_value` FROM `crm_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?",
			tenantID, bizType, dateKey,
		).Scan(&value).Error
	})
	if err != nil {
		return 0, err
	}
	return value, nil
}

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository。
// 编号发放与客户落库必须同事务：任一步失败整体回滚，不消耗序号、不留脏行。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}
