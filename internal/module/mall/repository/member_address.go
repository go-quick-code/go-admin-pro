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
