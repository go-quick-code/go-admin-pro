// oa_demo.go 写入 OA 演示数据（issue #63，docs/oa-mvp-spec.md §7.3）：
//   - 公告：草稿/已发布/已撤回各一，含自定义受众与已读回执；
//   - 请假：7 种请假类型、演示额度、一条已通过申请（带完整 bpm 实例/任务/
//     审批记录）与一条待审申请；
//   - 报销：草稿与已通过各一（含明细行），已通过单带 bpm 实例；
//   - 考勤：演示考勤组成员、近 5 个工作日打卡与派生结果、一条请假结果；
//   - 日程：私有/部门/公开各一；
//   - 会议室与预约：两个会议室、两条有效预约（各自动生成一条部门日程）；
//   - 文档：四个目录（含 ACL 继承与部门 ACL）与两条文件记录。
//
// 幂等：按表判空——每个域以自己的业务表为哨兵，已有行（含软删）时整段跳过；
// 角色/用户/部门负责人/额度按业务键逐行补齐，重复启动不重复插入。演示行
// 使用固定 ID 段（2001 起，按表各自独立），运行期自增 ID 不会撞主键。
//
// 依赖：基础种子（默认班次与考勤组、默认审批流程、报销类别字典）与 CRM
// 演示组织（sales1/sales2/manager）；缺账号时本文件会补建并绑定内置普通角色。
package seed

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	sysservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const oaDemoTenantID int64 = 1

// demoStorageBasePath 是本地文件存储根目录（app.New 在 seed 前注入）。
// 为空表示不落磁盘（例如直接调用 seed.Run 的单元测试）；文档记录仍会写入，
// 只是下载入口按「文件不存在」返回 404。
var demoStorageBasePath string

// SetDemoStorageBasePath 注入本地文件存储根目录，供 OA 演示文档落盘。
func SetDemoStorageBasePath(path string) { demoStorageBasePath = path }

// oaDemoUser 是演示用户的 ID/部门快照。
type oaDemoUser struct {
	ID     int64
	DeptID int64
}

func (u oaDemoUser) creator() string {
	if u.ID <= 0 {
		return "0"
	}
	return strconv.FormatInt(u.ID, 10)
}

// oaDemoOrg 是 OA 演示数据引用的账号集合。
type oaDemoOrg struct {
	admin   oaDemoUser
	demo    oaDemoUser
	sales1  oaDemoUser
	sales2  oaDemoUser
	manager oaDemoUser
}

// init 把演示数据入口注册给 seed.Run（见 seed.go 的 seedOADemoFunc 注释）。
func init() { seedOADemoFunc = seedOADemo }

// seedOADemo 写入 OA 演示数据：每个域先按表判空，再在同一事务里落库。
func seedOADemo(db *gorm.DB) error {
	err := tenant.Skip(db).Transaction(func(tx *gorm.DB) error {
		users, err := oaDemoOrgUsers(tx)
		if err != nil {
			return err
		}
		if err := oaDemoEnsureDeptLeaders(tx, users); err != nil {
			return err
		}
		// HR 演示角色（issue #64 review）：内置普通角色只授员工基础菜单，
		// 考勤结果/校正/全租户（533/5331/5332）改由 seed-demo 的 hr 角色承载。
		if err := oaDemoSeedHRRole(tx); err != nil {
			return err
		}
		if err := oaDemoSeedAnnouncements(tx, users); err != nil {
			return err
		}
		if err := oaDemoSeedLeave(tx, users); err != nil {
			return err
		}
		if err := oaDemoSeedExpenses(tx, users); err != nil {
			return err
		}
		if err := oaDemoSeedAttendance(tx, users); err != nil {
			return err
		}
		if err := oaDemoSeedCalendar(tx, users); err != nil {
			return err
		}
		if err := oaDemoSeedRooms(tx, users); err != nil {
			return err
		}
		return oaDemoSeedDocuments(tx, users)
	})
	if err != nil {
		return err
	}
	slog.Info("seed oa demo created",
		"announcements", 3, "leaveRequests", 2, "expenseClaims", 2,
		"attendanceDays", 5, "calendarEvents", 3, "rooms", 2, "documentFolders", 4)
	return nil
}

// ---------- 演示组织 ----------

// oaDemoOrgUsers 解析（必要时补建）演示账号：基础种子的 admin/demo，以及
// CRM 演示组织里的 sales1/sales2/manager。补建账号绑定内置普通角色 2，
// 保证新建库之外的历史库也能跑通演示。
func oaDemoOrgUsers(tx *gorm.DB) (oaDemoOrg, error) {
	var users oaDemoOrg
	var err error
	if users.admin, err = oaDemoEnsureUser(tx, "admin", "超级管理员", 100, "13800000001"); err != nil {
		return users, err
	}
	if users.demo, err = oaDemoEnsureUser(tx, "demo", "演示用户", 101, "13800000002"); err != nil {
		return users, err
	}
	if users.sales1, err = oaDemoEnsureUser(tx, "sales1", "销售一", 102, "13800000011"); err != nil {
		return users, err
	}
	if users.sales2, err = oaDemoEnsureUser(tx, "sales2", "销售二", 102, "13800000012"); err != nil {
		return users, err
	}
	if users.manager, err = oaDemoEnsureUser(tx, "manager", "销售主管", 102, "13800000013"); err != nil {
		return users, err
	}
	return users, nil
}

