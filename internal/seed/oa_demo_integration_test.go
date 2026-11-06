//go:build integration

// OA 演示数据集成测试（issue #63）：真实 MySQL 上验证
//   - seed.Run(db, true) 写入完整 OA 演示数据（公告/请假/报销/考勤/日程/
//     会议室/文档），且能通过各模块的读取与可见性口径；
//   - 重复执行按表幂等，不重复插入；
//   - 跨模块引用成立：请假/报销申请对应已签发/待办的 bpm 实例与任务，
//     会议室预约对应 source=room_booking 的日程，考勤结果与打卡事实一致；
//   - 内置普通角色获得员工侧与考勤结果按钮，演示账号能实际打开页面接口。
package seed_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// oaDemoCounts 是一次 OA 演示数据写入后的行数快照。
type oaDemoCounts struct {
	Announcements    int64
	AnnouncementTgt  int64
	AnnouncementRead int64
	LeaveTypes       int64
	LeaveRequests    int64
	ExpenseClaims    int64
	ExpenseItems     int64
	GroupUsers       int64
	Punches          int64
	Results          int64
	CalendarEvents   int64
	Rooms            int64
	Bookings         int64
	Folders          int64
	FolderACLs       int64
	DocumentFiles    int64
	InfraFiles       int64
	BPMInstances     int64
	BPMTasks         int64
	BPMRecords       int64
}

func countOADemo(t *testing.T, db *gorm.DB) oaDemoCounts {
	t.Helper()
	count := func(model any) int64 {
		var total int64
		if err := tenant.Skip(db).Model(model).Count(&total).Error; err != nil {
			t.Fatalf("统计 %T: %v", model, err)
		}
		return total
	}
	return oaDemoCounts{
		Announcements:    count(&oamodel.Announcement{}),
		AnnouncementTgt:  count(&oamodel.AnnouncementTarget{}),
		AnnouncementRead: count(&oamodel.AnnouncementRead{}),
		LeaveTypes:       count(&oamodel.LeaveType{}),
		LeaveRequests:    count(&oamodel.LeaveRequest{}),
		ExpenseClaims:    count(&oamodel.ExpenseClaim{}),
		ExpenseItems:     count(&oamodel.ExpenseItem{}),
		GroupUsers:       count(&oamodel.AttendanceGroupUser{}),
		Punches:          count(&oamodel.AttendancePunch{}),
		Results:          count(&oamodel.AttendanceResult{}),
		CalendarEvents:   count(&oamodel.CalendarEvent{}),
		Rooms:            count(&oamodel.MeetingRoom{}),
		Bookings:         count(&oamodel.RoomBooking{}),
		Folders:          count(&oamodel.DocumentFolder{}),
		FolderACLs:       count(&oamodel.DocumentFolderACL{}),
		DocumentFiles:    count(&oamodel.DocumentFile{}),
		InfraFiles:       count(&inframodel.File{}),
		BPMInstances:     count(&bpmmodel.ProcessInstance{}),
		BPMTasks:         count(&bpmmodel.Task{}),
		BPMRecords:       count(&bpmmodel.ApprovalRecord{}),
	}
}

