//go:build integration

// 站内通知主 seam 集成测试（issue #45，docs/oa-mvp-spec.md §3.2/§4.13/§5）。
//
// 约定的两条 seam：
//  1. 写 seam notifyservice.CreateInTx / Create —— bpm/公告/会议室生产者的
//     程序化投递入口：覆盖按接收人批量物化、与调用方事务同生共死（commit 落库、
//     rollback 蒸发、写失败传播后回滚）、输入契约（校验失败整批不写、空批 no-op、
//     nil tx 拒绝）。捕捉不到 HTTP 鉴权与分页行为。
//  2. 读 seam /admin-api/notify/message/*（真实 router + Auth + 租户插件 +
//     权限中间件）：覆盖本人 + 租户隔离、未读数、单条/全部标记已读幂等、
//     notify:message:query 权限门。捕捉不到生产者侧的事务语义。
//
// 测试库走 GAP_TEST_DATABASE_DSN（协调约定用 go_admin_pro_test_t45 后缀）：
//
//	GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test_t45?charset=utf8mb4&parseTime=True' \
//	  go test -tags=integration ./internal/module/notify/...
package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// newNotifyService 以 env.DB 装配 notify service（与 app.go 生产装配同构）：
// 生产者（bpm/公告/会议室）就是这样以程序化方式依赖 notify 模块的。
func newNotifyService(env *testutil.IntegrationEnv) *service.Service {
	return service.New(repository.New(env.DB))
}

// mustCode0 断言业务成功。
func mustCode0(tb testing.TB, status int, api testutil.APIResponse, what string) {
	tb.Helper()
	if status != http.StatusOK || api.Code != 0 {
		tb.Fatalf("%s: HTTP %d code %d msg %q", what, status, api.Code, api.Msg)
	}
}

// wantCode 断言业务码为指定值（失败用例）。
func wantCode(tb testing.TB, status int, api testutil.APIResponse, what string, want int) {
	tb.Helper()
	if api.Code != want {
		tb.Fatalf("%s: code = %d (HTTP %d, msg %q), want code %d", what, api.Code, status, api.Msg, want)
	}
}

// pageMessages 请求通知分页并解码 list/total。
func pageMessages(tb testing.TB, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	tb.Helper()
	status, api := env.Request(tb, http.MethodGet, "/notify/message/page"+query, token, nil)
	mustCode0(tb, status, api, "GET /notify/message/page"+query)
	var list []map[string]any
	var total int64
	if err := json.Unmarshal(api.Data, &struct {
		List  *[]map[string]any `json:"list"`
		Total *int64            `json:"total"`
	}{&list, &total}); err != nil {
		tb.Fatalf("解码分页响应: %v（data=%s）", err, string(api.Data))
	}
	return list, total
}

// unreadCount 请求当前用户未读数。
func unreadCount(tb testing.TB, env *testutil.IntegrationEnv, token string) int64 {
	tb.Helper()
	status, api := env.Request(tb, http.MethodGet, "/notify/message/unread-count", token, nil)
	mustCode0(tb, status, api, "GET /notify/message/unread-count")
	var data struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		tb.Fatalf("解码未读数响应: %v（data=%s）", err, string(api.Data))
	}
	return data.Count
}

// markRead 单条标记已读。
func markRead(tb testing.TB, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	tb.Helper()
	return env.Request(tb, http.MethodPut, fmt.Sprintf("/notify/message/%d/read", id), token, nil)
}

// messageID 从分页行取 id。
func messageID(tb testing.TB, row map[string]any) int64 {
	tb.Helper()
	v, ok := row["id"].(float64)
	if !ok {
		tb.Fatalf("分页行缺少数值 id: %v", row)
	}
	return int64(v)
}

