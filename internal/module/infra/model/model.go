package model

import (
	"time"

	basemodel "github.com/lautung/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座，定义见 internal/pkg/model。
type BaseEntity = basemodel.BaseEntity

// 任务状态：0 开启 / 1 暂停。
const (
	JobStatusRunning = 0
	JobStatusPaused  = 1
)

// File 是上传文件记录。
type File struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:255;not null" json:"name"`
	Path     string `gorm:"size:500;not null" json:"path"`
	URL      string `gorm:"size:500;not null" json:"url"`
	Size     int64  `json:"size"`
	Type     string `gorm:"size:100" json:"type"`
	Provider string `gorm:"size:32;not null;default:'local'" json:"provider"`
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
