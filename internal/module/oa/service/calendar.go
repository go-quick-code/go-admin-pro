// calendar.go 承载日程的完整功能（issue #56，docs/oa-mvp-spec.md
// §3.7/§4.11）：
//
//   - 创建：标题/描述/起止时间/全天/可见性；部门可见性在创建时写入创建人
//     部门快照（dept_id），之后调部门不刷新；
//   - 编辑：仅创建人、仅有效日程；可见性改为公开同样需要 oa:calendar:public；
//   - 取消：软取消（status=1），仅创建人；取消后列表/详情一律不再展示；
//   - 列表/详情：按三条可见性谓词读取（私有仅创建人、部门按创建时快照及
//     查看者祖先链、公开全租户），范围外/跨租户/已取消一律 404，不泄露存在性。
//
// 判定规则全部在 calendar_rule.go 的纯函数里（CalendarViewer.Permits 与
// repository 的单条 SQL 谓词语义一致）；本文件只做取数编排与落库。
// 会议室/预约的 CRUD 与冲突校验由 #57 承接（三表 schema 已在 00020 建全）。
package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// CreateCalendarEvent 创建日程（POST /oa/calendar/event）：
//   - 标题/描述/时间校验；
//   - 可见性枚举校验；公开需要 oa:calendar:public（否则 403）；
//   - dept_id 取当前创建人部门快照（无部门记 0，部门日程只对创建人可见）。
func (s *Service) CreateCalendarEvent(ctx context.Context, req *dto.CalendarEventSaveReq) (*dto.CalendarEventItem, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	title, description, err := ValidateCalendarEventText(req.Title, req.Description)
	if err != nil {
		return nil, err
	}
	start, end, err := s.parseCalendarWindow(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	if err := ValidateCalendarEventTimes(start, end); err != nil {
		return nil, err
	}
	hasPublic, err := s.calendarPublicAllowed(ctx, userID, req.Visibility)
	if err != nil {
		return nil, err
	}
	if err := ValidateCalendarVisibility(req.Visibility, hasPublic); err != nil {
		return nil, err
	}
	creatorDeptID, err := s.calendarCreatorDeptID(ctx, userID)
	if err != nil {
		return nil, err
	}

	row := &model.CalendarEvent{
		CreatorID:   userID,
		Title:       title,
		Description: description,
		StartTime:   start,
		EndTime:     end,
		AllDay:      req.AllDay,
		Visibility:  req.Visibility,
		DeptID:      ResolveCalendarDeptSnapshot(nil, creatorDeptID),
		Source:      model.CalendarSourceManual,
		Status:      model.CalendarEventStatusActive,
	}
	if err := s.repo.CreateCalendarEvent(ctx, row); err != nil {
		return nil, err
	}
	item := s.calendarEventItemOf(*row)
	return &item, nil
}

// UpdateCalendarEvent 编辑日程（PUT /oa/calendar/event）：仅创建人、仅有效
// 日程；dept_id 保留创建时快照（用户调部门后历史日程仍归属原部门）。
// 可见性改为公开同样要求 oa:calendar:public；source=room_booking 的日程
// 由会议室预约联动维护，日历接口编辑返回 409（PR #66 审查 P2-5）。
func (s *Service) UpdateCalendarEvent(ctx context.Context, req *dto.CalendarEventSaveReq) (*dto.CalendarEventItem, error) {
	if req.ID <= 0 {
		return nil, xerr.BadRequest("日程 ID 不能为空")
	}
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	existing, err := s.repo.FindCalendarEvent(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil || existing.CreatorID != userID || existing.Status != model.CalendarEventStatusActive {
		return nil, xerr.NotFound("日程不存在")
	}
	title, description, err := ValidateCalendarEventText(req.Title, req.Description)
	if err != nil {
		return nil, err
	}
	start, end, err := s.parseCalendarWindow(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	if err := ValidateCalendarEventTimes(start, end); err != nil {
		return nil, err
	}
	hasPublic, err := s.calendarPublicAllowed(ctx, userID, req.Visibility)
	if err != nil {
		return nil, err
	}
	if err := ValidateCalendarVisibility(req.Visibility, hasPublic); err != nil {
		return nil, err
	}

	var updated *model.CalendarEvent
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindCalendarEventForUpdate(ctx, req.ID)
		if err != nil {
			return err
		}
		if row == nil || row.CreatorID != userID || row.Status != model.CalendarEventStatusActive {
			return xerr.NotFound("日程不存在")
		}
		// 会议室预约生成的日程只允许经预约取消路径联动修改（P2-5）。
		if err := ValidateCalendarEventMutable(*row); err != nil {
			return err
		}
		row.Title = title
		row.Description = description
		row.StartTime = start
		row.EndTime = end
		row.AllDay = req.AllDay
		row.Visibility = req.Visibility
		// dept_id 不刷新：创建时快照是部门可见性的唯一依据（spec §4.11）。
		if err := tx.SaveCalendarEvent(ctx, row); err != nil {
			return err
		}
		updated = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	item := s.calendarEventItemOf(*updated)
	return &item, nil
}

// CancelCalendarEvent 取消日程（DELETE/POST /oa/calendar/event/{id}/cancel）：
// 仅创建人、仅有效日程；软取消写 status=1，取消后列表/详情不再展示
// （spec §4.11/任务验收：软取消）。重复取消按 404 处理（已不可见）。
// source=room_booking 的日程只能经 CancelRoomBooking 联动取消，本接口 409。
func (s *Service) CancelCalendarEvent(ctx context.Context, id int64) error {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return xerr.Unauthorized("")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindCalendarEventForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil || row.CreatorID != userID || row.Status != model.CalendarEventStatusActive {
			return xerr.NotFound("日程不存在")
		}
		// 会议室预约生成的日程由 CancelRoomBooking 联动取消（P2-5）。
		if err := ValidateCalendarEventMutable(*row); err != nil {
			return err
		}
		row.Status = model.CalendarEventStatusCancelled
		return tx.SaveCalendarEvent(ctx, row)
	})
}

// ListCalendarEvents 日程列表（GET /oa/calendar/event/list）：按可见性谓词
// 返回有效日程（start_time ASC）。时间窗口可选，区间语义为「与日程相交」；
// 关键词按标题模糊匹配。区间必须成对且 ≤366 天。
func (s *Service) ListCalendarEvents(ctx context.Context, req *dto.CalendarEventListReq) ([]dto.CalendarEventItem, error) {
	viewer, err := s.calendarViewer(ctx)
	if err != nil {
		return nil, err
	}
	loc := s.businessLocation()
	start, err := parseOptionalCalendarTime(req.StartTime, loc)
	if err != nil {
		return nil, err
	}
	end, err := parseOptionalCalendarTime(req.EndTime, loc)
	if err != nil {
		return nil, err
	}
	if err := ValidateCalendarListRange(start, end); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListCalendarEventsByVisibility(ctx, repository.CalendarListFilter{
		StartTime: start,
		EndTime:   end,
		Keyword:   req.Keyword,
		UserID:    viewer.UserID,
		DeptIDs:   viewer.DeptIDs,
	})
	if err != nil {
		return nil, err
	}
	items := make([]dto.CalendarEventItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.calendarEventItemOf(row))
	}
	return items, nil
}