// userIDByName 经系统用户分页按用户名反查 ID（测试内不直查库拿业务 ID）。
func userIDByName(tb testing.TB, env *testutil.IntegrationEnv, adminToken, username string) int64 {
	tb.Helper()
	status, api := env.Request(tb, http.MethodGet,
		"/system/user/page?pageNo=1&pageSize=100&username="+username, adminToken, nil)
	mustCode0(tb, status, api, "查询用户 "+username)
	var data struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		tb.Fatalf("解码用户分页: %v", err)
	}
	for _, u := range data.List {
		if u.Username == username {
			return u.ID
		}
	}
	tb.Fatalf("未找到用户 %s", username)
	return 0
}

// TestNotifyMessagePageUnreadMarkRead_HTTP 覆盖读 seam 主链路（admin）：
// 批量投递 → 未读数 → 分页（倒序/过滤）→ 单条已读（幂等）→ 全部已读（幂等），
// 以及租户隔离（租户 2 的通知对租户 1 用户不可见）。
func TestNotifyMessagePageUnreadMarkRead_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	notifySvc := newNotifyService(env)
	adminToken := env.Login(t, "admin", "123456")
	// 生产者在请求上下文里调用（租户插件 fail-closed，无租户上下文会拒绝写入）。
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)

	// 生产者批量投递：admin（用户 1）3 条，类型/路由各异。
	err := notifySvc.Create(ctx, []service.Input{
		{UserID: 1, Type: model.TypeBpmTodo, Title: "您有新的审批任务", Content: "张三提交了请假申请",
			BizType: "bpm_task", BizID: 11, Route: "/bpm/task-detail", RouteParams: `{"taskId":11}`},
		{UserID: 1, Type: model.TypeBpmTodo, Title: "您有新的报销审批", Content: "李四提交了报销单", BizType: "bpm_task", BizID: 12},
		{UserID: 1, Type: model.TypeAnnouncement, Title: "系统公告", Content: "五一放假安排", BizType: "oa_announcement", BizID: 5},
	})
	if err != nil {
		t.Fatalf("批量投递: %v", err)
	}
	// 租户 2 的通知（跨租户，经 service ctx 指定租户投递）。
	if err := notifySvc.Create(tenant.WithTenantID(ctx, 2), []service.Input{
		{UserID: 1, Type: model.TypeBpmTodo, Title: "租户2的任务"},
	}); err != nil {
		t.Fatalf("租户 2 投递: %v", err)
	}

	// 未读数：只统计本租户本人的 3 条。
	if got := unreadCount(t, env, adminToken); got != 3 {
		t.Fatalf("unread-count = %d, want 3（租户 2 的通知不得计入）", got)
	}

	// 分页：id 倒序 + 字段完整（title/content/biz/route/readAt）。
	list, total := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10")
	if total != 3 || len(list) != 3 {
		t.Fatalf("page total=%d len=%d, want 3/3", total, len(list))
	}
	first := list[0]
	if first["type"] != model.TypeAnnouncement || first["title"] != "系统公告" {
		t.Fatalf("最新一条应为 announcement，got %v/%v", first["type"], first["title"])
	}
	if first["bizType"] != "oa_announcement" || first["bizId"].(float64) != 5 {
		t.Fatalf("biz 字段不符: %v", first)
	}
	if first["readAt"] != nil {
		t.Fatalf("未读通知 readAt 应为 null, got %v", first["readAt"])
	}
	if first["tenantId"].(float64) != 1 {
		t.Fatalf("tenantId = %v, want 1", first["tenantId"])
	}
	// 倒序：最旧的一条（含跳转目标）在末尾。
	last := list[len(list)-1]
	if last["route"] != "/bpm/task-detail" || last["routeParams"] != `{"taskId":11}` {
		t.Fatalf("最旧一条的跳转目标不符: %v/%v", last["route"], last["routeParams"])
	}

	// 过滤：type / read。
	if _, n := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&type=bpm_todo"); n != 2 {
		t.Fatalf("type=bpm_todo total=%d, want 2", n)
	}
	if _, n := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&type=announcement"); n != 1 {
		t.Fatalf("type=announcement total=%d, want 1", n)
	}
	if _, n := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&read=false"); n != 3 {
		t.Fatalf("read=false total=%d, want 3", n)
	}
	if _, n := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&read=true"); n != 0 {
		t.Fatalf("read=true total=%d, want 0", n)
	}
	// 非法类型过滤直接拒绝。
	status, api := env.Request(t, http.MethodGet, "/notify/message/page?type=email", adminToken, nil)
	wantCode(t, status, api, "非法 type 过滤", 400)

	// 分页裁剪：pageSize=2 第一页应是最新两条。
	page1, total1 := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=2")
	if total1 != 3 || len(page1) != 2 || page1[0]["title"] != "系统公告" {
		t.Fatalf("pageSize=2 首页不符: total=%d len=%d", total1, len(page1))
	}

	// 单条标记已读（取最旧一条）→ 未读数 2 → 分页 read=true 1 条且带已读时间。
	oldestID := messageID(t, last)
	status, api = markRead(t, env, adminToken, oldestID)
	mustCode0(t, status, api, "标记已读")
	if got := unreadCount(t, env, adminToken); got != 2 {
		t.Fatalf("标记一条后 unread-count = %d, want 2", got)
	}
	readList, readTotal := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&read=true")
	if readTotal != 1 || len(readList) != 1 || readList[0]["readAt"] == nil {
		t.Fatalf("read=true 应为 1 条且带 readAt: total=%d row=%v", readTotal, readList)
	}
	if _, ok := readList[0]["readAt"].(string); !ok {
		t.Fatalf("readAt 应为时间字符串, got %T", readList[0]["readAt"])
	}

	// 幂等：重复标记同一条仍 200，未读数不变。
	status, api = markRead(t, env, adminToken, oldestID)
	mustCode0(t, status, api, "重复标记已读（幂等）")
	if got := unreadCount(t, env, adminToken); got != 2 {
		t.Fatalf("重复标记后 unread-count = %d, want 2", got)
	}

	// 全部已读（幂等）。
	status, api = env.Request(t, http.MethodPut, "/notify/message/read-all", adminToken, nil)
	mustCode0(t, status, api, "全部已读")
	if got := unreadCount(t, env, adminToken); got != 0 {
		t.Fatalf("全部已读后 unread-count = %d, want 0", got)
	}
	status, api = env.Request(t, http.MethodPut, "/notify/message/read-all", adminToken, nil)
	mustCode0(t, status, api, "重复全部已读（幂等）")
	if got := unreadCount(t, env, adminToken); got != 0 {
		t.Fatalf("重复全部已读后 unread-count = %d, want 0", got)
	}
	// 全部已读只触碰未读行：早先那条的已读时间不被覆盖（仍可按已读查出）。
	_, allReadTotal := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&read=true")
	if allReadTotal != 3 {
		t.Fatalf("read=true total = %d, want 3", allReadTotal)
	}
}

