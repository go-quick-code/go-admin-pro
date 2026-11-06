package model

import (
	"time"

	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座，定义见 internal/pkg/model。
type BaseEntity = basemodel.BaseEntity

// 任务状态：0 开启 / 1 暂停。
const (
	JobStatusRunning = 0
	JobStatusPaused  = 1
)

// File 是上传文件记录。
//
// BizType/BizID 是通用业务归属（issue #17 合同附件起引入）：一个文件可挂在
// 任意业务对象上（如 crm_contract + 合同 ID），空串/0 表示未关联业务。
// infra 侧只按这对键做归属过滤，不认识具体业务语义。
type File struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:255;not null" json:"name"`
	Path     string `gorm:"size:500;not null" json:"path"`
	URL      string `gorm:"size:500;not null" json:"url"`
	Size     int64  `json:"size"`
	Type     string `gorm:"size:100" json:"type"`
	Provider string `gorm:"size:32;not null;default:'local'" json:"provider"`
	BizType  string `gorm:"size:32;not null;default:''" json:"bizType"`
	BizID    int64  `gorm:"not null;default:0" json:"bizId"`
}

func (File) TableName() string { return "infra_file" }

// Config 是参数配置。
type Config struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Category string `gorm:"size:64;not null;default:'default'" json:"category"`
	Name     string `gorm:"size:100;not null" json:"name"`
	Key      string `gorm:"column:config_key;size:200;not null" json:"key"`
	Value    string `gorm:"type:text" json:"value"`
	Type     string `gorm:"size:32;not null;default:'string'" json:"type"`
	Visible  bool   `gorm:"not null;default:true" json:"visible"`
	Remark   string `gorm:"size:500" json:"remark"`
}

func (Config) TableName() string { return "infra_config" }

// Job 是定时任务。
type Job struct {
	BaseEntity
	TenantID       int64      `gorm:"index;not null;default:1" json:"tenantId"`
	Name           string     `gorm:"size:100;not null" json:"name"`
	HandlerName    string     `gorm:"size:100;not null" json:"handlerName"`
	HandlerParam   string     `gorm:"size:500" json:"handlerParam"`
	CronExpression string     `gorm:"size:100;not null" json:"cronExpression"`
	RetryCount     int        `gorm:"not null;default:0" json:"retryCount"`
	Status         int8       `gorm:"not null;default:1" json:"status"` // 0 开启 1 暂停
	Remark         string     `gorm:"size:500" json:"remark"`
	LastRunAt      *time.Time `json:"lastRunAt"`
	NextRunAt      *time.Time `gorm:"-" json:"nextRunAt,omitempty"`
}

func (Job) TableName() string { return "infra_job" }

// JobLog 是定时任务执行日志。
type JobLog struct {
	ID             int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt      time.Time  `json:"createTime"`
	TenantID       int64      `gorm:"index;not null;default:1" json:"tenantId"`
	JobID          int64      `gorm:"index;not null" json:"jobId"`
	HandlerName    string     `gorm:"size:100" json:"handlerName"`
	HandlerParam   string     `gorm:"size:500" json:"handlerParam"`
	CronExpression string     `gorm:"size:100" json:"cronExpression"`
	BeginTime      time.Time  `json:"beginTime"`
	EndTime        *time.Time `json:"endTime"`
	Duration       int64      `json:"duration"`
	Result         int8       `gorm:"not null;default:0" json:"result"` // 0 成功 1 失败
	Message        string     `gorm:"type:text" json:"message"`
}

func (JobLog) TableName() string { return "infra_job_log" }

// APILog 是 API 访问日志（issue #80，spec §3.7/§4.6，ADR 0014）。
//
// append-only（同 message_log 的取舍）：不设 updated_at/deleted_at/creator/
// updater，管理员删除/清空为硬删除。字段在采集时刻（请求完成后）定格，
// 异步落库只补 id，避免批量写入延迟拉长 created_at。
type APILog struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt      time.Time `json:"createTime"`
	TenantID       int64     `gorm:"not null;default:1" json:"tenantId"`
	UserID         int64     `gorm:"not null;default:0" json:"userId"`
	Username       string    `gorm:"size:64;not null;default:''" json:"username"`
	RequestMethod  string    `gorm:"size:16;not null" json:"requestMethod"`
	RequestPath    string    `gorm:"size:255;not null" json:"requestPath"`
	RequestQuery   string    `gorm:"size:1024" json:"requestQuery"`
	ResponseStatus int       `gorm:"not null;default:0" json:"responseStatus"`
	DurationMs     int64     `gorm:"not null;default:0" json:"durationMs"`
	ClientIP       string    `gorm:"size:64;not null;default:''" json:"clientIp"`
	UserAgent      string    `gorm:"size:500" json:"userAgent"`
	RequestID      string    `gorm:"size:64;not null;default:''" json:"requestId"`
}

func (APILog) TableName() string { return "infra_api_log" }
