// room.go 承载会议室与预约的完整功能（issue #57，
// docs/oa-mvp-spec.md §4.11/§4.14）：
//
//   - 会议室：CRUD + 启停；删除前检查未来有效预约（有则 409，只能停用）；
//   - 预约：时间范围校验 + 同会议室有效预约重叠校验（冲突 400）；
//     预约成功在**同一事务**内生成 source=room_booking 的部门可见日程
//     （source_id=预约 ID、visibility=部门、dept_id=预约人部门快照）；
//   - 取消：软取消预约，并在同一事务内走系统路径取消其生成的日程（不经过
//     仅限创建人的用户接口 CancelCalendarEvent）；
//   - 会议提醒：系统级定时任务每 5 分钟扫描 (now, now+15m] 内开始的预约，
//     条件更新 reminded_at 认领后向预约人写 meeting_reminder 通知，保证
//     只发一次；通知与 reminded_at 同事务提交/回滚。
//
// 判定规则全部在 room_rule.go 的纯函数里；本文件只做取数编排与落库。
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	notifymodel "github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/text"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// RoomBookingReminderHandlerName 是会议提醒任务的处理器名
// （infra_job.handler_name）：系统级（跨租户迭代），种子默认行每 5 分钟一次。
const RoomBookingReminderHandlerName = "oaRoomBookingReminder"

// ---------- 会议室 ----------

// CreateMeetingRoom 创建会议室（POST /oa/room）：名称/位置/容量校验，
// 新会议室默认启用。
func (s *Service) CreateMeetingRoom(ctx context.Context, req *dto.MeetingRoomSaveReq) (*dto.MeetingRoomResp, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, xerr.Unauthorized("")
	}
	if req == nil {
		return nil, xerr.BadRequest("请求不能为空")
	}
	name, location, err := ValidateMeetingRoomFields(req.Name, req.Location, req.Capacity)
	if err != nil {
		return nil, err
	}
	row := &oamodel.MeetingRoom{
		Name:     name,
		Location: location,
		Capacity: req.Capacity,
		Status:   oamodel.MeetingRoomStatusEnabled,
		Sort:     req.Sort,
	}
	if err := s.repo.CreateMeetingRoom(ctx, row); err != nil {
		return nil, err
	}
	resp := dto.NewMeetingRoomResp(row, s.businessLocation())
	return &resp, nil
}

// UpdateMeetingRoom 编辑会议室（PUT /oa/room/{id}）：状态不在此处变更
// （启停走 SetMeetingRoomStatus）。
func (s *Service) UpdateMeetingRoom(ctx context.Context, id int64, req *dto.MeetingRoomSaveReq) (*dto.MeetingRoomResp, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, xerr.Unauthorized("")
	}
	if id <= 0 || req == nil {
		return nil, xerr.NotFound("会议室不存在")
	}
	name, location, err := ValidateMeetingRoomFields(req.Name, req.Location, req.Capacity)
	if err != nil {
		return nil, err
	}
	var updated *oamodel.MeetingRoom
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindMeetingRoomForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("会议室不存在")
		}
		row.Name = name
		row.Location = location
		row.Capacity = req.Capacity
		row.Sort = req.Sort
		if err := tx.SaveMeetingRoom(ctx, row); err != nil {
			return err
		}
		updated = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	resp := dto.NewMeetingRoomResp(updated, s.businessLocation())
	return &resp, nil
}

// SetMeetingRoomStatus 启停会议室（PUT /oa/room/{id}/status）：0 启用 /
// 1 停用；停用不检查预约（已存在的预约仍有效）。
func (s *Service) SetMeetingRoomStatus(ctx context.Context, id int64, status int8) error {
	if CurrentUserID(ctx) <= 0 {
		return xerr.Unauthorized("")
	}
	if err := ValidateMeetingRoomStatus(status); err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindMeetingRoomForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("会议室不存在")
		}
		row.Status = status
		return tx.SaveMeetingRoom(ctx, row)
	})
}