// oaDemoEnsureUser 按 username 读用户；缺失时补建并绑定内置普通角色。
// 已有账号不重置密码/昵称，避免覆盖 CRM 演示数据与管理员修改。
func oaDemoEnsureUser(tx *gorm.DB, username, nickname string, deptID int64, mobile string) (oaDemoUser, error) {
	var user sysmodel.User
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND username = ?", oaDemoTenantID, username).
		First(&user).Error
	if err == nil {
		return oaDemoUser{ID: user.ID, DeptID: user.DeptID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return oaDemoUser{}, fmt.Errorf("读取演示用户 %s: %w", username, err)
	}
	hash, err := sysservice.HashPassword("123456")
	if err != nil {
		return oaDemoUser{}, fmt.Errorf("生成演示账号密码: %w", err)
	}
	user = sysmodel.User{
		TenantID: oaDemoTenantID, Username: username, Password: hash, Nickname: nickname,
		DeptID: deptID, Email: username + "@example.com", Mobile: mobile,
		Status: sysmodel.StatusEnable, Remark: "OA 演示数据补齐的演示账号",
	}
	if err := tenant.Skip(tx).Create(&user).Error; err != nil {
		return oaDemoUser{}, fmt.Errorf("创建演示用户 %s: %w", username, err)
	}
	role := sysmodel.UserRole{TenantID: oaDemoTenantID, UserID: user.ID, RoleID: 2}
	if err := tenant.Skip(tx).Clauses(clause.OnConflict{DoNothing: true}).Create(&role).Error; err != nil {
		return oaDemoUser{}, fmt.Errorf("绑定演示用户角色 %s: %w", username, err)
	}
	return oaDemoUser{ID: user.ID, DeptID: user.DeptID}, nil
}

// oaDemoEnsureDeptLeaders 补齐部门负责人（仅在尚未设置时写入）：
// 市场部（102）由销售主管审批，研发部/财务部/总公司由管理员审批。
// 请假/报销默认流程节点 1 依赖负责人，负责人缺失会使提交 400。
func oaDemoEnsureDeptLeaders(tx *gorm.DB, users oaDemoOrg) error {
	leaders := []struct {
		deptID int64
		userID int64
	}{
		{100, users.admin.ID},
		{101, users.admin.ID},
		{102, users.manager.ID},
		{103, users.admin.ID},
	}
	for _, item := range leaders {
		if err := tenant.Skip(tx).Model(&sysmodel.Dept{}).
			Where("tenant_id = ? AND id = ? AND leader_user_id = 0", oaDemoTenantID, item.deptID).
			Update("leader_user_id", item.userID).Error; err != nil {
			return fmt.Errorf("设置演示部门负责人 dept=%d: %w", item.deptID, err)
		}
	}
	return nil
}

// oaDemoHRRoleCode / oaDemoHRUsername 是 seed-demo 创建的 HR 视角角色与账号。
// 内置普通角色只授员工基础菜单（issue #64 review，spec §7.3），考勤管理面改由
// 该角色承载；仅 `make run-demo` / GAP_SEED_DEMO=true 时写入。
const (
	oaDemoHRRoleCode = "hr"
	oaDemoHRUsername = "hr"
)

// oaDemoSeedHRRole 幂等创建 HR 演示角色与账号，并补齐角色菜单：
// OA 根目录、通用待办（含通知查询）、请假类型/额度（默认流程节点 2 的
// oa:leave:manage）、班次/考勤组、考勤结果查询/校正/全租户、月度汇总。
// 已有角色/账号时只补缺失的关联行，不覆盖管理员修改。
func oaDemoSeedHRRole(tx *gorm.DB) error {
	menus := []int64{500, 512, 5121, 5123, 522, 527, 530, 533, 5331, 5332, 534}
	var role sysmodel.Role
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND code = ?", oaDemoTenantID, oaDemoHRRoleCode).
		First(&role).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		role = sysmodel.Role{
			TenantID: oaDemoTenantID, Name: "HR 演示角色", Code: oaDemoHRRoleCode, Sort: 5,
			DataScope: sysmodel.DataScopeAll, Status: sysmodel.StatusEnable, Type: 2,
			Remark: "OA 演示：考勤结果/校正/全租户与月度汇总（seed-demo 创建）",
		}
		if err := tenant.Skip(tx).Create(&role).Error; err != nil {
			return fmt.Errorf("创建 HR 演示角色: %w", err)
		}
	case err != nil:
		return fmt.Errorf("读取 HR 演示角色: %w", err)
	}
	for _, menuID := range menus {
		row := sysmodel.RoleMenu{RoleID: role.ID, MenuID: menuID, TenantID: oaDemoTenantID}
		if err := tenant.Skip(tx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return fmt.Errorf("绑定 HR 演示角色菜单 %d: %w", menuID, err)
		}
	}
	user, err := oaDemoEnsureUser(tx, oaDemoHRUsername, "HR 演示", 100, "13800000014")
	if err != nil {
		return err
	}
	userRole := sysmodel.UserRole{UserID: user.ID, RoleID: role.ID, TenantID: oaDemoTenantID}
	if err := tenant.Skip(tx).Clauses(clause.OnConflict{DoNothing: true}).Create(&userRole).Error; err != nil {
		return fmt.Errorf("绑定 HR 演示账号角色: %w", err)
	}
	return nil
}

// ---------- 公告 ----------

// oaDemoSeedAnnouncements 写入三条公告（草稿/已发布/已撤回）、自定义受众与
// 已读回执。哨兵是 oa_announcement（含软删行，删掉不会被重新种回）。
func oaDemoSeedAnnouncements(tx *gorm.DB, users oaDemoOrg) error {
	var count int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.Announcement{}).Count(&count).Error; err != nil {
		return fmt.Errorf("统计 oa_announcement: %w", err)
	}
	if count > 0 {
		return nil
	}

	now := time.Now()
	publishedAt := oaDemoDaysAgo(now, 3)
	withdrawnPublishedAt := oaDemoDaysAgo(now, 10)
	withdrawnAt := oaDemoDaysAgo(now, 5)
	rows := []oamodel.Announcement{
		{
			BaseEntity: oaDemoBase(2001, oaDemoDaysAgo(now, 2), users.admin.creator()),
			TenantID:   oaDemoTenantID,
			Title:      "关于第四季度工作安排的通知（草稿）",
			Content:    "<p>第四季度重点工作安排正在拟定，欢迎各部门在评论区补充意见。</p>",
			Status:     oamodel.AnnouncementStatusDraft, AudienceType: oamodel.AudienceTypeCustom,
			Pinned: false,
		},
		{
			BaseEntity: oaDemoBase(2002, oaDemoDaysAgo(now, 6), users.admin.creator()),
			TenantID:   oaDemoTenantID,
			Title:      "关于优化报销流程的通知",
			Content:    "<p>自本周起，报销单统一在 <strong>OA → 报销</strong> 中提交，附件随单上传，审批完成后进入财务归档。</p><p>请各位同事按实际发生日期填写明细。</p>",
			Status:     oamodel.AnnouncementStatusPublished, AudienceType: oamodel.AudienceTypeAll,
			Pinned: true, PublishedAt: &publishedAt,
		},
		{
			BaseEntity: oaDemoBase(2003, oaDemoDaysAgo(now, 12), users.admin.creator()),
			TenantID:   oaDemoTenantID,
			Title:      "关于临时调整会议室使用的通知",
			Content:    "<p>因三楼会议室装修改造，本周会议请优先预约二楼小会议室。</p>",
			Status:     oamodel.AnnouncementStatusWithdrawn, AudienceType: oamodel.AudienceTypeAll,
			PublishedAt: &withdrawnPublishedAt, WithdrawnAt: &withdrawnAt,
		},
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示公告: %w", err)
	}

	targets := []oamodel.AnnouncementTarget{
		{TenantID: oaDemoTenantID, AnnouncementID: 2001, TargetType: oamodel.AnnouncementTargetDept, TargetID: 101, CreatedAt: oaDemoDaysAgo(now, 2)},
		{TenantID: oaDemoTenantID, AnnouncementID: 2001, TargetType: oamodel.AnnouncementTargetRole, TargetID: 2, CreatedAt: oaDemoDaysAgo(now, 2)},
	}
	if err := tenant.Skip(tx).Create(&targets).Error; err != nil {
		return fmt.Errorf("写入演示公告受众: %w", err)
	}

	readAt := oaDemoDaysAgo(now, 1)
	reads := []oamodel.AnnouncementRead{
		{TenantID: oaDemoTenantID, AnnouncementID: 2002, UserID: users.demo.ID, ReadAt: readAt, CreatedAt: readAt},
		{TenantID: oaDemoTenantID, AnnouncementID: 2002, UserID: users.sales1.ID, ReadAt: readAt.Add(time.Hour), CreatedAt: readAt.Add(time.Hour)},
	}
	if err := tenant.Skip(tx).Create(&reads).Error; err != nil {
		return fmt.Errorf("写入演示公告已读: %w", err)
	}
	return nil
}

// ---------- 请假 ----------

