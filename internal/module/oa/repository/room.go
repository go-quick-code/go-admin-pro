// room.go 承载会议室与预约的数据访问（issue #57，docs/oa-mvp-spec.md
// §3.7/§4.11/§4.14）：
//
//   - 会议室：CRUD、分页（名称/状态筛选）、启用列表（员工预约下拉）；
//   - 预约：创建/取消/分页（按房间、日期窗口、状态）、同房间重叠计数
//     （半开区间 [start,end)，相邻不算冲突）、未来有效预约计数（删除约束）；
//   - 会议提醒：扫描窗口内的有效未提醒预约，条件更新 reminded_at 保证只发
//     一次（WHERE reminded_at IS NULL）。
//
// 租户条件由租户插件自动追加；跨租户业务 ID 一律未命中（service 层表现为
// 404）。冲突判定的纯函数镜像在 service.RoomBookingsOverlap/RoomReminderDue。
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm/clause"
)

// MeetingRoomFilter 是会议室分页筛选：Name 模糊匹配、Status 精确（nil 不过滤）。
type MeetingRoomFilter struct {
	Name   string
	Status *int8
}

// CreateMeetingRoom 落一条会议室；tenant_id 由插件回填。
func (r *Repository) CreateMeetingRoom(ctx context.Context, row *model.MeetingRoom) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建会议室: %w", err)
	}
	return nil
}

// SaveMeetingRoom 更新会议室全字段。
func (r *Repository) SaveMeetingRoom(ctx context.Context, row *model.MeetingRoom) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新会议室: %w", err)
	}
	return nil
}

// DeleteMeetingRoom 软删除会议室（仅在没有未来有效预约时由 service 调用）。
func (r *Repository) DeleteMeetingRoom(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&model.MeetingRoom{}, id).Error; err != nil {
		return fmt.Errorf("删除会议室: %w", err)
	}
	return nil
}

// FindMeetingRoom 按 ID 读会议室（不过滤状态）；未命中（含跨租户）返回 nil。
func (r *Repository) FindMeetingRoom(ctx context.Context, id int64) (*model.MeetingRoom, error) {
	var row model.MeetingRoom
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindMeetingRoomForUpdate 行锁读会议室：创建预约与删除约束共用同一把锁，
// 串行化同房间的并发写入。
func (r *Repository) FindMeetingRoomForUpdate(ctx context.Context, id int64) (*model.MeetingRoom, error) {
	var row model.MeetingRoom
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// PageMeetingRooms 分页列会议室（sort ASC, id ASC）：名称模糊、状态精确。
func (r *Repository) PageMeetingRooms(ctx context.Context, f MeetingRoomFilter, offset, limit int) ([]model.MeetingRoom, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.MeetingRoom{})
	if name := strings.TrimSpace(f.Name); name != "" {
		q = q.Where("`name` LIKE ? ESCAPE '\\\\'", "%"+escapeRoomLike(name)+"%")
	}
	if f.Status != nil {
		q = q.Where("`status` = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计会议室: %w", err)
	}
	var rows []model.MeetingRoom
	if err := q.Order("`sort` ASC, `id` ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询会议室: %w", err)
	}
	return rows, total, nil
}

// ListEnabledMeetingRooms 返回全部启用会议室（员工预约下拉），sort ASC。
func (r *Repository) ListEnabledMeetingRooms(ctx context.Context) ([]model.MeetingRoom, error) {
	var rows []model.MeetingRoom
	if err := r.db.WithContext(ctx).
		Where("`status` = ?", model.MeetingRoomStatusEnabled).
		Order("`sort` ASC, `id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询启用会议室: %w", err)
	}
	return rows, nil
}

// ListMeetingRoomsByIDs 批量读会议室（预约列表回填房间名称/位置）。
func (r *Repository) ListMeetingRoomsByIDs(ctx context.Context, ids []int64) ([]model.MeetingRoom, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []model.MeetingRoom
	if err := r.db.WithContext(ctx).Where("`id` IN ?", ids).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("批量查询会议室: %w", err)
	}
	return rows, nil
}

// CountActiveFutureRoomBookings 统计会议室「有效且开始时间晚于 now」的预约数
// （删除约束：>0 时只能停用，spec §4.14）。
func (r *Repository) CountActiveFutureRoomBookings(ctx context.Context, roomID int64, now time.Time) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.RoomBooking{}).
		Where("`room_id` = ? AND `status` = ? AND `start_time` > ?",
			roomID, model.RoomBookingStatusActive, now).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("统计会议室未来预约: %w", err)
	}
	return count, nil
}