// DeleteMeetingRoom 删除会议室（DELETE /oa/room/{id}）：有未来有效预约时
// 返回 409（只能停用，spec §4.14）；否则软删除。
func (s *Service) DeleteMeetingRoom(ctx context.Context, id int64) error {
	if CurrentUserID(ctx) <= 0 {
		return xerr.Unauthorized("")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindMeetingRoomForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("会议室不存在")
		}
		bookings, err := tx.CountActiveFutureRoomBookings(ctx, id, s.businessNow())
		if err != nil {
			return err
		}
		if bookings > 0 {
			return xerr.Conflict("会议室存在未来有效预约，只能停用不能删除")
		}
		return tx.DeleteMeetingRoom(ctx, id)
	})
}

// GetMeetingRoom 会议室详情（GET /oa/room/{id}）：跨租户/已删除 404。
func (s *Service) GetMeetingRoom(ctx context.Context, id int64) (*dto.MeetingRoomResp, error) {
	row, err := s.repo.FindMeetingRoom(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("会议室不存在")
	}
	resp := dto.NewMeetingRoomResp(row, s.businessLocation())
	return &resp, nil
}

// PageMeetingRooms 会议室分页（GET /oa/room/page）：名称模糊、状态精确。
func (s *Service) PageMeetingRooms(ctx context.Context, req *dto.MeetingRoomPageReq) ([]dto.MeetingRoomResp, int64, error) {
	page := req.PageQuery
	page.Normalize()
	rows, total, err := s.repo.PageMeetingRooms(ctx, repository.MeetingRoomFilter{
		Name:   req.Name,
		Status: req.Status,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	loc := s.businessLocation()
	list := make([]dto.MeetingRoomResp, 0, len(rows))
	for i := range rows {
		list = append(list, dto.NewMeetingRoomResp(&rows[i], loc))
	}
	return list, total, nil
}

// ListEnabledMeetingRooms 返回全部启用会议室（员工预约下拉，
// GET /oa/room/list，spec §5）。
func (s *Service) ListEnabledMeetingRooms(ctx context.Context) ([]dto.MeetingRoomResp, error) {
	rows, err := s.repo.ListEnabledMeetingRooms(ctx)
	if err != nil {
		return nil, err
	}
	loc := s.businessLocation()
	list := make([]dto.MeetingRoomResp, 0, len(rows))
	for i := range rows {
		list = append(list, dto.NewMeetingRoomResp(&rows[i], loc))
	}
	return list, nil
}

// ---------- 预约 ----------

// CreateRoomBooking 创建预约（POST /oa/room/booking）：
//   - 时间范围校验；同会议室有效预约重叠 → 400；
//   - 会议室必须存在且启用；跨租户/不存在 404；
//   - 同一事务内写预约 + 生成部门可见日程（source=1、source_id=预约 ID、
//     dept_id=预约人当前部门快照），并回填 calendar_event_id。
func (s *Service) CreateRoomBooking(ctx context.Context, req *dto.RoomBookingCreateReq) (*dto.RoomBookingResp, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	if req == nil {
		return nil, xerr.BadRequest("请求不能为空")
	}
	title, err := ValidateRoomBookingTitle(req.Title)
	if err != nil {
		return nil, err
	}
	start, end, err := s.parseCalendarWindow(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	if err := ValidateRoomBookingWindow(start, end); err != nil {
		return nil, err
	}
	deptID, err := s.calendarCreatorDeptID(ctx, userID)
	if err != nil {
		return nil, err
	}

	var created *oamodel.RoomBooking
	var room *oamodel.MeetingRoom
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 锁会议室行：同房间并发预约按此串行化，重叠校验读到的集合稳定。
		row, err := tx.FindMeetingRoomForUpdate(ctx, req.RoomID)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("会议室不存在")
		}
		if row.Status != oamodel.MeetingRoomStatusEnabled {
			return xerr.BadRequest("会议室已停用，不能预约")
		}
		conflicts, err := tx.CountActiveRoomBookingsOverlapping(ctx, req.RoomID, start, end, 0)
		if err != nil {
			return err
		}
		if conflicts > 0 {
			return xerr.BadRequest("该会议室在所选时段已有预约")
		}
		booking := &oamodel.RoomBooking{
			RoomID:    req.RoomID,
			UserID:    userID,
			Title:     title,
			StartTime: start,
			EndTime:   end,
			Status:    oamodel.RoomBookingStatusActive,
		}
		if err := tx.CreateRoomBooking(ctx, booking); err != nil {
			return err
		}
		event := &oamodel.CalendarEvent{
			CreatorID:   userID,
			Title:       title,
			Description: fmt.Sprintf("会议室：%s %s", row.Name, row.Location),
			StartTime:   start,
			EndTime:     end,
			Visibility:  oamodel.CalendarVisibilityDept,
			DeptID:      ResolveCalendarDeptSnapshot(nil, deptID),
			Source:      oamodel.CalendarSourceRoomBooking,
			SourceID:    &booking.ID,
			Status:      oamodel.CalendarEventStatusActive,
		}
		if err := tx.CreateCalendarEvent(ctx, event); err != nil {
			return err
		}
		eventID := event.ID
		booking.CalendarEventID = &eventID
		if err := tx.SaveRoomBooking(ctx, booking); err != nil {
			return err
		}
		created = booking
		room = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	list, err := s.roomBookingResps(ctx, []oamodel.RoomBooking{*created}, map[int64]*oamodel.MeetingRoom{room.ID: room})
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}

// CancelRoomBooking 取消预约（POST /oa/room/booking/{id}/cancel）：
// 预约人本人或持有 oa:room:manage 的用户；软取消预约，并在同一事务内走
// 系统路径取消其生成的日程（不经过仅限创建人的用户取消接口）。
func (s *Service) CancelRoomBooking(ctx context.Context, id int64) error {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return xerr.Unauthorized("")
	}
	canManage, err := s.sys.UserHasPermission(ctx, userID, RoomManagePermission)
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindRoomBookingForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil || (row.UserID != userID && !canManage) {
			// 非本人且无管理权：按不存在处理，不泄露跨用户/跨租户数据。
			return xerr.NotFound("预约不存在")
		}
		if row.Status != oamodel.RoomBookingStatusActive {
			return xerr.Conflict("预约已取消")
		}
		row.Status = oamodel.RoomBookingStatusCancelled
		if row.CalendarEventID != nil {
			event, err := tx.FindCalendarEventForUpdate(ctx, *row.CalendarEventID)
			if err != nil {
				return err
			}
			if event != nil && event.Status == oamodel.CalendarEventStatusActive {
				event.Status = oamodel.CalendarEventStatusCancelled
				if err := tx.SaveCalendarEvent(ctx, event); err != nil {
					return err
				}
			}
		}
		return tx.SaveRoomBooking(ctx, row)
	})
}