// oaDemoSeedLeave 写入演示额度、一条已通过申请与一条待审申请。哨兵是
// oa_leave_request（含软删行）：已有申请时整段跳过；额度按业务键逐行补齐。
func oaDemoSeedLeave(tx *gorm.DB, users oaDemoOrg) error {
	if err := oaDemoEnsureLeaveTypes(tx); err != nil {
		return err
	}
	types, err := oaDemoLeaveTypes(tx)
	if err != nil {
		return err
	}
	// 最近 5 个工作日用于考勤演示；第 6 个工作日作为已通过请假的日期，
	// 与考勤窗口错开，避免结果行冲突。
	workdays := oaDemoWorkdaysBefore(time.Now(), 6)
	leaveDate := workdays[5]
	pendingDate := oaDemoNextWorkday(time.Now())
	leaveYear := leaveDate.Year()
	now := time.Now()

	// 已通过的年假扣减 1 天；只影响 sales1 在请假年份的额度。
	if err := oaDemoEnsureBalances(tx, users, types, leaveYear, users.sales1.ID); err != nil {
		return err
	}

	var count int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.LeaveRequest{}).Count(&count).Error; err != nil {
		return fmt.Errorf("统计 oa_leave_request: %w", err)
	}
	if count > 0 {
		return nil
	}
	def, err := oaDemoPublishedDefinition(tx, "oa_leave_approval")
	if err != nil {
		return err
	}

	annual, ok := types["annual"]
	if !ok {
		return fmt.Errorf("OA 演示数据缺少请假类型 annual（年假），请确认 seedLeaveTypes 已执行")
	}
	personal, ok := types["personal"]
	if !ok {
		return fmt.Errorf("OA 演示数据缺少请假类型 personal（事假），请确认 seedLeaveTypes 已执行")
	}

	approvedStarted := leaveDate.AddDate(0, 0, -1)
	approvedNode0 := leaveDate
	approvedNode1 := leaveDate.Add(2 * time.Hour)
	approvedTitle := fmt.Sprintf("请假申请：%s 至 %s（%s %s 天）",
		oaDemoDateKey(leaveDate), oaDemoDateKey(leaveDate), annual.Name, oaDemoFormatDays(money.FromCents(100)))
	inst, err := oaDemoCreateInstance(tx, def, 2001, users.sales1, approvedTitle,
		"家中有事，申请年假一天。", approvedStarted, []oaDemoInstanceNode{
			{AssigneeID: users.manager.ID, ApprovedAt: &approvedNode0, Comment: "同意"},
			{AssigneeID: users.admin.ID, ApprovedAt: &approvedNode1, Comment: "已备案"},
		})
	if err != nil {
		return err
	}

	pendingTitle := fmt.Sprintf("请假申请：%s 至 %s（%s %s 天）",
		oaDemoDateKey(pendingDate), oaDemoDateKey(pendingDate), personal.Name, oaDemoFormatDays(money.FromCents(50)))
	pendingInst, err := oaDemoCreateInstance(tx, def, 2002, users.demo, pendingTitle,
		"下午办理个人事务，申请半天事假。", oaDemoDaysAgo(now, 1), []oaDemoInstanceNode{
			{AssigneeID: users.admin.ID},
		})
	if err != nil {
		return err
	}

	rows := []oamodel.LeaveRequest{
		{
			BaseEntity: oaDemoBase(2001, approvedStarted, users.sales1.creator()),
			TenantID:   oaDemoTenantID, UserID: users.sales1.ID, LeaveTypeID: annual.ID,
			StartDate: leaveDate, EndDate: leaveDate, StartHalf: 0, EndHalf: 1,
			DurationDays: money.FromCents(100), Reason: "家中有事，申请年假一天。",
			Status: oamodel.LeaveRequestStatusApproved, ApprovalInstanceID: &inst.ID,
		},
		{
			BaseEntity: oaDemoBase(2002, oaDemoDaysAgo(now, 1), users.demo.creator()),
			TenantID:   oaDemoTenantID, UserID: users.demo.ID, LeaveTypeID: personal.ID,
			StartDate: pendingDate, EndDate: pendingDate, StartHalf: 1, EndHalf: 1,
			DurationDays: money.FromCents(50), Reason: "下午办理个人事务，申请半天事假。",
			Status: oamodel.LeaveRequestStatusPending, ApprovalInstanceID: &pendingInst.ID,
		},
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示请假申请: %w", err)
	}
	return nil
}

// oaDemoEnsureLeaveTypes 写入默认请假类型（spec §7.3，issue #63 归入演示数据）：
// 年假（额度 5，占额度）、事假/病假（不占额度）、调休（占额度，0）、婚假（3）、
// 产假（98）、陪产假（15）。按 code 逐行幂等，已有类型不覆盖。
func oaDemoEnsureLeaveTypes(tx *gorm.DB) error {
	defaults := []oamodel.LeaveType{
		{Name: "年假", Code: "annual", BalanceManaged: true, AnnualQuota: money.FromCents(500), Status: oamodel.LeaveTypeStatusEnabled, Sort: 1, Remark: "默认年假额度 5 天，可被管理员调整"},
		{Name: "事假", Code: "personal", BalanceManaged: false, Status: oamodel.LeaveTypeStatusEnabled, Sort: 2, Remark: "不占用年度额度"},
		{Name: "病假", Code: "sick", BalanceManaged: false, Status: oamodel.LeaveTypeStatusEnabled, Sort: 3, Remark: "不占用年度额度"},
		{Name: "调休", Code: "comp", BalanceManaged: true, AnnualQuota: money.FromCents(0), Status: oamodel.LeaveTypeStatusEnabled, Sort: 4, Remark: "默认额度 0，由管理员按加班情况调整"},
		{Name: "婚假", Code: "marriage", BalanceManaged: true, AnnualQuota: money.FromCents(300), Status: oamodel.LeaveTypeStatusEnabled, Sort: 5, Remark: "默认额度 3 天"},
		{Name: "产假", Code: "maternity", BalanceManaged: true, AnnualQuota: money.FromCents(9800), Status: oamodel.LeaveTypeStatusEnabled, Sort: 6, Remark: "默认额度 98 天"},
		{Name: "陪产假", Code: "paternity", BalanceManaged: true, AnnualQuota: money.FromCents(1500), Status: oamodel.LeaveTypeStatusEnabled, Sort: 7, Remark: "默认额度 15 天"},
	}
	for i := range defaults {
		row := defaults[i]
		var count int64
		if err := tenant.Skip(tx).Unscoped().Model(&oamodel.LeaveType{}).
			Where("tenant_id = ? AND code = ?", oaDemoTenantID, row.Code).
			Count(&count).Error; err != nil {
			return fmt.Errorf("统计请假类型 %s: %w", row.Code, err)
		}
		if count > 0 {
			continue
		}
		row.TenantID = oaDemoTenantID
		if err := tenant.Skip(tx).Create(&row).Error; err != nil {
			return fmt.Errorf("写入演示请假类型 %s: %w", row.Code, err)
		}
	}
	return nil
}

// oaDemoLeaveTypes 读取演示请假类型（按 code 建索引）。
func oaDemoLeaveTypes(tx *gorm.DB) (map[string]oamodel.LeaveType, error) {
	var rows []oamodel.LeaveType
	if err := tenant.Skip(tx).Where("tenant_id = ?", oaDemoTenantID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取请假类型: %w", err)
	}
	out := make(map[string]oamodel.LeaveType, len(rows))
	for _, row := range rows {
		out[row.Code] = row
	}
	for _, code := range []string{"annual", "personal"} {
		if _, ok := out[code]; !ok {
			return nil, fmt.Errorf("OA 演示数据缺少请假类型 %s", code)
		}
	}
	return out, nil
}

