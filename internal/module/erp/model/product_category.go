package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// 分类/单位/产品状态枚举（芋道 ERP CommonStatus 口径：0 启用 / 1 停用，
// docs/erp-mvp-spec.md §3.1；与 CRM 产品的商品上下架 1 上架/0 下架刻意不同）。
const (
	StatusEnable  int8 = 0
	StatusDisable int8 = 1
)

// ValidStatus 报告主数据状态枚举是否合法。
func ValidStatus(v int8) bool {
	return v >= StatusEnable && v <= StatusDisable
}

// ProductCategory 是 ERP 产品分类（无限层级树，spec §3.1）。
// 列表接口返回扁平列表，树组装在前端（spec §5）；owner = 创建人
// （spec §4.10），owner_user_id/owner_dept_id 承载数据范围谓词（ADR 0004）。
type ProductCategory struct {
	model.BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	ParentID int64  `gorm:"index;not null;default:0" json:"parentId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Code     string `gorm:"size:64" json:"code"`
	Sort     int    `gorm:"not null;default:0" json:"sort"`
	// status 不用 gorm default 标签：0（启用）是合法值，带 default 会让 GORM
	// 在插入时把零值替换成库默认值，语义上等价但保持与 crm_product 相同的
	// 显式口径（0/1 都必须显式落库）。
	Status      int8  `gorm:"not null" json:"status"`
	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

func (ProductCategory) TableName() string { return "erp_product_category" }
