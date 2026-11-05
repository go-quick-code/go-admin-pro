package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// 联系人性别枚举（对齐字典 sys_user_sex：0 未知 / 1 男 / 2 女）。
const (
	ContactGenderUnknown int8 = 0
	ContactGenderMale    int8 = 1
	ContactGenderFemale  int8 = 2
)

// ValidContactGender 报告性别枚举是否合法。
func ValidContactGender(g int8) bool {
	return g >= ContactGenderUnknown && g <= ContactGenderFemale
}

// Contact 是联系人（GLOSSARY：客户内部的具体对接人，隶属于一个客户）。
// 联系人自身没有负责人（owner_*），可见性完全跟随所属客户的数据范围
// （docs/crm-mvp-spec.md §4.1：联系人 join 父记录后套用父谓词）。
type Contact struct {
	model.BaseEntity
	TenantID     int64  `gorm:"index;not null;default:1" json:"tenantId"`
	CustomerID   int64  `gorm:"index;not null" json:"customerId"`
	Name         string `gorm:"size:128;not null" json:"name"`
	Position     string `gorm:"size:64" json:"position"`
	Mobile       string `gorm:"size:32" json:"mobile"`
	Email        string `gorm:"size:128" json:"email"`
	Gender       int8   `gorm:"not null;default:0" json:"gender"`
	DecisionRole string `gorm:"size:64" json:"decisionRole"`
	Remark       string `gorm:"size:500" json:"remark"`

	// 非持久化字段：所属客户名称，由 service 层批量补齐供列表展示。
	CustomerName string `gorm:"-" json:"customerName,omitempty"`
}

func (Contact) TableName() string { return "crm_contact" }
