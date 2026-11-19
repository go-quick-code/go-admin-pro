package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// member_address.go 是会员收货地址的查询（issue #174）：T1 管理端只读
//（按会员查地址），写入随 C 端自助（T2/T5）。user_id 归属过滤在 service 层
// 强制（查指定会员时先做会员存在性校验，跨会员 ID 不可见）。

// AddressFilter 是地址分页查询条件。
type AddressFilter struct {
	UserID   int64
	Receiver string
}

// ListAddressesByUser 按会员查询地址清单（默认地址在前，其余 id ASC——
// 地址是有序业务数据，照等级页升序口径）。
func (r *Repository) ListAddressesByUser(ctx context.Context, f AddressFilter, page web.PageQuery) ([]model.MemberAddress, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.MemberAddress{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Receiver != "" {
		q = q.Where("receiver_name LIKE ?", "%"+f.Receiver+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.MemberAddress
	if err := q.Order("is_default DESC, id ASC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllAddressesByUser 按会员取全部地址（会员详情一次带出，不分页）。
func (r *Repository) ListAllAddressesByUser(ctx context.Context, userID int64) ([]model.MemberAddress, error) {
	var list []model.MemberAddress
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("is_default DESC, id ASC").Find(&list).Error
	return list, err
}

// FindAddressByID 按 ID 查地址（租户插件隔离；user_id 归属校验在 service 层
// ——地址行只对归属会员可见，跨会员 ID 一律 404）。未找到返回 (nil, nil)。
// 下单收货快照（issue #179）与 C 端自助消费。
func (r *Repository) FindAddressByID(ctx context.Context, id int64) (*model.MemberAddress, error) {
	var row model.MemberAddress
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ---------- C 端自助写入（issue #178/T6 补口：规格 §4 C 端地址维护；
// 管理端 T1 只读，写入唯一入口在 C 端 service 层）----------

// CountAddressesByUser 返回会员地址数量（C 端新增上限校验用）。
func (r *Repository) CountAddressesByUser(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.MemberAddress{}).
		Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

// CreateAddress 新增地址；is_default 由 service 在事务内先清旧默认再写入。
func (r *Repository) CreateAddress(ctx context.Context, row *model.MemberAddress) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// UpdateAddressFields 按 (id, user_id) 谓词更新字段（map 键即白名单，由
// service 从校验后的请求构造；越权/不存在返回 0 行）。软删行由 gorm
// DeletedAt 谓词天然排除。
func (r *Repository) UpdateAddressFields(ctx context.Context, userID, id int64, fields map[string]any) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.MemberAddress{}).
		Where("id = ? AND user_id = ?", id, userID).Updates(fields)
	return res.RowsAffected, res.Error
}

// ClearDefaultAddress 清掉该会员的全部默认位（设新默认前的同事务前置）。
func (r *Repository) ClearDefaultAddress(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Model(&model.MemberAddress{}).
		Where("user_id = ? AND is_default = 1", userID).
		Update("is_default", 0).Error
}

// SetDefaultAddressByID 按 (id, user_id) 谓词置默认（0 行 = 越权/不存在）。
func (r *Repository) SetDefaultAddressByID(ctx context.Context, userID, id int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.MemberAddress{}).
		Where("id = ? AND user_id = ?", id, userID).Update("is_default", 1)
	return res.RowsAffected, res.Error
}

// DeleteAddressByID 按 (id, user_id) 谓词软删（0 行 = 越权/不存在）。
func (r *Repository) DeleteAddressByID(ctx context.Context, userID, id int64) (int64, error) {
	res := r.db.WithContext(ctx).Delete(&model.MemberAddress{}, "id = ? AND user_id = ?", id, userID)
	return res.RowsAffected, res.Error
}

// PromoteOldestAddress 删除默认地址后的自动顶位：把剩余地址中最早一条置为
// 默认（结算页「默认地址」语义需要非空集；0 行 = 无剩余地址，合法终态）。
func (r *Repository) PromoteOldestAddress(ctx context.Context, userID int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.MemberAddress{}).
		Where("user_id = ?", userID).
		Order("id ASC").Limit(1).
		Update("is_default", 1)
	return res.RowsAffected, res.Error
}
