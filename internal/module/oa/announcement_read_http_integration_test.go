//go:build integration

// 公告已读与统计的 HTTP 集成用例（issue #49，docs/oa-mvp-spec.md §4.7）：
// 打开详情幂等写已读（重复打开仍一行）、列表未读标记与详情一致、已读
// 人数/名单（分页）准确、权限码 oa:announcement:read-stats 控权、撤回保留
// 历史已读、重新发布不重置、跨租户 404。主 seam = 真实 router + 生产中间件
// + 真实 MySQL（测试库 _t49，-p 1 串行）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// announcementBtnReadStats 是已读统计按钮（seed.go 521 段 5215）。
const announcementBtnReadStats = int64(5215)

// announcementReadStatsUser 是已读名单一行。
type announcementReadStatsUser struct {
	UserID   int64  `json:"userId"`
	Nickname string `json:"nickname"`
	ReadAt   string `json:"readAt"`
}

// announcementReadStatsData 是已读统计响应（readCount 与 total 同口径：
// 按 user_id 去重后的已读人数）。
type announcementReadStatsData struct {
	ReadCount int64                       `json:"readCount"`
	Total     int64                       `json:"total"`
	List      []announcementReadStatsUser `json:"list"`
}

// announcementReadFlags 是员工列表行的已读状态。
type announcementReadFlags struct {
	Read   bool `json:"read"`
	Unread bool `json:"unread"`
}

// getAnnouncementReadStats 请求已读统计，返回 (HTTP 状态, data, msg)。
func getAnnouncementReadStats(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, query string) (int, announcementReadStatsData, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		fmt.Sprintf("/oa/announcement/%d/read-stats%s", id, query), token, nil)
	if api.Code != 0 {
		return status, announcementReadStatsData{}, api.Msg
	}
	var data announcementReadStatsData
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析已读统计: %v（data=%s）", err, string(api.Data))
	}
	return status, data, ""
}

