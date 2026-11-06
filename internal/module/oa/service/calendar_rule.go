// calendar_rule.go 承载日程的核心判定规则（issue #56，docs/oa-mvp-spec.md
// §4.11）。全部为**纯函数/纯方法**（无存储访问、无 HTTP 上下文）：
//
//   - 可见性：私有仅创建人；部门对创建人创建时的部门快照及其子树可见（查看
//     者侧等价于 dept_id ∈ 查看者部门的祖先链）；公开对全租户可见。取消
//     （status=1）后一律不可见（列表与详情同一判定）；
//   - 创建公开日程需要 oa:calendar:public 权限；
//   - 部门快照：dept_id 只在创建时写入，编辑器不得刷新（用户调部门后历史
//     日程仍归属原部门）；
//   - 起止时间：结束必须晚于开始；列表区间必须有序且不超过一年。
//
// 仓储侧只消费同一语义的**单条谓词**（repository.ListCalendarEventsByVisibility：
// status=0 AND (creator_id=本人 OR (visibility=部门 AND dept_id IN 查看者祖先链)
// OR visibility=公开)），本文件的 CalendarViewer.Permits 是该谓词的内存镜像，
// 表驱动测试锁定二者语义一致。
package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// CalendarTimeLayout 是日程起止时间的对外格式（业务时区墙钟）。
const CalendarTimeLayout = "2006-01-02 15:04:05"

// 日程文本上限（oa_calendar_event 列宽）。
const (
	MaxCalendarEventTitleLen       = 200
	MaxCalendarEventDescriptionLen = 1000
)

// MaxCalendarListRange 是列表查询区间的最大跨度（366 天）：月/周视图远小于
// 该值，上限防止一次拉取过大区间。
const MaxCalendarListRange = 366 * 24 * time.Hour

// CalendarPublicPermission 是创建/编辑公开日程所需的权限码（spec §4.11）。
const CalendarPublicPermission = "oa:calendar:public"

// CalendarViewer 是可见性判定所需的查看者上下文（存储无关）：
//   - UserID 查看者用户 ID（<=0 视为未登录，什么都看不到）；
//   - DeptIDs 查看者所属部门及其**祖先链**（含自身，升序去重；由
//     SystemGateway 取数后经 CalendarViewerOf 归一化）。部门日程的 dept_id
//     命中该集合即对查看者可见（部门及子部门语义的查看者侧表达）。空集合
//     表示没有部门可见范围。
type CalendarViewer struct {
	UserID  int64
	DeptIDs []int64
}

// CalendarViewerOf 归一化查看者上下文（纯函数）：userID<=0 时 fail-closed
// 返回空视图；deptID<=0 且 deptIDs 为空时部门可见范围为空（只可能看到本人
// 与公开日程）。deptIDs 去重并升序，便于断言与 SQL IN 的稳定入参。
func CalendarViewerOf(userID, deptID int64, deptIDs []int64) CalendarViewer {
	if userID <= 0 {
		return CalendarViewer{DeptIDs: []int64{}}
	}
	set := make(map[int64]struct{}, len(deptIDs)+1)
	if deptID > 0 {
		set[deptID] = struct{}{}
	}
	for _, id := range deptIDs {
		if id > 0 {
			set[id] = struct{}{}
		}
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	// 小集合插入排序即可（部门祖先链通常 < 100）；避免额外依赖。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return CalendarViewer{UserID: userID, DeptIDs: out}
}

// Permits 判断一条日程对查看者是否可见（单条谓词的内存镜像）：
//   - 已取消（status != 0）→ false：取消后列表/详情都不再展示；
//   - 创建人本人 → 恒可见（与 SQL 谓词的 creator_id 短路一致，调岗不影响）；
//   - 私有 → 仅创建人；
//   - 部门 → 日程的 dept_id（创建时快照）∈ 查看者部门祖先链（含自身）：
//     子部门能看到祖先部门创建的日程，祖先部门看不到子部门创建的日程；
//   - 公开 → 登录用户（userID>0）全租户可见；
//   - 未知可见性 → fail-closed。
func (v CalendarViewer) Permits(event model.CalendarEvent) bool {
	if v.UserID <= 0 || event.Status != model.CalendarEventStatusActive {
		return false
	}
	if event.CreatorID == v.UserID {
		return true
	}
	switch event.Visibility {
	case model.CalendarVisibilityPrivate:
		return false
	case model.CalendarVisibilityDept:
		for _, id := range v.DeptIDs {
			if id == event.DeptID {
				return true
			}
		}
		return false
	case model.CalendarVisibilityPublic:
		return true
	default:
		return false
	}
}

// ValidateCalendarEventMutable 判断日程是否允许经日历接口改写/取消（纯函数）：
// source=room_booking 的日程由会议室预约在同一事务内创建与联动取消，只能经
// 会议室取消路径修改；日历接口的编辑/取消一律 409（PR #66 审查 P2-5），
// 避免两侧状态分叉（日程取消了预约还挂着，或反过来）。
func ValidateCalendarEventMutable(event model.CalendarEvent) error {
	if event.Source == model.CalendarSourceRoomBooking {
		return xerr.Conflict("会议室预约生成的日程不能通过日历修改或取消，请在会议室预约中操作")
	}
	return nil
}

// ValidateCalendarVisibility 校验可见性取值，并在公开时检查权限：
// 非法枚举 400；visibility=2 且 hasPublicPermission=false 返回 403
// （spec §4.11：公开日程创建需要 oa:calendar:public）。编辑把日程改为公开
// 时走同一函数。
func ValidateCalendarVisibility(visibility int8, hasPublicPermission bool) error {
	switch visibility {
	case model.CalendarVisibilityPrivate, model.CalendarVisibilityDept:
		return nil
	case model.CalendarVisibilityPublic:
		if !hasPublicPermission {
			return xerr.Forbidden("创建公开日程需要 oa:calendar:public 权限")
		}
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("非法的日程可见性：%d", visibility))
	}
}