// oaDemoBalanceNeed 是一行演示额度需求。
type oaDemoBalanceNeed struct {
	UserID    int64
	LeaveType oamodel.LeaveType
	Year      int
	Used      money.Money
}

// oaDemoEnsureBalances 为演示账号补齐当前年与请假年份的额度行：已有行不覆盖，
// 缺失行以类型年度额度快照落行。sales1 在请假年份的年假 used 记 1 天，
// 与已通过申请一致。
func oaDemoEnsureBalances(tx *gorm.DB, users oaDemoOrg, types map[string]oamodel.LeaveType, leaveYear int, approvedUserID int64) error {
	annual := types["annual"]
	comp := types["comp"]
	years := []int{time.Now().Year()}
	if leaveYear != time.Now().Year() {
		years = append(years, leaveYear)
	}
	needs := make([]oaDemoBalanceNeed, 0, 12)
	for _, user := range []oaDemoUser{users.demo, users.sales1, users.sales2, users.manager} {
		for _, year := range years {
			used := money.FromCents(0)
			if user.ID == approvedUserID && year == leaveYear {
				used = money.FromCents(100)
			}
			needs = append(needs, oaDemoBalanceNeed{UserID: user.ID, LeaveType: annual, Year: year, Used: used})
		}
		needs = append(needs, oaDemoBalanceNeed{UserID: user.ID, LeaveType: comp, Year: time.Now().Year()})
	}
	for _, need := range needs {
		var count int64
		if err := tenant.Skip(tx).Model(&oamodel.LeaveBalance{}).
			Where("tenant_id = ? AND user_id = ? AND leave_type_id = ? AND year = ?",
				oaDemoTenantID, need.UserID, need.LeaveType.ID, need.Year).
			Count(&count).Error; err != nil {
			return fmt.Errorf("统计请假额度 user=%d type=%d year=%d: %w", need.UserID, need.LeaveType.ID, need.Year, err)
		}
		if count > 0 {
			continue
		}
		row := oamodel.LeaveBalance{
			BaseEntity:  oaDemoBase(0, time.Now(), "1"),
			TenantID:    oaDemoTenantID,
			UserID:      need.UserID,
			LeaveTypeID: need.LeaveType.ID,
			Year:        need.Year,
			Quota:       need.LeaveType.AnnualQuota,
			Used:        need.Used,
		}
		row.ID = 0 // 额度行不做固定 ID，避免与管理员初始化行撞主键
		if err := tenant.Skip(tx).Create(&row).Error; err != nil {
			return fmt.Errorf("写入演示请假额度 user=%d type=%d year=%d: %w", need.UserID, need.LeaveType.ID, need.Year, err)
		}
	}
	return nil
}

// ---------- 报销 ----------

// oaDemoSeedExpenses 写入草稿与已通过报销单各一（含明细），已通过单带 bpm
// 实例。哨兵是 oa_expense_claim（含软删行）。
func oaDemoSeedExpenses(tx *gorm.DB, users oaDemoOrg) error {
	var count int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.ExpenseClaim{}).Count(&count).Error; err != nil {
		return fmt.Errorf("统计 oa_expense_claim: %w", err)
	}
	if count > 0 {
		return nil
	}
	if err := oaDemoCheckExpenseCategories(tx); err != nil {
		return err
	}

	now := time.Now()
	def, err := oaDemoPublishedDefinition(tx, "oa_expense_approval")
	if err != nil {
		return err
	}
	approvedStarted := oaDemoDaysAgo(now, 14)
	node0 := oaDemoDaysAgo(now, 13)
	node1 := oaDemoDaysAgo(now, 12).Add(2 * time.Hour)
	claimNo := fmt.Sprintf("BX-%06d", 2001)
	inst, err := oaDemoCreateInstance(tx, def, 2001, users.sales1,
		oaDemoTruncate(fmt.Sprintf("报销审批：%s %s", claimNo, "Q4 客户拜访差旅报销"), 128),
		oaDemoTruncate(fmt.Sprintf("单号：%s；部门：市场部；金额：3600.00 元；事由：拜访客户产生的交通、餐饮与住宿费用。", claimNo), 500),
		approvedStarted, []oaDemoInstanceNode{
			{AssigneeID: users.manager.ID, ApprovedAt: &node0, Comment: "费用合理"},
			{AssigneeID: users.admin.ID, ApprovedAt: &node1, Comment: "财务归档"},
		})
	if err != nil {
		return err
	}

	claims := []oamodel.ExpenseClaim{
		{
			BaseEntity: oaDemoBase(2001, approvedStarted, users.sales1.creator()),
			TenantID:   oaDemoTenantID, UserID: users.sales1.ID,
			Title: "Q4 客户拜访差旅报销", TotalAmount: money.FromCents(360000),
			Reason: "拜访客户产生的交通、餐饮与住宿费用。",
			Status: oamodel.ExpenseStatusApproved, ApprovalInstanceID: &inst.ID,
		},
		{
			BaseEntity: oaDemoBase(2002, oaDemoDaysAgo(now, 2), users.demo.creator()),
			TenantID:   oaDemoTenantID, UserID: users.demo.ID,
			Title: "研发团队办公用品采购", TotalAmount: money.FromCents(57850),
			Reason: "团队日常办公用品补充。", Status: oamodel.ExpenseStatusDraft,
		},
	}
	if err := tenant.Skip(tx).Create(&claims).Error; err != nil {
		return fmt.Errorf("写入演示报销单: %w", err)
	}

	items := []oamodel.ExpenseItem{
		{BaseEntity: oaDemoBase(2001, approvedStarted, users.sales1.creator()), TenantID: oaDemoTenantID, ClaimID: 2001, Category: "交通", Amount: money.FromCents(128000), ExpenseDate: oaDemoDaysAgo(now, 16), Remark: "客户现场往返高铁", Sort: 0},
		{BaseEntity: oaDemoBase(2002, approvedStarted, users.sales1.creator()), TenantID: oaDemoTenantID, ClaimID: 2001, Category: "餐饮", Amount: money.FromCents(32000), ExpenseDate: oaDemoDaysAgo(now, 16), Remark: "客户接待工作餐", Sort: 1},
		{BaseEntity: oaDemoBase(2003, approvedStarted, users.sales1.creator()), TenantID: oaDemoTenantID, ClaimID: 2001, Category: "住宿", Amount: money.FromCents(200000), ExpenseDate: oaDemoDaysAgo(now, 15), Remark: "出差住宿两晚", Sort: 2},
		{BaseEntity: oaDemoBase(2004, oaDemoDaysAgo(now, 2), users.demo.creator()), TenantID: oaDemoTenantID, ClaimID: 2002, Category: "办公", Amount: money.FromCents(45000), ExpenseDate: oaDemoDaysAgo(now, 3), Remark: "键盘与鼠标", Sort: 0},
		{BaseEntity: oaDemoBase(2005, oaDemoDaysAgo(now, 2), users.demo.creator()), TenantID: oaDemoTenantID, ClaimID: 2002, Category: "办公", Amount: money.FromCents(12850), ExpenseDate: oaDemoDaysAgo(now, 3), Remark: "打印纸与文具", Sort: 1},
	}
	if err := tenant.Skip(tx).Create(&items).Error; err != nil {
		return fmt.Errorf("写入演示报销明细: %w", err)
	}
	return nil
}

