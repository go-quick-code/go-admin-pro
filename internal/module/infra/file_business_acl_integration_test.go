//go:build integration

// 通用 infra 文件端点的业务 ACL 集成测试（PR #66 安全审计 finding 1：
// infra/file/download-delete-bypasses-business-acl）：
//
//   - GET /infra/file/:id/download 对 biz_type != "" 的文件必须经过业务
//     授权：只持 infra:file:query 的用户读取 ACL 拒绝的文档 404/403（不是
//     200），有文档读权限的用户仍 200；
//   - DELETE /infra/file/:id 必须经过业务删除授权：无目录管理权 404/403
//     且文件仍可下载；有管理权删除成功后同步软删 oa_document_file 归属行
//     （文档列表不再出现悬空条目）；
//   - GET /infra/file/page 默认只返回 biz_type=” 的通用文件；显式
//     bizType=oa_document 需要文档管理码（管理视角），否则 403；
//   - biz_type=” 的上传/下载/删除行为不变（回归）；
//   - 未注册授权器的业务类型（如 crm_contract）在通用端点上一律 404
//     （fail-closed），业务自己的鉴权下载端点不受影响。
package infra_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 菜单种子（internal/seed/seed.go）中本用例需要的权限码菜单：
//
//	201  infra:file:query / 2011 infra:file:upload / 2012 infra:file:delete
//	526  oa:document:query / 5261 oa:document:create / 5262 oa:document:manage
const (
	menuFileQuery       = int64(201)
	menuFileUpload      = int64(2011)
	menuFileDelete      = int64(2012)
	menuDocumentQuery   = int64(526)
	menuDocumentCreate  = int64(5261)
	menuDocumentManage  = int64(5262)
	documentACLDeptType = int8(1) // model.DocumentACLTargetDept
	fileACLDeptOwner    = int64(101)
	fileACLDeptAttacker = int64(102)
	secretDocumentBody  = "TOP-SECRET-DOCUMENT"
	secretDocumentName  = "secret.txt"
)

// mustOKForFileACL 断言业务成功（HTTP 200 且 code 0）。
func mustOKForFileACL(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// createRoleForFileACL 用 admin 创建角色并回读 ID（menuIds 决定权限码）。
func createRoleForFileACL(t *testing.T, env *testutil.IntegrationEnv, admin, name, code string, menuIDs []int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
		"name": name, "code": code, "dataScope": 1, "status": 0, "menuIds": menuIDs,
	})
	mustOKForFileACL(t, "创建角色 "+code, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/role/page?code="+code+"&pageNo=1&pageSize=10", admin, nil)
	mustOKForFileACL(t, "查询角色 "+code, status, api)
	var page struct {
		List []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析角色分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("按编码 %s 查询角色：total=%d，期望唯一命中", code, page.Total)
	}
	return page.List[0].ID
}

// createUserForFileACL 用 admin 创建启用用户并绑定角色。
func createUserForFileACL(t *testing.T, env *testutil.IntegrationEnv, admin, username string, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	mustOKForFileACL(t, "创建用户 "+username, status, api)
}

// createDocumentFolderForFileACL 用 owner 创建顶层目录，ACL 授予研发部（101）。
func createDocumentFolderForFileACL(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/document/folder", token, map[string]any{
		"parentId": 0,
		"name":     name,
		"sort":     0,
		"acl": []map[string]any{
			{"targetType": documentACLDeptType, "targetId": fileACLDeptOwner},
		},
	})
	mustOKForFileACL(t, "创建目录 "+name, status, api)
	var item struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &item); err != nil {
		t.Fatalf("解析目录视图: %v（data=%s）", err, string(api.Data))
	}
	if item.ID <= 0 {
		t.Fatalf("创建目录返回的 ID 非法: %s", string(api.Data))
	}
	return item.ID
}

// uploadDocumentFileForFileACL 上传文档文件到目录，返回归属行 ID 与
// infra_file ID（后者是通用文件端点的路径参数）。
func uploadDocumentFileForFileACL(t *testing.T, env *testutil.IntegrationEnv, token string, folderID int64, filename, content string) (int64, int64) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("folderId", fmt.Sprintf("%d", folderID)); err != nil {
		t.Fatalf("写入 folderId 字段: %v", err)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("写入 multipart 内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/oa/document/file/upload"), &buf)
	if err != nil {
		t.Fatalf("构造文档上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传文档文件: %v", err)
	}
	defer resp.Body.Close()

	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析文档上传响应（HTTP %d）: %v", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || api.Code != 0 {
		t.Fatalf("上传文档文件：HTTP %d code %d msg %q", resp.StatusCode, api.Code, api.Msg)
	}
	var item struct {
		ID     int64 `json:"id"`
		FileID int64 `json:"fileId"`
	}
	if err := json.Unmarshal(api.Data, &item); err != nil {
		t.Fatalf("解析文档文件视图: %v（data=%s）", err, string(api.Data))
	}
	if item.ID <= 0 || item.FileID <= 0 {
		t.Fatalf("文档上传应返回归属行 ID 与 infra 文件 ID，实际 %s", string(api.Data))
	}
	return item.ID, item.FileID
}