// GetRoomBooking 预约详情（GET /oa/room/booking/{id}）：预约人本人或
// 持有 oa:room:manage 可读；其余（含跨租户）404。
func (s *Service) GetRoomBooking(ctx context.Context, id int64) (*dto.RoomBookingResp, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	row, err := s.repo.FindRoomBooking(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("预约不存在")
	}
	canManage, err := s.sys.UserHasPermission(ctx, userID, RoomManagePermission)
	if err != nil {
		return nil, err
	}
	if row.UserID != userID && !canManage {
		return nil, xerr.NotFound("预约不存在")
	}
	list, err := s.roomBookingResps(ctx, []oamodel.RoomBooking{*row}, nil)
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}

// PageRoomBookings 预约分页（GET /oa/room/booking/list）：
//   - 持有 oa:room:manage：可按房间/日期/状态/预约人查看全租户预约；
//   - 否则强制只看本人（忽略请求里的 userId，spec §5 员工入口）。
func (s *Service) PageRoomBookings(ctx context.Context, req *dto.RoomBookingPageReq) ([]dto.RoomBookingResp, int64, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	canManage, err := s.sys.UserHasPermission(ctx, userID, RoomManagePermission)
	if err != nil {
		return nil, 0, err
	}
	filter := repository.RoomBookingFilter{
		RoomID: req.RoomID,
		Status: req.Status,
	}
	if canManage {
		filter.UserID = req.UserID
	} else {
		filter.UserID = userID
	}
	start, end, err := s.roomBookingDayWindow(req.Date)
	if err != nil {
		return nil, 0, err
	}
	filter.StartTime = start
	filter.EndTime = end

	page := req.PageQuery
	page.Normalize()
	rows, total, err := s.repo.PageRoomBookings(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.roomBookingResps(ctx, rows, nil)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ---------- 会议提醒任务 ----------

// RoomReminderSummary 汇总一次提醒任务的执行结果（handler 日志用）。
type RoomReminderSummary struct {
	// Now 本轮扫描的业务时区时间（YYYY-MM-DD HH:mm:ss，便于日志比对）。
	Now string
	// Tenants 迭代的启用租户数。
	Tenants int
	// Sent 本轮实际发出的提醒通知数。
	Sent int
}

// RunRoomBookingReminders 是会议提醒任务入口（infra_job handler）：系统级
// 动作，先以 tenant.Skip 列出启用租户，再逐租户扫描 (now, now+15m] 内开始
// 的有效未提醒预约并投递 meeting_reminder（spec §4.11）。单个租户失败不
// 阻塞其他租户，错误聚合返回首个。
//
// handler_param 可填业务时区墙钟 "YYYY-MM-DD HH:mm:ss" 或 RFC3339 固定
// 「现在」（测试/回放）；留空取 businessNow。
func (s *Service) RunRoomBookingReminders(ctx context.Context, param string) (RoomReminderSummary, error) {
	now, err := s.resolveRoomReminderNow(param)
	if err != nil {
		return RoomReminderSummary{}, err
	}
	summary := RoomReminderSummary{Now: now.Format(CalendarTimeLayout)}
	tenantIDs, err := s.sys.ListEnabledTenantIDs(ctx)
	if err != nil {
		return summary, err
	}
	summary.Tenants = len(tenantIDs)
	var errs []error
	for _, tenantID := range tenantIDs {
		sent, err := s.remindTenantRoomBookings(tenant.WithTenantID(ctx, tenantID), now)
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 会议提醒失败: %w", tenantID, err))
			continue
		}
		summary.Sent += sent
		if sent > 0 {
			slog.Info("room booking reminder tenant done", "tenantId", tenantID, "sent", sent)
		}
	}
	if len(errs) > 0 {
		return summary, errs[0]
	}
	return summary, nil
}

// remindTenantRoomBookings 扫描当前租户的提醒窗口并逐条投递。
// 每条预约独立事务：条件更新 reminded_at 认领成功后才写通知；任一步失败
// 一起回滚，下一轮可重试（不会重复发送已提交的提醒）。
func (s *Service) remindTenantRoomBookings(ctx context.Context, now time.Time) (int, error) {
	bookings, err := s.repo.ListRoomBookingsForReminder(ctx, now, now.Add(RoomReminderLeadTime))
	if err != nil {
		return 0, err
	}
	if len(bookings) == 0 {
		return 0, nil
	}
	roomIDs := make([]int64, 0, len(bookings))
	seen := make(map[int64]struct{}, len(bookings))
	for i := range bookings {
		if _, ok := seen[bookings[i].RoomID]; ok {
			continue
		}
		seen[bookings[i].RoomID] = struct{}{}
		roomIDs = append(roomIDs, bookings[i].RoomID)
	}
	rooms, err := s.repo.ListMeetingRoomsByIDs(ctx, roomIDs)
	if err != nil {
		return 0, err
	}
	roomByID := make(map[int64]*oamodel.MeetingRoom, len(rooms))
	for i := range rooms {
		roomByID[rooms[i].ID] = &rooms[i]
	}

	sent := 0
	for i := range bookings {
		booking := &bookings[i]
		room := roomByID[booking.RoomID]
		claimed := false
		var pending []notifyservice.Input
		err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
			affected, err := tx.MarkRoomBookingReminded(ctx, booking.ID, now)
			if err != nil {
				return err
			}
			if affected == 0 {
				// 已被并发任务认领或已取消：不重复发。
				return nil
			}
			if s.notify == nil {
				return xerr.Internal(fmt.Errorf("会议提醒缺少通知网关装配"))
			}
			pending = buildRoomReminderNotifies(booking, room, s.businessLocation())
			if err := s.notify.CreateInTx(ctx, tx.DB(), pending); err != nil {
				return err
			}
			claimed = true
			return nil
		})
		if err != nil {
			return sent, err
		}
		if claimed {
			sent++
			// 事务已提交：显式发布实时推送（issue #78，spec §4.5.5；best-effort，
			// 丢失由前端 60 秒轮询兜底，ADR 0013）。
			s.notify.PublishInputs(ctx, pending)
		}
	}
	return sent, nil
}

