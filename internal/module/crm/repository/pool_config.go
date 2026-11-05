// pool_config.go 承载租户级公海配置（issue #14）与回收候选查询的数据访问。
// 配置读写经租户插件自动带 tenant_id 条件；回收候选查询同样按 ctx 租户隔离。
package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"gorm.io/gorm"
)

// FindPoolConfig 读取当前租户的公海配置行，行缺失返回 nil（业务层按默认值执行）。
func (r *Repository) FindPoolConfig(ctx context.Context) (*model.PoolConfig, error) {
	var row model.PoolConfig
	if err := r.db.WithContext(ctx).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// SavePoolConfig 以 upsert 语义保存当前租户的公海配置：
//   - 行不存在 → 落新行（tenant_id 由租户插件按 ctx 回填）；
//   - 行存在（含软删行）→ 更新取值；软删行一并恢复——tenant_id 唯一键下
//     软删行会占位，直接插入会撞唯一键，恢复是唯一正确路径（本表 v1
//     虽无删除入口，仍按软删兜底处理，避免脏数据卡死配置编辑）。
func (r *Repository) SavePoolConfig(ctx context.Context, recycleDays, claimLimit int) error {
	var row model.PoolConfig
	err := r.db.WithContext(ctx).Unscoped().First(&row).Error
	switch {
	case err == nil:
		// 更新走 Unscoped：软删行也要能恢复（deleted_at 置 NULL），
		// 默认作用域的软删条件会让恢复更新匹配 0 行。
		return r.db.WithContext(ctx).Unscoped().Model(&row).
			Updates(map[string]any{"recycle_days": recycleDays, "claim_limit": claimLimit, "deleted_at": nil}).Error
	case err == gorm.ErrRecordNotFound:
		return r.db.WithContext(ctx).Create(&model.PoolConfig{
			RecycleDays: recycleDays,
			ClaimLimit:  claimLimit,
		}).Error
	default:
		return err
	}
}

// ListRecycleCandidates 查询自动回收候选客户（issue #14）：有负责人且
// 「最后跟进时间」早于 cutoff；last_follow_time 为 NULL 回退 created_at
// （COALESCE 语义，规格验收：从未跟进的新客户按创建时间参与回收）。
// 软删过滤由 GORM 自动追加；租户条件由插件按 ctx 追加。
//
// 本查询是批量预筛（COALESCE 让 MySQL 用不上 last_follow_time 索引，
// 数据量大时可再加生成列/冗余列优化，v1 客户量级可接受）；
// 命中行随后在 service 层逐条复核同一口径（PoolRecycleOverdue）并做
// 合同排除（ContractChecker），两处口径必须一致。
// created_at 为 NULL 的脏数据比较结果为 NULL 不会命中，宁漏回收不误回收。
func (r *Repository) ListRecycleCandidates(ctx context.Context, cutoff time.Time) ([]model.Customer, error) {
	var list []model.Customer
	err := r.db.WithContext(ctx).Model(&model.Customer{}).
		Where("owner_user_id IS NOT NULL").
		Where("COALESCE(last_follow_time, created_at) < ?", cutoff).
		Order("id ASC").
		Find(&list).Error
	return list, err
}
