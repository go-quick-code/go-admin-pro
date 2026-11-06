package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// SensitiveWordFilter 是敏感词列表过滤条件（spec §5.6：GET page）。
// 租户维度由插件按上下文追加，调用方必须携带租户上下文。
type SensitiveWordFilter struct {
	Word     string
	Category string
	Status   *int8
}

// ListSensitiveWords 敏感词分页（system_sensitive_word 登记 TenantTables，
// 插件自动限定当前租户）。
func (r *Repository) ListSensitiveWords(ctx context.Context, f SensitiveWordFilter, offset, limit int) ([]model.SensitiveWord, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SensitiveWord{})
	if f.Word != "" {
		q = q.Where("word LIKE ?", "%"+f.Word+"%")
	}
	if f.Category != "" {
		q = q.Where("category LIKE ?", "%"+f.Category+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SensitiveWord
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// FindSensitiveWordByID 按主键查敏感词（租户条件由插件追加，跨租户 404）；
// notFound 把 gorm.ErrRecordNotFound 转成 (nil, nil)，调用方显式判空。
func (r *Repository) FindSensitiveWordByID(ctx context.Context, id int64) (*model.SensitiveWord, error) {
	var row model.SensitiveWord
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// CountSensitiveWordsByWord 统计当前租户同名词数（创建/修改唯一性校验；
// 软删行不计入——deleted_at 入唯一键，软删后同词可重新添加）。
func (r *Repository) CountSensitiveWordsByWord(ctx context.Context, word string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SensitiveWord{}).Where("word = ?", word)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

// CountSensitiveWords 统计当前租户词库总数（单租户上限校验，spec §4.7.1）。
func (r *Repository) CountSensitiveWords(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.SensitiveWord{}).Count(&total).Error
	return total, err
}

// ListEnabledSensitiveWords 取当前租户全部启用词（匹配缓存构建用）。
// 词量上限 5000，一次载入内存可接受。
func (r *Repository) ListEnabledSensitiveWords(ctx context.Context) ([]model.SensitiveWord, error) {
	var list []model.SensitiveWord
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("id ASC").
		Find(&list).Error
	return list, err
}

func (r *Repository) CreateSensitiveWord(ctx context.Context, row *model.SensitiveWord) error {
	return r.db.WithContext(ctx).Create(row).Error
}

func (r *Repository) SaveSensitiveWord(ctx context.Context, row *model.SensitiveWord) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteSensitiveWord 软删（deleted_at 入唯一键，同词可重新添加）。
func (r *Repository) DeleteSensitiveWord(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.SensitiveWord{}, id).Error
}
