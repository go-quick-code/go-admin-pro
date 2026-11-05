// Package repository 是 notify 模块的持久化层（gorm）。
package repository

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/notify/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	"gorm.io/gorm"
)

// Repository 是 notify 模块的 gorm 仓储。
type Repository struct {
	db *gorm.DB
}

// New 创建 notify 仓储。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// Transaction 在本模块连接上开启事务执行 fn（无既有事务的生产者走这条路径）。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

// CreateBatch 批量插入通知行。db 必须显式传入：调用方事务句柄（spec §4.13
// 同一事务 seam，随调用方提交/回滚）或本模块连接。租户插件按 ctx 自动回填
// tenant_id 并追加租户条件。
func (r *Repository) CreateBatch(ctx context.Context, db *gorm.DB, rows []*model.Message) error {
	return db.WithContext(ctx).Create(&rows).Error
}

// CountUnread 统计用户未读数。租户插件追加 tenant_id 条件；谓词
// (tenant_id, user_id, read_at) 恰好命中 spec §3.2 的
// idx_notify_message_tenant_user_read_id 索引前缀。
func (r *Repository) CountUnread(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ?", userID).
		Where("read_at IS NULL").
		Count(&n).Error
	return n, err
}

// CountByUserAndID 统计用户名下指定 ID 的通知行数（0/1）：单条标记已读的
// 存在性判定（已读过 → 1；他人/不存在/已删 → 0）。
func (r *Repository) CountByUserAndID(ctx context.Context, userID, id int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ? AND user_id = ?", id, userID).
		Count(&n).Error
	return n, err
}

// MarkRead 把用户名下指定 ID 的未读通知置为已读，返回受影响行数。
// 只触碰 read_at IS NULL 的行：已读行不受影响（幂等），他人/已删行天然不匹配。
func (r *Repository) MarkRead(ctx context.Context, userID, id int64, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ? AND user_id = ?", id, userID).
		Where("read_at IS NULL").
		Update("read_at", now)
	return res.RowsAffected, res.Error
}

// MarkAllRead 把用户全部未读通知置为已读，返回受影响行数（重复执行恒 0 行，幂等）。
func (r *Repository) MarkAllRead(ctx context.Context, userID int64, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ?", userID).
		Where("read_at IS NULL").
		Update("read_at", now)
	return res.RowsAffected, res.Error
}

// PageByUser 按用户分页查询通知（id 倒序，走 idx_notify_message_tenant_user_id
// 索引；Type/Read 可选过滤）。租户插件追加 tenant_id 条件。
func (r *Repository) PageByUser(ctx context.Context, userID int64, req *dto.MessagePageReq) ([]model.Message, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ?", userID)
	if req.Type != "" {
		q = q.Where("`type` = ?", req.Type)
	}
	if req.Read != nil {
		if *req.Read {
			q = q.Where("read_at IS NOT NULL")
		} else {
			q = q.Where("read_at IS NULL")
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Message
	if err := q.Order("id DESC").
		Offset(req.Offset()).Limit(req.PageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
