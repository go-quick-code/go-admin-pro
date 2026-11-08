package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// Warehouse 是 ERP 仓库（issue #89，spec §3.1）：address/principal 展示字段、
// sort 排序、status 状态。principal 仅展示、不做独立权限（spec §4.10）；
// owner = 创建人，owner_user_id/owner_dept_id 承载数据范围谓词（ADR 0004）。
// status 沿用主数据统一口径 0 启用 / 1 停用（迁移 00029 注释）。
type Warehouse struct {
	model.BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Address  string `gorm:"size:255" json:"address"`
	// 负责人是纯展示文本（不是 system 用户引用），不做存在性校验。
	Principal string `gorm:"size:64" json:"principal"`
	// sort 不用 gorm default 标签：0 是合法值，Sort/Status 恒显式写入。
	Sort   int  `gorm:"not null" json:"sort"`
	Status int8 `gorm:"not null" json:"status"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

func (Warehouse) TableName() string { return "erp_warehouse" }