// TestIntegrationOADemoSeed 验证 OA 演示数据的完整性、幂等性与跨模块引用。
func TestIntegrationOADemoSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 测试基座默认关闭演示数据；显式写入第一遍（CRM + OA）。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("写入 OA 演示数据: %v", err)
	}
	first := countOADemo(t, env.DB)
	want := oaDemoCounts{
		Announcements: 3, AnnouncementTgt: 2, AnnouncementRead: 2,
		LeaveTypes: 7, LeaveRequests: 2,
		ExpenseClaims: 2, ExpenseItems: 5,
		GroupUsers: 4, Punches: 38, Results: 21,
		CalendarEvents: 5, Rooms: 2, Bookings: 2,
		Folders: 4, FolderACLs: 3, DocumentFiles: 2, InfraFiles: 2,
		BPMInstances: 3, BPMTasks: 5, BPMRecords: 4,
	}
	if first != want {
		t.Fatalf("OA 演示数据行数不符：\n got %+v\nwant %+v", first, want)
	}

	assertOADemoCounts(t, env.DB, want)

	// 重复执行必须幂等：所有 OA 表计数完全不变。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("重复写入 OA 演示数据: %v", err)
	}
	second := countOADemo(t, env.DB)
	if second != first {
		t.Fatalf("重复 seed 不应新增行：\n before %+v\n after %+v", first, second)
	}

	assertOADemoLeaveCrossModule(t, env.DB)
	assertOADemoExpenseCrossModule(t, env.DB)
	assertOADemoAttendance(t, env.DB)
	assertOADemoRoomBookingCalendar(t, env.DB)
	assertOADemoDocumentFiles(t, env)
	// 员工侧接口：演示账号能打开公告/请假/报销/考勤/会议室/文档页面。
	demo := env.Login(t, "demo", "123456")
	assertPageTotal(t, env, demo, "/oa/announcement/my/page", 1, "demo 我的公告")
	assertPageTotal(t, env, demo, "/oa/leave/request/page", 1, "demo 我的请假")
	assertPageTotal(t, env, demo, "/oa/expense/claim/page", 1, "demo 我的报销")
	assertPageTotal(t, env, demo, "/oa/attendance/result/mine", 5, "demo 我的考勤结果")
	// 内置普通角色在 issue #63 授予 533/5331/5332：有 manage 码时看全租户结果。
	assertPageTotal(t, env, demo, "/oa/attendance/result/page", 21, "demo 全租户考勤结果（oa:attendance:manage）")
	assertPageTotal(t, env, demo, "/oa/room/booking/list", 1, "demo 我的会议")
	assertPageTotal(t, env, admin, "/oa/leave/request/page", 2, "admin 全租户请假")
	assertPageTotal(t, env, admin, "/oa/expense/claim/page", 2, "admin 全租户报销")

	assertDocumentTreeVisibility(t, env, demo, admin)

	// 考勤日结果任务与会议提醒必须是「默认启用」（issue #63 收口 GORM 零值问题）。
	for _, handler := range []string{"oaAttendanceDailyResult", "oaRoomBookingReminder"} {
		var job inframodel.Job
		if err := tenant.Skip(env.DB).Where("handler_name = ?", handler).First(&job).Error; err != nil {
			t.Fatalf("读取定时任务 %s: %v", handler, err)
		}
		if job.Status != inframodel.JobStatusRunning {
			t.Fatalf("定时任务 %s 应默认启用，实际 status=%d", handler, job.Status)
		}
	}
}

