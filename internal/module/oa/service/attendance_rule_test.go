// attendance_rule_test.go 锁定考勤基础的字段校验规则（issue #60，spec §4.10）：
// 班次 time-of-day 严格格式（HH:MM，必须前导零、24 小时制）、不支持跨天
// （end > start）、宽限分钟边界、实体名称长度、打卡类型/来源与启用状态枚举。
// 全部为纯函数表驱动用例（无存储访问）。
package service

import (
	"strings"
	"testing"
)

func TestNormalizeTimeOfDay(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"零点", "00:00", "00:00", false},
		{"九点整", "09:00", "09:00", false},
		{"二十三点五十九", "23:59", "23:59", false},
		{"两位小时无前导零应拒绝", "9:00", "", true},
		{"小时越界", "24:00", "", true},
		{"小时负数", "-1:30", "", true},
		{"分钟越界", "09:60", "", true},
		{"缺分钟", "09", "", true},
		{"多段", "09:00:00", "", true},
		{"中文冒号", "09：00", "", true},
		{"空串", "", "", true},
		{"字母", "ab:cd", "", true},
		{"带空格", " 09:00", "", true},
		{"带尾随空格", "09:00 ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeTimeOfDay(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeTimeOfDay(%q) 应报错，实际得到 %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeTimeOfDay(%q) 意外报错: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeTimeOfDay(%q) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateShiftWindow(t *testing.T) {
	cases := []struct {
		name    string
		start   string
		end     string
		wantErr bool
	}{
		{"正常时段", "09:00", "18:00", false},
		{"早班", "06:00", "14:30", false},
		{"下班必须晚于上班", "09:00", "09:00", true},
		{"下班早于上班不支持跨天", "18:00", "09:00", true},
		{"零点到零点视为相等拒绝", "00:00", "00:00", true},
		{"夜班跨天拒绝", "22:00", "06:00", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateShiftWindow(tc.start, tc.end)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateShiftWindow(%q,%q) 应报错", tc.start, tc.end)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateShiftWindow(%q,%q) 意外报错: %v", tc.start, tc.end, err)
			}
		})
	}
}

func TestValidateGraceMinutes(t *testing.T) {
	cases := []struct {
		name    string
		in      int
		wantErr bool
	}{
		{"零宽限", 0, false},
		{"十分钟", 10, false},
		{"上限一天", MaxGraceMinutes, false},
		{"负数", -1, true},
		{"超上限", MaxGraceMinutes + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGraceMinutes(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateGraceMinutes(%d) 应报错", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateGraceMinutes(%d) 意外报错: %v", tc.in, err)
			}
		})
	}
}

func TestValidateAttendanceName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"一个字", "早班", false},
		{"六十四字", strings.Repeat("班", 64), false},
		{"空", "", true},
		{"纯空格", "   ", true},
		{"六十五字", strings.Repeat("班", 65), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateAttendanceName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateAttendanceName(%q) 应报错", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateAttendanceName(%q) 意外报错: %v", tc.in, err)
			}
			if got != tc.in {
				t.Fatalf("ValidateAttendanceName(%q) = %q，应原样返回修剪后的名称", tc.in, got)
			}
		})
	}
}

func TestValidateAttendanceSort(t *testing.T) {
	cases := []struct {
		name    string
		in      int
		wantErr bool
	}{
		{"零", 0, false},
		{"上限", MaxAttendanceSort, false},
		{"负数", -1, true},
		{"超上限", MaxAttendanceSort + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAttendanceSort(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateAttendanceSort(%d) 应报错", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateAttendanceSort(%d) 意外报错: %v", tc.in, err)
			}
		})
	}
}

func TestValidPunchTypeAndName(t *testing.T) {
	if !ValidPunchType(0) || !ValidPunchType(1) {
		t.Fatal("0/1 应为合法打卡类型")
	}
	if ValidPunchType(2) || ValidPunchType(-1) {
		t.Fatal("2/-1 应为非法打卡类型")
	}
	if got := PunchTypeName(0); got != "上班" {
		t.Fatalf("PunchTypeName(0) = %q，期望 上班", got)
	}
	if got := PunchTypeName(1); got != "下班" {
		t.Fatalf("PunchTypeName(1) = %q，期望 下班", got)
	}
	if got := PunchSourceName(0); got != "Web" {
		t.Fatalf("PunchSourceName(0) = %q，期望 Web", got)
	}
}

func TestValidAttendanceStatus(t *testing.T) {
	if !ValidAttendanceStatus(0) || !ValidAttendanceStatus(1) {
		t.Fatal("0/1 应为合法启用状态")
	}
	if ValidAttendanceStatus(2) || ValidAttendanceStatus(-1) {
		t.Fatal("2/-1 应为非法启用状态")
	}
	if got := AttendanceStatusNameOf(0); got != "启用" {
		t.Fatalf("AttendanceStatusNameOf(0) = %q，期望 启用", got)
	}
	if got := AttendanceStatusNameOf(1); got != "停用" {
		t.Fatalf("AttendanceStatusNameOf(1) = %q，期望 停用", got)
	}
}
