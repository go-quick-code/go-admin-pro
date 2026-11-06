// room_rule_test.go 是会议室/预约纯规则的表驱动测试（issue #57）：
// 重叠判定（相邻不算冲突、包含、交叉、相同）、删除约束（活动且未来才
// 阻塞）、提醒窗口边界（15 分钟整数点、超过 1 秒、已提醒/已取消不发）、
// 字段校验。
package service

import (
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

func TestRoomBookingsOverlap(t *testing.T) {
	at := func(hhmm string) time.Time {
		t.Helper()
		parsed, err := time.ParseInLocation("2006-01-02 15:04", "2030-01-10 "+hhmm, time.Local)
		if err != nil {
			t.Fatalf("解析时间 %s: %v", hhmm, err)
		}
		return parsed
	}
	cases := []struct {
		name         string
		aStart, aEnd string
		bStart, bEnd string
		wantOverlap  bool
	}{
		{name: "相邻不冲突", aStart: "09:00", aEnd: "10:00", bStart: "10:00", bEnd: "11:00", wantOverlap: false},
		{name: "相邻反向不冲突", aStart: "10:00", aEnd: "11:00", bStart: "09:00", bEnd: "10:00", wantOverlap: false},
		{name: "完全分离", aStart: "09:00", aEnd: "10:00", bStart: "11:00", bEnd: "12:00", wantOverlap: false},
		{name: "完全相同", aStart: "09:00", aEnd: "10:00", bStart: "09:00", bEnd: "10:00", wantOverlap: true},
		{name: "交叉", aStart: "09:00", aEnd: "10:30", bStart: "10:00", bEnd: "11:00", wantOverlap: true},
		{name: "包含", aStart: "08:00", aEnd: "12:00", bStart: "09:00", bEnd: "10:00", wantOverlap: true},
		{name: "被包含", aStart: "09:30", aEnd: "09:45", bStart: "09:00", bEnd: "10:00", wantOverlap: true},
		{name: "首端交叉", aStart: "08:00", aEnd: "09:30", bStart: "09:00", bEnd: "10:00", wantOverlap: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RoomBookingsOverlap(at(tc.aStart), at(tc.aEnd), at(tc.bStart), at(tc.bEnd))
			if got != tc.wantOverlap {
				t.Fatalf("RoomBookingsOverlap(%s-%s, %s-%s) = %v，期望 %v",
					tc.aStart, tc.aEnd, tc.bStart, tc.bEnd, got, tc.wantOverlap)
			}
		})
	}
}

func TestRoomDeleteBlockedByBooking(t *testing.T) {
	now := time.Date(2030, 1, 10, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name   string
		status int8
		start  time.Time
		want   bool
	}{
		{name: "有效未来预约阻塞删除", status: model.RoomBookingStatusActive, start: now.Add(time.Hour), want: true},
		{name: "有效已开始预约不阻塞", status: model.RoomBookingStatusActive, start: now, want: false},
		{name: "有效历史预约不阻塞", status: model.RoomBookingStatusActive, start: now.Add(-time.Hour), want: false},
		{name: "已取消未来预约不阻塞", status: model.RoomBookingStatusCancelled, start: now.Add(time.Hour), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoomDeleteBlockedByBooking(tc.status, tc.start, now); got != tc.want {
				t.Fatalf("RoomDeleteBlockedByBooking(status=%d, start=%s) = %v，期望 %v",
					tc.status, tc.start, got, tc.want)
			}
		})
	}
}

