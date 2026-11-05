// workday.go 承载工作日历的管理编排（issue #50，docs/oa-mvp-spec.md §3.3/
// §5 OA 考勤节）：按月查看、单日设置、批量设置、清除恢复默认。
// 判定语义全部在纯函数（workday_rule.go），本文件只做参数校验、取数与落库
// 编排——请假 #52 / 考勤 #61 直接复用纯函数，不经本层 HTTP。
package service

import (
	"context"
	"sort"
	"time"

	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 工作日历维护的取值边界：备注长度、批量日期条数、批量区间跨度（含两端）。
const (
	MaxWorkdayRemarkLen = 200
	MaxBatchDates       = 400
	MaxBatchRangeDays   = 400
)

// GetWorkdayMonth 按月查看：整月逐日合成（显式记录优先、无记录走默认规则），
// 附显式记录 ID 与备注。month 为 YYYY-MM。
func (s *Service) GetWorkdayMonth(ctx context.Context, month string) (*dto.WorkdayMonthResp, error) {
	firstDay, err := NormalizeMonth(month)
	if err != nil {
		return nil, xerr.BadRequest("月份格式应为 YYYY-MM")
	}
	// 月末：下月 1 日前一天（UTC，无时区偏移风险）。
	lastDay := firstDay.AddDate(0, 1, -1)

	rows, err := s.repo.ListWorkdaysByRange(ctx, firstDay.Format(DateLayout), lastDay.Format(DateLayout))
	if err != nil {
		return nil, err
	}
	explicit := explicitDaysOf(rows)
	days := ResolveRange(firstDay, lastDay, explicit)

	resp := &dto.WorkdayMonthResp{
		Month: month,
		Days:  make([]dto.WorkdayDay, 0, len(days)),
	}
	for _, day := range days {
		key := DateKey(day.Date)
		item := dto.WorkdayDay{
			Date:     key,
			Weekday:  int(day.Weekday),
			Type:     day.Type,
			Explicit: day.Explicit,
		}
		if day.Explicit {
			if row := rowsByKey(rows)[key]; row != nil {
				item.ID = row.ID
				item.Remark = row.Remark
			}
		}
		resp.Days = append(resp.Days, item)
	}
	return resp, nil
}

// SetWorkday 单日设置（upsert）：显式记录存在即覆盖类型与备注（含复活软删行）。
func (s *Service) SetWorkday(ctx context.Context, req *dto.WorkdaySetReq) error {
	date, err := validateWorkdayInput(req.Date, req.Type, req.Remark)
	if err != nil {
		return err
	}
	return s.repo.UpsertWorkdays(ctx, []model.WorkdayCalendar{{
		Date:   date,
		Type:   req.Type,
		Remark: req.Remark,
	}})
}

// BatchWorkday 批量设置（upsert）：显式日期列表或 [startDate, endDate] 区间
// （可选 weekdays 星期过滤，0=周日 … 6=周六，与 time.Weekday 对齐）。
// 返回实际写入的日期数（同批去重后）。
func (s *Service) BatchWorkday(ctx context.Context, req *dto.WorkdayBatchReq) (int, error) {
	if err := validateBatchTypeAndRemark(req.Type, req.Remark); err != nil {
		return 0, err
	}
	for _, wd := range req.Weekdays {
		if wd < 0 || wd > 6 {
			return 0, xerr.BadRequest("weekdays 取值应为 0（周日）到 6（周六）")
		}
	}

	dates, err := expandBatchDates(req)
	if err != nil {
		return 0, err
	}

	rows := make([]model.WorkdayCalendar, 0, len(dates))
	for _, key := range dates {
		date, _ := NormalizeDate(key) // expandBatchDates 产出的键恒合法
		rows = append(rows, model.WorkdayCalendar{Date: date, Type: req.Type, Remark: req.Remark})
	}
	if err := s.repo.UpsertWorkdays(ctx, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// ClearWorkday 清除单日显式记录（恢复默认规则）；无记录返回 404。
func (s *Service) ClearWorkday(ctx context.Context, date string) error {
	dateTime, err := NormalizeDate(date)
	if err != nil {
		return xerr.BadRequest("日期格式应为 YYYY-MM-DD")
	}
	deleted, err := s.repo.DeleteWorkdayByDate(ctx, dateTime.Format(DateLayout))
	if err != nil {
		return err
	}
	if !deleted {
		return xerr.NotFound("该日期没有显式记录")
	}
	return nil
}

// expandBatchDates 把批量请求展开为去重升序的日期键列表：
// dates 优先（非空即用）；否则按 [startDate, endDate] 展开（weekdays 过滤）。
func expandBatchDates(req *dto.WorkdayBatchReq) ([]string, error) {
	switch {
	case len(req.Dates) > 0:
		if len(req.Dates) > MaxBatchDates {
			return nil, xerr.BadRequest(fmt.Sprintf("单次批量设置不能超过 %d 个日期", MaxBatchDates))
		}
		seen := make(map[string]struct{}, len(req.Dates))
		dates := make([]string, 0, len(req.Dates))
		for _, raw := range req.Dates {
			date, err := NormalizeDate(raw)
			if err != nil {
				return nil, xerr.BadRequest("日期格式应为 YYYY-MM-DD")
			}
			key := date.Format(DateLayout)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			dates = append(dates, key)
		}
		sort.Strings(dates)
		return dates, nil

	case req.StartDate != "" || req.EndDate != "":
		start, err := NormalizeDate(req.StartDate)
		if err != nil {
			return nil, xerr.BadRequest("startDate 格式应为 YYYY-MM-DD")
		}
		end, err := NormalizeDate(req.EndDate)
		if err != nil {
			return nil, xerr.BadRequest("endDate 格式应为 YYYY-MM-DD")
		}
		if start.After(end) {
			return nil, xerr.BadRequest("startDate 不能晚于 endDate")
		}
		span := int(end.Sub(start).Hours()/24) + 1
		if span > MaxBatchRangeDays {
			return nil, xerr.BadRequest(fmt.Sprintf("批量区间跨度不能超过 %d 天", MaxBatchRangeDays))
		}
		weekdaySet := make(map[int]struct{}, len(req.Weekdays))
		for _, wd := range req.Weekdays {
			weekdaySet[wd] = struct{}{}
		}
		dates := make([]string, 0, span)
		for i := 0; i < span; i++ {
			cursor := start.AddDate(0, 0, i)
			if len(weekdaySet) > 0 {
				if _, ok := weekdaySet[int(cursor.Weekday())]; !ok {
					continue
				}
			}
			dates = append(dates, cursor.Format(DateLayout))
		}
		return dates, nil

	default:
		return nil, xerr.BadRequest("请提供 dates 日期列表，或 startDate/endDate 区间")
	}
}

// validateWorkdayInput 校验单日设置的日期/类型/备注，返回归一化日期。
func validateWorkdayInput(date string, typ int8, remark string) (time.Time, error) {
	dateTime, err := NormalizeDate(date)
	if err != nil {
		return time.Time{}, xerr.BadRequest("日期格式应为 YYYY-MM-DD")
	}
	if err := validateWorkdayTypeAndRemark(typ, remark); err != nil {
		return time.Time{}, err
	}
	return dateTime, nil
}

func validateWorkdayTypeAndRemark(typ int8, remark string) error {
	if !ValidWorkdayType(typ) {
		return xerr.BadRequest("日类型应为 0 工作日 / 1 休息日 / 2 节假日")
	}
	if len([]rune(remark)) > MaxWorkdayRemarkLen {
		return xerr.BadRequest(fmt.Sprintf("备注不能超过 %d 字", MaxWorkdayRemarkLen))
	}
	return nil
}

func validateBatchTypeAndRemark(typ int8, remark string) error {
	return validateWorkdayTypeAndRemark(typ, remark)
}

// explicitDaysOf 把显式记录行转成纯函数入参集合。
func explicitDaysOf(rows []model.WorkdayCalendar) ExplicitDays {
	if len(rows) == 0 {
		return nil
	}
	out := make(ExplicitDays, len(rows))
	for _, row := range rows {
		out[DateKey(row.Date)] = row.Type
	}
	return out
}

// rowsByKey 显式记录行按日期键索引（月视图回填 ID/备注用）。
func rowsByKey(rows []model.WorkdayCalendar) map[string]*model.WorkdayCalendar {
	out := make(map[string]*model.WorkdayCalendar, len(rows))
	for i := range rows {
		out[DateKey(rows[i].Date)] = &rows[i]
	}
	return out
}