// GetCalendarEvent 日程详情（GET /oa/calendar/event/{id}）：与列表同一可见性
// 谓词；范围外/跨租户/已取消一律 404，不泄露存在性。
func (s *Service) GetCalendarEvent(ctx context.Context, id int64) (*dto.CalendarEventItem, error) {
	viewer, err := s.calendarViewer(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.FindVisibleCalendarEvent(ctx, id, viewer.UserID, viewer.DeptIDs)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("日程不存在")
	}
	item := s.calendarEventItemOf(*row)
	return &item, nil
}

// ---------- 内部：取数与映射 ----------

// calendarViewer 组装查看者可见性上下文：本人部门及其**祖先链**（含自身）。
// 部门日程的语义是「对创建者部门及其子树可见」（spec §4.11），对查看者而言
// 即 event.dept_id ∈ 查看者部门的祖先链：子部门用户能看到祖先部门创建的日程，
// 祖先部门用户看不到子部门创建的日程。部门取数走 SystemGateway（租户插件已
// 限定本租户）；无部门时祖先链为空，查看者仍能看到本人的私有/部门日程与公开日程。
func (s *Service) calendarViewer(ctx context.Context) (CalendarViewer, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return CalendarViewer{}, xerr.Unauthorized("")
	}
	deptID, ok, err := s.sys.UserDeptID(ctx, userID)
	if err != nil {
		return CalendarViewer{}, err
	}
	// 向上构造祖先链（含自身，最多 64 层防环），与公告受众判定同源。
	var ancestors []int64
	for depth := 0; ok && deptID > 0 && depth < 64; depth++ {
		ancestors = append(ancestors, deptID)
		parent, has, err := s.sys.DeptParentID(ctx, deptID)
		if err != nil {
			return CalendarViewer{}, err
		}
		deptID, ok = parent, has
	}
	return CalendarViewerOf(userID, 0, ancestors), nil
}

// calendarCreatorDeptID 返回创建人当前部门（用于创建时快照）；无部门返回 0。
func (s *Service) calendarCreatorDeptID(ctx context.Context, userID int64) (int64, error) {
	deptID, ok, err := s.sys.UserDeptID(ctx, userID)
	if err != nil {
		return 0, err
	}
	if !ok || deptID <= 0 {
		return 0, nil
	}
	return deptID, nil
}

// calendarPublicAllowed 仅在需要公开可见性时查询权限码，避免每次创建/编辑
// 都多做一次权限解析；非公开直接返回 false（ValidateCalendarVisibility 不用）。
func (s *Service) calendarPublicAllowed(ctx context.Context, userID int64, visibility int8) (bool, error) {
	if visibility != model.CalendarVisibilityPublic {
		return false, nil
	}
	return s.sys.UserHasPermission(ctx, userID, CalendarPublicPermission)
}

// parseCalendarWindow 解析起止时间（业务时区口径）。
func (s *Service) parseCalendarWindow(startText, endText string) (time.Time, time.Time, error) {
	loc := s.businessLocation()
	start, err := parseCalendarTime(startText, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parseCalendarTime(endText, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end, nil
}

// calendarEventItemOf 组装日程行视图：时间统一按业务时区墙钟格式化，
// 与请求输入口径一致（ADR 0006）。
func (s *Service) calendarEventItemOf(row model.CalendarEvent) dto.CalendarEventItem {
	loc := s.businessLocation()
	item := dto.CalendarEventItem{
		ID:          row.ID,
		CreatorID:   row.CreatorID,
		Title:       row.Title,
		Description: row.Description,
		StartTime:   row.StartTime.In(loc).Format(CalendarTimeLayout),
		EndTime:     row.EndTime.In(loc).Format(CalendarTimeLayout),
		AllDay:      row.AllDay,
		Visibility:  row.Visibility,
		DeptID:      row.DeptID,
		Source:      row.Source,
		SourceID:    row.SourceID,
		Status:      row.Status,
	}
	if !row.CreatedAt.IsZero() {
		item.CreateTime = row.CreatedAt.In(loc).Format(timeLayout)
	}
	if !row.UpdatedAt.IsZero() {
		item.UpdateTime = row.UpdatedAt.In(loc).Format(timeLayout)
	}
	return item
}
