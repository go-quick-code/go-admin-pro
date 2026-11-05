package model

import (
	"github.com/lautung/go-admin-pro/internal/pkg/model"
	"time"
)

// 跟进类型枚举（issue #12，芋道字典语义：值 + 中文文案由展示层维护）：
// 1 电话 / 2 拜访 / 3 微信 / 4 邮件 / 5 其他。
const (
	FollowUpTypePhone  int8 = 1
	FollowUpTypeVisit  int8 = 2
	FollowUpTypeWeChat int8 = 3
	FollowUpTypeEmail  int8 = 4
	FollowUpTypeOther  int8 = 5
)

// ValidFollowUpType 报告跟进类型枚举是否合法。
func ValidFollowUpType(t int8) bool {
	return t >= FollowUpTypePhone && t <= FollowUpTypeOther
}

// FollowUp 是跟进记录（GLOSSARY：对客户、商机或合同的一次沟通记录，含类型、
// 内容与下次跟进时间）。#12 建了客户维度（customer_id），#15 扩展商机维度，
// #16 扩展合同维度：挂客户/商机/合同三选一——CustomerID=0 表示未挂客户
// （挂商机或合同时），OpportunityID/ContractID 为 NULL/0 表示未挂对应对象。
// 跟进自身没有负责人（owner_*），可见性跟随所属对象的数据范围
// （docs/crm-mvp-spec.md §4.1：join 父记录后套用父谓词）。
type FollowUp struct {
	model.BaseEntity
	TenantID      int64      `gorm:"index;not null;default:1" json:"tenantId"`
	CustomerID    int64      `gorm:"index;not null" json:"customerId"`
	OpportunityID int64      `gorm:"index" json:"opportunityId"`
	ContractID    int64      `gorm:"index" json:"contractId"`
	Type          int8       `gorm:"not null;default:5" json:"type"`
	Content       string     `gorm:"size:1000;not null" json:"content"`
	NextTime      *time.Time `gorm:"column:next_follow_time" json:"nextFollowTime"`

	// 非持久化字段：所属客户/商机/合同名称，由 service 层批量补齐供列表展示。
	CustomerName    string `gorm:"-" json:"customerName,omitempty"`
	OpportunityName string `gorm:"-" json:"opportunityName,omitempty"`
	ContractName    string `gorm:"-" json:"contractName,omitempty"`
}

func (FollowUp) TableName() string { return "crm_follow_up" }