// oaDemoCheckExpenseCategories 确认报销类别字典已由基础种子写入：缺失时
// 直接报错，避免演示数据落库后在页面上无法编辑/重提。
func oaDemoCheckExpenseCategories(tx *gorm.DB) error {
	var count int64
	if err := tenant.Skip(tx).Model(&sysmodel.DictData{}).
		Where("tenant_id = ? AND `dict_type` = ? AND `value` IN ?",
			oaDemoTenantID, "oa_expense_category", []string{"交通", "餐饮", "住宿", "办公", "其他"}).
		Count(&count).Error; err != nil {
		return fmt.Errorf("统计报销类别字典: %w", err)
	}
	if count < 5 {
		return fmt.Errorf("OA 演示数据缺少报销类别字典 oa_expense_category（应有 5 项，实际 %d）", count)
	}
	return nil
}

// ---------- 考勤 ----------

// oaDemoSeedAttendance 写入考勤组成员、近 5 个工作日打卡与派生结果，以及
// 已通过请假当天的「请假」结果。三张表分别判空，互不阻塞。
func oaDemoSeedAttendance(tx *gorm.DB, users oaDemoOrg) error {
	group, err := oaDemoAttendanceGroup(tx)
	if err != nil {
		return err
	}
	members := []oaDemoUser{users.demo, users.sales1, users.sales2, users.manager}

	var memberCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.AttendanceGroupUser{}).Count(&memberCount).Error; err != nil {
		return fmt.Errorf("统计考勤组成员: %w", err)
	}
	if memberCount == 0 {
		rows := make([]oamodel.AttendanceGroupUser, 0, len(members))
		for _, member := range members {
			rows = append(rows, oamodel.AttendanceGroupUser{
				BaseEntity: oaDemoBase(0, time.Now(), "1"),
				TenantID:   oaDemoTenantID, GroupID: group.ID, UserID: member.ID,
			})
			rows[len(rows)-1].ID = 0
		}
		if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
			return fmt.Errorf("写入演示考勤组成员: %w", err)
		}
	}

	days := oaDemoWorkdaysBefore(time.Now(), 6)
	attendanceDays := days[:5]
	leaveDate := days[5]
	shiftID := group.ShiftID
	punches, results := oaDemoAttendanceRows(attendanceDays, leaveDate, shiftID, members)

	var punchCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.AttendancePunch{}).Count(&punchCount).Error; err != nil {
		return fmt.Errorf("统计打卡记录: %w", err)
	}
	if punchCount == 0 {
		if err := tenant.Skip(tx).Create(&punches).Error; err != nil {
			return fmt.Errorf("写入演示打卡记录: %w", err)
		}
	}

	var resultCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.AttendanceResult{}).Count(&resultCount).Error; err != nil {
		return fmt.Errorf("统计考勤结果: %w", err)
	}
	if resultCount == 0 {
		if err := tenant.Skip(tx).Create(&results).Error; err != nil {
			return fmt.Errorf("写入演示考勤结果: %w", err)
		}
	}
	return nil
}

// oaDemoAttendanceGroup 返回演示使用的考勤组：优先基础种子的默认组（ID 1），
// 否则取第一个启用组，都没有时建一个演示组。
func oaDemoAttendanceGroup(tx *gorm.DB) (*oamodel.AttendanceGroup, error) {
	var group oamodel.AttendanceGroup
	err := tenant.Skip(tx).First(&group, 1).Error
	if err == nil {
		return &group, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("读取默认考勤组: %w", err)
	}
	var groups []oamodel.AttendanceGroup
	if err := tenant.Skip(tx).Where("tenant_id = ? AND status = ?", oaDemoTenantID, oamodel.AttendanceStatusEnabled).
		Order("id ASC").Limit(1).Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("读取考勤组: %w", err)
	}
	if len(groups) > 0 {
		return &groups[0], nil
	}
	var shift oamodel.AttendanceShift
	if err := tenant.Skip(tx).Where("tenant_id = ? AND status = ?", oaDemoTenantID, oamodel.AttendanceStatusEnabled).
		Order("id ASC").First(&shift).Error; err != nil {
		return nil, fmt.Errorf("OA 演示数据缺少可用班次（请确认 seedAttendanceDefaults 已执行）: %w", err)
	}
	group = oamodel.AttendanceGroup{
		BaseEntity: oaDemoBase(0, time.Now(), "1"), TenantID: oaDemoTenantID,
		Name: "演示考勤组", ShiftID: shift.ID, Status: oamodel.AttendanceStatusEnabled,
	}
	group.ID = 0
	if err := tenant.Skip(tx).Create(&group).Error; err != nil {
		return nil, fmt.Errorf("创建演示考勤组: %w", err)
	}
	return &group, nil
}

// oaDemoAttendancePlan 描述某用户在某一天的打卡与派生结果。
type oaDemoAttendancePlan struct {
	User       oaDemoUser
	In, Out    string // HH:MM；空串表示缺卡
	Status     int8
	Late       int
	Early      int
	MissingIn  int8
	MissingOut int8
}