// pageInfraFiles 查询 /infra/file/page 并返回 total 与文件 ID 集合。
func pageInfraFiles(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int64, []int64, int, testutil.APIResponse) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/infra/file/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		return 0, nil, status, api
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析文件分页: %v（data=%s）", err, string(api.Data))
	}
	ids := make([]int64, 0, len(page.List))
	for _, row := range page.List {
		ids = append(ids, row.ID)
	}
	return page.Total, ids, status, api
}

// setupDocumentACLFixture 建立四个主体：owner（文档读写 + 目录管理）、
// reader（仅 infra 读，但部门在 ACL 内）、attacker（仅 infra 读写、部门在
// ACL 外）。返回各自 token 与目录/文件 ID。
type documentACLFixture struct {
	ownerToken    string
	readerToken   string
	attackerToken string
	folderID      int64
	fileRowID     int64
	infraFileID   int64
}

func setupDocumentACLFixture(t *testing.T, env *testutil.IntegrationEnv, admin string) documentACLFixture {
	t.Helper()

	ownerRole := createRoleForFileACL(t, env, admin, "文档 ACL owner 角色", "doc_acl_owner_role",
		[]int64{menuDocumentQuery, menuDocumentCreate, menuDocumentManage, menuFileQuery, menuFileDelete})
	createUserForFileACL(t, env, admin, "doc_acl_owner", fileACLDeptOwner, []int64{ownerRole})

	readerRole := createRoleForFileACL(t, env, admin, "文档 ACL reader 角色", "doc_acl_reader_role",
		[]int64{menuFileQuery})
	createUserForFileACL(t, env, admin, "doc_acl_reader", fileACLDeptOwner, []int64{readerRole})

	attackerRole := createRoleForFileACL(t, env, admin, "文档 ACL attacker 角色", "doc_acl_attacker_role",
		[]int64{menuFileQuery, menuFileDelete})
	createUserForFileACL(t, env, admin, "doc_acl_attacker", fileACLDeptAttacker, []int64{attackerRole})

	owner := env.Login(t, "doc_acl_owner", "123456")
	reader := env.Login(t, "doc_acl_reader", "123456")
	attacker := env.Login(t, "doc_acl_attacker", "123456")

	folderID := createDocumentFolderForFileACL(t, env, owner, "probe-secret-folder")
	fileRowID, infraFileID := uploadDocumentFileForFileACL(t, env, owner, folderID,
		secretDocumentName, secretDocumentBody)

	return documentACLFixture{
		ownerToken: owner, readerToken: reader, attackerToken: attacker,
		folderID: folderID, fileRowID: fileRowID, infraFileID: infraFileID,
	}
}