// TestNotifyMessageSelfIsolation_HTTP 本人隔离 + 权限门：
//   - 持 notify:message:query 的普通用户（jack，经真实菜单→角色→用户授权链路）
//     只能看到/操作自己的通知；
//   - 标记他人通知 404，且他人的通知保持未读；
//   - 未授权用户（rose）page 403（权限门生效），unread-count 登录即可用。
func TestNotifyMessageSelfIsolation_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	notifySvc := newNotifyService(env)
	adminToken := env.Login(t, "admin", "123456")

	// 授权链路（真实 API）：按钮菜单（notify:message:query）→ 角色 → 用户 jack。
	status, api := env.Request(t, http.MethodPost, "/system/menu", adminToken, map[string]any{
		"name": "通知查询", "permission": "notify:message:query", "type": 3, "parentId": 0, "sort": 99,
	})
	mustCode0(t, status, api, "创建按钮菜单")
	status, api = env.Request(t, http.MethodGet, "/system/menu/list", adminToken, nil)
	mustCode0(t, status, api, "菜单列表")
	var menus []struct {
		ID         int64  `json:"id"`
		Permission string `json:"permission"`
	}
	if err := json.Unmarshal(api.Data, &menus); err != nil {
		t.Fatalf("解码菜单列表: %v", err)
	}
	var menuID int64
	for _, m := range menus {
		if m.Permission == "notify:message:query" {
			menuID = m.ID
		}
	}
	if menuID == 0 {
		t.Fatal("未找到 notify:message:query 菜单")
	}
	status, api = env.Request(t, http.MethodPost, "/system/role", adminToken, map[string]any{
		"name": "通知用户", "code": "notify_user", "dataScope": 1, "menuIds": []int64{menuID},
	})
	mustCode0(t, status, api, "创建角色")
	status, api = env.Request(t, http.MethodGet, "/system/role/page?pageNo=1&pageSize=50", adminToken, nil)
	mustCode0(t, status, api, "角色分页")
	var roles struct {
		List []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &roles); err != nil {
		t.Fatalf("解码角色分页: %v", err)
	}
	var notifyRoleID int64
	for _, r := range roles.List {
		if r.Code == "notify_user" {
			notifyRoleID = r.ID
		}
	}
	if notifyRoleID == 0 {
		t.Fatal("未找到 notify_user 角色")
	}
	roleIDs := map[string][]int64{"jack": {notifyRoleID}, "rose": nil}
	for _, u := range []string{"jack", "rose"} {
		status, api = env.Request(t, http.MethodPost, "/system/user", adminToken, map[string]any{
			"username": u, "password": "123456", "nickname": u, "roleIds": roleIDs[u],
		})
		mustCode0(t, status, api, "创建用户 "+u)
	}
	jackToken := env.Login(t, "jack", "123456")
	roseToken := env.Login(t, "rose", "123456")
	jackID := userIDByName(t, env, adminToken, "jack")

	// 投递：admin 2 条、jack 1 条（按查到的 jack ID 物化）。
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	if err := notifySvc.Create(ctx, []service.Input{
		{UserID: 1, Type: model.TypeBpmResult, Title: "审批通过", Content: "请假单已通过"},
		{UserID: 1, Type: model.TypeBpmTodo, Title: "admin 的待办"},
		{UserID: jackID, Type: model.TypeBpmTodo, Title: "jack 的待办", BizType: "bpm_task", BizID: 21},
	}); err != nil {
		t.Fatalf("批量投递: %v", err)
	}

	// rose（无角色）：page 被权限门拦下；unread-count 登录即可用且只统计自己。
	status, api = env.Request(t, http.MethodGet, "/notify/message/page?pageNo=1&pageSize=10", roseToken, nil)
	if status == http.StatusOK && api.Code == 0 {
		t.Fatal("无权限用户的 page 不应放行")
	}
	if got := unreadCount(t, env, roseToken); got != 0 {
		t.Fatalf("rose 未读数 = %d, want 0（本人隔离）", got)
	}

	// jack 只看到自己的 1 条。
	jackList, jackTotal := pageMessages(t, env, jackToken, "?pageNo=1&pageSize=10")
	if jackTotal != 1 || len(jackList) != 1 || jackList[0]["title"] != "jack 的待办" {
		t.Fatalf("jack 分页应只有自己的 1 条: total=%d list=%v", jackTotal, jackList)
	}
	if got := unreadCount(t, env, jackToken); got != 1 {
		t.Fatalf("jack 未读数 = %d, want 1", got)
	}
	// admin 只看到自己的 2 条。
	if _, adminTotal := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10"); adminTotal != 2 {
		t.Fatalf("admin 分页 total = %d, want 2（本人隔离）", adminTotal)
	}

	// jack 标记 admin 的未读通知 → 404；admin 的通知保持未读。
	adminList, _ := pageMessages(t, env, adminToken, "?pageNo=1&pageSize=10&type=bpm_todo")
	adminTodoID := messageID(t, adminList[0])
	status, api = markRead(t, env, jackToken, adminTodoID)
	wantCode(t, status, api, "jack 标记 admin 的通知", 404)
	if got := unreadCount(t, env, adminToken); got != 2 {
		t.Fatalf("他人标记后 admin 未读数 = %d, want 2（未被打掉）", got)
	}

	// jack 标记自己的 → 成功；rose 标记 jack 的 → 404。
	jackMsgID := messageID(t, jackList[0])
	status, api = markRead(t, env, jackToken, jackMsgID)
	mustCode0(t, status, api, "jack 标记自己的通知")
	status, api = markRead(t, env, roseToken, jackMsgID)
	wantCode(t, status, api, "rose 标记 jack 的通知", 404)
	if got := unreadCount(t, env, jackToken); got != 0 {
		t.Fatalf("jack 标记后未读数 = %d, want 0", got)
	}

	// 不存在的 ID 404（与幂等成功区分开）。
	status, api = markRead(t, env, jackToken, 999999)
	wantCode(t, status, api, "标记不存在的通知", 404)
}

