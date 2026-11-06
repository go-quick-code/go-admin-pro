//go:build integration

// 公告发布与受众的 HTTP 集成用例（issue #48，docs/oa-mvp-spec.md §3.4/§4.7/
// §4.14）：草稿→发布→通知落地→撤回→重新发布、发布后不可删除、四种受众
// 可见性、置顶排序、富文本 XSS 清洗、附件上传/下载、权限码逐码、租户隔离。
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t48，-p 1 串行）。
package oa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 菜单/按钮 ID（seed.go 521 段与 532 员工侧、5123 通知查询）。
const (
	announcementMenuID        = int64(521)
	announcementBtnCreate     = int64(5211)
	announcementBtnUpdate     = int64(5212)
	announcementBtnDelete     = int64(5213)
	announcementBtnPublish    = int64(5214)
	announcementMineMenuID    = int64(532)
	notifyQueryMenuID         = int64(5123)
	announcementPageSizeQuery = "?pageNo=1&pageSize=100"
)

// createAnnouncementAPI 创建公告，返回 (id, HTTP 状态, msg)。
func createAnnouncementAPI(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int64, int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/announcement", token, body)
	if api.Code != 0 {
		return 0, status, api.Msg
	}
	var data struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析创建公告响应: %v（data=%s）", err, string(api.Data))
	}
	return data.ID, status, ""
}

// publishAnnouncementAPI 发布/重新发布，返回 (HTTP 状态, msg)。
func publishAnnouncementAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/announcement/%d/publish", id), token, nil)
	return status, api.Msg
}

// withdrawAnnouncementAPI 撤回，返回 (HTTP 状态, msg)。
func withdrawAnnouncementAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/announcement/%d/withdraw", id), token, nil)
	return status, api.Msg
}

// myAnnouncements 员工「我的公告」列表，返回 id→title。
func myAnnouncements(t *testing.T, env *testutil.IntegrationEnv, token string) map[int64]string {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/announcement/my/page"+announcementPageSizeQuery, token, nil)
	mustOK(t, "我的公告列表", status, api)
	var page struct {
		List []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析我的公告: %v（data=%s）", err, string(api.Data))
	}
	out := make(map[int64]string, len(page.List))
	for _, row := range page.List {
		out[row.ID] = row.Title
	}
	return out
}

// adminAnnouncements 管理端公告列表，返回 id→status。
func adminAnnouncements(t *testing.T, env *testutil.IntegrationEnv, token string) map[int64]int8 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/announcement/page"+announcementPageSizeQuery, token, nil)
	mustOK(t, "管理端公告列表", status, api)
	var page struct {
		List []struct {
			ID     int64 `json:"id"`
			Status int8  `json:"status"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析管理端公告: %v（data=%s）", err, string(api.Data))
	}
	out := make(map[int64]int8, len(page.List))
	for _, row := range page.List {
		out[row.ID] = row.Status
	}
	return out
}

// announcementDetail 公告详情，返回 (HTTP 状态, data, msg)。
func announcementDetail(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/announcement/%d", id), token, nil)
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析公告详情: %v（data=%s）", err, string(api.Data))
	}
	return status, data, ""
}

// pageAnnouncementNotifies 当前用户 announcement 类型通知列表，返回
// bizID→title（用于断言发布通知落地）。
func pageAnnouncementNotifies(t *testing.T, env *testutil.IntegrationEnv, token string) map[int64]string {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/notify/message/page?pageNo=1&pageSize=100&type=announcement", token, nil)
	mustOK(t, "公告通知列表", status, api)
	var page struct {
		List []struct {
			Title   string `json:"title"`
			BizType string `json:"bizType"`
			BizID   int64  `json:"bizId"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析公告通知: %v（data=%s）", err, string(api.Data))
	}
	out := make(map[int64]string, len(page.List))
	for _, row := range page.List {
		if row.BizType == "oa_announcement" {
			out[row.BizID] = row.Title
		}
	}
	return out
}

