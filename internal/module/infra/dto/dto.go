package dto

import "time"

type FilePageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Type     string `form:"type"`
}

type ConfigPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Key      string `form:"key"`
	Category string `form:"category"`
}

type ConfigCreateReq struct {
	Category string `json:"category"`
	Name     string `json:"name" binding:"required"`
	Key      string `json:"key" binding:"required"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Visible  bool   `json:"visible"`
	Remark   string `json:"remark"`
}

type ConfigUpdateReq struct {
	ID       int64  `json:"id" binding:"required"`
	Category string `json:"category"`
	Name     string `json:"name" binding:"required"`
	Key      string `json:"key" binding:"required"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Visible  bool   `json:"visible"`
	Remark   string `json:"remark"`
}

type JobPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	Name        string `form:"name"`
	HandlerName string `form:"handlerName"`
	Status      *int8  `form:"status"`
}

type JobCreateReq struct {
	Name           string `json:"name" binding:"required"`
	HandlerName    string `json:"handlerName" binding:"required"`
	HandlerParam   string `json:"handlerParam"`
	CronExpression string `json:"cronExpression" binding:"required"`
	RetryCount     int    `json:"retryCount"`
	Status         int8   `json:"status"`
	Remark         string `json:"remark"`
}

type JobUpdateReq struct {
	ID             int64  `json:"id" binding:"required"`
	Name           string `json:"name" binding:"required"`
	HandlerName    string `json:"handlerName" binding:"required"`
	HandlerParam   string `json:"handlerParam"`
	CronExpression string `json:"cronExpression" binding:"required"`
	RetryCount     int    `json:"retryCount"`
	Status         int8   `json:"status"`
	Remark         string `json:"remark"`
}

type JobLogPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	JobID       int64  `form:"jobId"`
	HandlerName string `form:"handlerName"`
	Result      *int8  `form:"result"`
}

// JobLogVO 在日志基础上补充任务名称。
type JobLogVO struct {
	ID             int64      `json:"id"`
	JobID          int64      `json:"jobId"`
	JobName        string     `json:"jobName"`
	HandlerName    string     `json:"handlerName"`
	HandlerParam   string     `json:"handlerParam"`
	CronExpression string     `json:"cronExpression"`
	BeginTime      time.Time  `json:"beginTime"`
	EndTime        *time.Time `json:"endTime"`
	Duration       int64      `json:"duration"`
	Result         int8       `json:"result"`
	Message        string     `json:"message"`
	CreateTime     time.Time  `json:"createTime"`
}
