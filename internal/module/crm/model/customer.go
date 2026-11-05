// Package model 定义 CRM 模块的持久化模型。
// 实体统一嵌入 internal/pkg/model.BaseEntity（时间戳、软删除、creator/updater），
// 表结构由 goose 迁移管理（migrations/00004 起），模型标签仅描述列类型供 GORM 使用。
package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// 客户状态枚举（docs/crm-mvp-spec.md §3：1 潜在 / 2 活跃 / 3 流失）。
const (
	CustomerStatusPotential int8 = 1
	CustomerStatusActive    int8 = 2
	CustomerStatusLost      int8 = 3
)

// ValidCustomerStatus 报告状态枚举是否合法。
func ValidCustomerStatus(s int8) bool {
	return s >= CustomerStatusPotential && s <= CustomerStatusLost
}

// Customer 是客户（GLOSSARY：CRM 的账户主体；owner_user_id 为空即公海客户）。
// owner_dept_id 是负责人部门的快照，随 owner 写入同步（ADR 0004）。
type Customer struct {
	model.BaseEntity
	TenantID   int64  `gorm:"index;not null;default:1" json:"tenantId"`
	CustomerNo string `gorm:"size:32;not null" json:"customerNo"`
	Name       string `gorm:"size:128;not null" json:"name"`
	Industry   string `gorm:"size:64" json:"industry"`
	Level      string `gorm:"size:64" json:"level"`
	Source     string `gorm:"size:64" json:"source"`
	Phone      string `gorm:"size:32" json:"phone"`
	Website    string `gorm:"size:128" json:"website"`
	Address    string `gorm:"size:255" json:"address"`
	Status     int8   `gorm:"not null;default:1" json:"status"`
	// OwnerUserID 为空（0）表示无负责人（公海）；快照列同理，无负责人时不落部门。
	OwnerUserID    int64      `gorm:"index" json:"ownerUserId"`
	OwnerDeptID    int64      `gorm:"index" json:"ownerDeptId"`
	PoolEnterTime  *time.Time `json:"poolEnterTime"`
	LastFollowTime *time.Time `json:"lastFollowTime"`
	ReleaseReason  string     `gorm:"size:255" json:"releaseReason"`
	Remark         string     `gorm:"size:500" json:"remark"`

	// 非持久化字段：负责人昵称与部门名称，由 service 层批量补齐供展示。
	OwnerUserName string `gorm:"-" json:"ownerUserName,omitempty"`
	OwnerDeptName string `gorm:"-" json:"ownerDeptName,omitempty"`
}

func (Customer) TableName() string { return "crm_customer" }

// Sequence 是编号序列计数行（docs/crm-mvp-spec.md §3：计数支撑表，只有业务字段，
// 不套用实体底座）。唯一键 (tenant_id, biz_type, date_key) 上的行锁保证原子递增。
type Sequence struct {
	TenantID  int64  `gorm:"primaryKey;not null;default:1" json:"tenantId"`
	BizType   string `gorm:"primaryKey;size:32;not null" json:"bizType"`
	DateKey   string `gorm:"primaryKey;size:8;not null" json:"dateKey"`
	LastValue int64  `gorm:"not null;default:0" json:"lastValue"`
}

func (Sequence) TableName() string { return "crm_sequence" }