// ValidateCalendarEventText 校验标题与描述：标题去首尾空白后 1–200 字，
// 描述 ≤1000 字。返回归一化后的值。
func ValidateCalendarEventText(title, description string) (string, string, error) {
	title = trimSpace(title)
	description = trimSpace(description)
	if title == "" {
		return "", "", xerr.BadRequest("日程标题不能为空")
	}
	if len([]rune(title)) > MaxCalendarEventTitleLen {
		return "", "", xerr.BadRequest(fmt.Sprintf("日程标题不能超过 %d 字", MaxCalendarEventTitleLen))
	}
	if len([]rune(description)) > MaxCalendarEventDescriptionLen {
		return "", "", xerr.BadRequest(fmt.Sprintf("日程描述不能超过 %d 字", MaxCalendarEventDescriptionLen))
	}
	return title, description, nil
}

// ValidateCalendarEventTimes 校验起止时间：两者必须非零且结束晚于开始
// （spec §4.11 的时间范围语义）。全天日程同样要求结束晚于开始（前端按
// 「结束日期次日零点」提交）。
func ValidateCalendarEventTimes(start, end time.Time) error {
	if start.IsZero() || end.IsZero() {
		return xerr.BadRequest("日程起止时间不能为空")
	}
	if !end.After(start) {
		return xerr.BadRequest("日程结束时间必须晚于开始时间")
	}
	return nil
}

// ValidateCalendarListRange 校验列表查询区间（可选）：
//   - 两端都为 nil 表示不限；
//   - 任一端给出时只要求另一端存在（否则无从判断窗口）；
//   - 结束必须晚于开始，跨度不超过 MaxCalendarListRange。
func ValidateCalendarListRange(start, end *time.Time) error {
	if start == nil && end == nil {
		return nil
	}
	if start == nil || end == nil {
		return xerr.BadRequest("查询区间需同时提供开始与结束时间")
	}
	if !end.After(*start) {
		return xerr.BadRequest("查询结束时间必须晚于开始时间")
	}
	if end.Sub(*start) > MaxCalendarListRange {
		return xerr.BadRequest("查询区间不能超过 366 天")
	}
	return nil
}

// ResolveCalendarDeptSnapshot 返回日程落库时的部门快照（纯函数）：
//   - 创建（existing == nil）：取当前创建人部门；
//   - 编辑：保留原快照，忽略调用方/当前部门——用户调部门后历史日程仍归属
//     原部门（spec §4.11）。
func ResolveCalendarDeptSnapshot(existing *model.CalendarEvent, creatorDeptID int64) int64 {
	if existing != nil {
		return existing.DeptID
	}
	if creatorDeptID < 0 {
		return 0
	}
	return creatorDeptID
}

// parseCalendarTime 解析请求里的时间：支持业务时区墙钟
// "YYYY-MM-DD HH:mm:ss"（按业务时区解释）与 RFC3339（显式带偏移）。
// 两者都失败返回 400 可读错误。
func parseCalendarTime(value string, loc *time.Location) (time.Time, error) {
	text := trimSpace(value)
	if text == "" {
		return time.Time{}, xerr.BadRequest("日程时间不能为空")
	}
	if t, err := time.ParseInLocation(CalendarTimeLayout, text, loc); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	return time.Time{}, xerr.BadRequest("日程时间格式应为 YYYY-MM-DD HH:mm:ss 或 RFC3339")
}

// parseOptionalCalendarTime 解析可选的列表区间端点（空串返回 nil）。
func parseOptionalCalendarTime(value string, loc *time.Location) (*time.Time, error) {
	if trimSpace(value) == "" {
		return nil, nil
	}
	t, err := parseCalendarTime(value, loc)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
