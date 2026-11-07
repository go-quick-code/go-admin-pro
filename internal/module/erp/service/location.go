// location.go 承载 erp service 的业务时区与租户上下文支点（issue #90，
// ADR 0006：业务时区唯一访问点是 config.Location()，由 internal/app 装配时
// 注入；编号日期键、单据业务时间、流水筛选时间一律走这里，不直接用
// time.Now()/time.Local）。
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// SetLocation 注入业务时区（ADR 0006）。nil 忽略，保留当前值。
// 装配点：internal/app（与 crm SetLocation 同构）。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区：app 装配后是配置的 app.timezone；
// 未注入（如直接构造 Service 的测试）时退回 time.Local，与 crm 同口径。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// businessNow 返回业务时区下的「现在」：单号日期键一律走这里，不直接用
// time.Now()（其 Location 是服务器本地时区，跨时区部署会算错日期键）。
func (s *Service) businessNow() time.Time {
	return time.Now().In(s.businessLocation())
}

// erpTenantID 返回 context 中的当前租户 ID（裸 SQL 入账/序列发放的显式租户参数）。
func erpTenantID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.TenantID
	}
	return 0
}

// erpTimeLayouts 是单据业务时间接受的格式：纯日期、紧凑日期、前端 value-format
// （YYYY-MM-DD HH:mm:ss）与 RFC3339（与 crmDateLayouts 同口径）。
var erpTimeLayouts = []string{"2006-01-02", "20060102", "2006-01-02 15:04:05", time.RFC3339}

// parseDocTime 解析单个业务时间（必填场景）：空/非法返回 400 并带回原始输入。
func (s *Service) parseDocTime(value string, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, badDocTime(value, field)
	}
	if t, ok := parseErpTime(value, s.businessLocation()); ok {
		return t, nil
	}
	return time.Time{}, badDocTime(value, field)
}

// parseDocTimeRange 解析筛选时间区间 [start, end]：两端均可空；日期文本按业务
// 时区解释。end 为纯日期（YYYY-MM-DD）时按「含当日」语义外推到次日 0 点
// （区间谓词用 < 排他上界）。非法格式返回 400。
func (s *Service) parseDocTimeRange(start, end string) (*time.Time, *time.Time, error) {
	loc := s.businessLocation()
	var startTime, endTime *time.Time
	if v := strings.TrimSpace(start); v != "" {
		t, ok := parseErpTime(v, loc)
		if !ok {
			return nil, nil, badDocTime(v, "开始时间")
		}
		startTime = &t
	}
	if v := strings.TrimSpace(end); v != "" {
		t, ok := parseErpTime(v, loc)
		if !ok {
			return nil, nil, badDocTime(v, "结束时间")
		}
		if len(v) == 10 && !strings.HasSuffix(v, "Z") {
			// 纯日期：含当日（上界外推到次日 0 点，配合 < 排他谓词）。
			t = t.AddDate(0, 0, 1)
		}
		endTime = &t
	}
	return startTime, endTime, nil
}

// parseErpTime 按 layouts 依次用 loc 解析；loc 为 nil 时退回 time.Local
// （只用于未装配时区的测试场景）。
func parseErpTime(value string, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.Local
	}
	for _, layout := range erpTimeLayouts {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// badDocTime 生成业务时间的 400 错误（带原始输入，便于定位）。
func badDocTime(value, field string) error {
	return xerr.BadRequest(fmt.Sprintf("%s %q 格式不正确：应为 2026-01-02 或 2026-01-02 15:04:05 形式", field, value))
}
