// attendance_summary.go 承载考勤月度汇总的取数编排（issue #62，
// docs/oa-mvp-spec.md §4.10「月度汇总：按用户/月聚合出勤、正常、迟到、早退、
// 缺卡、请假天数；部门负责人看部门子树，HR/管理员看全租户」）。
//
// 口径与边界：
//   - 聚合是纯函数 AggregateAttendanceMonth*（leave_attendance_rule.go）：
//     只数结果行覆盖的日子，生效状态（override 优先）与每日结果逐项一致；
//   - 可见范围复用 VisibilityScopeOf("attendance")：oa:attendance:manage →
//     全租户、部门负责人 → 子树 ∪ 本人、否则仅本人；范围外/跨租户一律 404
//     或空页，不泄露存在性；
//   - month 缺省取业务时区（app.timezone）当月；按**用户**分页（每行一个用户）。
package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// normalizeSummaryMonth 严格归一化汇总月份（YYYY-MM；空 → 业务时区当月），
// 返回 (monthKey, startKey, endDay)。
func (s *Service) normalizeSummaryMonth(month string, now time.Time) (string, string, time.Time, error) {
	trimmed := trimSpace(month)
	if trimmed == "" {
		trimmed = now.Format("2006-01")
	}
	parsed, err := NormalizeMonth(trimmed)
	if err != nil {
		return "", "", time.Time{}, xerr.BadRequest("月份格式应为 YYYY-MM")
	}
	monthKey := parsed.Format("2006-01")
	startKey := monthKey + "-01"
	// 下月零日 = 本月最后一天（跨年由 time.Date 归一化）。
	last := time.Date(parsed.Year(), parsed.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return monthKey, startKey, last, nil
}

// PageAttendanceSummaries 管理端月度汇总分页（spec §4.10）：
//   - userId 指定且越出可见范围 → 空页（不泄露存在性）；
//   - 未指定 userId：全租户（manage）不加过滤，否则按可见用户名集合过滤；
//   - 按用户 ID 升序分页（总数 = 有结果行的用户数），昵称经 system 网关回填。
func (s *Service) PageAttendanceSummaries(ctx context.Context, req *dto.AttendanceSummaryPageReq) ([]dto.AttendanceSummaryResp, int64, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	monthKey, startKey, end, err := s.normalizeSummaryMonth(req.Month, s.businessNow())
	if err != nil {
		return nil, 0, err
	}
	scope, err := s.VisibilityScopeOf(ctx, "attendance")
	if err != nil {
		return nil, 0, err
	}
	filter := repository.AttendanceResultFilter{StartDate: startKey, EndDate: DateKey(end)}
	if req.UserID > 0 {
		if !scope.Permits(req.UserID) {
			return []dto.AttendanceSummaryResp{}, 0, nil
		}
		filter.UserID = req.UserID
	} else if !scope.All {
		filter.UserIDs = scope.UserIDs
		if filter.UserIDs == nil {
			filter.UserIDs = []int64{}
		}
	}

	rows, err := s.repo.ListAttendanceResultsInRange(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	summaries := AggregateAttendanceMonthByUser(monthKey, rows)
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	total := int64(len(summaries))
	offset := page.Offset()
	if offset >= len(summaries) {
		return []dto.AttendanceSummaryResp{}, total, nil
	}
	endIndex := offset + page.PageSize
	if endIndex > len(summaries) {
		endIndex = len(summaries)
	}
	list := summaries[offset:endIndex]
	nicknames, err := s.nicknamesOfSummaries(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	return summaryRespList(list, nicknames), total, nil
}

// GetAttendanceSummaryOfUser 读取指定用户在可见范围内的月度汇总：范围外/
// 跨租户/不存在一律 404（不泄露存在性）。
func (s *Service) GetAttendanceSummaryOfUser(ctx context.Context, req *dto.AttendanceSummaryOfUserReq) (*dto.AttendanceSummaryResp, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, xerr.Unauthorized("")
	}
	if req.UserID <= 0 {
		return nil, xerr.BadRequest("请选择要查询的用户")
	}
	monthKey, startKey, end, err := s.normalizeSummaryMonth(req.Month, s.businessNow())
	if err != nil {
		return nil, err
	}
	scope, err := s.VisibilityScopeOf(ctx, "attendance")
	if err != nil {
		return nil, err
	}
	if !scope.Permits(req.UserID) {
		return nil, xerr.NotFound("考勤汇总不存在")
	}
	// 用户必须存在于当前租户（跨租户/已删 404，不泄露存在性）：昵称映射只含
	// 本租户用户，缺席即不存在——即便调用方持全租户权限也不会暴露他租户用户。
	nicknames, err := s.sys.UserNicknames(ctx, []int64{req.UserID})
	if err != nil {
		return nil, err
	}
	if _, ok := nicknames[req.UserID]; !ok {
		return nil, xerr.NotFound("考勤汇总不存在")
	}
	summary, err := s.attendanceSummaryOf(ctx, req.UserID, monthKey, startKey, DateKey(end))
	if err != nil {
		return nil, err
	}
	resp := summaryRespOf(*summary)
	resp.Nickname = nicknames[req.UserID]
	return &resp, nil
}

// GetMyAttendanceSummary 员工本人月度汇总（强制本人，登录即可）：
// 「我的考勤」页的「本月汇总」卡片数据源，month 缺省取业务时区当月。
func (s *Service) GetMyAttendanceSummary(ctx context.Context, req *dto.MyAttendanceSummaryReq) (*dto.AttendanceSummaryResp, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	monthKey, startKey, end, err := s.normalizeSummaryMonth(req.Month, s.businessNow())
	if err != nil {
		return nil, err
	}
	summary, err := s.attendanceSummaryOf(ctx, userID, monthKey, startKey, DateKey(end))
	if err != nil {
		return nil, err
	}
	resp := summaryRespOf(*summary)
	return &resp, nil
}

// attendanceSummaryOf 读某用户区间内结果行并聚合（不做可见范围判定，
// 调用方已完成授权）。
func (s *Service) attendanceSummaryOf(ctx context.Context, userID int64, monthKey, startKey, endKey string) (*AttendanceSummary, error) {
	rows, err := s.repo.ListAttendanceResultsInRange(ctx, repository.AttendanceResultFilter{
		UserID: userID, StartDate: startKey, EndDate: endKey,
	})
	if err != nil {
		return nil, err
	}
	summary := AggregateAttendanceMonth(monthKey, userID, rows)
	return &summary, nil
}

// nicknamesOfSummaries 批量回填汇总行昵称（管理端展示；失败不阻塞聚合）。
func (s *Service) nicknamesOfSummaries(ctx context.Context, summaries []AttendanceSummary) (map[int64]string, error) {
	ids := make([]int64, 0, len(summaries))
	for i := range summaries {
		ids = append(ids, summaries[i].UserID)
	}
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	return s.sys.UserNicknames(ctx, ids)
}

// summaryRespList 把聚合结果转响应并回填昵称。
func summaryRespList(summaries []AttendanceSummary, nicknames map[int64]string) []dto.AttendanceSummaryResp {
	list := make([]dto.AttendanceSummaryResp, 0, len(summaries))
	for i := range summaries {
		resp := summaryRespOf(summaries[i])
		if nicknames != nil {
			resp.Nickname = nicknames[summaries[i].UserID]
		}
		list = append(list, resp)
	}
	return list
}

// summaryRespOf 单行聚合结果 → 响应。
func summaryRespOf(summary AttendanceSummary) dto.AttendanceSummaryResp {
	return dto.AttendanceSummaryResp{
		UserID:         summary.UserID,
		Month:          summary.Month,
		Days:           summary.Days,
		WorkDays:       summary.WorkDays,
		AttendanceDays: summary.AttendanceDays,
		Normal:         summary.Normal,
		Late:           summary.Late,
		Early:          summary.Early,
		LateAndEarly:   summary.LateAndEarly,
		Missing:        summary.Missing,
		Rest:           summary.Rest,
		Leave:          summary.Leave,
		LeaveDays:      summary.LeaveDays,
		LateMinutes:    summary.LateMinutes,
		EarlyMinutes:   summary.EarlyMinutes,
	}
}