// downloadDocumentFileRaw 走 OA 文档下载端点（与 infra 端点对照）。
func downloadDocumentFileRaw(t *testing.T, env *testutil.IntegrationEnv, token string, fileRowID int64) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		env.APIPrefixURL(fmt.Sprintf("/oa/document/file/%d/download", fileRowID)), nil)
	if err != nil {
		t.Fatalf("构造文档下载请求: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载文档文件: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestIntegrationDocumentFileInfraDownloadEnforcesACL 下载与分页：infra 端点
// 不能绕过文档目录 ACL，分页默认隐藏业务文件，显式 bizType 需要管理视角。
func TestIntegrationDocumentFileInfraDownloadEnforcesACL(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	fx := setupDocumentACLFixture(t, env, admin)

	// 对照：owner 的文档端点 200；attacker 的文档端点 403（无查询码）。
	if status := downloadDocumentFileRaw(t, env, fx.ownerToken, fx.fileRowID); status != http.StatusOK {
		t.Fatalf("owner 文档端点下载：HTTP %d，期望 200", status)
	}
	if status := downloadDocumentFileRaw(t, env, fx.attackerToken, fx.fileRowID); status != http.StatusForbidden {
		t.Fatalf("attacker 文档端点下载：HTTP %d，期望 403", status)
	}

	// attacker 走 infra 端点：ACL 外 → 404/403，绝不能 200 拿到内容。
	status, _, body := downloadInfraFileRaw(t, env, fx.attackerToken, fx.infraFileID)
	if status == http.StatusOK {
		t.Fatalf("attacker 经 infra 端点下载应当被拒，实际 200 body=%q", string(body))
	}
	if status != http.StatusNotFound && status != http.StatusForbidden {
		t.Fatalf("attacker 经 infra 端点下载：HTTP %d，期望 404/403", status)
	}

	// reader 部门命中 ACL（且持 infra:file:query）：200 且内容一致。
	status, _, body = downloadInfraFileRaw(t, env, fx.readerToken, fx.infraFileID)
	if status != http.StatusOK || string(body) != secretDocumentBody {
		t.Fatalf("ACL 内用户经 infra 端点下载：HTTP %d body=%q，期望 200 且内容一致", status, string(body))
	}

	// 分页默认只列 biz_type='' 的通用文件：文档文件对 admin/attacker 都不出现。
	if total, ids, st, api := pageInfraFiles(t, env, admin, ""); st != http.StatusOK || api.Code != 0 || total != 0 || len(ids) != 0 {
		t.Fatalf("admin 默认分页应隐藏业务文件，实际 HTTP %d total=%d ids=%v msg=%q", st, total, ids, api.Msg)
	}
	if total, _, st, api := pageInfraFiles(t, env, fx.attackerToken, ""); st != http.StatusOK || api.Code != 0 || total != 0 {
		t.Fatalf("attacker 默认分页应隐藏业务文件，实际 HTTP %d total=%d msg=%q", st, total, api.Msg)
	}

	// 显式 bizType=oa_document 是类型级管理视角：无管理码 403。
	if _, _, st, api := pageInfraFiles(t, env, fx.attackerToken, "&bizType=oa_document"); st != http.StatusForbidden {
		t.Fatalf("attacker 显式列出 oa_document：HTTP %d code %d msg %q，期望 403", st, api.Code, api.Msg)
	}

	// 文档管理码持有者（admin 与 owner 均有）可以看到该业务文件。
	for name, token := range map[string]string{"admin": admin, "owner": fx.ownerToken} {
		total, ids, st, api := pageInfraFiles(t, env, token, "&bizType=oa_document")
		if st != http.StatusOK || api.Code != 0 || total != 1 || len(ids) != 1 || ids[0] != fx.infraFileID {
			t.Fatalf("%s 显式列出 oa_document：HTTP %d total=%d ids=%v msg=%q，期望唯一命中 %d",
				name, st, total, ids, api.Msg, fx.infraFileID)
		}
	}
}

// TestIntegrationDocumentFileInfraDeleteEnforcesManage 删除：无目录管理权被拒
// 且文件仍可下载；有管理权删除成功后业务归属行同步软删（无悬空条目）。
func TestIntegrationDocumentFileInfraDeleteEnforcesManage(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	fx := setupDocumentACLFixture(t, env, admin)

	// attacker 持 infra:file:delete 但没有目录管理权：删除被拒。
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/infra/file/%d", fx.infraFileID), fx.attackerToken, nil)
	if status == http.StatusOK {
		t.Fatalf("attacker 经 infra 端点删除文档应当被拒，实际 HTTP 200")
	}
	if status != http.StatusNotFound && status != http.StatusForbidden {
		t.Fatalf("attacker 删除文档：HTTP %d code %d msg %q，期望 404/403", status, api.Code, api.Msg)
	}

	// 文件仍在：owner 走文档端点与 infra 端点都能下载。
	if status := downloadDocumentFileRaw(t, env, fx.ownerToken, fx.fileRowID); status != http.StatusOK {
		t.Fatalf("删除被拒后 owner 文档端点下载：HTTP %d，期望 200", status)
	}
	if status, _, body := downloadInfraFileRaw(t, env, fx.ownerToken, fx.infraFileID); status != http.StatusOK || string(body) != secretDocumentBody {
		t.Fatalf("删除被拒后 owner infra 下载：HTTP %d body=%q，期望 200 且内容一致", status, string(body))
	}

	// owner（目录 owner + oa:document:manage）经 infra 端点删除：成功。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/infra/file/%d", fx.infraFileID), fx.ownerToken, nil)
	mustOKForFileACL(t, "owner 经 infra 端点删除文档", status, api)

	// 文件本体与 infra 入口关闭。
	if status := downloadDocumentFileRaw(t, env, fx.ownerToken, fx.fileRowID); status != http.StatusNotFound {
		t.Fatalf("删除后文档端点下载：HTTP %d，期望 404", status)
	}
	if status, _, _ := downloadInfraFileRaw(t, env, fx.ownerToken, fx.infraFileID); status != http.StatusNotFound {
		t.Fatalf("删除后 infra 端点下载：HTTP %d，期望 404", status)
	}

	// 业务归属行同步软删：文档文件列表不再出现悬空条目。
	status, api = env.Request(t, http.MethodGet,
		fmt.Sprintf("/oa/document/file/page?folderId=%d&pageNo=1&pageSize=10", fx.folderID), fx.ownerToken, nil)
	mustOKForFileACL(t, "删除后查询文档文件分页", status, api)
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析文档文件分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 0 {
		t.Fatalf("删除后文档文件列表应为空（业务行同步软删），实际 total=%d", page.Total)
	}
}

// TestIntegrationGenericFileInfraEndpointsUnaffected 回归：biz_type=” 的
// 通用文件在 infra 端点上的上传/下载/分页/删除行为不变（不需要业务授权器）。
func TestIntegrationGenericFileInfraEndpointsUnaffected(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	role := createRoleForFileACL(t, env, admin, "通用文件管理员角色", "generic_file_role",
		[]int64{menuFileQuery, menuFileUpload, menuFileDelete})
	createUserForFileACL(t, env, admin, "generic_file_user", fileACLDeptAttacker, []int64{role})
	user := env.Login(t, "generic_file_user", "123456")

	body := []byte("plain-generic-file")
	file := uploadInfraFile(t, env, user, "通用文件.txt", body)

	status, _, downloaded := downloadInfraFileRaw(t, env, user, file.ID)
	if status != http.StatusOK || !bytes.Equal(downloaded, body) {
		t.Fatalf("通用文件下载：HTTP %d body=%q，期望 200 且内容一致", status, string(downloaded))
	}

	total, ids, st, api := pageInfraFiles(t, env, user, "")
	if st != http.StatusOK || api.Code != 0 || total != 1 || len(ids) != 1 || ids[0] != file.ID {
		t.Fatalf("通用文件默认分页：HTTP %d total=%d ids=%v msg=%q，期望唯一命中 %d",
			st, total, ids, api.Msg, file.ID)
	}

	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/infra/file/%d", file.ID), user, nil)
	mustOKForFileACL(t, "通用文件删除", status, api)
	if status, _, _ := downloadInfraFileRaw(t, env, user, file.ID); status != http.StatusNotFound {
		t.Fatalf("删除后通用文件下载：HTTP %d，期望 404", status)
	}
}

// TestIntegrationUnregisteredBizTypeFailClosed 未注册授权器的业务类型
// （crm_contract）在 infra 通用端点上一律 404（fail-closed）；合同自己的
// 鉴权下载端点不受影响。
func TestIntegrationUnregisteredBizTypeFailClosed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomerForInfraTest(t, env, admin)
	contract := createContractForInfraTest(t, env, admin, customer)

	body := []byte("contract-attachment")
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "合同附件.txt")
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("写入附件内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost,
		env.APIPrefixURL(fmt.Sprintf("/crm/contract/%d/files", contract)), &buf)
	if err != nil {
		t.Fatalf("构造合同附件上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传合同附件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析附件上传响应: %v", err)
	}
	if resp.StatusCode != http.StatusOK || api.Code != 0 {
		t.Fatalf("上传合同附件：HTTP %d code %d msg %q", resp.StatusCode, api.Code, api.Msg)
	}
	var file struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(api.Data, &file); err != nil {
		t.Fatalf("解析附件视图: %v（data=%s）", err, string(api.Data))
	}

	// 合同端点仍按合同 ACL 工作：200。
	req, err = http.NewRequest(http.MethodGet, env.Server.URL+file.URL, nil)
	if err != nil {
		t.Fatalf("构造合同附件下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err = env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载合同附件: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("合同附件下载：HTTP %d，期望 200", resp.StatusCode)
	}

	// 通用 infra 端点：未注册 crm_contract 授权器 → 404（读取与删除都拒绝）。
	if status, _, _ := downloadInfraFileRaw(t, env, admin, file.ID); status != http.StatusNotFound {
		t.Fatalf("未注册业务类型的 infra 下载：HTTP %d，期望 404", status)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/infra/file/%d", file.ID), admin, nil); status != http.StatusNotFound {
		t.Fatalf("未注册业务类型的 infra 删除：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// 删除被拒后合同附件仍可下载。
	req, err = http.NewRequest(http.MethodGet, env.Server.URL+file.URL, nil)
	if err != nil {
		t.Fatalf("构造合同附件二次下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err = env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("二次下载合同附件: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除被拒后合同附件下载：HTTP %d，期望 200", resp.StatusCode)
	}
}
