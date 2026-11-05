package model

import "github.com/go-quick-code/go-admin-pro/internal/pkg/model"

// PoolConfig 是租户级公海配置（issue #14，docs/crm-mvp-spec.md §4.2）：
// 每租户一行（tenant_id 唯一），自动回收天数与认领上限按租户生效。
// 行缺失（新建租户尚未配置）时按默认值执行（service.DefaultPoolConfigValues），
// 首次编辑时落行。
type PoolConfig struct {
	model.BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// RecycleDays 自动回收天数：last_follow_time 超过该天数（NULL 回退
	// created_at，规格验收）且无未作废合同的客户回收回公海。
	RecycleDays int `gorm:"not null;default:30" json:"recycleDays"`
	// ClaimLimit 认领上限：owner 名下未删除客户总数达到上限即拒绝认领。
	ClaimLimit int `gorm:"not null;default:50" json:"claimLimit"`
}

func (PoolConfig) TableName() string { return "crm_pool_config" }