func TestRoomReminderDue(t *testing.T) {
	now := time.Date(2030, 1, 10, 9, 0, 0, 0, time.Local)
	reminded := now.Add(-time.Minute)
	cases := []struct {
		name       string
		status     int8
		start      time.Time
		remindedAt *time.Time
		want       bool
	}{
		{name: "15 分钟整点进入提醒", status: model.RoomBookingStatusActive, start: now.Add(15 * time.Minute), want: true},
		{name: "窗口内（5 分钟后）进入提醒", status: model.RoomBookingStatusActive, start: now.Add(5 * time.Minute), want: true},
		{name: "超过 15 分钟 1 秒不发", status: model.RoomBookingStatusActive, start: now.Add(15*time.Minute + time.Second), want: false},
		{name: "恰好现在（已开始）不发", status: model.RoomBookingStatusActive, start: now, want: false},
		{name: "已提醒不再发", status: model.RoomBookingStatusActive, start: now.Add(5 * time.Minute), remindedAt: &reminded, want: false},
		{name: "已取消不发", status: model.RoomBookingStatusCancelled, start: now.Add(5 * time.Minute), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoomReminderDue(tc.status, tc.start, tc.remindedAt, now); got != tc.want {
				t.Fatalf("RoomReminderDue(status=%d, start=%s) = %v，期望 %v", tc.status, tc.start, got, tc.want)
			}
		})
	}

	start, end := RoomReminderWindow(now)
	if !start.Equal(now) || !end.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("RoomReminderWindow = (%s, %s)，期望 (%s, %s)", start, end, now, now.Add(15*time.Minute))
	}
}

func TestValidateMeetingRoomFields(t *testing.T) {
	cases := []struct {
		name      string
		roomName  string
		location  string
		capacity  int
		wantName  string
		wantError bool
	}{
		{name: "合法并去空白", roomName: "  大会议室 ", location: " 3 层 ", capacity: 20, wantName: "大会议室"},
		{name: "空名称", roomName: "  ", capacity: 1, wantError: true},
		{name: "名称超长", roomName: string(make([]rune, MaxMeetingRoomNameLen+1)), capacity: 1, wantError: true},
		{name: "位置超长", roomName: "会议室", location: string(make([]rune, MaxMeetingRoomLocationLen+1)), wantError: true},
		{name: "负容量", roomName: "会议室", capacity: -1, wantError: true},
		{name: "容量超限", roomName: "会议室", capacity: MaxMeetingRoomCapacity + 1, wantError: true},
		{name: "容量为零合法", roomName: "会议室", capacity: 0, wantName: "会议室"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, _, err := ValidateMeetingRoomFields(tc.roomName, tc.location, tc.capacity)
			if tc.wantError {
				if err == nil {
					t.Fatalf("期望校验失败，实际成功（name=%q）", gotName)
				}
				return
			}
			if err != nil {
				t.Fatalf("校验失败: %v", err)
			}
			if gotName != tc.wantName {
				t.Fatalf("名称 = %q，期望 %q", gotName, tc.wantName)
			}
		})
	}

	if err := ValidateMeetingRoomStatus(model.MeetingRoomStatusEnabled); err != nil {
		t.Fatalf("启用状态应合法: %v", err)
	}
	if err := ValidateMeetingRoomStatus(model.MeetingRoomStatusDisabled); err != nil {
		t.Fatalf("停用状态应合法: %v", err)
	}
	if err := ValidateMeetingRoomStatus(2); err == nil {
		t.Fatal("状态 2 期望校验失败")
	}
}

func TestValidateRoomBookingTitleAndWindow(t *testing.T) {
	if title, err := ValidateRoomBookingTitle("  周会 "); err != nil || title != "周会" {
		t.Fatalf("标题归一化失败: title=%q err=%v", title, err)
	}
	if _, err := ValidateRoomBookingTitle("  "); err == nil {
		t.Fatal("空标题期望校验失败")
	}
	if _, err := ValidateRoomBookingTitle(string(make([]rune, MaxRoomBookingTitleLen+1))); err == nil {
		t.Fatal("超长标题期望校验失败")
	}

	start := time.Date(2030, 1, 10, 9, 0, 0, 0, time.Local)
	if err := ValidateRoomBookingWindow(start, start.Add(time.Hour)); err != nil {
		t.Fatalf("合法窗口被拒绝: %v", err)
	}
	for _, tc := range []struct {
		name       string
		start, end time.Time
	}{
		{name: "结束等于开始", start: start, end: start},
		{name: "结束早于开始", start: start, end: start.Add(-time.Minute)},
		{name: "零值", start: time.Time{}, end: start},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRoomBookingWindow(tc.start, tc.end); err == nil {
				t.Fatal("期望校验失败")
			} else if _, ok := xerr.As(err); !ok {
				t.Fatalf("期望业务错误，实际 %T", err)
			}
		})
	}
}