// assertOADemoLeaveCrossModule 校验已通过/待审请假与 bpm 实例、额度的一致性。
func assertOADemoLeaveCrossModule(t *testing.T, db *gorm.DB) {
	t.Helper()
	users := usersByName(t, db)

	var approved oamodel.LeaveRequest
	if err := tenant.Skip(db).First(&approved, 2001).Error; err != nil {
		t.Fatalf("读取已通过请假: %v", err)
	}
	if approved.Status != oamodel.LeaveRequestStatusApproved || approved.ApprovalInstanceID == nil {
		t.Fatalf("已通过请假状态/实例不符：%+v", approved)
	}
	if approved.UserID != users["sales1"].ID || approved.DurationDays.Cents() != 100 {
		t.Fatalf("已通过请假归属/时长不符：%+v", approved)
	}
	var inst bpmmodel.ProcessInstance
	if err := tenant.Skip(db).First(&inst, *approved.ApprovalInstanceID).Error; err != nil {
		t.Fatalf("读取请假审批实例: %v", err)
	}
	if inst.BusinessType != "oa_leave_approval" || inst.BusinessID != 2001 ||
		inst.Status != bpmmodel.InstanceStatusApproved || inst.DefinitionKey != "oa_leave_approval" {
		t.Fatalf("请假审批实例不符：%+v", inst)
	}
	var taskCount, recordCount int64
	tenant.Skip(db).Model(&bpmmodel.Task{}).Where("instance_id = ? AND status = ?", inst.ID, bpmmodel.TaskStatusApproved).Count(&taskCount)
	tenant.Skip(db).Model(&bpmmodel.ApprovalRecord{}).Where("instance_id = ? AND action = ?", inst.ID, bpmmodel.RecordActionApprove).Count(&recordCount)
	if taskCount != 2 || recordCount != 2 {
		t.Fatalf("已通过请假应有 2 条通过任务/2 条审批记录，实际 task=%d record=%d", taskCount, recordCount)
	}

	// 额度扣减与已通过申请一致：sales1 在请假年份的年假 used = 1 天。
	var balance oamodel.LeaveBalance
	if err := tenant.Skip(db).Where("user_id = ? AND leave_type_id = ? AND year = ?",
		users["sales1"].ID, approved.LeaveTypeID, approved.StartDate.Year()).First(&balance).Error; err != nil {
		t.Fatalf("读取 sales1 年假额度: %v", err)
	}
	if balance.Used.Cents() != 100 || balance.Quota.Cents() != 500 {
		t.Fatalf("sales1 年假额度应为 quota=5/used=1，实际 %+v", balance)
	}

	var pending oamodel.LeaveRequest
	if err := tenant.Skip(db).First(&pending, 2002).Error; err != nil {
		t.Fatalf("读取待审请假: %v", err)
	}
	if pending.Status != oamodel.LeaveRequestStatusPending || pending.ApprovalInstanceID == nil {
		t.Fatalf("待审请假状态/实例不符：%+v", pending)
	}
	var pendingInst bpmmodel.ProcessInstance
	if err := tenant.Skip(db).First(&pendingInst, *pending.ApprovalInstanceID).Error; err != nil {
		t.Fatalf("读取待审请假实例: %v", err)
	}
	if pendingInst.Status != bpmmodel.InstanceStatusRunning {
		t.Fatalf("待审请假实例应为运行中：%+v", pendingInst)
	}
	var todo bpmmodel.Task
	if err := tenant.Skip(db).Where("instance_id = ? AND node_index = 0 AND status = ?",
		pendingInst.ID, bpmmodel.TaskStatusTodo).First(&todo).Error; err != nil {
		t.Fatalf("读取待审请假待办: %v", err)
	}
	// demo 在研发部（101），负责人由演示种子补齐为 admin。
	if todo.AssigneeID != users["admin"].ID {
		t.Fatalf("待审请假节点 1 应派给研发部负责人 admin，实际 %d", todo.AssigneeID)
	}
}