// buildRoomReminderNotifies 构造预约人的 meeting_reminder 通知（单接收人）：
// 标题/内容按 notify_message 列宽截断；biz_type/biz_id 定位预约，路由指向
// 员工侧「我的会议」（菜单 539）。
func buildRoomReminderNotifies(booking *oamodel.RoomBooking, room *oamodel.MeetingRoom, loc *time.Location) []notifyservice.Input {
	roomName := "会议室"
	if room != nil {
		if room.Name != "" {
			roomName = room.Name
		}
		if room.Location != "" {
			roomName += "（" + room.Location + "）"
		}
	}
	title := text.TruncateRunes("会议提醒："+booking.Title, 128)
	content := text.TruncateRunes(fmt.Sprintf("%s，时间 %s - %s",
		roomName,
		booking.StartTime.In(loc).Format(CalendarTimeLayout),
		booking.EndTime.In(loc).Format(CalendarTimeLayout)), 500)
	return []notifyservice.Input{{
		UserID:      booking.UserID,
		Type:        notifymodel.TypeMeetingReminder,
		Title:       title,
		Content:     content,
		BizType:     RoomBookingNotifyBizType,
		BizID:       booking.ID,
		Route:       RoomBookingNotifyRoute,
		RouteParams: fmt.Sprintf(`{"id":%d}`, booking.ID),
	}}
}

