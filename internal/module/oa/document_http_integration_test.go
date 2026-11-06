//go:build integration

// 文档目录与 ACL 的 HTTP 集成用例（issue #58，docs/oa-mvp-spec.md §3.8/§4.12）：
//   - 目录树 CRUD：创建顶层/子目录、改名、删除；非空目录删除 409；
//   - 四类 ACL 主体（全员/部门子树/角色/用户）的可见性；
//   - 最近祖先继承与子目录覆盖父目录（不叠加）；无 ACL 顶层仅 owner/管理员可见；
//   - owner 旁路与 oa:document:manage 管理员旁路；
//   - 父目录不可见时子目录提升为根节点（可见子树不从导航消失）；
//   - ACL 整体替换、顶层目录清空 ACL 400、顶层创建缺 ACL 400；
//   - 权限码（query/create）与目录管理权（owner 或 manage，403/404 区分）；
//   - 跨租户 404、菜单种子 526/5261–5263/537。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t58，-p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 文档菜单/按钮码（菜单 526 + 5261–5263，员工页 537；issue #58 种子）。
const (
	documentMenuID        = int64(526)
	documentCreateBtn     = int64(5261)
	documentManageBtn     = int64(5262)
	documentUploadBtn     = int64(5263)
	documentMineMenuID    = int64(537)
	documentACLTargetAll  = int8(0)
	documentACLTargetDept = int8(1)
	documentACLTargetRole = int8(2)
	documentACLTargetUser = int8(3)
)

// documentACLTargetBody 是一条 ACL 请求条目。
type documentACLTargetBody struct {
	TargetType int8  `json:"targetType"`
	TargetID   int64 `json:"targetId"`
}

// documentFolderBody 是创建/改名请求体。
type documentFolderBody struct {
	ID       int64                   `json:"id,omitempty"`
	ParentID int64                   `json:"parentId"`
	Name     string                  `json:"name"`
	Sort     int                     `json:"sort"`
	ACL      []documentACLTargetBody `json:"acl,omitempty"`
}

// documentFolderResp 是目录树/详情响应节点。
type documentFolderResp struct {
	ID            int64                `json:"id"`
	ParentID      int64                `json:"parentId"`
	Name          string               `json:"name"`
	OwnerID       int64                `json:"ownerId"`
	Sort          int                  `json:"sort"`
	ACLConfigured bool                 `json:"aclConfigured"`
	CreateTime    string               `json:"createTime"`
	UpdateTime    string               `json:"updateTime"`
	Children      []documentFolderResp `json:"children"`
}

// documentACLItemResp 是 ACL 条目视图。
type documentACLItemResp struct {
	TargetType int8   `json:"targetType"`
	TargetID   int64  `json:"targetId"`
	TargetName string `json:"targetName"`
}

// documentFolderACLResp 是目录 ACL 视图。
type documentFolderACLResp struct {
	FolderID         int64                 `json:"folderId"`
	OwnerID          int64                 `json:"ownerId"`
	Explicit         bool                  `json:"explicit"`
	SourceFolderID   int64                 `json:"sourceFolderId"`
	SourceFolderName string                `json:"sourceFolderName"`
	Entries          []documentACLItemResp `json:"entries"`
	EffectiveEntries []documentACLItemResp `json:"effectiveEntries"`
}

// documentTarget 构造 ACL 条目。
func documentTarget(targetType int8, targetID int64) documentACLTargetBody {
	return documentACLTargetBody{TargetType: targetType, TargetID: targetID}
}

// createDocumentFolderAPI 创建目录并断言成功。
func createDocumentFolderAPI(t *testing.T, env *testutil.IntegrationEnv, token string, parentID int64, name string, acl ...documentACLTargetBody) documentFolderResp {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/document/folder", token, documentFolderBody{
		ParentID: parentID, Name: name, ACL: acl,
	})
	mustOK(t, "创建目录 "+name, status, api)
	return decodeDocumentFolder(t, api)
}

// decodeDocumentFolder 解析目录响应。
func decodeDocumentFolder(t *testing.T, api testutil.APIResponse) documentFolderResp {
	t.Helper()
	var out documentFolderResp
	if err := json.Unmarshal(api.Data, &out); err != nil {
		t.Fatalf("解析目录响应: %v（data=%s）", err, string(api.Data))
	}
	return out
}

// listDocumentFolderTreeAPI 读取可见目录树；业务失败返回 nil。
func listDocumentFolderTreeAPI(t *testing.T, env *testutil.IntegrationEnv, token string) (int, testutil.APIResponse, []documentFolderResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/document/folder/tree", token, nil)
	if api.Code != 0 {
		return status, api, nil
	}
	var rows []documentFolderResp
	if err := json.Unmarshal(api.Data, &rows); err != nil {
		t.Fatalf("解析目录树: %v（data=%s）", err, string(api.Data))
	}
	return status, api, rows
}

