// calendar.go 承载日程的数据访问（issue #56，spec §3.7/§4.11）：
// 创建/更新/读取、可见范围列表与取消（软取消写 status=1）。
//
// 可见性只有**一条谓词**（ListCalendarEventsByVisibility 与
// FindVisibleCalendarEvent 共用 calendarVisibleClause）：
//
//	status = 0 AND (
//	  creator_id = 查看者
//	  OR (visibility = 1 AND dept_id IN 查看者部门子树)
//	  OR visibility = 2
//	)
//
// 与 service.CalendarViewer.Permits 的内存语义逐条对应（纯函数表驱动锁定）。
// 租户条件由插件自动追加；跨租户业务 ID 一律未命中（service 层表现为 404）。
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CalendarListFilter 是日程列表的查询条件：
//   - StartTime/EndTime 可选，区间语义为「日程与查询窗口相交」：
//     end_time > start AND start_time < end；
//   - Keyword 可选，标题模糊匹配（LIKE，转义通配符）；
//   - UserID/DeptIDs 是查看者上下文（部门子树含自身；空集合时只可能命中
//     本人与公开日程）；
//   - Limit 结果上限（<=0 时取 500）。
type CalendarListFilter struct {
	StartTime *time.Time
	EndTime   *time.Time
	Keyword   string
	UserID    int64
	DeptIDs   []int64
	Limit     int
}

// defaultCalendarListLimit 是列表未指定上限时的默认条数。
const defaultCalendarListLimit = 500

// calendarVisibleClause 是唯一的可见性 SQL 谓词（与 service 纯函数镜像）。
func calendarVisibleClause(q *gorm.DB, userID int64, deptIDs []int64) *gorm.DB {
	if userID <= 0 {
		// fail-closed：未登录上下文什么都看不到。
		return q.Where("1 = 0")
	}
	if len(deptIDs) == 0 {
		return q.Where(
			"`status` = ? AND (`creator_id` = ? OR `visibility` = ?)",
			model.CalendarEventStatusActive, userID, model.CalendarVisibilityPublic,
		)
	}
	return q.Where(
		"`status` = ? AND (`creator_id` = ? OR (`visibility` = ? AND `dept_id` IN ?) OR `visibility` = ?)",
		model.CalendarEventStatusActive, userID,
		model.CalendarVisibilityDept, deptIDs,
		model.CalendarVisibilityPublic,
	)
}

// CreateCalendarEvent 落一条日程；tenant_id 由插件回填。
func (r *Repository) CreateCalendarEvent(ctx context.Context, row *model.CalendarEvent) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建日程: %w", err)
	}
	return nil
}

// SaveCalendarEvent 更新日程的全部可变字段（标题/描述/时间/可见性/状态）。
// dept_id 是创建时快照，service 不会改写（spec §4.11）。
func (r *Repository) SaveCalendarEvent(ctx context.Context, row *model.CalendarEvent) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新日程: %w", err)
	}
	return nil
}

// FindCalendarEvent 按 ID 读日程（不过滤可见性）；未命中（含跨租户）返回 nil。
func (r *Repository) FindCalendarEvent(ctx context.Context, id int64) (*model.CalendarEvent, error) {
	var row model.CalendarEvent
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindCalendarEventForUpdate 行锁读日程（编辑/取消的写入口，串行化并发写）。
func (r *Repository) FindCalendarEventForUpdate(ctx context.Context, id int64) (*model.CalendarEvent, error) {
	var row model.CalendarEvent
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListCalendarEventsByVisibility 按可见性谓词列日程（start_time ASC）：
// 只返回有效日程，可叠加时间窗口/关键词；结果上限默认 500。
func (r *Repository) ListCalendarEventsByVisibility(ctx context.Context, f CalendarListFilter) ([]model.CalendarEvent, error) {
	q := r.db.WithContext(ctx).Model(&model.CalendarEvent{})
	q = calendarVisibleClause(q, f.UserID, f.DeptIDs)
	if f.StartTime != nil {
		q = q.Where("`end_time` > ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("`start_time` < ?", *f.EndTime)
	}
	if keyword := strings.TrimSpace(f.Keyword); keyword != "" {
		q = q.Where("`title` LIKE ? ESCAPE '\\\\'", "%"+escapeCalendarLike(keyword)+"%")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultCalendarListLimit
	}
	var rows []model.CalendarEvent
	if err := q.Order("`start_time` ASC, `id` ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询日程: %w", err)
	}
	return rows, nil
}

// FindVisibleCalendarEvent 按同一可见性谓词读单条日程；不可见（含已取消、
// 跨租户、范围外）返回 nil，调用方统一 404 不泄露存在性。
func (r *Repository) FindVisibleCalendarEvent(ctx context.Context, id, userID int64, deptIDs []int64) (*model.CalendarEvent, error) {
	q := r.db.WithContext(ctx).Model(&model.CalendarEvent{})
	q = calendarVisibleClause(q, userID, deptIDs)
	var row model.CalendarEvent
	if err := q.Where("`id` = ?", id).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// escapeCalendarLike 转义 LIKE 中的通配符（%、_、\），使关键词按字面匹配。
func escapeCalendarLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