// oaDemoAttendanceRows 生成近 5 个工作日的打卡与结果，并补一条请假结果。
// 状态与打卡事实一一对应，派生口径与 service.DeriveAttendanceResult 一致
// （默认班次 09:00–18:00、宽限 5 分钟）。
func oaDemoAttendanceRows(days []time.Time, leaveDate time.Time, shiftID int64, members []oaDemoUser) ([]oamodel.AttendancePunch, []oamodel.AttendanceResult) {
	demo, sales1, sales2, manager := members[0], members[1], members[2], members[3]
	plans := [][]oaDemoAttendancePlan{
		// 第 0 天（最近一个工作日）：sales2 缺上班卡。
		{
			{User: sales1, In: "08:58", Out: "18:10", Status: oamodel.AttendanceResultNormal},
			{User: demo, In: "09:01", Out: "18:02", Status: oamodel.AttendanceResultNormal},
			{User: sales2, Out: "18:05", Status: oamodel.AttendanceResultMissing, MissingIn: 1},
			{User: manager, In: "08:55", Out: "18:01", Status: oamodel.AttendanceResultNormal},
		},
		// 第 1 天：sales1 迟到 20 分钟。
		{
			{User: sales1, In: "09:25", Out: "18:03", Status: oamodel.AttendanceResultLate, Late: 20},
			{User: demo, In: "08:56", Out: "18:07", Status: oamodel.AttendanceResultNormal},
			{User: sales2, In: "08:59", Out: "18:04", Status: oamodel.AttendanceResultNormal},
			{User: manager, In: "08:57", Out: "18:02", Status: oamodel.AttendanceResultNormal},
		},
		// 第 2 天：sales1 早退 35 分钟；demo 缺下班卡。
		{
			{User: sales1, In: "08:57", Out: "17:20", Status: oamodel.AttendanceResultEarly, Early: 35},
			{User: demo, In: "09:00", Status: oamodel.AttendanceResultMissing, MissingOut: 1},
			{User: sales2, In: "08:58", Out: "18:00", Status: oamodel.AttendanceResultNormal},
			{User: manager, In: "08:59", Out: "18:06", Status: oamodel.AttendanceResultNormal},
		},
		// 第 3 天：manager 迟到 7 分钟。
		{
			{User: sales1, In: "08:58", Out: "18:08", Status: oamodel.AttendanceResultNormal},
			{User: demo, In: "08:55", Out: "18:03", Status: oamodel.AttendanceResultNormal},
			{User: sales2, In: "09:00", Out: "18:02", Status: oamodel.AttendanceResultNormal},
			{User: manager, In: "09:12", Out: "18:00", Status: oamodel.AttendanceResultLate, Late: 7},
		},
		// 第 4 天：sales1 迟到且早退。
		{
			{User: sales1, In: "09:15", Out: "17:30", Status: oamodel.AttendanceResultLateAndEarly, Late: 10, Early: 25},
			{User: demo, In: "08:57", Out: "18:04", Status: oamodel.AttendanceResultNormal},
			{User: sales2, In: "08:56", Out: "18:01", Status: oamodel.AttendanceResultNormal},
			{User: manager, In: "08:58", Out: "18:05", Status: oamodel.AttendanceResultNormal},
		},
	}

	punches := make([]oamodel.AttendancePunch, 0, len(days)*len(members)*2)
	results := make([]oamodel.AttendanceResult, 0, len(days)*len(members)+1)
	for dayIndex, day := range days {
		for _, plan := range plans[dayIndex] {
			creator := plan.User.creator()
			if plan.In != "" {
				punches = append(punches, oaDemoPunch(plan.User, day, oaDemoAt(day, plan.In), oamodel.PunchTypeIn, creator))
			}
			if plan.Out != "" {
				punches = append(punches, oaDemoPunch(plan.User, day, oaDemoAt(day, plan.Out), oamodel.PunchTypeOut, creator))
			}
			result := oamodel.AttendanceResult{
				BaseEntity:   oaDemoBase(0, day, creator),
				TenantID:     oaDemoTenantID,
				UserID:       plan.User.ID,
				WorkDate:     day,
				ShiftID:      &shiftID,
				LateMinutes:  plan.Late,
				EarlyMinutes: plan.Early,
				MissingIn:    plan.MissingIn,
				MissingOut:   plan.MissingOut,
				Status:       plan.Status,
				ComputedAt:   oaDemoPtrTime(oaDemoAt(day, "19:00")),
			}
			result.ID = 0
			if plan.In != "" {
				result.InTime = oaDemoPtrTime(oaDemoAt(day, plan.In))
			}
			if plan.Out != "" {
				result.OutTime = oaDemoPtrTime(oaDemoAt(day, plan.Out))
			}
			results = append(results, result)
		}
	}
	// 已通过请假当天：整天请假结果（leave_days=1，不再判缺卡）。
	leaveResult := oamodel.AttendanceResult{
		BaseEntity: oaDemoBase(0, leaveDate, sales1.creator()),
		TenantID:   oaDemoTenantID, UserID: sales1.ID, WorkDate: leaveDate,
		ShiftID: &shiftID, LeaveDays: 1,
		Status:     oamodel.AttendanceResultLeave,
		ComputedAt: oaDemoPtrTime(oaDemoAt(leaveDate, "19:00")),
	}
	leaveResult.ID = 0
	results = append(results, leaveResult)
	return punches, results
}

// oaDemoPunch 构造一条 Web 打卡。
func oaDemoPunch(user oaDemoUser, day, at time.Time, punchType int8, creator string) oamodel.AttendancePunch {
	return oamodel.AttendancePunch{
		BaseEntity: oaDemoBase(0, at, creator), TenantID: oaDemoTenantID,
		UserID: user.ID, PunchDate: day, PunchTime: at, Type: punchType,
		Source: oamodel.PunchSourceWeb, IP: "127.0.0.1", UserAgent: "oa-demo-seed",
	}
}

// ---------- 日程 ----------

// oaDemoSeedCalendar 写入私有/部门/公开日程各一。哨兵只统计手动日程
// （source=0），会议室预约生成的日程不影响本段幂等。
func oaDemoSeedCalendar(tx *gorm.DB, users oaDemoOrg) error {
	var count int64
	if err := tenant.Skip(tx).Model(&oamodel.CalendarEvent{}).
		Where("tenant_id = ? AND source = ?", oaDemoTenantID, oamodel.CalendarSourceManual).
		Count(&count).Error; err != nil {
		return fmt.Errorf("统计手动日程: %w", err)
	}
	if count > 0 {
		return nil
	}
	tomorrow := oaDemoDateOnly(time.Now()).AddDate(0, 0, 1)
	rows := []oamodel.CalendarEvent{
		{
			BaseEntity: oaDemoBase(2001, time.Now(), users.demo.creator()),
			TenantID:   oaDemoTenantID, CreatorID: users.demo.ID,
			Title: "产品评审会准备", Description: "整理评审材料与演示脚本。",
			StartTime: oaDemoAt(tomorrow, "10:00"), EndTime: oaDemoAt(tomorrow, "11:00"),
			Visibility: oamodel.CalendarVisibilityPrivate, DeptID: users.demo.DeptID,
		},
		{
			BaseEntity: oaDemoBase(2002, time.Now(), users.sales1.creator()),
			TenantID:   oaDemoTenantID, CreatorID: users.sales1.ID,
			Title: "市场部月度复盘", Description: "回顾本月客户拜访与商机推进情况。",
			StartTime: oaDemoAt(tomorrow, "14:00"), EndTime: oaDemoAt(tomorrow, "15:00"),
			Visibility: oamodel.CalendarVisibilityDept, DeptID: users.sales1.DeptID,
		},
		{
			BaseEntity: oaDemoBase(2003, time.Now(), users.admin.creator()),
			TenantID:   oaDemoTenantID, CreatorID: users.admin.ID,
			Title: "全员技术分享：OA 实践", Description: "介绍 OA 模块的审批、考勤与文档能力。",
			StartTime: oaDemoAt(tomorrow, "16:00"), EndTime: oaDemoAt(tomorrow, "17:00"),
			Visibility: oamodel.CalendarVisibilityPublic, DeptID: users.admin.DeptID,
		},
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示日程: %w", err)
	}
	return nil
}

// ---------- 会议室与预约 ----------

