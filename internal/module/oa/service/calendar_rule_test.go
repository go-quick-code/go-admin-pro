// calendar_rule_test.go 是日程可见性/校验规则的表驱动测试（issue #56，
// docs/oa-mvp-spec.md §4.11）：
//   - 三种可见性（私有/部门/公开）与取消后不可见；
//   - 部门快照隔离：事件按创建时 dept_id 判定，创建者后来换部门不影响；
//   - 公开日程权限码、时间窗口与文本边界。
package service

import (
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// calendarTestEvent 构造一条有效日程（默认手动来源）。
func calendarTestEvent(creatorID int64, visibility int8, deptID int64, status int8) model.CalendarEvent {
	return model.CalendarEvent{
		CreatorID:  creatorID,
		Visibility: visibility,
		DeptID:     deptID,
		Status:     status,
	}
}

// TestCalendarViewerPermits 锁定「单条谓词」的内存语义：三条可见性 + 取消
// + 未登录 + 未知枚举（fail-closed）。
func TestCalendarViewerPermits(t *testing.T) {
	const (
		creatorID = int64(11)
		otherID   = int64(22)
		deptA     = int64(102)
		deptB     = int64(103)
	)
	// 查看者：用户 22，部门 102，部门祖先链 = {100, 102}。
	viewer := CalendarViewerOf(otherID, deptA, []int64{100, 102})
	// 创建者视角：用户 11，部门 103（后来调岗），祖先链 = {100, 103}。
	creatorMoved := CalendarViewerOf(creatorID, deptB, []int64{100, 103})

	tests := []struct {
		name  string
		event model.CalendarEvent
		want  bool
	}{
		{
			name:  "私有_仅创建人可见",
			event: calendarTestEvent(creatorID, model.CalendarVisibilityPrivate, deptA, model.CalendarEventStatusActive),
			want:  false,
		},
		{
			name:  "私有_创建人本人可见",
			event: calendarTestEvent(otherID, model.CalendarVisibilityPrivate, deptA, model.CalendarEventStatusActive),
			want:  true,
		},
		{
			name:  "部门_本部门快照可见",
			event: calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptA, model.CalendarEventStatusActive),
			want:  true,
		},
		{
			name:  "部门_祖先部门快照可见",
			event: calendarTestEvent(creatorID, model.CalendarVisibilityDept, 100, model.CalendarEventStatusActive),
			want:  true,
		},
		{
			name:  "部门_兄弟部门快照不可见",
			event: calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptB, model.CalendarEventStatusActive),
			want:  false,
		},
		{
			name:  "部门_创建者调岗到外部门后原部门查看者仍按快照可见",
			event: calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptA, model.CalendarEventStatusActive),
			want:  true,
		},
		{
			name:  "部门_快照为外部门时本查看者不可见",
			event: calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptB, model.CalendarEventStatusActive),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := viewer.Permits(tt.event); got != tt.want {
				t.Fatalf("viewer.Permits(%s) = %v，期望 %v", tt.name, got, tt.want)
			}
		})
	}

	// 创建者本人：调岗后仍能看到自己创建的任何部门日程（creator_id 短路）；
	// 而新部门（103）的其他查看者按原快照（102）看不到这条日程。
	if !creatorMoved.Permits(calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptA, model.CalendarEventStatusActive)) {
		t.Fatalf("创建人调岗后应仍能看到自己创建的部门日程（creator_id 命中）")
	}
	newDeptViewer := CalendarViewerOf(otherID, deptB, []int64{100, 103})
	if newDeptViewer.Permits(calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptA, model.CalendarEventStatusActive)) {
		t.Fatalf("创建者新部门的用户不应看到按原部门快照（102）归属的日程")
	}

	// 取消后不可见（列表与详情同一判定），即使创建人本人。
	for _, visibility := range []int8{
		model.CalendarVisibilityPrivate,
		model.CalendarVisibilityDept,
		model.CalendarVisibilityPublic,
	} {
		event := calendarTestEvent(otherID, visibility, deptA, model.CalendarEventStatusCancelled)
		if viewer.Permits(event) {
			t.Fatalf("已取消日程（visibility=%d）不应可见", visibility)
		}
	}

	// 公开：登录用户全租户可见；未登录（UserID=0）不可见。
	publicEvent := calendarTestEvent(creatorID, model.CalendarVisibilityPublic, deptB, model.CalendarEventStatusActive)
	if !viewer.Permits(publicEvent) {
		t.Fatalf("公开日程应对登录用户可见")
	}
	if (CalendarViewer{}).Permits(publicEvent) {
		t.Fatalf("未登录（UserID=0）不应看到任何日程")
	}

	// 未知可见性 fail-closed。
	if viewer.Permits(calendarTestEvent(creatorID, 9, deptA, model.CalendarEventStatusActive)) {
		t.Fatalf("未知可见性应 fail-closed")
	}
}