// getDocumentFolderAPI 读取目录详情；业务失败返回空行。
func getDocumentFolderAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse, documentFolderResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/document/folder/%d", id), token, nil)
	if api.Code != 0 {
		return status, api, documentFolderResp{}
	}
	return status, api, decodeDocumentFolder(t, api)
}

// getDocumentFolderACLAPI 读取目录 ACL；业务失败返回 nil。
func getDocumentFolderACLAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse, *documentFolderACLResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/document/folder/%d/acl", id), token, nil)
	if api.Code != 0 {
		return status, api, nil
	}
	var out documentFolderACLResp
	if err := json.Unmarshal(api.Data, &out); err != nil {
		t.Fatalf("解析目录 ACL: %v（data=%s）", err, string(api.Data))
	}
	return status, api, &out
}

// setDocumentFolderACLAPI 整体替换目录 ACL。
func setDocumentFolderACLAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, entries []documentACLTargetBody) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, fmt.Sprintf("/oa/document/folder/%d/acl", id), token, map[string]any{
		"entries": entries,
	})
}

// renameDocumentFolderAPI 改名/调整排序。
func renameDocumentFolderAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, name string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/oa/document/folder", token, map[string]any{
		"id": id, "name": name, "sort": 0,
	})
}

// deleteDocumentFolderAPI 删除目录。
func deleteDocumentFolderAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/document/folder/%d", id), token, nil)
}

// documentFlatten 深度优先展开目录树（按名称索引；测试内目录名唯一）。
func documentFlatten(nodes []documentFolderResp) map[string]documentFolderResp {
	out := make(map[string]documentFolderResp)
	var walk func([]documentFolderResp)
	walk = func(rows []documentFolderResp) {
		for _, row := range rows {
			out[row.Name] = row
			walk(row.Children)
		}
	}
	walk(nodes)
	return out
}

// assertDocumentVisibility 断言可见/不可见目录集合（按名称）。
func assertDocumentVisibility(t *testing.T, phase string, env *testutil.IntegrationEnv, token string, wantVisible, wantHidden []string) {
	t.Helper()
	status, api, rows := listDocumentFolderTreeAPI(t, env, token)
	mustOK(t, phase, status, api)
	got := documentFlatten(rows)
	for _, name := range wantVisible {
		if _, ok := got[name]; !ok {
			t.Fatalf("%s：应看到目录 %q，实际可见 %v", phase, name, documentNamesOf(got))
		}
	}
	for _, name := range wantHidden {
		if _, ok := got[name]; ok {
			t.Fatalf("%s：不应看到目录 %q，实际可见 %v", phase, name, documentNamesOf(got))
		}
	}
}

// documentNamesOf 提取目录名集合（失败信息用）。
func documentNamesOf(rows map[string]documentFolderResp) []string {
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	return names
}

// bindDocumentRoles 给用户绑定角色并保留部门（系统用户更新的最小请求体）。
func bindDocumentRoles(t *testing.T, env *testutil.IntegrationEnv, admin, username string, userID, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/system/user", admin, map[string]any{
		"id": userID, "username": username, "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	mustOK(t, "绑定文档角色 "+username, status, api)
}

// insertDocumentFolderRaw 直接插入一条历史目录行（模拟无 ACL 顶层等脏数据；
// 集成环境没有创建「无 ACL 顶层」的 API 入口）。
func insertDocumentFolderRaw(t *testing.T, env *testutil.IntegrationEnv, parentID int64, name string, ownerID int64) int64 {
	t.Helper()
	if err := env.DB.Exec(
		"INSERT INTO `oa_document_folder` (`tenant_id`, `parent_id`, `name`, `owner_id`, `sort`) VALUES (1, ?, ?, ?, 0)",
		parentID, name, ownerID,
	).Error; err != nil {
		t.Fatalf("插入目录 %s: %v", name, err)
	}
	var id int64
	if err := env.DB.Raw(
		"SELECT `id` FROM `oa_document_folder` WHERE `tenant_id` = 1 AND `name` = ? ORDER BY `id` DESC LIMIT 1",
		name,
	).Scan(&id).Error; err != nil {
		t.Fatalf("回读目录 %s: %v", name, err)
	}
	if id <= 0 {
		t.Fatalf("回读目录 %s 失败", name)
	}
	return id
}

// insertDocumentFileRaw 直接插入一条文件归属行（上传接口属 #59；删除约束
// 需要 file 表已有数据）。
func insertDocumentFileRaw(t *testing.T, env *testutil.IntegrationEnv, folderID int64) {
	t.Helper()
	if err := env.DB.Exec(
		"INSERT INTO `oa_document_file` (`tenant_id`, `folder_id`, `file_id`, `name`, `size`, `type`, `uploader_id`) "+
			"VALUES (1, ?, 900001, '约束测试.txt', 12, 'text/plain', 1)",
		folderID,
	).Error; err != nil {
		t.Fatalf("插入文件归属行: %v", err)
	}
}