// oaDemoSeedRooms 写入两个会议室与两条有效预约；预约各自生成一条
// source=1、部门可见的日程（与 service.CreateRoomBooking 同构）。
func oaDemoSeedRooms(tx *gorm.DB, users oaDemoOrg) error {
	var roomCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.MeetingRoom{}).Count(&roomCount).Error; err != nil {
		return fmt.Errorf("统计会议室: %w", err)
	}
	if roomCount == 0 {
		rooms := []oamodel.MeetingRoom{
			{BaseEntity: oaDemoBase(2001, time.Now(), users.admin.creator()), TenantID: oaDemoTenantID, Name: "大会议室", Location: "3F-301", Capacity: 20, Status: oamodel.MeetingRoomStatusEnabled, Sort: 0},
			{BaseEntity: oaDemoBase(2002, time.Now(), users.admin.creator()), TenantID: oaDemoTenantID, Name: "小会议室", Location: "2F-203", Capacity: 6, Status: oamodel.MeetingRoomStatusEnabled, Sort: 1},
		}
		if err := tenant.Skip(tx).Create(&rooms).Error; err != nil {
			return fmt.Errorf("写入演示会议室: %w", err)
		}
	}

	var bookingCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.RoomBooking{}).Count(&bookingCount).Error; err != nil {
		return fmt.Errorf("统计会议室预约: %w", err)
	}
	if bookingCount > 0 {
		return nil
	}
	rooms, err := oaDemoBookableRooms(tx)
	if err != nil {
		return err
	}
	if len(rooms) == 0 {
		return fmt.Errorf("OA 演示数据缺少可用会议室")
	}
	next := oaDemoNextWorkday(time.Now())
	specs := []struct {
		id         int64
		room       oamodel.MeetingRoom
		user       oaDemoUser
		title      string
		start, end time.Time
	}{
		{2001, rooms[0], users.demo, "产品需求评审", oaDemoAt(next, "11:00"), oaDemoAt(next, "12:00")},
		{2002, rooms[len(rooms)-1], users.manager, "销售周例会", oaDemoAt(next, "14:00"), oaDemoAt(next, "15:00")},
	}
	for _, spec := range specs {
		booking := oamodel.RoomBooking{
			BaseEntity: oaDemoBase(spec.id, time.Now(), spec.user.creator()),
			TenantID:   oaDemoTenantID, RoomID: spec.room.ID, UserID: spec.user.ID,
			Title: spec.title, StartTime: spec.start, EndTime: spec.end,
			Status: oamodel.RoomBookingStatusActive,
		}
		if err := tenant.Skip(tx).Create(&booking).Error; err != nil {
			return fmt.Errorf("写入演示会议室预约: %w", err)
		}
		event := oamodel.CalendarEvent{
			BaseEntity: oaDemoBase(0, time.Now(), spec.user.creator()),
			TenantID:   oaDemoTenantID, CreatorID: spec.user.ID,
			Title:       spec.title,
			Description: fmt.Sprintf("会议室：%s %s", spec.room.Name, spec.room.Location),
			StartTime:   spec.start, EndTime: spec.end,
			Visibility: oamodel.CalendarVisibilityDept, DeptID: spec.user.DeptID,
			Source: oamodel.CalendarSourceRoomBooking, SourceID: &booking.ID,
			Status: oamodel.CalendarEventStatusActive,
		}
		event.ID = 0
		if err := tenant.Skip(tx).Create(&event).Error; err != nil {
			return fmt.Errorf("写入预约日程: %w", err)
		}
		booking.CalendarEventID = &event.ID
		if err := tenant.Skip(tx).Save(&booking).Error; err != nil {
			return fmt.Errorf("回填预约日程 ID: %w", err)
		}
	}
	return nil
}

// oaDemoBookableRooms 返回演示可用的启用会议室（优先固定 ID 2001/2002）。
func oaDemoBookableRooms(tx *gorm.DB) ([]oamodel.MeetingRoom, error) {
	var rows []oamodel.MeetingRoom
	if err := tenant.Skip(tx).Where("tenant_id = ? AND status = ?", oaDemoTenantID, oamodel.MeetingRoomStatusEnabled).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取可用会议室: %w", err)
	}
	if len(rows) <= 2 {
		return rows, nil
	}
	return []oamodel.MeetingRoom{rows[0], rows[len(rows)-1]}, nil
}

// ---------- 文档 ----------

// oaDemoSeedDocuments 写入目录树（含 ACL 继承与部门 ACL）与两条文件记录。
// 文件本体同步落到本地存储根目录（未注入时跳过落盘）。
func oaDemoSeedDocuments(tx *gorm.DB, users oaDemoOrg) error {
	var folderCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.DocumentFolder{}).Count(&folderCount).Error; err != nil {
		return fmt.Errorf("统计文档目录: %w", err)
	}
	if folderCount == 0 {
		created := oaDemoDaysAgo(time.Now(), 20)
		folders := []oamodel.DocumentFolder{
			{BaseEntity: oaDemoBase(2001, created, users.admin.creator()), TenantID: oaDemoTenantID, ParentID: 0, Name: "公司制度", OwnerID: users.admin.ID, Sort: 1},
			{BaseEntity: oaDemoBase(2002, created, users.admin.creator()), TenantID: oaDemoTenantID, ParentID: 2001, Name: "人事制度", OwnerID: users.admin.ID, Sort: 1},
			{BaseEntity: oaDemoBase(2003, created, users.admin.creator()), TenantID: oaDemoTenantID, ParentID: 0, Name: "研发资料", OwnerID: users.admin.ID, Sort: 2},
			{BaseEntity: oaDemoBase(2004, created, users.admin.creator()), TenantID: oaDemoTenantID, ParentID: 0, Name: "市场部资料", OwnerID: users.admin.ID, Sort: 3},
		}
		if err := tenant.Skip(tx).Create(&folders).Error; err != nil {
			return fmt.Errorf("写入演示文档目录: %w", err)
		}
		acls := []oamodel.DocumentFolderACL{
			{TenantID: oaDemoTenantID, FolderID: 2001, TargetType: oamodel.DocumentACLTargetAll, TargetID: 0, CreatedAt: created},
			{TenantID: oaDemoTenantID, FolderID: 2003, TargetType: oamodel.DocumentACLTargetDept, TargetID: 101, CreatedAt: created},
			{TenantID: oaDemoTenantID, FolderID: 2004, TargetType: oamodel.DocumentACLTargetDept, TargetID: 102, CreatedAt: created},
		}
		if err := tenant.Skip(tx).Create(&acls).Error; err != nil {
			return fmt.Errorf("写入演示文档目录 ACL: %w", err)
		}
	}

	var fileCount int64
	if err := tenant.Skip(tx).Unscoped().Model(&oamodel.DocumentFile{}).Count(&fileCount).Error; err != nil {
		return fmt.Errorf("统计文档文件: %w", err)
	}
	if folderCount > 0 || fileCount > 0 {
		// 目录或文件已存在（管理员自建、上一轮演示或已软删）：不注入演示文件，
		// 避免把演示文件挂到未知目录或让删除过的文件复活。
		return nil
	}
	specs := []struct {
		id       int64
		folderID int64
		name     string
		relPath  string
		content  string
		by       oaDemoUser
	}{
		{2001, 2002, "员工手册.md", "demo/oa-demo/employee-handbook.md", "# 员工手册\n\n欢迎加入 go-admin-pro。\n\n- 请假与报销请在 OA 中提交\n- 考勤打卡在「我的考勤」完成\n", users.admin},
		{2002, 2003, "研发规范.md", "demo/oa-demo/dev-guide.md", "# 研发规范\n\n- 分支与提交信息遵循仓库约定\n- 新增业务表必须登记租户隔离表清单\n", users.admin},
	}
	for _, spec := range specs {
		oaDemoWriteFile(spec.relPath, spec.content)
		file := inframodel.File{
			BaseEntity: oaDemoBase(0, time.Now(), spec.by.creator()),
			TenantID:   oaDemoTenantID, Name: spec.name, Path: spec.relPath, URL: "",
			Size: int64(len(spec.content)), Type: "text/markdown", Provider: "local",
			BizType: "oa_document", BizID: spec.folderID,
		}
		file.ID = 0
		if err := tenant.Skip(tx).Create(&file).Error; err != nil {
			return fmt.Errorf("写入演示文件记录 %s: %w", spec.name, err)
		}
		doc := oamodel.DocumentFile{
			BaseEntity: oaDemoBase(spec.id, time.Now(), spec.by.creator()),
			TenantID:   oaDemoTenantID, FolderID: spec.folderID, FileID: file.ID,
			Name: spec.name, Size: file.Size, Type: file.Type, UploaderID: spec.by.ID,
		}
		if err := tenant.Skip(tx).Create(&doc).Error; err != nil {
			return fmt.Errorf("写入演示文档归属 %s: %w", spec.name, err)
		}
	}
	return nil
}

