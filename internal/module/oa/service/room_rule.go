// room_rule.go 承载会议室与预约的纯判定规则（issue #57，
// docs/oa-mvp-spec.md §4.11/§4.14）。全部为**纯函数**（无存储访问、无
// HTTP 上下文）：
//
//   - 时间重叠：半开区间 [start, end)，相邻（前一个结束 == 后一个开始）
//     不算冲突；
//   - 删除约束：会议室有「有效且尚未开始」的预约时只能停用（409），预约
//     取消或已开始后不再阻塞删除；
//   - 会议提醒：每 5 分钟扫描「已经进入提醒窗口（开始时间 ≤ now+15 分钟）」
//     的有效预约；已写 reminded_at 的只发一次；
//   - 字段校验：会议室名称/位置/容量、预约标题、起止时间。
package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 会议室/预约权限码（spec §5 OA 日程与会议室；菜单 525/538/539 段）。
const (
	RoomQueryPermission   = "oa:room:query"
	RoomManagePermission  = "oa:room:manage"
	RoomBookingPermission = "oa:room:booking"
)

// 会议室/预约字段上限（oa_meeting_room / oa_room_booking 列宽）。
const (
	MaxMeetingRoomNameLen     = 100
	MaxMeetingRoomLocationLen = 200
	MaxMeetingRoomCapacity    = 100000
	MaxRoomBookingTitleLen    = 200
)

// RoomReminderLeadTime 是会议提醒提前量：任务每 5 分钟扫描一次，落在
// 「开始时间 ≤ now+15 分钟」窗口内的有效预约都会收到提醒（spec §4.11）。
const RoomReminderLeadTime = 15 * time.Minute

// 会议提醒通知的业务定位（notify_message.biz_type/biz_id）与跳转路由：
// 员工侧「我的会议」菜单 539 的路由（OA 根 /oa + room-mine）。
const (
	RoomBookingNotifyBizType = "oa_room_booking"
	RoomBookingNotifyRoute   = "/oa/room-mine"
)

// ValidateMeetingRoomFields 校验会议室字段并返回归一化值：名称去首尾
// 空白后 1–100 字，位置 ≤200 字，容量 0–100000（0 表示未填写）。
func ValidateMeetingRoomFields(name, location string, capacity int) (string, string, error) {
	name = trimSpace(name)
	location = trimSpace(location)
	if name == "" {
		return "", "", xerr.BadRequest("会议室名称不能为空")
	}
	if len([]rune(name)) > MaxMeetingRoomNameLen {
		return "", "", xerr.BadRequest(fmt.Sprintf("会议室名称不能超过 %d 字", MaxMeetingRoomNameLen))
	}
	if len([]rune(location)) > MaxMeetingRoomLocationLen {
		return "", "", xerr.BadRequest(fmt.Sprintf("会议室位置不能超过 %d 字", MaxMeetingRoomLocationLen))
	}
	if capacity < 0 || capacity > MaxMeetingRoomCapacity {
		return "", "", xerr.BadRequest(fmt.Sprintf("会议室容量应在 0–%d 之间", MaxMeetingRoomCapacity))
	}
	return name, location, nil
}

// ValidateMeetingRoomStatus 校验会议室状态：0 启用 / 1 停用。
func ValidateMeetingRoomStatus(status int8) error {
	switch status {
	case model.MeetingRoomStatusEnabled, model.MeetingRoomStatusDisabled:
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("非法的会议室状态：%d", status))
	}
}

// ValidateRoomBookingTitle 校验预约标题：去首尾空白后 1–200 字。
func ValidateRoomBookingTitle(title string) (string, error) {
	title = trimSpace(title)
	if title == "" {
		return "", xerr.BadRequest("预约标题不能为空")
	}
	if len([]rune(title)) > MaxRoomBookingTitleLen {
		return "", xerr.BadRequest(fmt.Sprintf("预约标题不能超过 %d 字", MaxRoomBookingTitleLen))
	}
	return title, nil
}

// ValidateRoomBookingWindow 校验预约起止时间：两者非零且结束晚于开始。
func ValidateRoomBookingWindow(start, end time.Time) error {
	if start.IsZero() || end.IsZero() {
		return xerr.BadRequest("预约起止时间不能为空")
	}
	if !end.After(start) {
		return xerr.BadRequest("预约结束时间必须晚于开始时间")
	}
	return nil
}

// RoomBookingsOverlap 判断两个预约时段是否重叠（半开区间 [start, end)）：
//
//	aStart < bEnd && bStart < aEnd
//
// 相邻（aEnd == bStart 或 bEnd == aStart）不算冲突；包含、交叉、完全相同
// 都算冲突。仓储层的重叠计数与前端提示共用同一语义。
func RoomBookingsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// RoomDeleteBlockedByBooking 判断一条预约是否阻止会议室删除（spec §4.14：
// 有未来有效预约时只能停用不能删除）：预约有效（status=0）且开始时间严格
// 晚于 now。已取消预约与已开始的预约都不阻塞删除。
func RoomDeleteBlockedByBooking(status int8, start, now time.Time) bool {
	return status == model.RoomBookingStatusActive && start.After(now)
}

// RoomReminderWindow 返回提醒扫描窗口 (now, now+15m]：开始时间恰好落在
// now+15m 的预约进入提醒；开始时间等于 now（已开始）或晚于 now+15m 的
// 不进入本轮。
func RoomReminderWindow(now time.Time) (time.Time, time.Time) {
	return now, now.Add(RoomReminderLeadTime)
}

// RoomReminderDue 判断一条预约在本轮扫描中是否应发提醒（单条谓词的内存
// 镜像）：有效、未提醒过、开始时间在 (now, now+15m] 窗口内。已取消、已
// 提醒、已开始或还早于窗口的都不发。
func RoomReminderDue(status int8, start time.Time, remindedAt *time.Time, now time.Time) bool {
	if status != model.RoomBookingStatusActive || remindedAt != nil {
		return false
	}
	return start.After(now) && !start.After(now.Add(RoomReminderLeadTime))
}