// TestCalendarViewerPermitsDeptAncestorChain 种子部门树 100 总公司 →
// 101/102/103 的部门日程方向（PR #66 审查 P1-1）：日程对创建者部门及子树
// 可见，查看者侧按祖先链匹配。
func TestCalendarViewerPermitsDeptAncestorChain(t *testing.T) {
	const creatorID = int64(11)
	deptEvent := func(deptID int64) model.CalendarEvent {
		return calendarTestEvent(creatorID, model.CalendarVisibilityDept, deptID, model.CalendarEventStatusActive)
	}
	// 查看者 → 其部门祖先链（含自身）。
	viewers := map[string]CalendarViewer{
		"100 总公司": CalendarViewerOf(1, 0, []int64{100}),
		"101 研发部": CalendarViewerOf(2, 0, []int64{100, 101}),
		"102 市场部": CalendarViewerOf(3, 0, []int64{100, 102}),
		"103 财务部": CalendarViewerOf(4, 0, []int64{100, 103}),
		"101 子部门": CalendarViewerOf(5, 0, []int64{100, 101, 104}),
	}
	cases := []struct {
		name        string
		viewer      string
		eventDeptID int64
		want        bool
	}{
		{"101 日程_101 可见", "101 研发部", 101, true},
		{"101 日程_101 子部门可见", "101 子部门", 101, true},
		{"101 日程_102 不可见", "102 市场部", 101, false},
		{"101 日程_103 不可见", "103 财务部", 101, false},
		{"101 日程_100 祖先不可见", "100 总公司", 101, false},
		{"100 日程_100 可见", "100 总公司", 100, true},
		{"100 日程_101 可见", "101 研发部", 100, true},
		{"100 日程_102 可见", "102 市场部", 100, true},
		{"100 日程_103 可见", "103 财务部", 100, true},
		{"102 日程_101 子部门不可见", "101 子部门", 102, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := viewers[tc.viewer].Permits(deptEvent(tc.eventDeptID)); got != tc.want {
				t.Fatalf("%s.Permits(dept=%d) = %v，期望 %v", tc.viewer, tc.eventDeptID, got, tc.want)
			}
		})
	}
}

// TestCalendarViewerOf 归一化：去重、升序、含自身部门、非法输入 fail-closed。
func TestCalendarViewerOf(t *testing.T) {
	got := CalendarViewerOf(7, 102, []int64{103, 102, 101, 103, 0, -1})
	want := []int64{101, 102, 103}
	if got.UserID != 7 || len(got.DeptIDs) != len(want) {
		t.Fatalf("CalendarViewerOf = %+v，期望 userID=7 deptIDs=%v", got, want)
	}
	for i := range want {
		if got.DeptIDs[i] != want[i] {
			t.Fatalf("CalendarViewerOf deptIDs = %v，期望升序去重 %v", got.DeptIDs, want)
		}
	}
	if noLogin := CalendarViewerOf(0, 102, []int64{102}); noLogin.UserID != 0 || len(noLogin.DeptIDs) != 0 {
		t.Fatalf("未登录应返回空视图，得到 %+v", noLogin)
	}
	if noDept := CalendarViewerOf(7, 0, nil); noDept.UserID != 7 || len(noDept.DeptIDs) != 0 {
		t.Fatalf("无部门应返回空子树，得到 %+v", noDept)
	}
}

// TestResolveCalendarDeptSnapshot 部门快照：创建取当前部门；编辑永远保留
// 原快照（创建者后来换部门不影响历史日程）。
func TestResolveCalendarDeptSnapshot(t *testing.T) {
	if got := ResolveCalendarDeptSnapshot(nil, 102); got != 102 {
		t.Fatalf("创建快照 = %d，期望 102", got)
	}
	if got := ResolveCalendarDeptSnapshot(nil, 0); got != 0 {
		t.Fatalf("无部门创建快照 = %d，期望 0", got)
	}
	existing := &model.CalendarEvent{DeptID: 102}
	if got := ResolveCalendarDeptSnapshot(existing, 103); got != 102 {
		t.Fatalf("编辑后快照 = %d，期望保留 102（创建者已调部门 103）", got)
	}
}

// TestValidateCalendarEventMutable 会议室预约生成的日程不得经日历接口
// 编辑/取消（PR #66 审查 P2-5）：手动日程放行，预约来源 409。
func TestValidateCalendarEventMutable(t *testing.T) {
	manual := calendarTestEvent(1, model.CalendarVisibilityDept, 101, model.CalendarEventStatusActive)
	if err := ValidateCalendarEventMutable(manual); err != nil {
		t.Fatalf("手动日程应可编辑/取消：%v", err)
	}
	booking := manual
	booking.Source = model.CalendarSourceRoomBooking
	err := ValidateCalendarEventMutable(booking)
	if e, ok := xerr.As(err); !ok || e.Status != 409 {
		t.Fatalf("预约生成的日程应 409，得到 %v", err)
	}
}

