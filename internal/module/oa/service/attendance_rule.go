// attendance_rule.go 承载考勤基础的字段校验与枚举规则（issue #60，
// docs/oa-mvp-spec.md §3.9/§4.10）。全部为**纯函数**（无存储访问、无 HTTP
// 上下文），供 service 编排层与单测直接复用：
//   - 班次时间为 time-of-day「HH:MM」（24 小时制、必须前导零），不支持跨天
//     ——end 必须晚于 start，夜班（跨天）在 v1 明确拒绝；
//   - 宽限分钟 0–1440；名称 1–64 字；排序 0–9999；
//   - 打卡类型 0 上班/1 下班，来源 0 Web；启用状态 0 启用/1 停用。
package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 考勤字段的取值边界：宽限分钟上限（一天）、名称长度上限、排序上限。
const (
	MaxGraceMinutes    = 1440
	MaxAttendanceName  = 64
	MaxAttendanceSort  = 9999
	MaxAttendanceNote  = 200
	MaxUserAgentLength = 512
)

// timeOfDayPattern 是严格的 HH:MM（24 小时制、前导零必须）。
var timeOfDayPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// NormalizeTimeOfDay 严格校验 time-of-day 输入并原样返回（HH:MM）。
// 非法输入（缺前导零、越界、带秒、带空白等）返回可读错误。
func NormalizeTimeOfDay(s string) (string, error) {
	if !timeOfDayPattern.MatchString(s) {
		return "", fmt.Errorf("时间格式应为 HH:MM（24 小时制，如 09:30）：%q", s)
	}
	return s, nil
}

// ValidateShiftWindow 校验班次时段（spec §4.10：一天一个时段，end > start，
// 不支持跨天）。start/end 必须已通过 NormalizeTimeOfDay。
func ValidateShiftWindow(start, end string) error {
	if end <= start {
		return xerr.BadRequest("下班时间必须晚于上班时间（不支持跨天班次）")
	}
	return nil
}

// ValidateGraceMinutes 校验宽限分钟（0–MaxGraceMinutes）。
func ValidateGraceMinutes(n int) error {
	if n < 0 || n > MaxGraceMinutes {
		return xerr.BadRequest(fmt.Sprintf("宽限分钟应为 0 到 %d 之间的整数", MaxGraceMinutes))
	}
	return nil
}

// ValidateAttendanceName 校验班次/考勤组名称（去首尾空白后 1–64 字），
// 返回修剪后的名称。
func ValidateAttendanceName(name string) (string, error) {
	trimmed := trimSpace(name)
	if trimmed == "" {
		return "", xerr.BadRequest("名称不能为空")
	}
	if n := len([]rune(trimmed)); n > MaxAttendanceName {
		return "", xerr.BadRequest(fmt.Sprintf("名称不能超过 %d 字", MaxAttendanceName))
	}
	return trimmed, nil
}

// ValidateAttendanceSort 校验展示排序（0–9999）。
func ValidateAttendanceSort(sort int) error {
	if sort < 0 || sort > MaxAttendanceSort {
		return xerr.BadRequest(fmt.Sprintf("排序应为 0 到 %d 之间的整数", MaxAttendanceSort))
	}
	return nil
}

// ValidPunchType 报告打卡类型取值是否合法（0 上班 / 1 下班）。
func ValidPunchType(t int8) bool { return t == model.PunchTypeIn || t == model.PunchTypeOut }

// PunchTypeName 返回打卡类型中文名（转发 model，service 内统一入口）。
func PunchTypeName(t int8) string { return model.PunchTypeName(t) }

// PunchSourceName 返回打卡来源中文名。
func PunchSourceName(s int8) string { return model.PunchSourceName(s) }

// ValidAttendanceStatus 报告启用状态取值是否合法（0 启用 / 1 停用）。
func ValidAttendanceStatus(s int8) bool {
	return s == model.AttendanceStatusEnabled || s == model.AttendanceStatusDisabled
}

// AttendanceStatusNameOf 返回启用状态中文名。
func AttendanceStatusNameOf(s int8) string { return model.AttendanceStatusName(s) }

// trimSpace 去除首尾空白。
func trimSpace(s string) string { return strings.TrimSpace(s) }
