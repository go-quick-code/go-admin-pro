package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// member_level.go 是会员等级的数据访问原语与查询（issue #174）。
//
// name/level 双租户内唯一（插件自动隔离）；删除是软删，唯一键不含
// deleted_at（照 crm_product code 先例：软删行保持占位，同值不可重建）。
// 删除守卫「被会员引用禁删」的引用计数在 CountMembersByLevel。

// LevelFilter 是等级分页查询条件（管理端，spec §4）。
type LevelFilter struct {
	Name   string
	Status *int8
}

// FindLevelByID 按 ID 查等级：未找到返回 (nil, nil)。
func (r *Repository) FindLevelByID(ctx context.Context, id int64) (*model.MemberLevel, error) {
	var row model.MemberLevel
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// FindLevelByValue 按等级值查启用等级（租户内）：C 端注册默认等级的查询点
// （level=1「普通会员」，spec §3.1 种子行；issue #178）。过滤 status 启用
// ——停用等级不派发新注册会员；未找到返回 (nil, nil)（注册侧落 level_id=0
// 未分配，不因等级主数据缺失阻断注册）。
func (r *Repository) FindLevelByValue(ctx context.Context, level int) (*model.MemberLevel, error) {
	var row model.MemberLevel
	err := r.db.WithContext(ctx).
		Where("level = ? AND status = ?", level, model.StatusEnable).
		First(&row).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// levelQuery 组装等级筛选查询。
func (r *Repository) levelQuery(ctx context.Context, f LevelFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.MemberLevel{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListLevels 分页查询等级（固定 level ASC——等级页按等级值升序更贴合排序语义，
// id DESC 的 append 次序对等级无意义）。
func (r *Repository) ListLevels(ctx context.Context, f LevelFilter, page web.PageQuery) ([]model.MemberLevel, int64, error) {
	q := r.levelQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.MemberLevel
	if err := q.Order("level ASC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabledLevels 全量启用等级（等级值升序）：会员表单等级下拉的数据源
// （同 pay ListChannelOptions 的「清单只读」形状）。
func (r *Repository) ListEnabledLevels(ctx context.Context) ([]model.MemberLevel, error) {
	var list []model.MemberLevel
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("level ASC").Find(&list).Error
	return list, err
}

// CreateLevel 创建等级。撞 UNIQUE(tenant_id, name)/(tenant_id, level) 返回
// 规整后的 ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreateLevel(ctx context.Context, row *model.MemberLevel) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateLevelFields 按列更新等级（列白名单在 service 层组装）。name/level
// 变更撞唯一键返回 ErrDuplicatedKey。
func (r *Repository) UpdateLevelFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.MemberLevel{}).Where("id = ?", id).Updates(fields).Error)
}

// DeleteLevel 软删等级（删除守卫在 service 层：被会员引用 409）。
func (r *Repository) DeleteLevel(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.MemberLevel{}, "id = ?", id).Error
}

// CountMembersByLevel 统计引用该等级的会员数（删除守卫的引用计数：
// 不带 deleted_at 过滤——会员不可删，引用永久）。
func (r *Repository) CountMembersByLevel(ctx context.Context, levelID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.MemberUser{}).
		Where("level_id = ?", levelID).
		Count(&count).Error
	return count, err
}
