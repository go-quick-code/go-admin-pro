// attendance_result_rule.go 承载考勤结果派生的**纯函数**（issue #61，
// docs/oa-mvp-spec.md §4.10）。无存储访问、无 HTTP 上下文，供 service 编排与
// 表驱动单测直接复用：
//
//   - 派生优先级：非工作日 → 休息；整天请假（leave_days >= 1）→ 请假；
//     缺上班卡或缺下班卡 → 缺卡；迟到且早退 → 迟到且早退；仅迟到 → 迟到；
//     仅早退 → 早退；否则正常；
//   - 迟到/早退分钟 = 超出宽限的部分（宽限内为 0，恰好落在宽限上界不算）；
//   - 同一天多次打卡：上班取最早、下班取最晚；
//   - 时间比较一律用业务时区墙钟（MinutesOfDay / time-of-day），与数据库
//     读回时区（loc）无关——班次是 time-of-day，不支持跨天。
package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// MaxAttendanceResultRangeDays 是单次重算/查询允许的最大日期跨度（含两端）：
// 手工重算与每日任务都按范围执行，超过一年的请求按 400 拒绝，避免误操作
// 触发全量重算。
const MaxAttendanceResultRangeDays = 366

// MinutesOfDay 返回时间的**墙钟**分钟数（0–1439）：只看时分、不看时区，
// 因为班次是 time-of-day，打卡时间落库为墙钟，读回时区（loc）不影响比较。
func MinutesOfDay(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}

// TimeOfDayMinutes 把严格 HH:MM 解析为当日分钟数；非法输入返回 -1
// （格式规则与班次校验共用 NormalizeTimeOfDay 的正则）。
func TimeOfDayMinutes(s string) int {
	if !timeOfDayPattern.MatchString(s) {
		return -1
	}
	hour := int(s[0]-'0')*10 + int(s[1]-'0')
	minute := int(s[3]-'0')*10 + int(s[4]-'0')
	return hour*60 + minute
}

// AttendanceDeriveInput 是一次结果派生的全部输入（存储无关）：
// 日期类型来自工作日历（#50 ResolveDayType），班次来自考勤组绑定，
// LeaveDays 由请假联动提供（#62 接点，本工单恒 0）。InMinutes/OutMinutes
// 是当日最早上班/最晚下班的墙钟分钟数，nil 表示该类型无打卡。
type AttendanceDeriveInput struct {
	// DayType 工作日/休息日/节假日（#50 WorkdayType*）。
	DayType int8
	// LeaveDays 当日请假天数：0 无、0.5 半天（不改变状态）、>=1 整天。
	LeaveDays float64
	// HasShift 当日是否解析到班次（未入组/组未绑班次为 false）。
	HasShift bool
	// ShiftStart/ShiftEnd 班次时段 HH:MM；GraceMinutes 迟到/早退宽限分钟。
	ShiftStart   string
	ShiftEnd     string
	GraceMinutes int
	// InMinutes 最早上班打卡（墙钟分钟数），nil = 无上班卡。
	InMinutes *int
	// OutMinutes 最晚下班打卡（墙钟分钟数），nil = 无下班卡。
	OutMinutes *int
}

// AttendanceDerivation 是派生结论（与 result 表列一一对应）。
type AttendanceDerivation struct {
	Status       int8
	LateMinutes  int
	EarlyMinutes int
	MissingIn    int8
	MissingOut   int8
}