// oaDemoWriteFile 尽力把演示文件写入本地存储；失败只告警，不阻塞种子。
func oaDemoWriteFile(relPath, content string) {
	if demoStorageBasePath == "" {
		return
	}
	abs := filepath.Join(demoStorageBasePath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		slog.Warn("seed oa demo file dir", "path", abs, "err", err)
		return
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		slog.Warn("seed oa demo file write", "path", abs, "err", err)
	}
}

// ---------- bpm 演示实例 ----------

// oaDemoInstanceNode 是一个节点的审批快照：ApprovedAt 为空表示待办。
type oaDemoInstanceNode struct {
	AssigneeID int64
	ApprovedAt *time.Time
	Comment    string
}

// oaDemoCreateInstance 写入演示审批实例、任务与审批记录。所有节点都已办理
// 时实例置「通过」；否则实例保持「运行中」，只物化待办节点。
func oaDemoCreateInstance(tx *gorm.DB, def *bpmmodel.ProcessDefinition, businessID int64, initiator oaDemoUser,
	title, summary string, startedAt time.Time, nodes []oaDemoInstanceNode) (*bpmmodel.ProcessInstance, error) {
	if def == nil || len(def.Nodes) == 0 {
		return nil, fmt.Errorf("OA 演示审批流程定义缺少节点")
	}
	// 运行中的实例只物化到当前节点：nodes 允许是定义节点的前缀。
	if len(nodes) == 0 || len(nodes) > len(def.Nodes) {
		return nil, fmt.Errorf("OA 演示审批实例节点数非法：实例 %d / 定义 %d", len(nodes), len(def.Nodes))
	}
	for i := 0; i < len(nodes)-1; i++ {
		if nodes[i].ApprovedAt == nil {
			return nil, fmt.Errorf("OA 演示审批实例节点 %d 未办理但后续节点已物化", i)
		}
	}
	running := nodes[len(nodes)-1].ApprovedAt == nil
	inst := &bpmmodel.ProcessInstance{
		BaseEntity:        oaDemoBase(0, startedAt, initiator.creator()),
		TenantID:          oaDemoTenantID,
		DefinitionID:      def.ID,
		DefinitionKey:     def.Key,
		DefinitionVersion: def.Version,
		BusinessType:      def.BusinessType,
		BusinessID:        businessID,
		InitiatorID:       initiator.ID,
		Title:             oaDemoTruncate(title, 128),
		Summary:           oaDemoTruncate(summary, 500),
		Status:            bpmmodel.InstanceStatusApproved,
		StartedAt:         startedAt,
	}
	if running {
		inst.Status = bpmmodel.InstanceStatusRunning
	}
	inst.ID = 0
	if running {
		inst.EndedAt = nil
	} else {
		inst.EndedAt = nodes[len(nodes)-1].ApprovedAt
	}
	if err := tenant.Skip(tx).Create(inst).Error; err != nil {
		return nil, fmt.Errorf("写入演示审批实例: %w", err)
	}

	for index, node := range nodes {
		task := bpmmodel.Task{
			BaseEntity: oaDemoBase(0, startedAt, "0"),
			TenantID:   oaDemoTenantID,
			InstanceID: inst.ID,
			NodeIndex:  index,
			NodeName:   def.Nodes[index].Name,
			AssigneeID: node.AssigneeID,
			Status:     bpmmodel.TaskStatusTodo,
		}
		task.ID = 0
		if node.ApprovedAt != nil {
			task.Status = bpmmodel.TaskStatusApproved
			task.HandledAt = node.ApprovedAt
		}
		if err := tenant.Skip(tx).Create(&task).Error; err != nil {
			return nil, fmt.Errorf("写入演示审批任务: %w", err)
		}
		if node.ApprovedAt == nil {
			continue
		}
		record := bpmmodel.ApprovalRecord{
			TenantID:   oaDemoTenantID,
			InstanceID: inst.ID,
			TaskID:     task.ID,
			NodeIndex:  index,
			NodeName:   def.Nodes[index].Name,
			OperatorID: node.AssigneeID,
			Action:     bpmmodel.RecordActionApprove,
			Comment:    oaDemoTruncate(node.Comment, 500),
			CreatedAt:  *node.ApprovedAt,
		}
		if err := tenant.Skip(tx).Create(&record).Error; err != nil {
			return nil, fmt.Errorf("写入演示审批记录: %w", err)
		}
	}
	return inst, nil
}

// oaDemoPublishedDefinition 读取已发布的默认流程定义（基础种子写入）。
func oaDemoPublishedDefinition(tx *gorm.DB, key string) (*bpmmodel.ProcessDefinition, error) {
	var def bpmmodel.ProcessDefinition
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND `key` = ? AND status = ?", oaDemoTenantID, key, bpmmodel.DefinitionStatusPublished).
		Order("version DESC").First(&def).Error
	if err != nil {
		return nil, fmt.Errorf("OA 演示数据缺少已发布流程定义 %s（请确认 seedBPMDefinitions 已执行）: %w", key, err)
	}
	return &def, nil
}

// ---------- 小工具 ----------

// oaDemoBase 构造统一实体底座（固定 ID；id=0 时交给自增）。
func oaDemoBase(id int64, createdAt time.Time, creator string) basemodel.BaseEntity {
	if creator == "" {
		creator = "0"
	}
	return basemodel.BaseEntity{
		ID:        id,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Creator:   creator,
		Updater:   creator,
	}
}

func oaDemoPtrTime(t time.Time) *time.Time { return &t }

// oaDemoDateOnly 归一到当天零点（业务表 date 列口径）。
func oaDemoDateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// oaDemoDateKey 返回 YYYY-MM-DD。
func oaDemoDateKey(t time.Time) string { return t.Format("2006-01-02") }

// oaDemoAt 把日期与 HH:MM 拼成业务时区的时刻。
func oaDemoAt(date time.Time, hhmm string) time.Time {
	hour, _ := strconv.Atoi(hhmm[0:2])
	minute, _ := strconv.Atoi(hhmm[3:5])
	return time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, date.Location())
}

// oaDemoDaysAgo 返回 n 天前当天 10:00（与 CRM 演示数据的时间口径一致）。
func oaDemoDaysAgo(now time.Time, days int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location()).
		AddDate(0, 0, -days)
}

// oaDemoWorkdaysBefore 返回今天之前最近 n 个工作日（不含今天），最近的在前。
func oaDemoWorkdaysBefore(now time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	day := oaDemoDateOnly(now).AddDate(0, 0, -1)
	for len(out) < n {
		if weekday := day.Weekday(); weekday != time.Saturday && weekday != time.Sunday {
			out = append(out, day)
		}
		day = day.AddDate(0, 0, -1)
	}
	return out
}

// oaDemoNextWorkday 返回今天之后最近的一个工作日。
func oaDemoNextWorkday(now time.Time) time.Time {
	day := oaDemoDateOnly(now).AddDate(0, 0, 1)
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, 1)
	}
	return day
}

// oaDemoTruncate 按字符截断到 max 个字符（与 service 的列宽口径一致）。
func oaDemoTruncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

// oaDemoFormatDays 把天数格式化为可读文本（去掉无意义尾零）。
func oaDemoFormatDays(v money.Money) string {
	out := strings.TrimRight(v.String(), "0")
	out = strings.TrimRight(out, ".")
	if out == "" || out == "-" {
		return "0"
	}
	return out
}