// TestIntegrationDocumentFolderCRUD 目录树 CRUD 与非空目录删除约束。
func TestIntegrationDocumentFolderCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	root := createDocumentFolderAPI(t, env, admin, 0, "技术资料", documentTarget(documentACLTargetAll, 0))
	if root.ID <= 0 || root.OwnerID <= 0 {
		t.Fatalf("创建响应缺少 id/ownerId: %+v", root)
	}
	if root.Name != "技术资料" || root.ParentID != 0 || !root.ACLConfigured {
		t.Fatalf("顶层目录回显不符: %+v", root)
	}
	if root.CreateTime == "" || root.UpdateTime == "" {
		t.Fatalf("目录缺少时间字段: %+v", root)
	}

	status, api, detail := getDocumentFolderAPI(t, env, admin, root.ID)
	mustOK(t, "目录详情", status, api)
	if detail.Name != root.Name || detail.ParentID != 0 {
		t.Fatalf("详情不符: %+v", detail)
	}

	status, api = renameDocumentFolderAPI(t, env, admin, root.ID, "技术资料（改）")
	mustOK(t, "目录改名", status, api)
	renamed := decodeDocumentFolder(t, api)
	if renamed.Name != "技术资料（改）" {
		t.Fatalf("改名后回显 = %q", renamed.Name)
	}
	status, api, rows := listDocumentFolderTreeAPI(t, env, admin)
	mustOK(t, "改名后目录树", status, api)
	if len(rows) != 1 || rows[0].Name != "技术资料（改）" {
		t.Fatalf("改名后目录树 = %+v", rows)
	}

	// 子目录不带 ACL：继承父目录（acl_configured=false）。
	child := createDocumentFolderAPI(t, env, admin, root.ID, "需求文档")
	if child.ParentID != root.ID || child.ACLConfigured {
		t.Fatalf("子目录回显不符: %+v", child)
	}

	status, api, rows = listDocumentFolderTreeAPI(t, env, admin)
	mustOK(t, "父子目录树", status, api)
	if len(rows) != 1 || len(rows[0].Children) != 1 || rows[0].Children[0].ID != child.ID {
		t.Fatalf("父子树结构不符: %+v", rows)
	}

	// 非空目录删除 → 409。
	status, api = deleteDocumentFolderAPI(t, env, admin, root.ID)
	if status != http.StatusConflict {
		t.Fatalf("删除非空目录: HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 删除子目录后父目录可删除；删除后详情 404。
	status, api = deleteDocumentFolderAPI(t, env, admin, child.ID)
	mustOK(t, "删除空子目录", status, api)
	status, api = deleteDocumentFolderAPI(t, env, admin, root.ID)
	mustOK(t, "删除空父目录", status, api)
	status, api, _ = getDocumentFolderAPI(t, env, admin, root.ID)
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, rows = listDocumentFolderTreeAPI(t, env, admin)
	mustOK(t, "删除后目录树", status, api)
	if len(rows) != 0 {
		t.Fatalf("删除后目录树应为空，得到 %+v", rows)
	}
}

// TestIntegrationDocumentACLVisibility 四类主体、继承/覆盖、无 ACL 顶层与
// 孤儿节点提升。
func TestIntegrationDocumentACLVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	staffRole := createRoleWithMenus(t, env, admin, "doc_staff", []int64{documentMineMenuID, documentCreateBtn})
	specialRole := createRoleWithMenus(t, env, admin, "doc_special", []int64{documentMineMenuID})

	dept102ID := createUser(t, env, admin, "doc_dept102", 102)
	dept101ID := createUser(t, env, admin, "doc_dept101", 101)
	roleUserID := createUser(t, env, admin, "doc_role_user", 103)
	userTargetID := createUser(t, env, admin, "doc_user_target", 103)
	otherID := createUser(t, env, admin, "doc_other", 103)
	noDeptID := createUser(t, env, admin, "doc_no_dept", 0)
	bindDocumentRoles(t, env, admin, "doc_dept102", dept102ID, 102, []int64{staffRole})
	bindDocumentRoles(t, env, admin, "doc_dept101", dept101ID, 101, []int64{staffRole})
	bindDocumentRoles(t, env, admin, "doc_role_user", roleUserID, 103, []int64{staffRole, specialRole})
	bindDocumentRoles(t, env, admin, "doc_user_target", userTargetID, 103, []int64{staffRole})
	bindDocumentRoles(t, env, admin, "doc_other", otherID, 103, []int64{staffRole})
	bindDocumentRoles(t, env, admin, "doc_no_dept", noDeptID, 0, []int64{staffRole})

	dept102 := env.Login(t, "doc_dept102", "123456")
	dept101 := env.Login(t, "doc_dept101", "123456")
	roleUser := env.Login(t, "doc_role_user", "123456")
	userTarget := env.Login(t, "doc_user_target", "123456")
	other := env.Login(t, "doc_other", "123456")
	noDept := env.Login(t, "doc_no_dept", "123456")

	// 定义目录矩阵。
	createDocumentFolderAPI(t, env, admin, 0, "全员目录", documentTarget(documentACLTargetAll, 0))
	createDocumentFolderAPI(t, env, admin, 0, "部门102目录", documentTarget(documentACLTargetDept, 102))
	roleFolder := createDocumentFolderAPI(t, env, admin, 0, "角色目录", documentTarget(documentACLTargetRole, specialRole))
	createDocumentFolderAPI(t, env, admin, 0, "用户目录", documentTarget(documentACLTargetUser, userTargetID))

	parentAll := createDocumentFolderAPI(t, env, admin, 0, "父全员目录", documentTarget(documentACLTargetAll, 0))
	override101 := createDocumentFolderAPI(t, env, admin, parentAll.ID, "子覆盖101", documentTarget(documentACLTargetDept, 101))

	parentDept102 := createDocumentFolderAPI(t, env, admin, 0, "父部门102目录", documentTarget(documentACLTargetDept, 102))
	inherit102 := createDocumentFolderAPI(t, env, admin, parentDept102.ID, "继承部门102")
	// 隔代继承：孙目录自身与父目录都没有 ACL，仍取最近显式 ACL 祖先（父部门102目录）。
	grandchild102 := createDocumentFolderAPI(t, env, admin, inherit102.ID, "孙目录")

	// 无 ACL 顶层（历史数据）+ 其下的显式 ACL 子目录：父不可见时子目录提升为根。
	hiddenRootID := insertDocumentFolderRaw(t, env, 0, "隐藏顶层", 1)
	orphan := createDocumentFolderAPI(t, env, admin, hiddenRootID, "孤儿可见", documentTarget(documentACLTargetDept, 101))

	// 管理端可见性：管理员旁路看到全部（含无 ACL 顶层）。
	status, api, adminRows := listDocumentFolderTreeAPI(t, env, admin)
	mustOK(t, "管理员目录树", status, api)
	adminNames := documentFlatten(adminRows)
	for _, name := range []string{"全员目录", "部门102目录", "角色目录", "用户目录", "父全员目录", "子覆盖101", "父部门102目录", "继承部门102", "孙目录", "隐藏顶层", "孤儿可见"} {
		if _, ok := adminNames[name]; !ok {
			t.Fatalf("管理员应看到 %q，实际 %v", name, documentNamesOf(adminNames))
		}
	}

	assertDocumentVisibility(t, "部门 102 用户", env, dept102,
		[]string{"全员目录", "部门102目录", "父全员目录", "父部门102目录", "继承部门102", "孙目录"},
		[]string{"子覆盖101", "角色目录", "用户目录", "隐藏顶层", "孤儿可见"},
	)
	assertDocumentVisibility(t, "部门 101 用户", env, dept101,
		[]string{"全员目录", "父全员目录", "子覆盖101", "孤儿可见"},
		[]string{"部门102目录", "继承部门102", "孙目录", "父部门102目录", "角色目录", "用户目录", "隐藏顶层"},
	)
	assertDocumentVisibility(t, "角色用户", env, roleUser,
		[]string{"全员目录", "角色目录", "父全员目录"},
		[]string{"部门102目录", "用户目录", "父部门102目录", "子覆盖101", "隐藏顶层"},
	)
	assertDocumentVisibility(t, "用户主体", env, userTarget,
		[]string{"全员目录", "用户目录", "父全员目录"},
		[]string{"部门102目录", "角色目录", "父部门102目录", "子覆盖101", "隐藏顶层"},
	)
	assertDocumentVisibility(t, "无命中用户", env, other,
		[]string{"全员目录", "父全员目录"},
		[]string{"部门102目录", "角色目录", "用户目录", "父部门102目录", "子覆盖101", "隐藏顶层"},
	)
	// 无部门用户（DeptIDs 为空集合）：只命中全员 ACL，不命中任何部门 ACL。
	assertDocumentVisibility(t, "无部门用户", env, noDept,
		[]string{"全员目录", "父全员目录"},
		[]string{"部门102目录", "子覆盖101", "继承部门102", "孙目录", "角色目录", "用户目录", "隐藏顶层"},
	)

	// 父目录不可见 → 子目录提升为根节点。
	status, api, rows := listDocumentFolderTreeAPI(t, env, dept101)
	mustOK(t, "部门 101 根节点", status, api)
	roots := make(map[string]bool, len(rows))
	for _, row := range rows {
		roots[row.Name] = true
	}
	if !roots["孤儿可见"] {
		t.Fatalf("父目录不可见时子目录应提升为根节点，根节点 = %v", roots)
	}
	if roots["隐藏顶层"] {
		t.Fatalf("无 ACL 顶层不应出现在目录树，根节点 = %v", roots)
	}
	if override := documentFlatten(rows)["子覆盖101"]; override.ParentID != parentAll.ID {
		t.Fatalf("子覆盖101 parentId = %d，期望 %d", override.ParentID, parentAll.ID)
	}

	// 详情：范围外 404、范围内 200。
	status, api, _ = getDocumentFolderAPI(t, env, dept101, override101.ID)
	mustOK(t, "部门 101 读子覆盖101", status, api)
	status, api, _ = getDocumentFolderAPI(t, env, dept102, override101.ID)
	if status != http.StatusNotFound {
		t.Fatalf("部门 102 读子覆盖101: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = getDocumentFolderAPI(t, env, dept102, inherit102.ID)
	mustOK(t, "部门 102 读继承部门102", status, api)
	status, api, _ = getDocumentFolderAPI(t, env, dept101, inherit102.ID)
	if status != http.StatusNotFound {
		t.Fatalf("部门 101 读继承部门102: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = getDocumentFolderAPI(t, env, other, roleFolder.ID)
	if status != http.StatusNotFound {
		t.Fatalf("无命中用户读角色目录: HTTP %d code %d，期望 404", status, api.Code)
	}

	// ACL 视图：子目录继承（source 指向父目录，own entries 为空）。
	status, api, acl := getDocumentFolderACLAPI(t, env, admin, inherit102.ID)
	mustOK(t, "继承目录 ACL 视图", status, api)
	if acl.Explicit || acl.SourceFolderID != parentDept102.ID || acl.SourceFolderName != "父部门102目录" {
		t.Fatalf("继承 ACL 视图不符: %+v", acl)
	}
	if len(acl.Entries) != 0 || len(acl.EffectiveEntries) != 1 ||
		acl.EffectiveEntries[0].TargetType != documentACLTargetDept || acl.EffectiveEntries[0].TargetID != 102 {
		t.Fatalf("继承 ACL 条目不符: %+v", acl)
	}

	// 隔代继承：孙目录的有效 ACL 来源仍是父部门102目录（最近显式祖先）。
	status, api, acl = getDocumentFolderACLAPI(t, env, admin, grandchild102.ID)
	mustOK(t, "隔代继承 ACL 视图", status, api)
	if acl.Explicit || acl.SourceFolderID != parentDept102.ID || acl.SourceFolderName != "父部门102目录" {
		t.Fatalf("隔代继承 ACL 视图不符: %+v", acl)
	}

	// 子覆盖父：子目录 ACL 视图 source 指向自身。
	status, api, acl = getDocumentFolderACLAPI(t, env, admin, override101.ID)
	mustOK(t, "覆盖目录 ACL 视图", status, api)
	if !acl.Explicit || acl.SourceFolderID != override101.ID || acl.SourceFolderName != "子覆盖101" {
		t.Fatalf("覆盖 ACL 视图不符: %+v", acl)
	}

	// owner 旁路：部门 101 用户创建目录（owner），ACL 只给 user target；
	// owner 仍可见/可改名，target 用户可见，其他用户不可见。
	ownerFolder := createDocumentFolderAPI(t, env, dept101, 0, "owner目录", documentTarget(documentACLTargetUser, userTargetID))
	assertDocumentVisibility(t, "owner 目录 owner 可见", env, dept101,
		[]string{"全员目录", "父全员目录", "子覆盖101", "孤儿可见", "owner目录"},
		[]string{"部门102目录", "角色目录", "用户目录", "隐藏顶层"},
	)
	assertDocumentVisibility(t, "owner 目录 target 可见", env, userTarget,
		[]string{"全员目录", "用户目录", "父全员目录", "owner目录"},
		[]string{"部门102目录", "角色目录", "父部门102目录", "子覆盖101"},
	)
	assertDocumentVisibility(t, "owner 目录他人不可见", env, dept102,
		[]string{"全员目录", "部门102目录", "父全员目录", "父部门102目录", "继承部门102"},
		[]string{"owner目录", "子覆盖101", "角色目录", "用户目录"},
	)
	status, api = renameDocumentFolderAPI(t, env, dept101, ownerFolder.ID, "owner目录（改）")
	mustOK(t, "owner 改名（无 manage 码）", status, api)
	status, api, _ = getDocumentFolderACLAPI(t, env, dept101, ownerFolder.ID)
	mustOK(t, "owner 读 ACL", status, api)
	// 子目录不带 ACL：继承 owner 目录的显式 ACL（user target）。
	childOfOwner := createDocumentFolderAPI(t, env, dept101, ownerFolder.ID, "owner子目录")
	assertDocumentVisibility(t, "owner 子目录继承", env, userTarget,
		[]string{"全员目录", "用户目录", "父全员目录", "owner目录（改）", "owner子目录"},
		[]string{"角色目录"},
	)
	if childOfOwner.ACLConfigured {
		t.Fatalf("owner 子目录不应有显式 ACL: %+v", childOfOwner)
	}

	// 不可见即不可管理：部门 102 用户读 owner 目录 ACL → 404。
	status, api, _ = getDocumentFolderACLAPI(t, env, dept102, ownerFolder.ID)
	if status != http.StatusNotFound {
		t.Fatalf("不可见目录读 ACL: HTTP %d code %d，期望 404", status, api.Code)
	}
	// 管理员旁路：读任意目录 ACL 成功。
	status, api, _ = getDocumentFolderACLAPI(t, env, admin, ownerFolder.ID)
	mustOK(t, "管理员读 ACL", status, api)

	if orphan.ID <= 0 {
		t.Fatalf("孤儿目录创建失败: %+v", orphan)
	}
}

// TestIntegrationDocumentACLReplace ACL 整体替换、继承与校验边界。
func TestIntegrationDocumentACLReplace(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	staffRole := createRoleWithMenus(t, env, admin, "doc_replace_staff", []int64{documentMineMenuID})
	dept101ID := createUser(t, env, admin, "doc_replace_101", 101)
	dept102ID := createUser(t, env, admin, "doc_replace_102", 102)
	bindDocumentRoles(t, env, admin, "doc_replace_101", dept101ID, 101, []int64{staffRole})
	bindDocumentRoles(t, env, admin, "doc_replace_102", dept102ID, 102, []int64{staffRole})
	dept101 := env.Login(t, "doc_replace_101", "123456")
	dept102 := env.Login(t, "doc_replace_102", "123456")

	// 顶层创建不带 ACL → 400；带空数组 → 400。
	status, api := env.Request(t, http.MethodPost, "/oa/document/folder", admin, map[string]any{"name": "缺 ACL 顶层"})
	if status != http.StatusBadRequest {
		t.Fatalf("顶层缺 ACL 创建: HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPost, "/oa/document/folder", admin, map[string]any{
		"name": "空 ACL 顶层", "acl": []documentACLTargetBody{},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("顶层空 ACL 创建: HTTP %d code %d，期望 400", status, api.Code)
	}

	root := createDocumentFolderAPI(t, env, admin, 0, "通知资料", documentTarget(documentACLTargetAll, 0))
	child := createDocumentFolderAPI(t, env, admin, root.ID, "部门资料", documentTarget(documentACLTargetDept, 101))
	if !child.ACLConfigured {
		t.Fatalf("子目录应标记显式 ACL: %+v", child)
	}
	assertDocumentVisibility(t, "初始 dept101", env, dept101, []string{"通知资料", "部门资料"}, nil)
	assertDocumentVisibility(t, "初始 dept102", env, dept102, []string{"通知资料"}, []string{"部门资料"})

	// 整体替换为部门 102 → dept102 可见、dept101 不可见。
	status, api = setDocumentFolderACLAPI(t, env, admin, child.ID, []documentACLTargetBody{
		documentTarget(documentACLTargetDept, 102),
	})
	mustOK(t, "替换子目录 ACL", status, api)
	assertDocumentVisibility(t, "替换后 dept101", env, dept101, []string{"通知资料"}, []string{"部门资料"})
	assertDocumentVisibility(t, "替换后 dept102", env, dept102, []string{"通知资料", "部门资料"}, nil)

	status, api, acl := getDocumentFolderACLAPI(t, env, admin, child.ID)
	mustOK(t, "替换后 ACL 视图", status, api)
	if !acl.Explicit || len(acl.Entries) != 1 || acl.Entries[0].TargetID != 102 ||
		len(acl.EffectiveEntries) != 1 || acl.EffectiveEntries[0].TargetID != 102 {
		t.Fatalf("替换后 ACL 视图不符: %+v", acl)
	}

	// 清空子目录 ACL → 改为继承父目录（全员），两人都可见。
	status, api = setDocumentFolderACLAPI(t, env, admin, child.ID, []documentACLTargetBody{})
	mustOK(t, "清空子目录 ACL", status, api)
	assertDocumentVisibility(t, "继承后 dept101", env, dept101, []string{"通知资料", "部门资料"}, nil)
	assertDocumentVisibility(t, "继承后 dept102", env, dept102, []string{"通知资料", "部门资料"}, nil)
	status, api, acl = getDocumentFolderACLAPI(t, env, admin, child.ID)
	mustOK(t, "继承后 ACL 视图", status, api)
	if acl.Explicit || acl.SourceFolderID != root.ID || len(acl.Entries) != 0 {
		t.Fatalf("继承后 ACL 视图不符: %+v", acl)
	}

	// 顶层清空 ACL → 400。
	status, api = setDocumentFolderACLAPI(t, env, admin, root.ID, []documentACLTargetBody{})
	if status != http.StatusBadRequest {
		t.Fatalf("顶层清空 ACL: HTTP %d code %d，期望 400", status, api.Code)
	}

	// 非法主体：未知类型 / 全员带目标 / 部门缺目标 → 400。
	for _, entries := range [][]documentACLTargetBody{
		{documentTarget(9, 1)},
		{documentTarget(documentACLTargetAll, 7)},
		{documentTarget(documentACLTargetDept, 0)},
	} {
		status, api = setDocumentFolderACLAPI(t, env, admin, child.ID, entries)
		if status != http.StatusBadRequest {
			t.Fatalf("非法 ACL %+v: HTTP %d code %d，期望 400", entries, status, api.Code)
		}
	}
}

// TestIntegrationDocumentDeleteConstraints 有文件的目录禁止删除（409）。
func TestIntegrationDocumentDeleteConstraints(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	withFile := createDocumentFolderAPI(t, env, admin, 0, "有文件目录", documentTarget(documentACLTargetAll, 0))
	insertDocumentFileRaw(t, env, withFile.ID)
	status, api := deleteDocumentFolderAPI(t, env, admin, withFile.ID)
	if status != http.StatusConflict {
		t.Fatalf("删除有文件目录: HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 删除子目录后仍有文件 → 依然 409。
	child := createDocumentFolderAPI(t, env, admin, withFile.ID, "子目录")
	status, api = deleteDocumentFolderAPI(t, env, admin, withFile.ID)
	if status != http.StatusConflict {
		t.Fatalf("删除有子目录的目录: HTTP %d code %d，期望 409", status, api.Code)
	}
	status, api = deleteDocumentFolderAPI(t, env, admin, child.ID)
	mustOK(t, "删除空子目录", status, api)
}

// TestIntegrationDocumentPermissionCodes 权限码与目录管理权（403/404 区分）。
func TestIntegrationDocumentPermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	plainID := createUser(t, env, admin, "doc_plain", 102)
	plain := env.Login(t, "doc_plain", "123456")

	// 无任何文档菜单：树/创建 403。
	status, api := env.Request(t, http.MethodGet, "/oa/document/folder/tree", plain, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无查询码读树: HTTP %d code %d，期望 403", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/oa/document/folder", plain, map[string]any{
		"name": "无权限目录", "acl": []documentACLTargetBody{documentTarget(documentACLTargetAll, 0)},
	})
	if status != http.StatusForbidden {
		t.Fatalf("无创建码创建: HTTP %d code %d，期望 403", status, api.Code)
	}

	// 只授予查询（537）：树 200，创建 403。
	queryRole := createRoleWithMenus(t, env, admin, "doc_query_only", []int64{documentMineMenuID})
	bindDocumentRoles(t, env, admin, "doc_plain", plainID, 102, []int64{queryRole})
	plain = env.Login(t, "doc_plain", "123456")
	status, api, _ = listDocumentFolderTreeAPI(t, env, plain)
	mustOK(t, "仅查询码读树", status, api)
	status, api = env.Request(t, http.MethodPost, "/oa/document/folder", plain, map[string]any{
		"name": "仍无创建码", "acl": []documentACLTargetBody{documentTarget(documentACLTargetAll, 0)},
	})
	if status != http.StatusForbidden {
		t.Fatalf("仅查询码创建: HTTP %d code %d，期望 403", status, api.Code)
	}

	// 管理员建一个对 plain 可见的目录；plain 可见但不可管理 → 改名/ACL 403。
	visible := createDocumentFolderAPI(t, env, admin, 0, "可见目录", documentTarget(documentACLTargetUser, plainID))
	status, api = renameDocumentFolderAPI(t, env, plain, visible.ID, "越权改名")
	if status != http.StatusForbidden {
		t.Fatalf("非 owner 改名: HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	status, api, _ = getDocumentFolderACLAPI(t, env, plain, visible.ID)
	if status != http.StatusForbidden {
		t.Fatalf("非 owner 读 ACL: HTTP %d code %d，期望 403", status, api.Code)
	}
	status, api = deleteDocumentFolderAPI(t, env, plain, visible.ID)
	if status != http.StatusForbidden {
		t.Fatalf("非 owner 删除: HTTP %d code %d，期望 403", status, api.Code)
	}

	// 授予管理码（5262）：改名/读 ACL/替换 ACL 均成功，并能看到无 ACL 顶层。
	manageRole := createRoleWithMenus(t, env, admin, "doc_manage", []int64{documentManageBtn})
	bindDocumentRoles(t, env, admin, "doc_plain", plainID, 102, []int64{queryRole, manageRole})
	plain = env.Login(t, "doc_plain", "123456")
	status, api = renameDocumentFolderAPI(t, env, plain, visible.ID, "管理码改名")
	mustOK(t, "管理码改名", status, api)
	status, api, _ = getDocumentFolderACLAPI(t, env, plain, visible.ID)
	mustOK(t, "管理码读 ACL", status, api)
	status, api = setDocumentFolderACLAPI(t, env, plain, visible.ID, []documentACLTargetBody{
		documentTarget(documentACLTargetAll, 0),
	})
	mustOK(t, "管理码替换 ACL", status, api)

	hidden := insertDocumentFolderRaw(t, env, 0, "无ACL顶层", 1)
	assertDocumentVisibility(t, "管理码可见无 ACL 顶层", env, plain, []string{"管理码改名", "无ACL顶层"}, nil)
	status, api, _ = getDocumentFolderAPI(t, env, plain, hidden)
	mustOK(t, "管理码读无 ACL 顶层", status, api)

	// 授予创建码（5261）：可以创建顶层目录（必须带 ACL）。
	createRole := createRoleWithMenus(t, env, admin, "doc_create", []int64{documentCreateBtn})
	bindDocumentRoles(t, env, admin, "doc_plain", plainID, 102, []int64{queryRole, createRole})
	plain = env.Login(t, "doc_plain", "123456")
	created := createDocumentFolderAPI(t, env, plain, 0, "员工自建目录", documentTarget(documentACLTargetUser, plainID))
	if created.OwnerID != plainID {
		t.Fatalf("员工自建目录 owner = %d，期望 %d", created.OwnerID, plainID)
	}
}

// TestIntegrationDocumentTenantIsolation 跨租户目录不可见、按 ID 读取 404。
func TestIntegrationDocumentTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	folder := createDocumentFolderAPI(t, env, admin, 0, "租户一目录", documentTarget(documentACLTargetAll, 0))

	status, api := env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": "租户二", "contactName": "联系人", "status": 0, "domain": "doc-t2.example.com",
	})
	mustOK(t, "创建租户", status, api)
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"tenantId": 2, "username": "admin", "password": "123456",
	})
	mustOK(t, "租户 2 登录", status, api)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &login); err != nil || login.AccessToken == "" {
		t.Fatalf("解析租户 2 登录响应: %v（data=%s）", err, string(api.Data))
	}
	t2 := login.AccessToken

	status, api, rows := listDocumentFolderTreeAPI(t, env, t2)
	mustOK(t, "租户 2 目录树", status, api)
	if len(rows) != 0 {
		t.Fatalf("租户 2 不应看到租户 1 的目录，得到 %+v", rows)
	}
	status, api, _ = getDocumentFolderAPI(t, env, t2, folder.ID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 跨租户读目录: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = getDocumentFolderACLAPI(t, env, t2, folder.ID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 跨租户读 ACL: HTTP %d code %d，期望 404", status, api.Code)
	}

	// 租户 2 在自己的租户内创建目录成功，且不影响租户 1 的树。
	createDocumentFolderAPI(t, env, t2, 0, "租户二目录", documentTarget(documentACLTargetAll, 0))
	status, api, rows = listDocumentFolderTreeAPI(t, env, t2)
	mustOK(t, "租户 2 创建后目录树", status, api)
	if names := documentFlatten(rows); len(names) != 1 || names["租户二目录"].ID == 0 {
		t.Fatalf("租户 2 目录树不符: %v", documentNamesOf(names))
	}
	status, api, rows = listDocumentFolderTreeAPI(t, env, admin)
	mustOK(t, "租户 1 目录树", status, api)
	if names := documentFlatten(rows); len(names) != 1 || names["租户一目录"].ID == 0 {
		t.Fatalf("租户 1 目录树被跨租户污染: %v", documentNamesOf(names))
	}
}

// documentMenuNode 是菜单树节点（按钮挂在菜单的 children 下）。
type documentMenuNode struct {
	ID         int64              `json:"id"`
	ParentID   int64              `json:"parentId"`
	Name       string             `json:"name"`
	Permission string             `json:"permission"`
	Children   []documentMenuNode `json:"children"`
}

// walkDocumentMenus 深度优先遍历菜单树（含按钮）。
func walkDocumentMenus(nodes []documentMenuNode, fn func(documentMenuNode)) {
	for _, node := range nodes {
		fn(node)
		walkDocumentMenus(node.Children, fn)
	}
}

// TestIntegrationDocumentSeedMenus 菜单种子：526/5261–5263 与 537 下发，
// 权限码与前端/后端约定一致。
func TestIntegrationDocumentSeedMenus(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/system/menu/list", admin, nil)
	mustOK(t, "查询菜单列表", status, api)
	var menus []documentMenuNode
	if err := json.Unmarshal(api.Data, &menus); err != nil {
		t.Fatalf("解析菜单列表: %v（data=%s）", err, string(api.Data))
	}
	want := map[int64]string{
		documentMenuID:     "oa:document:query",
		documentCreateBtn:  "oa:document:create",
		documentManageBtn:  "oa:document:manage",
		documentUploadBtn:  "oa:document:upload",
		documentMineMenuID: "oa:document:query",
	}
	found := make(map[int64]string)
	walkDocumentMenus(menus, func(m documentMenuNode) {
		if _, ok := want[m.ID]; ok {
			found[m.ID] = m.Permission
		}
	})
	if len(found) != len(want) {
		t.Fatalf("文档菜单缺失：找到 %v，期望 %v", found, want)
	}
	for id, permission := range want {
		if found[id] != permission {
			t.Fatalf("菜单 %d 权限码 = %q，期望 %q", id, found[id], permission)
		}
	}
}
