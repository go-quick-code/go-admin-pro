package dto

import (
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
)

// notify.go 是回调通知管理端的 HTTP 契约（issue #134，docs/pay-mvp-spec.md
// §5）：分页筛选 + 详情（含执行日志时间线）。交易单据 append-only，
// 无新增/编辑/删除端点（spec §4.10）。

// NotifyTaskPageReq 回调任务分页筛选（GET /pay/notify-task/page，权限
// pay:notify-task:query）。
type NotifyTaskPageReq struct {
	PageNo          int    `form:"pageNo"`
	PageSize        int    `form:"pageSize"`
	Type            *int8  `form:"type"`
	Status          *int8  `form:"status"`
	AppID           int64  `form:"appId"`
	MerchantOrderID string `form:"merchantOrderId"`
}

// NotifyTaskDetail 回调任务详情（GET /pay/notify-task/:id）：任务 + 执行日志
// （append-only 时间线，id ASC；response 为响应摘要，spec §3.3）。
type NotifyTaskDetail struct {
	*paymodel.NotifyTask
	Logs []paymodel.NotifyLog `json:"logs"`
}