// TestNotifyCreateInTxSeam 写 seam 的事务语义（spec §4.13：与触发事件同一事务，
// 写失败源操作回滚）。以 system_dict_type 行模拟「调用方的源操作写入」：
// 它与通知同处一个事务，用它的存亡证明事务边界。
func TestNotifyCreateInTxSeam(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	notifySvc := newNotifyService(env)
	// 源操作标记行写在租户 1（与默认登录租户一致），租户表写入需要租户上下文。
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)

	marker := func(name string) *sysmodel.DictType {
		return &sysmodel.DictType{Name: name, Type: name, Status: sysmodel.StatusEnable}
	}
	// countByType 返回「十位：指定标记行的源操作行数；个位：指定 biz_type 通知行数」，
	// 用单一数字同时断言源操作与通知两边的存亡。每步用独立的标记名与 biz_type，
	// 避免跨步计数串扰。
	countByType := func(dictType, bizType string) int64 {
		t.Helper()
		var n int64
		if err := tenant.Skip(env.DB).Model(&sysmodel.DictType{}).
			Where("type = ?", dictType).Count(&n).Error; err != nil {
			t.Fatalf("统计标记行: %v", err)
		}
		var m int64
		if err := tenant.Skip(env.DB).Model(&model.Message{}).
			Where("biz_type = ?", bizType).Count(&m).Error; err != nil {
			t.Fatalf("统计通知行: %v", err)
		}
		return n*10 + m
	}
	batch := func(bizType string) []service.Input {
		return []service.Input{
			{UserID: 1, Type: model.TypeBpmTodo, Title: "seam 待办 1", BizType: bizType},
			{UserID: 2, Type: model.TypeBpmResult, Title: "seam 结果 2", BizType: bizType},
		}
	}

	// 1) 同事务成功：源操作 + 通知随调用方 commit 一起落库（含多接收人物化）。
	tx1 := env.DB.WithContext(ctx).Begin()
	if tx1.Error != nil {
		t.Fatalf("开启事务: %v", tx1.Error)
	}
	if err := tx1.Create(marker("t45_marker_commit")).Error; err != nil {
		t.Fatalf("写源操作: %v", err)
	}
	if err := notifySvc.CreateInTx(ctx, tx1, batch("t45_seam_commit")); err != nil {
		tx1.Rollback()
		t.Fatalf("CreateInTx: %v", err)
	}
	if err := tx1.Commit().Error; err != nil {
		t.Fatalf("提交事务: %v", err)
	}
	if got := countByType("t45_marker_commit", "t45_seam_commit"); got != 12 {
		t.Fatalf("commit 后存亡 = %d（十位源操作/个位通知）, want 12（1 源操作 + 2 条通知按接收人物化）", got)
	}

	// 2) 调用方显式 rollback：写入成功过也一并蒸发——证明通知挂在调用方事务上，
	//    不是独立连接的隐式自动提交。
	tx2 := env.DB.WithContext(ctx).Begin()
	if tx2.Error != nil {
		t.Fatalf("开启事务: %v", tx2.Error)
	}
	if err := tx2.Create(marker("t45_marker_rollback")).Error; err != nil {
		t.Fatalf("写源操作: %v", err)
	}
	if err := notifySvc.CreateInTx(ctx, tx2, batch("t45_seam_rollback")); err != nil {
		tx2.Rollback()
		t.Fatalf("CreateInTx: %v", err)
	}
	if err := tx2.Rollback().Error; err != nil {
		t.Fatalf("回滚事务: %v", err)
	}
	if got := countByType("t45_marker_rollback", "t45_seam_rollback"); got != 0 {
		t.Fatalf("rollback 后存亡 = %d, want 0（源操作与通知都应蒸发）", got)
	}

	// 3) 写失败传播：通知写入失败（ctx 已取消，gorm 拒绝执行）→ 返回错误 →
	//    调用方回滚 → 源操作不落库。
	tx3 := env.DB.WithContext(ctx).Begin()
	if tx3.Error != nil {
		t.Fatalf("开启事务: %v", tx3.Error)
	}
	if err := tx3.Create(marker("t45_marker_fail")).Error; err != nil {
		t.Fatalf("写源操作: %v", err)
	}
	failCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := notifySvc.CreateInTx(failCtx, tx3, batch("t45_seam_fail")); err == nil {
		tx3.Rollback()
		t.Fatal("写失败应返回错误，got nil")
	}
	if err := tx3.Rollback().Error; err != nil {
		t.Fatalf("回滚事务: %v", err)
	}
	if got := countByType("t45_marker_fail", "t45_seam_fail"); got != 0 {
		t.Fatalf("写失败回滚后存亡 = %d, want 0", got)
	}

	// 4) 输入契约：任一条非法 → 整批不写，错误交调用方处理（这里选择提交源操作，
	//    验证「合法一条也不落库」且源操作不受污染）。
	tx4 := env.DB.WithContext(ctx).Begin()
	if tx4.Error != nil {
		t.Fatalf("开启事务: %v", tx4.Error)
	}
	if err := tx4.Create(marker("t45_marker_invalid")).Error; err != nil {
		t.Fatalf("写源操作: %v", err)
	}
	badBatch := []service.Input{
		{UserID: 1, Type: model.TypeBpmTodo, Title: "合法的一条", BizType: "t45_seam_invalid"},
		{UserID: 2, Type: "email", Title: "非法类型", BizType: "t45_seam_invalid"},
	}
	if err := notifySvc.CreateInTx(ctx, tx4, badBatch); err == nil {
		tx4.Rollback()
		t.Fatal("非法输入应返回错误，got nil")
	}
	if err := tx4.Commit().Error; err != nil {
		t.Fatalf("提交事务: %v", err)
	}
	if got := countByType("t45_marker_invalid", "t45_seam_invalid"); got != 10 {
		t.Fatalf("非法批次后存亡 = %d, want 10（合法一条也不落库，源操作照常提交）", got)
	}

	// 5) 空批次 no-op：不报错、不写行（候选接收人为空的生产者流）。
	if err := notifySvc.CreateInTx(ctx, env.DB, nil); err != nil {
		t.Fatalf("空批次应 no-op: %v", err)
	}
	if err := notifySvc.Create(ctx, []service.Input{}); err != nil {
		t.Fatalf("空批次 Create 应 no-op: %v", err)
	}

	// 6) nil 事务句柄直接拒绝：通知不允许脱离调用方事务隐式自动提交。
	if err := notifySvc.CreateInTx(ctx, nil, batch("t45_seam_nil")); err == nil {
		t.Fatal("CreateInTx(nil tx) 应报错")
	}

	// 7) Create（自开事务）便捷入口：正常落库（meeting_reminder 类型覆盖枚举全量）。
	if err := notifySvc.Create(ctx, []service.Input{
		{UserID: 1, Type: model.TypeMeetingReminder, Title: "会议 10 分钟后开始", BizType: "oa_room_booking", BizID: 3,
			Route: "/oa/calendar", RouteParams: `{"bookingId":3}`},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var n int64
	if err := tenant.Skip(env.DB).Model(&model.Message{}).
		Where("type = ?", model.TypeMeetingReminder).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("meeting_reminder 通知数 = %d err=%v, want 1", n, err)
	}
}
