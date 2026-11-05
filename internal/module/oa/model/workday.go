// workday.go 承载工作日历的持久化模型（issue #50，docs/oa-mvp-spec.md §3.3）。
// 日期三态的判定语义（默认规则/调休/节假日）是纯函数，见 service/workday_rule.go；
// 本模型只是显式记录（显式记录存在时以记录为准）的存储形态。
package model

import (
	"time"

	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座（internal/pkg/model），见 migrations/00017_oa_workday_calendar.sql。
type BaseEntity = basemodel.BaseEntity

// WorkdayCalendar 是工作日历的显式记录（spec §3.3）：
// 租户内 date 唯一（唯一键 uk_oa_workday_tenant_date (tenant_id, date)）。
// 无显式记录的日期由默认规则推出（周一至周五工作日、周六周日休息），
// 不落行；显式记录覆盖默认值，type=0 可表达周末调休上班。
type WorkdayCalendar struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// Date 日历日期（date 列；入库前由 service 归一化为当日零点）。
	Date time.Time `gorm:"type:date;not null" json:"date"`
	// Type 日类型：0 工作日（含周末调休上班）/ 1 休息日 / 2 节假日（spec §3.3）。
	Type int8 `gorm:"not null" json:"type"`
	// Remark 备注（如节假日名称、调休说明），≤200 字。
	Remark string `gorm:"size:200" json:"remark"`
}

func (WorkdayCalendar) TableName() string { return "oa_workday_calendar" }
