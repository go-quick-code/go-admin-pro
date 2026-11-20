package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// member_user.go 是会员用户的数据访问原语与查询（issue #174）。
//
// 走 gorm 模型查询：租户插件自动追加 tenant_id 条件——FindMemberByMobile
// 的唯一性判定天然租户内（跨租户同号互不可见）；写入撞 UNIQUE(tenant_id,
// mobile) 时经 wrapDuplicateEntry 规整，service 层翻译 409。

// MemberFilter 是会员分页查询条件（管理端，spec §4）。
type MemberFilter struct {
	Mobile   string
	Nickname string
	LevelID  *int64
	Status   *int8
}

// FindMemberByID 按 ID 查会员（详情/存在性校验）：未找到返回 (nil, nil)。
func (r *Repository) FindMemberByID(ctx context.Context, id int64) (*model.MemberUser, error) {
	var row model.MemberUser
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// FindMemberByMobile 按手机号查会员（租户内）：T2 C 端注册/登录复用。
// 未找到返回 (nil, nil)。
func (r *Repository) FindMemberByMobile(ctx context.Context, mobile string) (*model.MemberUser, error) {
	var row model.MemberUser
	err := r.db.WithContext(ctx).Where("mobile = ?", mobile).First(&row).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// memberQuery 组装会员筛选查询（分页共用谓词，租户插件自动隔离）。
func (r *Repository) memberQuery(ctx context.Context, f MemberFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.MemberUser{})
	if f.Mobile != "" {
		q = q.Where("mobile = ?", f.Mobile)
	}
	if f.Nickname != "" {
		q = q.Where("nickname LIKE ?", "%"+f.Nickname+"%")
	}
	if f.LevelID != nil {
		q = q.Where("level_id = ?", *f.LevelID)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListMembers 分页查询会员（固定 id DESC，spec §4）。
func (r *Repository) ListMembers(ctx context.Context, f MemberFilter, page web.PageQuery) ([]model.MemberUser, int64, error) {
	q := r.memberQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.MemberUser
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateMember 创建会员。撞 UNIQUE(tenant_id, mobile) 返回规整后的
// ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreateMember(ctx context.Context, row *model.MemberUser) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateMemberFields 按列更新会员（列白名单在 service 层组装，照 pay
// UpdateAppFields 口径）。mobile 变更撞唯一键返回 ErrDuplicatedKey。
func (r *Repository) UpdateMemberFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.MemberUser{}).Where("id = ?", id).Updates(fields).Error)
}