// TestValidateCalendarVisibility 公开日程权限码与枚举校验。
func TestValidateCalendarVisibility(t *testing.T) {
	if err := ValidateCalendarVisibility(model.CalendarVisibilityPrivate, false); err != nil {
		t.Fatalf("私有不需要公开权限：%v", err)
	}
	if err := ValidateCalendarVisibility(model.CalendarVisibilityDept, false); err != nil {
		t.Fatalf("部门不需要公开权限：%v", err)
	}
	if err := ValidateCalendarVisibility(model.CalendarVisibilityPublic, true); err != nil {
		t.Fatalf("有公开权限应通过：%v", err)
	}
	err := ValidateCalendarVisibility(model.CalendarVisibilityPublic, false)
	if e, ok := xerr.As(err); !ok || e.Status != 403 {
		t.Fatalf("无公开权限应 403，得到 %v", err)
	}
	err = ValidateCalendarVisibility(9, true)
	if e, ok := xerr.As(err); !ok || e.Status != 400 {
		t.Fatalf("非法可见性应 400，得到 %v", err)
	}
}

// TestValidateCalendarEventTimes 起止时间边界。
func TestValidateCalendarEventTimes(t *testing.T) {
	base := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		start     time.Time
		end       time.Time
		wantError bool
	}{
		{name: "正常", start: base, end: base.Add(time.Hour)},
		{name: "结束等于开始", start: base, end: base, wantError: true},
		{name: "结束早于开始", start: base, end: base.Add(-time.Minute), wantError: true},
		{name: "零值", start: time.Time{}, end: base, wantError: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCalendarEventTimes(tt.start, tt.end)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateCalendarEventTimes = %v，期望错误=%v", err, tt.wantError)
			}
		})
	}
}

// TestValidateCalendarListRange 列表窗口：可选但必须成对、有序且 ≤366 天。
func TestValidateCalendarListRange(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	tooLong := start.Add(MaxCalendarListRange + time.Hour)
	cases := []struct {
		name      string
		start     *time.Time
		end       *time.Time
		wantError bool
	}{
		{name: "都不给", start: nil, end: nil},
		{name: "正常一个月", start: &start, end: &end},
		{name: "只给开始", start: &start, end: nil, wantError: true},
		{name: "只给结束", start: nil, end: &end, wantError: true},
		{name: "倒置", start: &end, end: &start, wantError: true},
		{name: "超过一年", start: &start, end: &tooLong, wantError: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCalendarListRange(tt.start, tt.end)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateCalendarListRange = %v，期望错误=%v", err, tt.wantError)
			}
		})
	}
}

// TestValidateCalendarEventText 标题/描述边界。
func TestValidateCalendarEventText(t *testing.T) {
	title, desc, err := ValidateCalendarEventText("  部门周会  ", "  讨论季度目标 ")
	if err != nil || title != "部门周会" || desc != "讨论季度目标" {
		t.Fatalf("去空白失败：title=%q desc=%q err=%v", title, desc, err)
	}
	if _, _, err := ValidateCalendarEventText("   ", ""); err == nil {
		t.Fatalf("空白标题应 400")
	}
	longTitle := make([]rune, MaxCalendarEventTitleLen+1)
	for i := range longTitle {
		longTitle[i] = '会'
	}
	if _, _, err := ValidateCalendarEventText(string(longTitle), ""); err == nil {
		t.Fatalf("超长标题应 400")
	}
}

// TestParseCalendarTime 业务时区墙钟与 RFC3339 两种输入口径。
func TestParseCalendarTime(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	got, err := parseCalendarTime("2026-11-06 09:30:00", loc)
	if err != nil {
		t.Fatalf("解析墙钟时间: %v", err)
	}
	if got.In(loc).Format(CalendarTimeLayout) != "2026-11-06 09:30:00" {
		t.Fatalf("墙钟时间被改写：%v", got)
	}
	got, err = parseCalendarTime("2026-11-06T09:30:00+08:00", loc)
	if err != nil {
		t.Fatalf("解析 RFC3339: %v", err)
	}
	if got.In(loc).Format(CalendarTimeLayout) != "2026-11-06 09:30:00" {
		t.Fatalf("RFC3339 时刻错误：%v", got)
	}
	if _, err := parseCalendarTime("2026/11/06 09:30", loc); err == nil {
		t.Fatalf("非法格式应 400")
	}
}