// CountActiveRoomBookingsOverlapping 统计同会议室在 [start,end) 内的有效预约
// 数（冲突校验）：end_time > start AND start_time < end；相邻不算冲突。
// excludeID > 0 时排除自身（编辑场景预留）。
func (r *Repository) CountActiveRoomBookingsOverlapping(ctx context.Context, roomID int64, start, end time.Time, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.RoomBooking{}).
		Where("`room_id` = ? AND `status` = ? AND `end_time` > ? AND `start_time` < ?",
			roomID, model.RoomBookingStatusActive, start, end)
	if excludeID > 0 {
		q = q.Where("`id` <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("统计预约冲突: %w", err)
	}
	return count, nil
}

// CreateRoomBooking 落一条预约；tenant_id 由插件回填。
func (r *Repository) CreateRoomBooking(ctx context.Context, row *model.RoomBooking) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建预约: %w", err)
	}
	return nil
}

// SaveRoomBooking 更新预约全字段（状态/日程 ID/提醒时间）。
func (r *Repository) SaveRoomBooking(ctx context.Context, row *model.RoomBooking) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新预约: %w", err)
	}
	return nil
}

// FindRoomBooking 按 ID 读预约；未命中（含跨租户）返回 nil。
func (r *Repository) FindRoomBooking(ctx context.Context, id int64) (*model.RoomBooking, error) {
	var row model.RoomBooking
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindRoomBookingForUpdate 行锁读预约（取消动作串行化）。
func (r *Repository) FindRoomBookingForUpdate(ctx context.Context, id int64) (*model.RoomBooking, error) {
	var row model.RoomBooking
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// RoomBookingFilter 是预约分页筛选：
//   - RoomID 精确；UserID 精确（员工侧强制本人）；
//   - Status 精确（nil 不过滤）；
//   - StartTime/EndTime 是查询窗口（可选），语义为「预约与窗口相交」：
//     end_time > start AND start_time < end。
type RoomBookingFilter struct {
	RoomID    int64
	UserID    int64
	Status    *int8
	StartTime *time.Time
	EndTime   *time.Time
}

// PageRoomBookings 分页列预约（start_time ASC, id ASC），total 为同条件总数。
func (r *Repository) PageRoomBookings(ctx context.Context, f RoomBookingFilter, offset, limit int) ([]model.RoomBooking, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.RoomBooking{})
	if f.RoomID > 0 {
		q = q.Where("`room_id` = ?", f.RoomID)
	}
	if f.UserID > 0 {
		q = q.Where("`user_id` = ?", f.UserID)
	}
	if f.Status != nil {
		q = q.Where("`status` = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("`end_time` > ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("`start_time` < ?", *f.EndTime)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计预约: %w", err)
	}
	var rows []model.RoomBooking
	if err := q.Order("`start_time` ASC, `id` ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询预约: %w", err)
	}
	return rows, total, nil
}

// ListRoomBookingsForReminder 扫描提醒窗口 (now, until] 内、有效且尚未提醒
// 的预约（status=0 AND reminded_at IS NULL AND start_time > now AND
// start_time <= until），start_time ASC。
func (r *Repository) ListRoomBookingsForReminder(ctx context.Context, now, until time.Time) ([]model.RoomBooking, error) {
	var rows []model.RoomBooking
	if err := r.db.WithContext(ctx).
		Where("`status` = ? AND `reminded_at` IS NULL AND `start_time` > ? AND `start_time` <= ?",
			model.RoomBookingStatusActive, now, until).
		Order("`start_time` ASC, `id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("扫描待提醒预约: %w", err)
	}
	return rows, nil
}

// MarkRoomBookingReminded 条件更新 reminded_at（只对有效且尚未提醒的预约
// 生效）：返回受影响行数，0 表示已被其他并发任务认领/已取消，调用方跳过
// 通知，保证提醒只发一次（spec §4.11）。
func (r *Repository) MarkRoomBookingReminded(ctx context.Context, id int64, at time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Model(&model.RoomBooking{}).
		Where("`id` = ? AND `reminded_at` IS NULL AND `status` = ?",
			id, model.RoomBookingStatusActive).
		Update("`reminded_at`", at)
	if result.Error != nil {
		return 0, fmt.Errorf("标记预约已提醒: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// escapeRoomLike 转义 LIKE 通配符（%、_、\），使名称按字面匹配。
func escapeRoomLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
