package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// ---------- 字典类型 ----------

type DictTypeFilter struct {
	Name   string
	Type   string
	Status *int8
}

func (r *Repository) ListDictTypes(ctx context.Context, f DictTypeFilter, offset, limit int) ([]model.DictType, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.DictType{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Type != "" {
		q = q.Where("type LIKE ?", "%"+f.Type+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.DictType
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) FindDictTypeByID(ctx context.Context, id int64) (*model.DictType, error) {
	var t model.DictType
	if err := r.db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (r *Repository) CountDictTypeByType(ctx context.Context, dictType string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.DictType{}).Where("type = ?", dictType)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func (r *Repository) CreateDictType(ctx context.Context, t *model.DictType) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *Repository) SaveDictType(ctx context.Context, t *model.DictType) error {
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *Repository) DeleteDictType(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.DictType{}, id).Error
}

// ---------- 字典数据 ----------

func (r *Repository) CountDictDataByType(ctx context.Context, dictType string) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.DictData{}).Where("dict_type = ?", dictType).Count(&total).Error
	return total, err
}

func (r *Repository) ListDictData(ctx context.Context, dictType, label string, status *int8) ([]model.DictData, error) {
	q := r.db.WithContext(ctx).Model(&model.DictData{})
	if dictType != "" {
		q = q.Where("dict_type = ?", dictType)
	}
	if label != "" {
		q = q.Where("label LIKE ?", "%"+label+"%")
	}
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	var list []model.DictData
	err := q.Order("sort ASC, id ASC").Find(&list).Error
	return list, err
}

func (r *Repository) FindDictDataByID(ctx context.Context, id int64) (*model.DictData, error) {
	var d model.DictData
	if err := r.db.WithContext(ctx).First(&d, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &d, nil
}

func (r *Repository) CreateDictData(ctx context.Context, d *model.DictData) error {
	return r.db.WithContext(ctx).Create(d).Error
}

func (r *Repository) SaveDictData(ctx context.Context, d *model.DictData) error {
	return r.db.WithContext(ctx).Save(d).Error
}

func (r *Repository) DeleteDictData(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.DictData{}, id).Error
}
