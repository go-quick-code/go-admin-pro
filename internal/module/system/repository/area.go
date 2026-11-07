package repository

import (
	"context"

	"gorm.io/gorm/clause"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// 地区仓储（issue #82，spec §3.9/§4.8）。system_area 是平台级参考数据，
// 未登记 TenantTables，查询/写入不经租户插件。

// CountAreas 返回地区总行数（种子判断「表为空」用）。
func (r *Repository) CountAreas(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Area{}).Count(&count).Error
	return count, err
}

// ListAreasByParent 返回指定父编码的直接子级（平铺），按 sort、code 升序；
// 仅返回启用行（树/下拉展示口径）。parentCode 为空返回省级。
func (r *Repository) ListAreasByParent(ctx context.Context, parentCode string) ([]model.Area, error) {
	var list []model.Area
	q := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable)
	if parentCode == "" {
		q = q.Where("level = ?", 1)
	} else {
		q = q.Where("parent_code = ?", parentCode)
	}
	err := q.Order("sort, code").Find(&list).Error
	return list, err
}

// ListAllAreas 返回全部启用行（树接口一次性载入后内存组树；
// 省市两级 + 常见区县量级在数千行内，避免逐层 N+1）。
func (r *Repository) ListAllAreas(ctx context.Context) ([]model.Area, error) {
	var list []model.Area
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("sort, code").
		Find(&list).Error
	return list, err
}

// GetAreasByCodes 按 code 批量取行（导入的父节点存在性校验用）。
func (r *Repository) GetAreasByCodes(ctx context.Context, codes []string) (map[string]model.Area, error) {
	if len(codes) == 0 {
		return map[string]model.Area{}, nil
	}
	var list []model.Area
	if err := r.db.WithContext(ctx).Where("code IN ?", codes).Find(&list).Error; err != nil {
		return nil, err
	}
	m := make(map[string]model.Area, len(list))
	for _, a := range list {
		m[a.Code] = a
	}
	return m, nil
}

// UpsertAreas 按 code 批量 upsert：存在则更新名称/父子/层级/排序/状态，
// 不存在则插入（spec §4.8.2「按 code upsert，重复导入不产生重复行」）。
// 分批写入，控制单条 SQL 体积。
func (r *Repository) UpsertAreas(ctx context.Context, areas []model.Area) error {
	const batch = 500
	for start := 0; start < len(areas); start += batch {
		end := start + batch
		if end > len(areas) {
			end = len(areas)
		}
		batchRows := areas[start:end]
		if err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "code"}},
				DoUpdates: clause.AssignmentColumns(
					[]string{"parent_code", "name", "level", "sort", "status", "updated_at"},
				),
			}).
			Create(&batchRows).Error; err != nil {
			return err
		}
	}
	return nil
}