// DeriveAttendanceResult 按 spec §4.10 的优先级派生当日考勤结果（纯函数）。
// 非工作日只看休息——有打卡也不算出勤、不判迟到早退；整天请假优先于缺卡
// （请假当天不再计缺卡），半天请假不改变状态只由结果行记录 leave_days。
func DeriveAttendanceResult(in AttendanceDeriveInput) AttendanceDerivation {
	// 1. 非工作日 → 休息（含节假日；调休上班由日历显式标工作日，不会走到这里）。
	if in.DayType != WorkdayTypeWorking {
		return AttendanceDerivation{Status: model.AttendanceResultRest}
	}

	out := AttendanceDerivation{Status: model.AttendanceResultNormal}
	if in.InMinutes == nil {
		out.MissingIn = 1
	}
	if in.OutMinutes == nil {
		out.MissingOut = 1
	}

	// 2. 整天请假 → 请假：优先于缺卡判定（spec：leave_days >= 1 判请假）。
	if in.LeaveDays >= 1 {
		return AttendanceDerivation{Status: model.AttendanceResultLeave}
	}

	// 3. 缺上班卡或缺下班卡 → 缺卡（明细留 missing_*，供汇总区分）。
	if out.MissingIn == 1 || out.MissingOut == 1 {
		out.Status = model.AttendanceResultMissing
		return out
	}

	// 无班次无法判定迟到/早退：双卡齐全按正常（v1 允许用户未入组的场景）。
	if !in.HasShift {
		return out
	}
	startMin := TimeOfDayMinutes(in.ShiftStart)
	endMin := TimeOfDayMinutes(in.ShiftEnd)
	if startMin < 0 || endMin < 0 || endMin <= startMin {
		// 脏数据兜底：班次时间非法时不做迟到/早退判定（校验在写入侧）。
		return out
	}

	// 4. 迟到/早退以宽限为上界：恰好等于上界不计，超出部分按分钟记录。
	if late := *in.InMinutes - (startMin + in.GraceMinutes); late > 0 {
		out.LateMinutes = late
	}
	if early := (endMin - in.GraceMinutes) - *in.OutMinutes; early > 0 {
		out.EarlyMinutes = early
	}

	switch {
	case out.LateMinutes > 0 && out.EarlyMinutes > 0:
		out.Status = model.AttendanceResultLateAndEarly
	case out.LateMinutes > 0:
		out.Status = model.AttendanceResultLate
	case out.EarlyMinutes > 0:
		out.Status = model.AttendanceResultEarly
	}
	return out
}

// PunchSample 是一次打卡的派生输入（纯函数入参）：Type 0 上班/1 下班，
// Time 为打卡时刻（取墙钟时分秒参与比较）。
type PunchSample struct {
	Type int8
	Time time.Time
}

// wallClockKeyOf 返回墙钟排序键（同日 HH:MM:SS.mmm）：只比较时分秒，
// 不受读回时区影响；同一秒内按毫秒。
func wallClockKeyOf(t time.Time) string {
	return t.Format("15:04:05.000")
}

// SelectFirstInLastOut 在当日全部打卡中选「最早上班」与「最晚下班」，
// 返回两者在 samples 中的下标（-1 = 该类型无打卡）。同值取首次出现，
// 保证结果确定。排序按墙钟时间，与输入顺序无关。
func SelectFirstInLastOut(samples []PunchSample) (inIndex, outIndex int) {
	inIndex, outIndex = -1, -1
	var inKey, outKey string
	for i, s := range samples {
		key := wallClockKeyOf(s.Time)
		switch s.Type {
		case model.PunchTypeIn:
			if inIndex < 0 || key < inKey {
				inIndex, inKey = i, key
			}
		case model.PunchTypeOut:
			if outIndex < 0 || key >= outKey {
				outIndex, outKey = i, key
			}
		}
	}
	return inIndex, outIndex
}

// ValidateAttendanceOverride 校验管理员校正入参：状态取值合法（0–6）、
// 备注去空白后 1–200 字。返回修剪后的备注。
func ValidateAttendanceOverride(status int8, remark string) (string, error) {
	if !ValidAttendanceResultStatus(status) {
		return "", xerr.BadRequest("校正状态应为 正常/迟到/早退/迟到且早退/缺卡/休息/请假")
	}
	trimmed := strings.TrimSpace(remark)
	if trimmed == "" {
		return "", xerr.BadRequest("请填写校正备注")
	}
	if n := len([]rune(trimmed)); n > MaxAttendanceNote {
		return "", xerr.BadRequest(fmt.Sprintf("校正备注不能超过 %d 字", MaxAttendanceNote))
	}
	return trimmed, nil
}

// ValidAttendanceResultStatus 报告结果状态取值是否合法（派生与校正共用）。
func ValidAttendanceResultStatus(status int8) bool {
	return model.ValidAttendanceResultStatus(status)
}

// AttendanceResultStatusNameOf 返回结果状态中文名（model 转发的统一入口）。
func AttendanceResultStatusNameOf(status int8) string {
	return model.AttendanceResultStatusName(status)
}
