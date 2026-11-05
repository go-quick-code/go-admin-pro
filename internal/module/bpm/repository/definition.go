// definition.go 承载流程定义的数据访问（issue #44，spec §3.1/§4.1）：
// 版本号发放、草稿 CRUD、发布/停用的行锁读写与同 key 单发布版本约束。
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefinitionFilter 是定义列表查询条件（key/name 模糊、businessType 精确、
// status 精确；nil 表示不过滤）。版本历史 = 按 key 查询（含全部状态）。
type DefinitionFilter struct {
	Key          string
	Name         string
	BusinessType string
	Status       *int8
}

// NextVersion 返回同 key 的下一个版本号：按「含软删行」取 max+1——
// 唯一键 (tenant_id, key, version) 不区分软删，软删行占用的版本号不能复用，
// 版本历史因此无洞可穿（版本号发放后不可变，spec §4.1）。
//
// 租户条件显式入参（与 crm NextSequenceValue 同款的裸 SQL 例外）：聚合
// SELECT 经 gorm 的 Scan 路径不触发租户插件回调（回归见租户隔离集成用例），
// 这里手工过滤并 fail-closed——缺租户上下文直接报错，绝不退化为默认租户；
// 软删行一并计入（deleted_at 不过滤，与唯一键口径一致）。
func (r *Repository) NextVersion(ctx context.Context, key string) (int, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return 0, tenant.ErrNoTenantContext
	}
	var max int
	err := r.db.WithContext(ctx).Raw(
		"SELECT COALESCE(MAX(`version`), 0) FROM `bpm_process_definition` WHERE `tenant_id` = ? AND `key` = ?",
		tenantID, key,
	).Scan(&max).Error
	if err != nil {
		return 0, fmt.Errorf("查询流程版本号: %w", err)
	}
	return max + 1, nil
}

// CreateDefinition 落一条草稿（version 由 service 先经 NextVersion 发放）。
// tenant_id 由租户插件回填；并发同版本撞唯一键时经 wrapDuplicateEntry
// 规整为 gorm.ErrDuplicatedKey，service 层翻译为 409。
func (r *Repository) CreateDefinition(ctx context.Context, def *model.ProcessDefinition) error {
	if err := r.db.WithContext(ctx).Create(def).Error; err != nil {
		return wrapDuplicateEntry(err)
	}
	return nil
}

// wrapDuplicateEntry 把 MySQL 1062 重复键错误规整为 gorm.ErrDuplicatedKey 链
// （与 crm repository 同款兜底：gorm 默认不开错误翻译）。
func wrapDuplicateEntry(err error) error {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return errors.Join(gorm.ErrDuplicatedKey, err)
	}
	return err
}

// FindDefinitionByID 按 ID 读定义；未命中（含跨租户）返回 nil。
func (r *Repository) FindDefinitionByID(ctx context.Context, id int64) (*model.ProcessDefinition, error) {
	var row model.ProcessDefinition
	err := r.db.WithContext(ctx).First(&row, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindDefinitionByIDForUpdate 行锁读定义（发布/停用/编辑的串行化入口）：
// 先办到的请求锁住行，并发方在其事务提交后重读最新状态再判状态机。
func (r *Repository) FindDefinitionByIDForUpdate(ctx context.Context, id int64) (*model.ProcessDefinition, error) {
	var row model.ProcessDefinition
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// UpdateDefinitionDraft 更新草稿的可编辑字段（name/nodes/remark）。
// key 与 businessType 创建后不可变（版本语义锚定 key，spec §4.1），
// 本方法不涉及；仅草稿可调用（service 层状态机把关）。
func (r *Repository) UpdateDefinitionDraft(ctx context.Context, id int64, name string, nodes model.NodeList, remark string) error {
	return r.db.WithContext(ctx).Model(&model.ProcessDefinition{}).
		Where("id = ?", id).
		Updates(map[string]any{"name": name, "nodes": nodes, "remark": remark}).Error
}

// DeleteDefinition 软删定义（仅草稿无实例可删，spec §4.14）。
func (r *Repository) DeleteDefinition(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProcessDefinition{}, id).Error
}

// PageDefinitions 分页查询定义（tenant_id 由插件追加）。
func (r *Repository) PageDefinitions(ctx context.Context, filter DefinitionFilter, offset, limit int) ([]model.ProcessDefinition, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.ProcessDefinition{})
	if filter.Key != "" {
		q = q.Where("`key` LIKE ?", "%"+filter.Key+"%")
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ?", "%"+filter.Name+"%")
	}
	if filter.BusinessType != "" {
		q = q.Where("business_type = ?", filter.BusinessType)
	}
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计流程定义: %w", err)
	}
	var list []model.ProcessDefinition
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// LockPublishedByKeyForUpdate 行锁读同 key 当前发布版本（发布事务用）：
// 两个草稿并发发布时，后到者在锁上排队，读到前者已置发布后按状态机停用前者，
// 保证「同 key 同时只有一个发布版本」（spec §4.1）。
func (r *Repository) LockPublishedByKeyForUpdate(ctx context.Context, key string, excludeID int64) (*model.ProcessDefinition, error) {
	var row model.ProcessDefinition
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("`key` = ? AND status = ? AND id <> ?", key, model.DefinitionStatusPublished, excludeID).
		First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// DisableDefinition 把定义置为停用（发布事务内停旧版本、停用入口停当前版本共用）。
func (r *Repository) DisableDefinition(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&model.ProcessDefinition{}).
		Where("id = ?", id).
		Update("status", model.DefinitionStatusDisabled).Error
}

// PublishDefinition 把定义置为发布并落发布时间（状态机校验在 service 层）。
func (r *Repository) PublishDefinition(ctx context.Context, id int64, publishedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&model.ProcessDefinition{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": model.DefinitionStatusPublished, "published_at": publishedAt}).Error
}

// CountInstancesByDefinition 统计引用某定义的实例数（删除约束：
// 定义存在实例时不能删除，只能停用，spec §4.1/§4.14）。
// #46 起实例写入后本查询成为删除路径的真实闸门。
func (r *Repository) CountInstancesByDefinition(ctx context.Context, definitionID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ProcessInstance{}).
		Where("definition_id = ?", definitionID).
		Count(&count).Error
	return count, err
}