// assertOADemoExpenseCrossModule 校验报销单与明细合计、bpm 实例的一致性。
func assertOADemoExpenseCrossModule(t *testing.T, db *gorm.DB) {
	t.Helper()
	var approved oamodel.ExpenseClaim
	if err := tenant.Skip(db).First(&approved, 2001).Error; err != nil {
		t.Fatalf("读取已通过报销: %v", err)
	}
	if approved.Status != oamodel.ExpenseStatusApproved || approved.ApprovalInstanceID == nil {
		t.Fatalf("已通过报销状态/实例不符：%+v", approved)
	}
	if approved.TotalAmount.Cents() != 360000 {
		t.Fatalf("已通过报销合计应为 3600.00，实际 %s", approved.TotalAmount.String())
	}
	var items []oamodel.ExpenseItem
	if err := tenant.Skip(db).Where("claim_id = ?", approved.ID).Order("sort ASC").Find(&items).Error; err != nil {
		t.Fatalf("读取报销明细: %v", err)
	}
	sum := money.FromCents(0)
	for _, item := range items {
		if item.Category == "" || item.Amount.Cents() <= 0 {
			t.Fatalf("报销明细不合法：%+v", item)
		}
		var err error
		if sum, err = sum.Add(item.Amount); err != nil {
			t.Fatalf("累加报销明细: %v", err)
		}
	}
	if len(items) != 3 || sum != approved.TotalAmount {
		t.Fatalf("报销明细应为 3 条且合计等于表头：items=%d sum=%s total=%s",
			len(items), sum.String(), approved.TotalAmount.String())
	}
	var inst bpmmodel.ProcessInstance
	if err := tenant.Skip(db).First(&inst, *approved.ApprovalInstanceID).Error; err != nil {
		t.Fatalf("读取报销审批实例: %v", err)
	}
	if inst.BusinessType != "oa_expense_approval" || inst.BusinessID != 2001 ||
		inst.Status != bpmmodel.InstanceStatusApproved {
		t.Fatalf("报销审批实例不符：%+v", inst)
	}

	var draft oamodel.ExpenseClaim
	if err := tenant.Skip(db).First(&draft, 2002).Error; err != nil {
		t.Fatalf("读取报销草稿: %v", err)
	}
	if draft.Status != oamodel.ExpenseStatusDraft || draft.ApprovalInstanceID != nil {
		t.Fatalf("报销草稿状态/实例不符：%+v", draft)
	}
	if draft.TotalAmount.Cents() != 57850 {
		t.Fatalf("报销草稿合计应为 578.50，实际 %s", draft.TotalAmount.String())
	}
}

