package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// SocialClientFilter 是社交客户端列表过滤条件（平台级资源，无租户维度）。
type SocialClientFilter struct {
	Code     string
	Name     string
	Provider string
	Status   *int8
}

// ListSocialClients 社交客户端分页。表是平台级资源（未登记 TenantTables），
// 插件不做租户改写，直接查询。
func (r *Repository) ListSocialClients(ctx context.Context, f SocialClientFilter, offset, limit int) ([]model.SocialClient, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SocialClient{})
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
	}
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Provider != "" {
		q = q.Where("provider = ?", f.Provider)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SocialClient
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// FindSocialClientByID 按主键查客户端；notFound 把 gorm.ErrRecordNotFound
// 转成 (nil, nil)，调用方必须显式判空并返回 404。
func (r *Repository) FindSocialClientByID(ctx context.Context, id int64) (*model.SocialClient, error) {
	var client model.SocialClient
	if err := r.db.WithContext(ctx).First(&client, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &client, nil
}

// CountSocialClientsByCode 统计同编码客户端数（创建/修改唯一性校验）。
func (r *Repository) CountSocialClientsByCode(ctx context.Context, code string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SocialClient{}).Where("code = ?", code)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

// FindEnabledSocialClientByProvider 取某平台第一个启用的客户端（登录页按
// provider 展示按钮，spec §4.4.1；多客户端时取 ID 最小者，骨架从简）。
func (r *Repository) FindEnabledSocialClientByProvider(ctx context.Context, provider string) (*model.SocialClient, error) {
	var client model.SocialClient
	err := r.db.WithContext(ctx).
		Where("provider = ? AND status = ?", provider, model.StatusEnable).
		Order("id ASC").
		First(&client).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &client, nil
}

func (r *Repository) CreateSocialClient(ctx context.Context, client *model.SocialClient) error {
	return r.db.WithContext(ctx).Create(client).Error
}

// ListEnabledSocialClients 列出全部启用客户端（登录页可用平台列表用，
// issue #77；平台级资源，无租户维度）。
func (r *Repository) ListEnabledSocialClients(ctx context.Context) ([]model.SocialClient, error) {
	var list []model.SocialClient
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("id ASC").
		Find(&list).Error
	return list, err
}

func (r *Repository) SaveSocialClient(ctx context.Context, client *model.SocialClient) error {
	return r.db.WithContext(ctx).Save(client).Error
}

func (r *Repository) DeleteSocialClient(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.SocialClient{}, id).Error
}

// SocialUserFilter 是绑定用户列表过滤条件（租户维度由插件按上下文追加）。
type SocialUserFilter struct {
	Provider string
	UserID   int64
}

// ListSocialUsers 绑定关系分页。system_social_user 登记 TenantTables，
// 调用方必须携带租户上下文（登录用户/回调签发路径均已注入）。
func (r *Repository) ListSocialUsers(ctx context.Context, f SocialUserFilter, offset, limit int) ([]model.SocialUser, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SocialUser{})
	if f.Provider != "" {
		q = q.Where("provider = ?", f.Provider)
	}
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SocialUser
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// FindSocialUserByID 按主键查绑定关系（租户条件由插件追加，跨租户 404）。
func (r *Repository) FindSocialUserByID(ctx context.Context, id int64) (*model.SocialUser, error) {
	var su model.SocialUser
	if err := r.db.WithContext(ctx).First(&su, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &su, nil
}

// FindSocialUserBinding 按 (tenant, provider, openid) 查启用绑定；租户条件
// 由插件按上下文追加。未找到返回 (nil, nil)。
func (r *Repository) FindSocialUserBinding(ctx context.Context, provider, openid string) (*model.SocialUser, error) {
	var su model.SocialUser
	err := r.db.WithContext(ctx).
		Where("provider = ? AND openid = ?", provider, openid).
		First(&su).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &su, nil
}

func (r *Repository) CreateSocialUser(ctx context.Context, su *model.SocialUser) error {
	return r.db.WithContext(ctx).Create(su).Error
}

func (r *Repository) SaveSocialUser(ctx context.Context, su *model.SocialUser) error {
	return r.db.WithContext(ctx).Save(su).Error
}

// DeleteSocialUser 解绑：软删（deleted_at 入唯一键，同一 openid 可重新绑定）。
func (r *Repository) DeleteSocialUser(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.SocialUser{}, id).Error
}

// ListUsersByIDs 批量取用户（绑定列表回填用户名，行数受分页限制）。
func (r *Repository) ListUsersByIDs(ctx context.Context, ids []int64) ([]model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var users []model.User
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error
	return users, err
}
