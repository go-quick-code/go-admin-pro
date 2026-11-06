// attendance_result.go 承载考勤结果的派生、重算、校正与每日任务编排
// （issue #61，docs/oa-mvp-spec.md §3.9/§4.10）：
//
//   - 派生：打卡（最早上班/最晚下班）+ 工作日历（#50）+ 班次宽限 → 结果；
//     纯规则见 attendance_result_rule.go；
//   - 触发：打卡后重算当日、每日任务生成前一日、管理员手工重算范围；
//     校正同样先重算派生字段再写 override（校正优先且不覆盖原始打卡）；
//   - 幂等：结果按唯一键 (tenant_id, user_id, work_date) upsert，重算可重复；
//   - 可见范围：员工仅本人；部门负责人看子树；持有 oa:attendance:manage 的
//     管理员看全租户（复用 #43 VisibilityScopeOf），跨租户 404。
//
// 请假联动（leave_days 写入、请假通过/取消触发重算）由 #62 承接：
// 本工单留 LeaveDaysProvider 接缝，未注入时按无请假（0）派生。
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// AttendanceDailyResultHandlerName 是考勤日结果任务的处理器名
// （infra_job.handler_name）：系统级（跨租户迭代），种子默认行每天 01:00。
const AttendanceDailyResultHandlerName = "oaAttendanceDailyResult"

// LeaveDaysProvider 是请假天数的取数接缝（#62 接点）：给定用户与日期返回
// 当日已批准请假天数（0/0.5/>=1）。#61 不读请假表，未注入时恒 0。
type LeaveDaysProvider interface {
	LeaveDaysOn(ctx context.Context, userID int64, date string) (float64, error)
}

// SetLeaveDaysProvider 注入请假天数取数实现（#62 装配；nil 表示无请假联动）。
func (s *Service) SetLeaveDaysProvider(p LeaveDaysProvider) { s.leaveDays = p }

// RecomputeUserDate 重算单个用户单日结果（打卡/校正后的受影响日期），
// 返回写入行数（恒 1；日期非法 400）。幂等：重复调用覆盖同一行。
func (s *Service) RecomputeUserDate(ctx context.Context, userID int64, date string) (int, error) {
	if userID <= 0 {
		return 0, xerr.BadRequest("请选择要重算的用户")
	}
	key, err := normalizeResultDate(date)
	if err != nil {
		return 0, err
	}
	shift, err := s.shiftOfUser(ctx, s.repo, userID)
	if err != nil {
		return 0, err
	}
	return s.recomputeUserDates(ctx, s.repo, userID, key, key, s.loadExplicitDays(ctx, key, key), shift)
}

// RecomputeUserRange 按用户 + 日期范围重算（管理端手工重算，spec §4.10）：
// 含两端，最多 MaxAttendanceResultRangeDays 天；返回写入行数。可见范围按
// §4.6（本人 / 部门负责人子树 / oa:attendance:manage 全租户），范围外 404。
func (s *Service) RecomputeUserRange(ctx context.Context, req *dto.AttendanceRecomputeReq) (int, error) {
	return s.recomputeUserRange(ctx, req, true)
}

// recomputeUserRange 是重算的公共实现；enforceVisibility=false 供系统触发的
// 联动路径使用（请假通过/取消后的考勤重算：动作本人/审批人未必持有考勤
// 可见范围，重算对象由请假单本身限定，PR #66 审查 P2-4）。
func (s *Service) recomputeUserRange(ctx context.Context, req *dto.AttendanceRecomputeReq, enforceVisibility bool) (int, error) {
	if req == nil || req.UserID <= 0 {
		return 0, xerr.BadRequest("请选择要重算的用户")
	}
	startKey, endKey, err := normalizeResultRange(req.StartDate, req.EndDate)
	if err != nil {
		return 0, err
	}
	if enforceVisibility {
		// 可见范围（PR #66 审查 P2-4）：手工重算与结果列表/校正同一口径，
		// 本人或管辖子树之外的用户一律 404，不泄露存在性。
		scope, err := s.VisibilityScopeOf(ctx, "attendance")
		if err != nil {
			return 0, err
		}
		if !scope.Permits(req.UserID) {
			return 0, xerr.NotFound("用户不存在")
		}
	}
	// 用户必须存在于当前租户（跨租户/已删 404，不泄露存在性）：昵称映射只含
	// 本租户用户，缺席即不存在。未入组用户仍允许重算（先补算、后补建组）。
	names, err := s.sys.UserNicknames(ctx, []int64{req.UserID})
	if err != nil {
		return 0, err
	}
	if _, ok := names[req.UserID]; !ok {
		return 0, xerr.NotFound("用户不存在")
	}
	shift, err := s.shiftOfUser(ctx, s.repo, req.UserID)
	if err != nil {
		return 0, err
	}
	return s.recomputeUserDates(ctx, s.repo, req.UserID, startKey, endKey,
		s.loadExplicitDays(ctx, startKey, endKey), shift)
}

