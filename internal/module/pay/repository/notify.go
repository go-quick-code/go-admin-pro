package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
)

// notify.go 是商户回调任务/日志与超时关单的数据访问原语（issue #134，
// docs/pay-mvp-spec.md §3.3/§4.5/§7）。口径与 order.go 一致：
//   - 扫描与认领走「显式租户参数的裸 SQL」：并发认领是防重发的最终裁决，
//     条件（status IN (0,21,22) + next_notify_time 到期）必须与租户条件同语句；
//   - 日志插入与分页查询走 gorm 模型：租户插件自动回填/追加 tenant_id。
//
// 认领语义：notify_times+1 且 next_notify_time 推进到本次失败后的重试点——
// 两个并发扫描即使同时读到同一任务，后认领者因 next_notify_time 已越过
// 「当前时刻」而更新 0 行，保证同任务单次触发只发送一次。

// NotifyTaskFilter 是回调任务分页筛选（管理端，spec §5）。
type NotifyTaskFilter struct {
	Type            *int8
	Status          *int8
	AppID           int64
	MerchantOrderID string
}

// ListDueNotifyTasks 扫描到期任务（单租户，job 每分钟）：status IN (0,21,22)
// 且 next_notify_time <= now 且 notify_times < max_notify_times（防御第 9 次
// 失败后因进程中断残留 21/22 的行被第 10 次发送）。固定 id ASC 保序、LIMIT
// 防单轮积压过大。
func (r *Repository) ListDueNotifyTasks(ctx context.Context, now time.Time, limit int) ([]model.NotifyTask, error) {
	var list []model.NotifyTask
	err := r.db.WithContext(ctx).Raw(
		"SELECT * FROM `pay_notify_task` "+
			"WHERE `status` IN (0, 21, 22) AND `next_notify_time` IS NOT NULL AND `next_notify_time` <= ? "+
			"AND `notify_times` < `max_notify_times` AND `deleted_at` IS NULL "+
			"ORDER BY `id` ASC LIMIT ?",
		now, limit,
	).Scan(&list).Error
	return list, err
}

// ClaimNotifyTask 认领任务（并发防重发的最终裁决）：notify_times+1、记
// last_execute_time、next_notify_time 预推进到「本次失败后的重试点」。
// 0 行 = 已被并发方认领（或状态已流转），调用方跳过不发送。
// 只能在逐租户上下文调用（显式租户参数）。
func (r *Repository) ClaimNotifyTask(ctx context.Context, tenantID, taskID int64, executeTime, nextNotifyTime, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_notify_task` SET `notify_times` = `notify_times` + 1, `last_execute_time` = ?, "+
			"`next_notify_time` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` IN (0, 21, 22) "+
			"AND `next_notify_time` IS NOT NULL AND `next_notify_time` <= ? "+
			"AND `notify_times` < `max_notify_times` AND `deleted_at` IS NULL",
		executeTime, nextNotifyTime, now, databaseAuditUser(ctx), taskID, tenantID, now,
	)
	return res.RowsAffected, res.Error
}

// AdvanceNotifyTaskFailClosed 密钥缺失拒发（fail-closed）时的条件推进：
// 不计通知次数（无发送即无尝试），只把 next_notify_time 推过当前时刻，
// 任务保持待重试；条件更新兼作并发互斥（后到者 0 行，不重复记日志）。
func (r *Repository) AdvanceNotifyTaskFailClosed(ctx context.Context, tenantID, taskID int64, nextNotifyTime, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_notify_task` SET `next_notify_time` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `id` = ? AND `tenant_id` = ? AND `status` IN (0, 21, 22) "+
			"AND `next_notify_time` IS NOT NULL AND `next_notify_time` <= ? AND `deleted_at` IS NULL",
		nextNotifyTime, now, databaseAuditUser(ctx), taskID, tenantID, now,
	)
	return res.RowsAffected, res.Error
}

// UpdateNotifyTaskResult 回写任务终态（一次执行的裁决）：成功置 10 并清空
// next_notify_time；失败置 21/22（继续重试，next_notify_time 已由认领预推进）
// 或 20（第 9 次失败彻底失败）。条件更新防并发错写（认领失败的执行方 0 行）。
func (r *Repository) UpdateNotifyTaskResult(ctx context.Context, tenantID, taskID int64, status int8, now time.Time) error {
	q := "UPDATE `pay_notify_task` SET `status` = ?, "
	if status == model.NotifyTaskStatusSuccess {
		q += "`next_notify_time` = NULL, "
	}
	q += "`updated_at` = ?, `updater` = ? " +
		"WHERE `id` = ? AND `tenant_id` = ? AND `status` IN (0, 21, 22) AND `deleted_at` IS NULL"
	return r.db.WithContext(ctx).Exec(q, status, now, databaseAuditUser(ctx), taskID, tenantID).Error
}

// InsertNotifyLog 追加回调执行日志（append-only，spec §4.10：交易单据无删除）。
func (r *Repository) InsertNotifyLog(ctx context.Context, row *model.NotifyLog) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// ListNotifyLogsByTaskID 返回任务的执行日志（append-only 时间线，id ASC）。
func (r *Repository) ListNotifyLogsByTaskID(ctx context.Context, taskID int64) ([]model.NotifyLog, error) {
	var list []model.NotifyLog
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("id ASC").Find(&list).Error
	return list, err
}

// ListNotifyTasks 按筛选分页查询回调任务（管理端，固定 id DESC，spec §5）。
func (r *Repository) ListNotifyTasks(ctx context.Context, f NotifyTaskFilter, offset, limit int) ([]model.NotifyTask, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.NotifyTask{})
	if f.Type != nil {
		q = q.Where("`type` = ?", *f.Type)
	}
	if f.Status != nil {
		q = q.Where("`status` = ?", *f.Status)
	}
	if f.AppID > 0 {
		q = q.Where("app_id = ?", f.AppID)
	}
	if f.MerchantOrderID != "" {
		q = q.Where("merchant_order_id = ?", f.MerchantOrderID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.NotifyTask
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// FindNotifyTaskByID 按 ID 查任务（租户级），不存在/跨租户返回 nil。
func (r *Repository) FindNotifyTaskByID(ctx context.Context, id int64) (*model.NotifyTask, error) {
	var row model.NotifyTask
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// CloseExpiredOrders 关闭过期待支付订单（超时关单 job，spec §7）：条件 UPDATE
// status 0 → 30（expire_time < now）；已支付/已退款/已关闭不受影响（WHERE
// status=0）。返回关闭行数。显式租户参数裸 SQL。
func (r *Repository) CloseExpiredOrders(ctx context.Context, tenantID int64, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `pay_order` SET `status` = ?, `updated_at` = ?, `updater` = ? "+
			"WHERE `tenant_id` = ? AND `status` = 0 AND `expire_time` IS NOT NULL AND `expire_time` < ? "+
			"AND `deleted_at` IS NULL",
		model.OrderStatusClosed, now, databaseAuditUser(ctx), tenantID, now,
	)
	return res.RowsAffected, res.Error
}

// databaseAuditUser 取审计用户（job 上下文无登录主体时落系统账号 0）。
func databaseAuditUser(ctx context.Context) string {
	return database.AuditUser(ctx)
}