// createUserWithRoles 用 admin 创建指定部门/角色的用户并登录，返回 (userID, token)。
func createUserWithRoles(t *testing.T, env *testutil.IntegrationEnv, admin, username string, deptID int64, roleIDs []int64) (int64, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "nickname": username, "password": "123456",
		"deptId": deptID, "status": 0, "roleIds": roleIDs,
	})
	mustOK(t, "创建用户 "+username, status, api)
	status, api = env.Request(t, http.MethodGet,
		"/system/user/page?username="+username+"&pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "查询用户 "+username, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("用户 %s 应唯一命中: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID, env.Login(t, username, "123456")
}

// uploadAnnouncementFileAPI 以 multipart 上传公告附件。
func uploadAnnouncementFileAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, name string, content []byte) (int, testutil.APIResponse) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("构造 multipart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL(fmt.Sprintf("/oa/announcement/%d/files", id)), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传附件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	return resp.StatusCode, api
}

// listAnnouncementFilesAPI 公告附件列表，返回 fileID→url。
func listAnnouncementFilesAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, map[int64]string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/announcement/%d/files", id), token, nil)
	if api.Code != 0 {
		return status, nil
	}
	var files []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(api.Data, &files); err != nil {
		t.Fatalf("解析附件列表: %v（data=%s）", err, string(api.Data))
	}
	out := make(map[int64]string, len(files))
	for _, f := range files {
		out[f.ID] = f.URL
	}
	return status, out
}

// downloadFile 以鉴权端点下载附件，返回 (HTTP 状态, 内容)。url 为后端返回的
// 相对路径（/admin-api/...），这里补上测试 server 的地址。
func downloadFile(t *testing.T, env *testutil.IntegrationEnv, token, url string) (int, []byte) {
	t.Helper()
	if strings.HasPrefix(url, "/") {
		url = env.Server.URL + url
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载附件: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取附件: %v", err)
	}
	return resp.StatusCode, body
}