// RecomputeTenantDate 为当前租户内全部考勤组成员重算指定日期（每日任务
// 与系统级批量路径）：日期类型与班次映射只解析一次。
func (s *Service) RecomputeTenantDate(ctx context.Context, date string) (int, error) {
	key, err := normalizeResultDate(date)
	if err != nil {
		return 0, err
	}
	groups, err := s.repo.ListAllGroups(ctx)
	if err != nil {
		return 0, err
	}
	groupByID := make(map[int64]*model.AttendanceGroup, len(groups))
	for i := range groups {
		groupByID[groups[i].ID] = &groups[i]
	}
	members, err := s.repo.ListAllGroupUsers(ctx)
	if err != nil {
		return 0, err
	}
	explicit := s.loadExplicitDays(ctx, key, key)
	shifts := make(map[int64]*model.AttendanceShift)
	count := 0
	for i := range members {
		member := &members[i]
		if member.UserID <= 0 {
			continue
		}
		group := groupByID[member.GroupID]
		if group == nil {
			continue // 组已删除但关系残留：不派生（与打卡侧兜底一致）
		}
		shift, ok := shifts[group.ShiftID]
		if !ok {
			if shift, err = s.repo.FindShiftByID(ctx, group.ShiftID); err != nil {
				return count, err
			}
			shifts[group.ShiftID] = shift
		}
		if _, err := s.recomputeUserDates(ctx, s.repo, member.UserID, key, key, explicit, shift); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// AttendanceDailySummary 汇总一次每日任务的执行结果（handler 日志用）。
type AttendanceDailySummary struct {
	Date    string
	Tenants int
	Users   int
}

// RunDailyAttendanceResult 是考勤日结果任务入口（infra_job handler）：
// 系统级动作，先以 tenant.Skip 列出启用租户，再逐租户携带租户上下文派生
// 指定日期（默认业务时区的“前一日”，spec §4.10）。单个租户失败不阻塞其他
// 租户，错误聚合返回首个。
func (s *Service) RunDailyAttendanceResult(ctx context.Context, param string) (AttendanceDailySummary, error) {
	date := trimSpace(param)
	if date == "" {
		now := s.businessNow()
		date = DateKey(now.AddDate(0, 0, -1))
	}
	key, err := normalizeResultDate(date)
	if err != nil {
		return AttendanceDailySummary{Date: date}, err
	}
	tenantIDs, err := s.sys.ListEnabledTenantIDs(ctx)
	if err != nil {
		return AttendanceDailySummary{Date: key}, err
	}
	summary := AttendanceDailySummary{Date: key, Tenants: len(tenantIDs)}
	var errs []error
	for _, tenantID := range tenantIDs {
		users, err := s.RecomputeTenantDate(tenant.WithTenantID(ctx, tenantID), key)
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 考勤结果生成失败: %w", tenantID, err))
			continue
		}
		summary.Users += users
		slog.Info("attendance daily result tenant done", "tenantId", tenantID, "date", key, "users", users)
	}
	if len(errs) > 0 {
		return summary, errs[0]
	}
	return summary, nil
}

// PageMyAttendanceResults 员工「我的考勤」结果列表（强制本人收口）。
func (s *Service) PageMyAttendanceResults(ctx context.Context, req *dto.MyAttendanceResultPageReq) ([]dto.AttendanceResultResp, int64, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	if err := validateResultDateBounds(req.StartDate, req.EndDate); err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageAttendanceResults(ctx, repository.AttendanceResultFilter{
		UserID:    userID,
		StartDate: trimSpace(req.StartDate),
		EndDate:   trimSpace(req.EndDate),
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return resultRespList(rows, nil), total, nil
}

// PageAttendanceResults 管理端结果列表（spec §4.10 可见范围）：
//   - 无管理权限：部门负责人看子树（含本人）、其他人只看本人；
//   - 持有 oa:attendance:manage：全租户；
//   - userId 过滤越出可见范围时返回空页（不泄露存在性）；跨租户行天然不可见。
func (s *Service) PageAttendanceResults(ctx context.Context, req *dto.AttendanceResultPageReq) ([]dto.AttendanceResultResp, int64, error) {
	if req.Status != nil && !ValidAttendanceResultStatus(*req.Status) {
		return nil, 0, xerr.BadRequest("考勤结果状态不合法")
	}
	if err := validateResultDateBounds(req.StartDate, req.EndDate); err != nil {
		return nil, 0, err
	}
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	scope, err := s.VisibilityScopeOf(ctx, "attendance")
	if err != nil {
		return nil, 0, err
	}

	filter := repository.AttendanceResultFilter{
		StartDate: trimSpace(req.StartDate),
		EndDate:   trimSpace(req.EndDate),
		Status:    req.Status,
	}
	if req.UserID > 0 {
		if !scope.Permits(req.UserID) {
			return []dto.AttendanceResultResp{}, 0, nil
		}
		filter.UserID = req.UserID
	} else if !scope.All {
		filter.UserIDs = scope.UserIDs
	}

	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageAttendanceResults(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	nicknames, err := s.nicknamesOf(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return resultRespList(rows, nicknames), total, nil
}

// CorrectAttendanceResult 管理员校正（spec §4.10）：先重算派生字段（保证
// 校正基于最新打卡），再写 override_status + override_remark；不覆盖原始
// 打卡与派生字段。范围外/跨租户 404，不泄露存在性。
func (s *Service) CorrectAttendanceResult(ctx context.Context, id int64, req *dto.AttendanceCorrectReq) error {
	correctedBy := CurrentUserID(ctx)
	if correctedBy <= 0 {
		return xerr.Unauthorized("")
	}
	if req == nil || req.OverrideStatus == nil {
		return xerr.BadRequest("请选择校正状态")
	}
	remark, err := ValidateAttendanceOverride(*req.OverrideStatus, req.Remark)
	if err != nil {
		return err
	}
	row, err := s.mustResultRow(ctx, id)
	if err != nil {
		return err
	}
	// 重算派生字段：校正优先但不冻结派生值（打卡补齐后派生值仍应更新）。
	if _, err := s.RecomputeUserDate(ctx, row.UserID, DateKey(row.WorkDate)); err != nil {
		return err
	}
	ok, err := s.repo.SetAttendanceCorrection(ctx, row.ID, *req.OverrideStatus, remark, correctedBy, s.businessNow())
	if err != nil {
		return err
	}
	if !ok {
		return xerr.NotFound("考勤结果不存在")
	}
	return nil
}

// ClearAttendanceResultCorrection 撤销校正：override_* 归空，结果回落到派生值。
func (s *Service) ClearAttendanceResultCorrection(ctx context.Context, id int64) error {
	if CurrentUserID(ctx) <= 0 {
		return xerr.Unauthorized("")
	}
	row, err := s.mustResultRow(ctx, id)
	if err != nil {
		return err
	}
	ok, err := s.repo.ClearAttendanceCorrection(ctx, row.ID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.NotFound("考勤结果不存在")
	}
	return nil
}

// mustResultRow 读结果行并校验可见范围：不存在/跨租户/范围外一律 404。
func (s *Service) mustResultRow(ctx context.Context, id int64) (*model.AttendanceResult, error) {
	if id <= 0 {
		return nil, xerr.NotFound("考勤结果不存在")
	}
	row, err := s.repo.FindAttendanceResultByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("考勤结果不存在")
	}
	scope, err := s.VisibilityScopeOf(ctx, "attendance")
	if err != nil {
		return nil, err
	}
	if !scope.Permits(row.UserID) {
		return nil, xerr.NotFound("考勤结果不存在")
	}
	return row, nil
}

// recomputeUserDates 逐日派生并 upsert [startKey, endKey] 的结果。
// explicit 是区间内的工作日历显式记录（一次查询，逐日 ResolveDayType）。
func (s *Service) recomputeUserDates(ctx context.Context, repo *repository.Repository, userID int64, startKey, endKey string, explicit ExplicitDays, shift *model.AttendanceShift) (int, error) {
	count := 0
	for key := startKey; key <= endKey; key = nextDateKey(key) {
		date, err := NormalizeDate(key)
		if err != nil {
			return count, err
		}
		row, err := s.computeResultRow(ctx, repo, userID, key, ResolveDayType(date, explicit), shift)
		if err != nil {
			return count, err
		}
		if err := repo.UpsertAttendanceResult(ctx, row); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// computeResultRow 派生单条结果行（不改库）：打卡取最早上班/最晚下班，
// 请假天数经 LeaveDaysProvider（#62 接点，未注入为 0）。
func (s *Service) computeResultRow(ctx context.Context, repo *repository.Repository, userID int64, dateKey string, dayType int8, shift *model.AttendanceShift) (*model.AttendanceResult, error) {
	punches, err := repo.ListPunchesByUserDate(ctx, userID, dateKey)
	if err != nil {
		return nil, err
	}
	samples := make([]PunchSample, 0, len(punches))
	for i := range punches {
		samples = append(samples, PunchSample{Type: punches[i].Type, Time: punches[i].PunchTime})
	}
	inIndex, outIndex := SelectFirstInLastOut(samples)

	leaveDays, err := s.leaveDaysOn(ctx, userID, dateKey)
	if err != nil {
		return nil, err
	}
	input := AttendanceDeriveInput{
		DayType:   dayType,
		LeaveDays: leaveDays,
	}
	var inTime, outTime *time.Time
	if inIndex >= 0 {
		minutes := MinutesOfDay(samples[inIndex].Time)
		input.InMinutes = &minutes
		t := samples[inIndex].Time
		inTime = &t
	}
	if outIndex >= 0 {
		minutes := MinutesOfDay(samples[outIndex].Time)
		input.OutMinutes = &minutes
		t := samples[outIndex].Time
		outTime = &t
	}
	if shift != nil {
		input.HasShift = true
		input.ShiftStart = shift.StartTime
		input.ShiftEnd = shift.EndTime
		input.GraceMinutes = shift.GraceMinutes
	}
	derivation := DeriveAttendanceResult(input)

	workDate, err := NormalizeDate(dateKey)
	if err != nil {
		return nil, err
	}
	now := s.businessNow()
	row := &model.AttendanceResult{
		UserID:       userID,
		WorkDate:     workDate,
		InTime:       inTime,
		OutTime:      outTime,
		LateMinutes:  derivation.LateMinutes,
		EarlyMinutes: derivation.EarlyMinutes,
		MissingIn:    derivation.MissingIn,
		MissingOut:   derivation.MissingOut,
		LeaveDays:    leaveDays,
		Status:       derivation.Status,
		ComputedAt:   &now,
	}
	if shift != nil {
		shiftID := shift.ID
		row.ShiftID = &shiftID
	}
	return row, nil
}

// shiftOfUser 解析用户当日适用班次：组员关系 → 考勤组 → 班次；任一缺失
// 返回 nil（不报错：未入组用户仍可派生缺卡/休息结果）。
func (s *Service) shiftOfUser(ctx context.Context, repo *repository.Repository, userID int64) (*model.AttendanceShift, error) {
	membership, err := repo.FindGroupUserByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if membership == nil {
		return nil, nil
	}
	group, err := repo.FindGroupByID(ctx, membership.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, nil
	}
	return repo.FindShiftByID(ctx, group.ShiftID)
}

// leaveDaysOn 取当日请假天数（#62 接缝）；未注入/非法值按 0（无请假）处理。
func (s *Service) leaveDaysOn(ctx context.Context, userID int64, dateKey string) (float64, error) {
	if s.leaveDays == nil {
		return 0, nil
	}
	days, err := s.leaveDays.LeaveDaysOn(ctx, userID, dateKey)
	if err != nil {
		return 0, err
	}
	if days <= 0 {
		return 0, nil
	}
	if days > 1 {
		days = 1 // 一人一天最多 1 天（结果列语义 0/0.5/1）
	}
	return days, nil
}

// loadExplicitDays 读区间内的工作日历显式记录（一次查询）。
func (s *Service) loadExplicitDays(ctx context.Context, startKey, endKey string) ExplicitDays {
	rows, err := s.repo.ListWorkdaysByRange(ctx, startKey, endKey)
	if err != nil {
		// 日历读取失败不应让打卡/每日任务崩掉：按默认规则（周一至周五）派生，
		// 下次重算会修正。错误已在仓储层包装，这里只记录。
		slog.Warn("read workday calendar failed, fallback to default rules", "err", err, "start", startKey, "end", endKey)
		return nil
	}
	return explicitDaysOf(rows)
}

// nicknamesOf 批量回填用户昵称（管理员列表展示；失败不阻塞列表）。
func (s *Service) nicknamesOf(ctx context.Context, rows []model.AttendanceResult) (map[int64]string, error) {
	ids := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for i := range rows {
		if _, ok := seen[rows[i].UserID]; ok {
			continue
		}
		seen[rows[i].UserID] = struct{}{}
		ids = append(ids, rows[i].UserID)
	}
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	return s.sys.UserNicknames(ctx, ids)
}

// resultRespList 把结果行转响应并回填昵称（nicknames 为 nil 时不回填）。
func resultRespList(rows []model.AttendanceResult, nicknames map[int64]string) []dto.AttendanceResultResp {
	list := make([]dto.AttendanceResultResp, 0, len(rows))
	for i := range rows {
		resp := dto.NewAttendanceResultResp(&rows[i])
		if nicknames != nil {
			resp.Nickname = nicknames[rows[i].UserID]
		}
		list = append(list, resp)
	}
	return list
}

// normalizeResultDate 严格校验 YYYY-MM-DD 并归一化为日期键。
func normalizeResultDate(date string) (string, error) {
	trimmed := trimSpace(date)
	if trimmed == "" {
		return "", xerr.BadRequest("请选择日期（YYYY-MM-DD）")
	}
	parsed, err := NormalizeDate(trimmed)
	if err != nil {
		return "", xerr.BadRequest("日期格式应为 YYYY-MM-DD")
	}
	return DateKey(parsed), nil
}

// normalizeResultRange 校验重算日期范围：两端必填、顺序正确、跨度不超过
// MaxAttendanceResultRangeDays 天（含两端）。
func normalizeResultRange(start, end string) (string, string, error) {
	startKey, err := normalizeResultDate(start)
	if err != nil {
		return "", "", err
	}
	endKey, err := normalizeResultDate(end)
	if err != nil {
		return "", "", err
	}
	if startKey > endKey {
		return "", "", xerr.BadRequest("开始日期不能晚于结束日期")
	}
	startDate, _ := NormalizeDate(startKey)
	endDate, _ := NormalizeDate(endKey)
	if span := int(endDate.Sub(startDate).Hours()/24) + 1; span > MaxAttendanceResultRangeDays {
		return "", "", xerr.BadRequest(fmt.Sprintf("单次重算不能超过 %d 天", MaxAttendanceResultRangeDays))
	}
	return startKey, endKey, nil
}

// validateResultDateBounds 校验列表的日期边界（可只填一端）：非法格式 400，
// 两端都填时 start 不能晚于 end。
func validateResultDateBounds(start, end string) error {
	startKey, endKey := trimSpace(start), trimSpace(end)
	if startKey != "" {
		parsed, err := NormalizeDate(startKey)
		if err != nil {
			return xerr.BadRequest("开始日期格式应为 YYYY-MM-DD")
		}
		startKey = DateKey(parsed)
	}
	if endKey != "" {
		parsed, err := NormalizeDate(endKey)
		if err != nil {
			return xerr.BadRequest("结束日期格式应为 YYYY-MM-DD")
		}
		endKey = DateKey(parsed)
	}
	if startKey != "" && endKey != "" && startKey > endKey {
		return xerr.BadRequest("开始日期不能晚于结束日期")
	}
	return nil
}