// assertOADemoAttendance 校验打卡与结果的派生一致性。
func assertOADemoAttendance(t *testing.T, db *gorm.DB) {
	t.Helper()
	users := usersByName(t, db)

	var late oamodel.AttendanceResult
	if err := tenant.Skip(db).Where("user_id = ? AND status = ? AND late_minutes > 0",
		users["sales1"].ID, oamodel.AttendanceResultLate).First(&late).Error; err != nil {
		t.Fatalf("读取迟到结果: %v", err)
	}
	if late.LateMinutes != 20 {
		t.Fatalf("sales1 迟到结果应为 20 分钟，实际 %+v", late)
	}
	var missingOut oamodel.AttendanceResult
	if err := tenant.Skip(db).Where("user_id = ? AND missing_out = 1", users["demo"].ID).First(&missingOut).Error; err != nil {
		t.Fatalf("读取缺下班卡结果: %v", err)
	}
	if missingOut.Status != oamodel.AttendanceResultMissing {
		t.Fatalf("缺下班卡结果状态应为缺卡：%+v", missingOut)
	}
	var leave oamodel.AttendanceResult
	if err := tenant.Skip(db).Where("user_id = ? AND status = ?", users["sales1"].ID, oamodel.AttendanceResultLeave).First(&leave).Error; err != nil {
		t.Fatalf("读取请假考勤结果: %v", err)
	}
	if leave.LeaveDays != 1 {
		t.Fatalf("请假考勤结果应为整天假：%+v", leave)
	}

	// 结果与打卡事实一致：每条有出勤的结果都能找到当天的上/下班打卡。
	var results []oamodel.AttendanceResult
	if err := tenant.Skip(db).Where("leave_days = 0 AND missing_in = 0 AND missing_out = 0").Find(&results).Error; err != nil {
		t.Fatalf("读取考勤结果: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("应存在完整打卡的考勤结果")
	}
	for _, result := range results {
		var inCount, outCount int64
		day := result.WorkDate.Format("2006-01-02")
		tenant.Skip(db).Model(&oamodel.AttendancePunch{}).
			Where("user_id = ? AND punch_date = ? AND type = ?", result.UserID, day, oamodel.PunchTypeIn).Count(&inCount)
		tenant.Skip(db).Model(&oamodel.AttendancePunch{}).
			Where("user_id = ? AND punch_date = ? AND type = ?", result.UserID, day, oamodel.PunchTypeOut).Count(&outCount)
		if inCount == 0 || outCount == 0 {
			t.Fatalf("考勤结果缺少打卡事实：user=%d date=%s in=%d out=%d", result.UserID, day, inCount, outCount)
		}
	}

	// 结果至少覆盖正常/迟到/早退/迟到且早退/缺卡/请假六种状态中的五种+请假。
	var distinct int64
	if err := tenant.Skip(db).Model(&oamodel.AttendanceResult{}).Distinct("status").Count(&distinct).Error; err != nil {
		t.Fatalf("统计考勤结果状态: %v", err)
	}
	if distinct < 6 {
		t.Fatalf("考勤结果应覆盖至少 6 种状态，实际 %d", distinct)
	}
}

// assertOADemoRoomBookingCalendar 校验预约与自动生成日程的双向引用。
func assertOADemoRoomBookingCalendar(t *testing.T, db *gorm.DB) {
	t.Helper()
	var bookings []oamodel.RoomBooking
	if err := tenant.Skip(db).Order("id ASC").Find(&bookings).Error; err != nil {
		t.Fatalf("读取会议室预约: %v", err)
	}
	if len(bookings) != 2 {
		t.Fatalf("应有 2 条会议室预约，实际 %d", len(bookings))
	}
	for _, booking := range bookings {
		if booking.CalendarEventID == nil {
			t.Fatalf("预约未回填日程 ID：%+v", booking)
		}
		var event oamodel.CalendarEvent
		if err := tenant.Skip(db).First(&event, *booking.CalendarEventID).Error; err != nil {
			t.Fatalf("读取预约日程: %v", err)
		}
		if event.Source != oamodel.CalendarSourceRoomBooking || event.SourceID == nil ||
			*event.SourceID != booking.ID || event.Visibility != oamodel.CalendarVisibilityDept {
			t.Fatalf("预约日程来源/可见性不符：booking=%+v event=%+v", booking, event)
		}
		if !event.StartTime.Equal(booking.StartTime) || !event.EndTime.Equal(booking.EndTime) {
			t.Fatalf("预约与日程时间不一致：booking=%+v event=%+v", booking, event)
		}
	}
}

// assertOADemoDocumentFiles 校验目录 ACL、文件归属与磁盘落盘。
func assertOADemoDocumentFiles(t *testing.T, env *testutil.IntegrationEnv) {
	t.Helper()
	var files []oamodel.DocumentFile
	if err := tenant.Skip(env.DB).Order("id ASC").Find(&files).Error; err != nil {
		t.Fatalf("读取文档文件: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("应有 2 条文档文件，实际 %d", len(files))
	}
	for _, file := range files {
		var infra inframodel.File
		if err := tenant.Skip(env.DB).First(&infra, file.FileID).Error; err != nil {
			t.Fatalf("读取文件本体记录: %v", err)
		}
		if infra.BizType != "oa_document" || infra.BizID != file.FolderID {
			t.Fatalf("文件归属不符：doc=%+v infra=%+v", file, infra)
		}
		abs := filepath.Join(env.Config.Storage.Local.BasePath, filepath.FromSlash(infra.Path))
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("演示文档应落盘到 %s: %v", abs, err)
		}
	}
	// 顶层目录必须显式 ACL；子目录「人事制度」继承「公司制度」的全员 ACL。
	var all, dept int64
	tenant.Skip(env.DB).Model(&oamodel.DocumentFolderACL{}).
		Where("folder_id = ? AND target_type = ?", 2001, oamodel.DocumentACLTargetAll).Count(&all)
	tenant.Skip(env.DB).Model(&oamodel.DocumentFolderACL{}).
		Where("folder_id = ? AND target_type = ? AND target_id = ?", 2003, oamodel.DocumentACLTargetDept, 101).Count(&dept)
	if all != 1 || dept != 1 {
		t.Fatalf("目录 ACL 不符：all=%d dept=%d", all, dept)
	}
}

// assertDocumentTreeVisibility 通过接口验证目录 ACL 过滤：
// demo（研发部 + 普通角色）能看公司制度/人事制度/研发资料，看不到市场部资料。
func assertDocumentTreeVisibility(t *testing.T, env *testutil.IntegrationEnv, demoToken, adminToken string) {
	t.Helper()
	paths := map[string]bool{}
	for _, token := range []string{demoToken, adminToken} {
		status, api := env.Request(t, http.MethodGet, "/oa/document/folder/tree", token, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("查询文档目录树：HTTP %d code %d msg %q", status, api.Code, api.Msg)
		}
		var nodes []map[string]any
		if err := json.Unmarshal(api.Data, &nodes); err != nil {
			t.Fatalf("解析文档目录树: %v（data=%s）", err, string(api.Data))
		}
		collectFolderNames(nodes, paths)
	}
	for _, name := range []string{"公司制度", "人事制度", "研发资料", "市场部资料"} {
		if !paths[name] {
			t.Fatalf("目录树应包含 %s：%v", name, paths)
		}
	}

	status, api := env.Request(t, http.MethodGet, "/oa/document/folder/tree", demoToken, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("demo 查询文档目录树：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var nodes []map[string]any
	if err := json.Unmarshal(api.Data, &nodes); err != nil {
		t.Fatalf("解析 demo 目录树: %v（data=%s）", err, string(api.Data))
	}
	visible := map[string]bool{}
	collectFolderNames(nodes, visible)
	if visible["市场部资料"] {
		t.Fatalf("demo 不应看到市场部资料（部门 102 ACL）：%v", visible)
	}
	if !visible["研发资料"] || !visible["人事制度"] {
		t.Fatalf("demo 应看到研发资料（部门 101）与继承 ACL 的人事制度：%v", visible)
	}
	if len(visible) != 3 {
		t.Fatalf("demo 应看到 3 个目录，实际 %v", visible)
	}
}

func collectFolderNames(nodes []map[string]any, out map[string]bool) {
	for _, node := range nodes {
		if name, _ := node["name"].(string); name != "" {
			out[name] = true
		}
		children, _ := node["children"].([]any)
		converted := make([]map[string]any, 0, len(children))
		for _, child := range children {
			if row, ok := child.(map[string]any); ok {
				converted = append(converted, row)
			}
		}
		collectFolderNames(converted, out)
	}
}

// oaDemoUser 是测试侧的用户快照（按 username 建索引）。
type oaDemoUser struct {
	ID     int64
	DeptID int64
}

func usersByName(t *testing.T, db *gorm.DB) map[string]oaDemoUser {
	t.Helper()
	var users []sysmodel.User
	if err := tenant.Skip(db).Where("tenant_id = ?", 1).Find(&users).Error; err != nil {
		t.Fatalf("读取演示用户: %v", err)
	}
	out := make(map[string]oaDemoUser, len(users))
	for _, user := range users {
		out[user.Username] = oaDemoUser{ID: user.ID, DeptID: user.DeptID}
	}
	for _, name := range []string{"admin", "demo", "sales1", "sales2", "manager"} {
		if _, ok := out[name]; !ok {
			t.Fatalf("缺少演示账号 %s", name)
		}
	}
	return out
}

// assertOADemoCounts 校验关键表的行数（与 countOADemo 的快照解耦，便于定位）。
func assertOADemoCounts(t *testing.T, db *gorm.DB, want oaDemoCounts) {
	t.Helper()
	got := countOADemo(t, db)
	if got.Announcements != want.Announcements || got.LeaveRequests != want.LeaveRequests ||
		got.ExpenseClaims != want.ExpenseClaims || got.Punches != want.Punches ||
		got.Results != want.Results || got.Rooms != want.Rooms || got.Bookings != want.Bookings ||
		got.Folders != want.Folders || got.FolderACLs != want.FolderACLs ||
		got.DocumentFiles != want.DocumentFiles {
		t.Fatalf("OA 演示数据行数不符：\n got %+v\nwant %+v", got, want)
	}
}