// TestIntegrationAnnouncementLifecycle 覆盖公告闭环：
// 草稿→发布（通知落地）→发布后不可删除→撤回（列表不可见、详情显示已撤回、
// 通知保留）→重新发布；草稿可删除；已发布不可编辑。
func TestIntegrationAnnouncementLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")

	// 草稿：员工看不到，管理端列表可见。
	id, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "全员公告", "content": "<p>大家好</p>", "audienceType": 0, "pinned": true,
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建公告：HTTP %d msg %q", status, msg)
	}
	if got := adminAnnouncements(t, env, admin)[id]; got != 0 {
		t.Fatalf("新建公告状态 = %d，期望 0 草稿", got)
	}
	if _, ok := myAnnouncements(t, env, demo)[id]; ok {
		t.Fatal("草稿不应出现在员工列表")
	}

	// 发布：员工列表可见，通知落地（bizType=oa_announcement、bizId=公告 ID）。
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("发布公告：HTTP %d msg %q", status, msg)
	}
	if _, ok := myAnnouncements(t, env, demo)[id]; !ok {
		t.Fatal("已发布公告应出现在员工列表")
	}
	notifies := pageAnnouncementNotifies(t, env, admin)
	if _, ok := notifies[id]; !ok {
		t.Fatalf("发布后应给受众写 announcement 通知，实际 %v", notifies)
	}

	// 发布后不可删除、不可编辑（只能撤回）。
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/announcement/%d", id), admin, nil); status != http.StatusConflict {
		t.Fatalf("发布后删除应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodPut, "/oa/announcement", admin, map[string]any{
		"id": id, "title": "改标题", "content": "<p>x</p>", "audienceType": 0,
	}); status != http.StatusConflict {
		t.Fatalf("发布后编辑应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 撤回：列表不可见；直接打开详情仍可见「已撤回」；通知保留。
	if status, msg := withdrawAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("撤回公告：HTTP %d msg %q", status, msg)
	}
	if _, ok := myAnnouncements(t, env, demo)[id]; ok {
		t.Fatal("撤回后公告不应出现在员工列表")
	}
	status, detail, msg := announcementDetail(t, env, demo, id)
	if status != http.StatusOK {
		t.Fatalf("撤回后受众打开详情应成功（显示已撤回）：HTTP %d msg %q", status, msg)
	}
	if got := int8(detail["status"].(float64)); got != 2 {
		t.Fatalf("撤回公告详情 status = %d，期望 2", got)
	}
	if _, ok := pageAnnouncementNotifies(t, env, admin)[id]; !ok {
		t.Fatal("撤回不删除已写通知")
	}

	// 重新发布：回到员工列表，并再次投递通知。
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("重新发布：HTTP %d msg %q", status, msg)
	}
	if _, ok := myAnnouncements(t, env, demo)[id]; !ok {
		t.Fatal("重新发布后应回到员工列表")
	}

	// 草稿可删除（发布过的公告再删除仍 409）。
	id2, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "待删草稿", "content": "<p>draft</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("创建草稿：HTTP %d msg %q", status, msg)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/announcement/%d", id2), admin, nil); status != http.StatusOK {
		t.Fatalf("删除草稿应成功，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if _, ok := adminAnnouncements(t, env, admin)[id2]; ok {
		t.Fatal("已删除草稿不应出现在管理列表")
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/announcement/%d", id2), admin, nil); status != http.StatusNotFound {
		t.Fatalf("重复删除应 404，实际 HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationAnnouncementAudience 覆盖四种受众可见性（spec §4.7）：
// 全员；部门子树；角色；部门∪角色（命中任一即可见）。
func TestIntegrationAnnouncementAudience(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	subDept := createSubDept(t, env, admin, "公告子部门", 101)
	empRole := createRoleWithMenus(t, env, admin, "announcement_emp", []int64{announcementMineMenuID, notifyQueryMenuID})

	// 用户：子部门（无角色）、市场部（无角色）、市场部（持角色）、财务部（无角色）。
	_, subToken := createUserWithRoles(t, env, admin, "ann_sub", subDept, nil)
	_, otherToken := createUserWithRoles(t, env, admin, "ann_other", 102, nil)
	_, roleToken := createUserWithRoles(t, env, admin, "ann_role", 102, []int64{empRole})
	_, allToken := createUserWithRoles(t, env, admin, "ann_all", 103, nil)

	// 四条公告：全员 / 部门 101（含子树）/ 角色 / 部门∪角色。
	ids := map[string]int64{}
	specs := []struct {
		key      string
		body     map[string]any
		wantSub  bool
		wantOth  bool
		wantRole bool
		wantAll  bool
	}{
		{
			key: "all", body: map[string]any{"title": "全员公告", "content": "<p>all</p>", "audienceType": 0},
			wantSub: true, wantOth: true, wantRole: true, wantAll: true,
		},
		{
			key: "dept", body: map[string]any{
				"title": "部门公告", "content": "<p>dept</p>", "audienceType": 1,
				"targets": []map[string]any{{"targetType": 1, "targetId": 101}},
			},
			wantSub: true, wantOth: false, wantRole: false, wantAll: false,
		},
		{
			key: "role", body: map[string]any{
				"title": "角色公告", "content": "<p>role</p>", "audienceType": 1,
				"targets": []map[string]any{{"targetType": 2, "targetId": empRole}},
			},
			wantSub: false, wantOth: false, wantRole: true, wantAll: false,
		},
		{
			key: "both", body: map[string]any{
				"title": "部门或角色公告", "content": "<p>both</p>", "audienceType": 1,
				"targets": []map[string]any{
					{"targetType": 1, "targetId": 101},
					{"targetType": 2, "targetId": empRole},
				},
			},
			wantSub: true, wantOth: false, wantRole: true, wantAll: false,
		},
	}
	for _, spec := range specs {
		id, status, msg := createAnnouncementAPI(t, env, admin, spec.body)
		if status != http.StatusOK {
			t.Fatalf("创建公告 %s：HTTP %d msg %q", spec.key, status, msg)
		}
		if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
			t.Fatalf("发布公告 %s：HTTP %d msg %q", spec.key, status, msg)
		}
		ids[spec.key] = id
	}

	assertVisible := func(name, token, key string, want bool) {
		t.Helper()
		_, got := myAnnouncements(t, env, token)[ids[key]]
		if got != want {
			t.Fatalf("%s 对公告 %s 可见性 = %v，期望 %v", name, key, got, want)
		}
	}
	for _, spec := range specs {
		assertVisible("子部门用户", subToken, spec.key, spec.wantSub)
		assertVisible("市场部用户", otherToken, spec.key, spec.wantOth)
		assertVisible("持角色用户", roleToken, spec.key, spec.wantRole)
		assertVisible("财务部用户", allToken, spec.key, spec.wantAll)
	}

	// 不可见用户打开详情 404（不泄露存在性）；可见用户详情正常。
	if status, _, _ := announcementDetail(t, env, otherToken, ids["role"]); status != http.StatusNotFound {
		t.Fatalf("未命中角色的用户查看角色公告应 404，实际 HTTP %d", status)
	}
	if status, _, _ := announcementDetail(t, env, roleToken, ids["role"]); status != http.StatusOK {
		t.Fatalf("命中角色的用户查看角色公告应成功，实际 HTTP %d", status)
	}
}

// TestIntegrationAnnouncementXSSAndPinned 覆盖富文本清洗与置顶排序：
// script/onclick 被移除；置顶公告排在员工列表最前。
func TestIntegrationAnnouncementXSSAndPinned(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")

	normal, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "普通公告", "content": "<p>普通</p>", "audienceType": 0, "pinned": false,
	})
	if status != http.StatusOK {
		t.Fatalf("创建普通公告：HTTP %d msg %q", status, msg)
	}
	pinned, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "置顶公告", "content": `<p onclick="alert(1)">置顶内容</p><script>alert('xss')</script><h3>标题</h3>`,
		"audienceType": 0, "pinned": true,
	})
	if status != http.StatusOK {
		t.Fatalf("创建置顶公告：HTTP %d msg %q", status, msg)
	}
	for _, id := range []int64{normal, pinned} {
		if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
			t.Fatalf("发布公告 %d：HTTP %d msg %q", id, status, msg)
		}
	}

	// 置顶排序：员工列表第一条为置顶公告。
	status, api := env.Request(t, http.MethodGet, "/oa/announcement/my/page"+announcementPageSizeQuery, demo, nil)
	mustOK(t, "置顶排序查询", status, api)
	var page struct {
		List []struct {
			ID     int64 `json:"id"`
			Pinned bool  `json:"pinned"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析员工公告列表: %v", err)
	}
	if len(page.List) < 2 || page.List[0].ID != pinned || !page.List[0].Pinned {
		t.Fatalf("置顶公告应排在最前，实际 %+v", page.List)
	}

	// 富文本清洗：script 与事件处理器被移除，正常标签保留。
	status, detail, msg := announcementDetail(t, env, demo, pinned)
	if status != http.StatusOK {
		t.Fatalf("查看置顶公告：HTTP %d msg %q", status, msg)
	}
	content, _ := detail["content"].(string)
	if strings.Contains(strings.ToLower(content), "<script") || strings.Contains(strings.ToLower(content), "onclick") {
		t.Fatalf("富文本未被清洗：%s", content)
	}
	if !strings.Contains(content, "置顶内容") || !strings.Contains(content, "<h3>") {
		t.Fatalf("清洗后应保留正文与安全标签：%s", content)
	}
}

// TestIntegrationAnnouncementPermissions 权限码逐码（菜单种子 521 段）：
// 查询/创建/编辑/删除/发布各自控权；员工侧列表与已发布详情登录即可；
// 无管理查询码不能预览草稿（404）。
func TestIntegrationAnnouncementPermissions(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	readerRole := createRoleWithMenus(t, env, admin, "ann_reader", []int64{announcementMenuID})
	writerRole := createRoleWithMenus(t, env, admin, "ann_writer",
		[]int64{announcementMenuID, announcementBtnCreate, announcementBtnUpdate, announcementBtnDelete})
	employeeRole := createRoleWithMenus(t, env, admin, "ann_employee", []int64{announcementMineMenuID})

	reader := oaLoginNewUser(t, env, admin, "ann_reader_u", readerRole)
	writer := oaLoginNewUser(t, env, admin, "ann_writer_u", writerRole)
	employee := oaLoginNewUser(t, env, admin, "ann_employee_u", employeeRole)

	// 员工：我的公告与已发布详情登录即可；管理列表与写动作 403。
	if status, api := env.Request(t, http.MethodGet, "/oa/announcement/my/page", employee, nil); status != http.StatusOK {
		t.Fatalf("员工我的公告应登录即可：HTTP %d code %d", status, api.Code)
	}
	if status, api := env.Request(t, http.MethodGet, "/oa/announcement/page", employee, nil); status != http.StatusForbidden {
		t.Fatalf("无 oa:announcement:query 访问管理列表应 403，实际 HTTP %d code %d", status, api.Code)
	}
	if status, api := env.Request(t, http.MethodPost, "/oa/announcement", employee, map[string]any{
		"title": "无权公告", "content": "<p>x</p>", "audienceType": 0,
	}); status != http.StatusForbidden {
		t.Fatalf("无创建码应 403，实际 HTTP %d code %d", status, api.Code)
	}

	// 查询者：可看管理列表，不能创建/发布。
	if status, api := env.Request(t, http.MethodGet, "/oa/announcement/page", reader, nil); status != http.StatusOK {
		t.Fatalf("查询码应可看管理列表：HTTP %d code %d", status, api.Code)
	}
	if status, api := env.Request(t, http.MethodPost, "/oa/announcement", reader, map[string]any{
		"title": "无权公告", "content": "<p>x</p>", "audienceType": 0,
	}); status != http.StatusForbidden {
		t.Fatalf("查询者创建应 403，实际 HTTP %d code %d", status, api.Code)
	}

	// 写者：可创建/编辑/删除草稿，不能发布。
	id, status, msg := createAnnouncementAPI(t, env, writer, map[string]any{
		"title": "写者草稿", "content": "<p>w</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("写者创建草稿：HTTP %d msg %q", status, msg)
	}
	if status, api := env.Request(t, http.MethodPut, "/oa/announcement", writer, map[string]any{
		"id": id, "title": "写者草稿-改", "content": "<p>w2</p>", "audienceType": 0,
	}); status != http.StatusOK {
		t.Fatalf("写者编辑草稿应成功：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/announcement/%d/publish", id), writer, nil); status != http.StatusForbidden {
		t.Fatalf("无发布码应 403，实际 HTTP %d code %d", status, api.Code)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/announcement/%d", id), writer, nil); status != http.StatusOK {
		t.Fatalf("写者删除草稿应成功：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 无查询码不能预览草稿（404，不泄露存在性）。
	draftID, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "管理员草稿", "content": "<p>d</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("管理员创建草稿：HTTP %d msg %q", status, msg)
	}
	if status, _, _ := announcementDetail(t, env, employee, draftID); status != http.StatusNotFound {
		t.Fatalf("员工预览草稿应 404，实际 HTTP %d", status)
	}
	if status, _, msg := announcementDetail(t, env, admin, draftID); status != http.StatusOK {
		t.Fatalf("管理员预览草稿应成功：HTTP %d msg %q", status, msg)
	}
}

// TestIntegrationAnnouncementAttachment 覆盖附件：草稿上传/列表/鉴权下载/
// 删除；发布后上传 409；草稿删除时附件记录软删除（deleted_at 置位）。
func TestIntegrationAnnouncementAttachment(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	id, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "带附件公告", "content": "<p>附件</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("创建公告：HTTP %d msg %q", status, msg)
	}

	payload := []byte("hello announcement attachment")
	status, api := uploadAnnouncementFileAPI(t, env, admin, id, "notice.txt", payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("上传附件：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, files := listAnnouncementFilesAPI(t, env, admin, id)
	if status != http.StatusOK || len(files) != 1 {
		t.Fatalf("附件列表应为 1 条：HTTP %d files=%v", status, files)
	}
	var fileID int64
	var fileURL string
	for fid, url := range files {
		fileID, fileURL = fid, url
	}
	if !strings.Contains(fileURL, fmt.Sprintf("/oa/announcement/%d/files/%d/download", id, fileID)) {
		t.Fatalf("附件 URL 应为鉴权下载端点，实际 %q", fileURL)
	}
	status, got := downloadFile(t, env, admin, fileURL)
	if status != http.StatusOK || !bytes.Equal(got, payload) {
		t.Fatalf("下载附件：HTTP %d body=%q", status, got)
	}

	// 发布后不能再改附件（409）。
	if status, msg := publishAnnouncementAPI(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("发布：HTTP %d msg %q", status, msg)
	}
	if status, api := uploadAnnouncementFileAPI(t, env, admin, id, "late.txt", []byte("x")); status != http.StatusConflict {
		t.Fatalf("发布后上传附件应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 草稿删除：附件记录软删除（仍可通过 Unscoped 查到 deleted_at 非空）。
	draftID, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "带附件草稿", "content": "<p>d</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("创建草稿：HTTP %d msg %q", status, msg)
	}
	if status, api := uploadAnnouncementFileAPI(t, env, admin, draftID, "draft.txt", []byte("d")); status != http.StatusOK {
		t.Fatalf("草稿上传附件：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/announcement/%d", draftID), admin, nil); status != http.StatusOK {
		t.Fatalf("删除草稿：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var rows []model.File
	if err := tenant.Skip(env.DB).Unscoped().Model(&model.File{}).
		Where("biz_type = ? AND biz_id = ?", "oa_announcement", draftID).
		Find(&rows).Error; err != nil {
		t.Fatalf("查询附件记录: %v", err)
	}
	if len(rows) != 1 || !rows[0].DeletedAt.Valid {
		t.Fatalf("草稿删除后附件记录应软删除（保留行 + deleted_at），实际 %+v", rows)
	}
}

// TestIntegrationAnnouncementTenantIsolation 租户隔离（spec §4.6）：跨租户
// 业务 ID 一律 404；列表/通知互不可见；同标题/同受众互不影响。
func TestIntegrationAnnouncementTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	id1, status, msg := createAnnouncementAPI(t, env, admin, map[string]any{
		"title": "租户1公告", "content": "<p>t1</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("租户 1 创建公告：HTTP %d msg %q", status, msg)
	}
	if status, msg := publishAnnouncementAPI(t, env, admin, id1); status != http.StatusOK {
		t.Fatalf("租户 1 发布：HTTP %d msg %q", status, msg)
	}

	tenant2 := createOATenant(t, env, admin, "announcement_tenant_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)

	// 租户 2 列表看不到租户 1 的公告；同标题可独立创建。
	if _, ok := adminAnnouncements(t, env, token2)[id1]; ok {
		t.Fatal("租户 2 不应看到租户 1 的公告")
	}
	id2, status, msg := createAnnouncementAPI(t, env, token2, map[string]any{
		"title": "租户1公告", "content": "<p>t2</p>", "audienceType": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("租户 2 同标题创建：HTTP %d msg %q", status, msg)
	}
	if status, msg := publishAnnouncementAPI(t, env, token2, id2); status != http.StatusOK {
		t.Fatalf("租户 2 发布：HTTP %d msg %q", status, msg)
	}
	if _, ok := myAnnouncements(t, env, token2)[id1]; ok {
		t.Fatal("租户 2 员工列表不应包含租户 1 公告")
	}
	if _, ok := myAnnouncements(t, env, admin)[id2]; ok {
		t.Fatal("租户 1 员工列表不应包含租户 2 公告")
	}

	// 跨租户业务 ID 一律 404（详情/删除/发布/撤回/附件列表）。
	if status, _, _ := announcementDetail(t, env, token2, id1); status != http.StatusNotFound {
		t.Fatalf("跨租户详情应 404，实际 HTTP %d", status)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/announcement/%d", id2), admin, nil); status != http.StatusNotFound {
		t.Fatalf("跨租户删除应 404，实际 HTTP %d code %d", status, api.Code)
	}
	if status, _ := publishAnnouncementAPI(t, env, admin, id2); status != http.StatusNotFound {
		t.Fatalf("跨租户发布应 404，实际 HTTP %d", status)
	}
	if status, _ := withdrawAnnouncementAPI(t, env, admin, id2); status != http.StatusNotFound {
		t.Fatalf("跨租户撤回应 404，实际 HTTP %d", status)
	}
	if status, _ := listAnnouncementFilesAPI(t, env, admin, id2); status != http.StatusNotFound {
		t.Fatalf("跨租户附件列表应 404，实际 HTTP %d", status)
	}
}