// resolveRoomReminderNow 解析任务参数中的「现在」：空取 businessNow；
// 支持业务时区墙钟与 RFC3339（非法返回 400）。
func (s *Service) resolveRoomReminderNow(param string) (time.Time, error) {
	text := trimSpace(param)
	if text == "" {
		return s.businessNow(), nil
	}
	loc := s.businessLocation()
	if t, err := time.ParseInLocation(CalendarTimeLayout, text, loc); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	return time.Time{}, xerr.BadRequest("提醒时间参数格式应为 YYYY-MM-DD HH:mm:ss 或 RFC3339")
}

// ---------- 内部：取数与映射 ----------

// roomBookingDayWindow 把业务时区日期 YYYY-MM-DD 解析为当日窗口
// [00:00, 次日 00:00)；空串不过滤。
func (s *Service) roomBookingDayWindow(date string) (*time.Time, *time.Time, error) {
	text := trimSpace(date)
	if text == "" {
		return nil, nil, nil
	}
	day, err := time.ParseInLocation("2006-01-02", text, s.businessLocation())
	if err != nil {
		return nil, nil, xerr.BadRequest("日期格式应为 YYYY-MM-DD")
	}
	end := day.AddDate(0, 0, 1)
	return &day, &end, nil
}

// roomBookingResps 把预约行转响应并回填房间名称/位置与预约人昵称；
// rooms 非 nil 时复用（创建路径已持有锁内读到的会议室行）。
func (s *Service) roomBookingResps(ctx context.Context, rows []oamodel.RoomBooking, rooms map[int64]*oamodel.MeetingRoom) ([]dto.RoomBookingResp, error) {
	loc := s.businessLocation()
	if rooms == nil {
		roomIDs := make([]int64, 0, len(rows))
		seenRoom := make(map[int64]struct{}, len(rows))
		for i := range rows {
			if _, ok := seenRoom[rows[i].RoomID]; ok {
				continue
			}
			seenRoom[rows[i].RoomID] = struct{}{}
			roomIDs = append(roomIDs, rows[i].RoomID)
		}
		list, err := s.repo.ListMeetingRoomsByIDs(ctx, roomIDs)
		if err != nil {
			return nil, err
		}
		rooms = make(map[int64]*oamodel.MeetingRoom, len(list))
		for i := range list {
			rooms[list[i].ID] = &list[i]
		}
	}
	userIDs := make([]int64, 0, len(rows))
	seenUser := make(map[int64]struct{}, len(rows))
	for i := range rows {
		if _, ok := seenUser[rows[i].UserID]; ok {
			continue
		}
		seenUser[rows[i].UserID] = struct{}{}
		userIDs = append(userIDs, rows[i].UserID)
	}
	names := map[int64]string{}
	if len(userIDs) > 0 {
		var err error
		names, err = s.sys.UserNicknames(ctx, userIDs)
		if err != nil {
			return nil, err
		}
	}
	out := make([]dto.RoomBookingResp, 0, len(rows))
	for i := range rows {
		resp := dto.NewRoomBookingResp(&rows[i], loc)
		if room := rooms[rows[i].RoomID]; room != nil {
			resp.RoomName = room.Name
			resp.RoomLocation = room.Location
		}
		resp.UserName = names[rows[i].UserID]
		out = append(out, resp)
	}
	return out, nil
}