// myAnnouncementReadFlags 员工「我的公告」列表，返回 id→(read, unread)。
func myAnnouncementReadFlags(t *testing.T, env *testutil.IntegrationEnv, token string) map[int64]announcementReadFlags {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/announcement/my/page"+announcementPageSizeQuery, token, nil)
	mustOK(t, "我的公告列表", status, api)
	var page struct {
		List []struct {
			ID     int64 `json:"id"`
			Read   bool  `json:"read"`
			Unread bool  `json:"unread"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析我的公告: %v（data=%s）", err, string(api.Data))
	}
	out := make(map[int64]announcementReadFlags, len(page.List))
	for _, row := range page.List {
		out[row.ID] = announcementReadFlags{Read: row.Read, Unread: row.Unread}
	}
	return out
}

// countAnnouncementReadRows 直接统计已读表行数（验证幂等写入不产生重复行）。
// 测试侧无租户上下文，显式 tenant.Skip 跳过插件（系统级断言）。
func countAnnouncementReadRows(t *testing.T, env *testutil.IntegrationEnv, announcementID, userID int64) int64 {
	t.Helper()
	var count int64
	if err := tenant.Skip(env.DB).Table("oa_announcement_read").
		Where("announcement_id = ? AND user_id = ?", announcementID, userID).
		Count(&count).Error; err != nil {
		t.Fatalf("统计已读行: %v", err)
	}
	return count
}

// usernameOf 按用户名回读用户 ID（默认租户，测试侧 tenant.Skip）。
func usernameOf(t *testing.T, env *testutil.IntegrationEnv, username string) int64 {
	t.Helper()
	var user struct {
		ID int64
	}
	if err := tenant.Skip(env.DB).Table("system_user").
		Select("id").Where("username = ?", username).Scan(&user).Error; err != nil {
		t.Fatalf("查询用户 %s: %v", username, err)
	}
	if user.ID == 0 {
		t.Fatalf("用户 %s 不存在", username)
	}
	return user.ID
}

// statsUserRow 在已读名单中按用户 ID 找一行；未命中返回 nil。
func statsUserRow(data announcementReadStatsData, userID int64) *announcementReadStatsUser {
	for i := range data.List {
		if data.List[i].UserID == userID {
			return &data.List[i]
		}
	}
	return nil
}

// TestIntegrationAnnouncementReadFlow 覆盖最小闭环：
// 发布 → 员工列表未读 → 打开详情写已读 → 重复打开仍 1 行 → 列表转已读 →
// 管理端已读人数/名单准确。
func TestIntegrationAnnouncementReadFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")
	demoID := usernameOf(t, env, "demo")

	id, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "已读闭环公告", "content": "<p>请阅读</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("创建公告：HTTP %d msg %q", status, msg)
	}
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("发布公告：HTTP %d msg %q", status, msg)
	}

	// 发布后未打开：列表 read=false、unread=true。
	before := myAnnouncementReadFlags(t, env, demo)
	row, ok := before[id]
	if !ok {
		t.Fatalf("已发布公告应出现在员工列表：%v", before)
	}
	if row.Read || !row.Unread {
		t.Fatalf("未打开前列表应为未读：read=%v unread=%v", row.Read, row.Unread)
	}

	// 打开详情：写已读，详情自身返回 read=true/unread=false。
	status, detail, msg := announcementDetail(t, env, demo, id)
	if status != http.StatusOK {
		t.Fatalf("打开公告详情：HTTP %d msg %q", status, msg)
	}
	if got, _ := detail["read"].(bool); !got {
		t.Fatalf("详情 read=%v，期望 true（打开即已读）", detail["read"])
	}
	if got, _ := detail["unread"].(bool); got {
		t.Fatalf("详情 unread=%v，期望 false", detail["unread"])
	}
	if n := countAnnouncementReadRows(t, env, id, demoID); n != 1 {
		t.Fatalf("首次打开后已读行数 = %d，期望 1", n)
	}

	// 重复打开两次：幂等，仍只有一行已读。
	for i := 0; i < 2; i++ {
		if status, _, msg := announcementDetail(t, env, demo, id); status != http.StatusOK {
			t.Fatalf("重复打开详情 #%d：HTTP %d msg %q", i+1, status, msg)
		}
	}
	if n := countAnnouncementReadRows(t, env, id, demoID); n != 1 {
		t.Fatalf("重复打开后已读行数 = %d，期望仍为 1（唯一键幂等）", n)
	}

	// 列表转为已读。
	after := myAnnouncementReadFlags(t, env, demo)
	row, ok = after[id]
	if !ok {
		t.Fatalf("已读公告仍应在员工列表：%v", after)
	}
	if !row.Read || row.Unread {
		t.Fatalf("打开后列表应为已读：read=%v unread=%v", row.Read, row.Unread)
	}

	// 管理端已读统计：人数 1，名单含 demo 与已读时间。
	status, stats, msg := getAnnouncementReadStats(t, env, admin, id, "?pageNo=1&pageSize=10")
	if status != http.StatusOK {
		t.Fatalf("已读统计：HTTP %d msg %q", status, msg)
	}
	if stats.ReadCount != 1 || stats.Total != 1 {
		t.Fatalf("已读人数 = readCount %d / total %d，期望 1", stats.ReadCount, stats.Total)
	}
	if len(stats.List) != 1 {
		t.Fatalf("已读名单长度 = %d，期望 1（%+v）", len(stats.List), stats.List)
	}
	if row := statsUserRow(stats, demoID); row == nil {
		t.Fatalf("已读名单缺少 demo（userId=%d）：%+v", demoID, stats.List)
	} else if row.Nickname == "" || row.ReadAt == "" {
		t.Fatalf("已读名单行缺少昵称/已读时间：%+v", *row)
	}
}

// TestIntegrationAnnouncementReadRetention 覆盖统计口径与状态流转：
// 多用户去重计数、名单分页、撤回保留历史已读、重新发布不重置已读。
func TestIntegrationAnnouncementReadRetention(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	readerRole := createRoleWithMenus(t, env, admin, "ann_read_retention", []int64{announcementMineMenuID})
	users := make([]string, 0, 3)
	userIDs := make([]int64, 0, 3)
	for _, name := range []string{"read_ret_a", "read_ret_b", "read_ret_c"} {
		userID, token := createUserWithRoles(t, env, admin, name, 101, []int64{readerRole})
		users = append(users, token)
		userIDs = append(userIDs, userID)
	}

	id, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "已读保留公告", "content": "<p>历史已读</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("创建公告：HTTP %d msg %q", status, msg)
	}
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("发布公告：HTTP %d msg %q", status, msg)
	}

	// 第一位用户打开两次：人数按用户去重，仍为 1。
	for i := 0; i < 2; i++ {
		if status, _, msg := announcementDetail(t, env, users[0], id); status != http.StatusOK {
			t.Fatalf("用户 A 打开详情 #%d：HTTP %d msg %q", i+1, status, msg)
		}
	}
	_, stats, msg := getAnnouncementReadStats(t, env, admin, id, "")
	if stats.ReadCount != 1 {
		t.Fatalf("同一用户重复打开后已读人数 = %d，期望 1（msg=%q）", stats.ReadCount, msg)
	}
	firstReadAt := ""
	if row := statsUserRow(stats, userIDs[0]); row == nil {
		t.Fatalf("已读名单缺少用户 A（%d）：%+v", userIDs[0], stats.List)
	} else {
		firstReadAt = row.ReadAt
	}

	// 第二、三位用户打开：人数 3，名单分页准确。
	for _, token := range users[1:] {
		if status, _, msg := announcementDetail(t, env, token, id); status != http.StatusOK {
			t.Fatalf("用户打开详情：HTTP %d msg %q", status, msg)
		}
	}
	status, page1, msg := getAnnouncementReadStats(t, env, admin, id, "?pageNo=1&pageSize=2")
	if status != http.StatusOK || page1.ReadCount != 3 || page1.Total != 3 || len(page1.List) != 2 {
		t.Fatalf("第 1 页：HTTP %d readCount=%d total=%d len=%d msg=%q，期望 3/3/2",
			status, page1.ReadCount, page1.Total, len(page1.List), msg)
	}
	status, page2, msg := getAnnouncementReadStats(t, env, admin, id, "?pageNo=2&pageSize=2")
	if status != http.StatusOK || page2.Total != 3 || len(page2.List) != 1 {
		t.Fatalf("第 2 页：HTTP %d total=%d len=%d msg=%q，期望 3/1",
			status, page2.Total, len(page2.List), msg)
	}
	seen := make(map[int64]bool, 3)
	for _, row := range append(page1.List, page2.List...) {
		seen[row.UserID] = true
	}
	for _, userID := range userIDs {
		if !seen[userID] {
			t.Fatalf("分页名单缺少用户 %d：page1=%+v page2=%+v", userID, page1.List, page2.List)
		}
	}

	// 撤回：员工列表不可见，但历史已读保留、统计不变。
	if status, msg := withdrawAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("撤回公告：HTTP %d msg %q", status, msg)
	}
	if _, ok := myAnnouncements(t, env, users[0])[id]; ok {
		t.Fatal("撤回后公告不应出现在员工列表")
	}
	_, stats, msg = getAnnouncementReadStats(t, env, admin, id, "")
	if stats.ReadCount != 3 {
		t.Fatalf("撤回后已读人数 = %d，期望保留 3（msg=%q）", stats.ReadCount, msg)
	}

	// 重新发布：已读状态不重置（read=true 且首次已读时间不变）。
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("重新发布：HTTP %d msg %q", status, msg)
	}
	flags := myAnnouncementReadFlags(t, env, users[0])
	row, ok := flags[id]
	if !ok || !row.Read || row.Unread {
		t.Fatalf("重新发布后用户 A 应保持已读：存在=%v read=%v unread=%v", ok, row.Read, row.Unread)
	}
	_, stats, msg = getAnnouncementReadStats(t, env, admin, id, "")
	if stats.ReadCount != 3 {
		t.Fatalf("重新发布后已读人数 = %d，期望仍为 3（msg=%q）", stats.ReadCount, msg)
	}
	if row := statsUserRow(stats, userIDs[0]); row == nil {
		t.Fatalf("重新发布后名单缺少用户 A：%+v", stats.List)
	} else if row.ReadAt != firstReadAt {
		t.Fatalf("重新发布后首次已读时间被重置：got %q want %q", row.ReadAt, firstReadAt)
	}
}

// TestIntegrationAnnouncementReadStatsPermissions 覆盖权限码与租户边界：
// 仅持 oa:announcement:read-stats 可看统计；管理查询码/员工码无该码 403；
// 跨租户返回 404 不泄露存在性。
func TestIntegrationAnnouncementReadStatsPermissions(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	id, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "统计权限公告", "content": "<p>权限</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("创建公告：HTTP %d msg %q", status, msg)
	}
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("发布公告：HTTP %d msg %q", status, msg)
	}

	statsRole := createRoleWithMenus(t, env, admin, "ann_read_stats_only", []int64{announcementBtnReadStats})
	manageRole := createRoleWithMenus(t, env, admin, "ann_manage_no_stats", []int64{announcementMenuID})
	employeeRole := createRoleWithMenus(t, env, admin, "ann_read_employee", []int64{announcementMineMenuID})
	statsUser := oaLoginNewUser(t, env, admin, "ann_readstats_u", statsRole)
	manageUser := oaLoginNewUser(t, env, admin, "ann_manage_nostats_u", manageRole)
	employee := oaLoginNewUser(t, env, admin, "ann_read_employee_u", employeeRole)

	// 仅统计码：可读统计（即使没有管理列表查询码）。
	if status, data, msg := getAnnouncementReadStats(t, env, statsUser, id, ""); status != http.StatusOK {
		t.Fatalf("仅统计码访问已读统计应成功：HTTP %d msg %q", status, msg)
	} else if data.ReadCount != 0 {
		t.Fatalf("无人打开时已读人数 = %d，期望 0", data.ReadCount)
	}
	// 管理查询码与员工码都不含统计码：403。
	if status, _, msg := getAnnouncementReadStats(t, env, manageUser, id, ""); status != http.StatusForbidden {
		t.Fatalf("无统计码（管理查询码）应 403，实际 HTTP %d msg %q", status, msg)
	}
	if status, _, msg := getAnnouncementReadStats(t, env, employee, id, ""); status != http.StatusForbidden {
		t.Fatalf("无统计码（员工码）应 403，实际 HTTP %d msg %q", status, msg)
	}

	// 跨租户：租户 2 管理员（super_admin 通配）查租户 1 公告统计 → 404。
	tenant2 := createOATenant(t, env, admin, "announcement_read_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	if status, _, _ := getAnnouncementReadStats(t, env, token2, id, ""); status != http.StatusNotFound {
		t.Fatalf("跨租户已读统计应 404，实际 HTTP %d", status)
	}
	// 不存在的公告同口径 404。
	if status, _, _ := getAnnouncementReadStats(t, env, admin, id+999999, ""); status != http.StatusNotFound {
		t.Fatalf("不存在公告的已读统计应 404，实际 HTTP %d", status)
	}
}
