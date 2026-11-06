//go:build integration

// 文档文件上传/下载/删除的 HTTP 集成用例（issue #59，
// docs/oa-mvp-spec.md §3.8/§4.12）：
//   - 有管理权（owner 或 oa:document:manage）可上传/删除，无管理权 403；
//   - 无读权限目录的文件列表 404、下载 404；文件 ID 配可读目录也读不到
//     其他目录的文件（不能绕过 ACL）；
//   - 跨租户 404；未登录 401；
//   - 下载走鉴权端点，URL/响应不含公开静态直链；
//   - 软删后不可列表/下载，目录删除计数只算未软删文件（有文件目录 409）；
//   - 上传同时写 oa_document_file 与 infra_file（biz_type=oa_document，
//     biz_id=目录 ID）。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t59，-p 1 串行）。
package oa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// documentFileResp 是文件列表/上传响应行。
type documentFileResp struct {
	ID           int64  `json:"id"`
	FolderID     int64  `json:"folderId"`
	FileID       int64  `json:"fileId"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Type         string `json:"type"`
	UploaderID   int64  `json:"uploaderId"`
	UploaderName string `json:"uploaderName"`
	CreateTime   string `json:"createTime"`
	URL          string `json:"url"`
}

// uploadDocumentFileAPI 以 multipart 上传文档文件（字段 folderId + file）。
func uploadDocumentFileAPI(t *testing.T, env *testutil.IntegrationEnv, token string, folderID int64, name string, content []byte) (int, testutil.APIResponse) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("folderId", strconv.FormatInt(folderID, 10)); err != nil {
		t.Fatalf("写入 folderId 字段: %v", err)
	}
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
	req, err := http.NewRequest(http.MethodPost,
		env.APIPrefixURL("/oa/document/file/upload"), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传文档文件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	return resp.StatusCode, api
}

// decodeDocumentFile 解析上传响应行。
func decodeDocumentFile(t *testing.T, api testutil.APIResponse) documentFileResp {
	t.Helper()
	var out documentFileResp
	if err := json.Unmarshal(api.Data, &out); err != nil {
		t.Fatalf("解析文件响应: %v（data=%s）", err, string(api.Data))
	}
	return out
}

// listDocumentFilesAPI 读取目录内文件分页；业务失败返回 nil。
func listDocumentFilesAPI(t *testing.T, env *testutil.IntegrationEnv, token string, folderID int64) (int, testutil.APIResponse, []documentFileResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		fmt.Sprintf("/oa/document/file/page?folderId=%d&pageNo=1&pageSize=100", folderID), token, nil)
	if api.Code != 0 {
		return status, api, nil
	}
	var page struct {
		List  []documentFileResp `json:"list"`
		Total int64              `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析文件分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != int64(len(page.List)) {
		t.Fatalf("文件分页 total=%d 与 list 长度 %d 不一致（data=%s）", page.Total, len(page.List), string(api.Data))
	}
	return status, api, page.List
}

// deleteDocumentFileAPI 按文件 ID 删除文档文件。
func deleteDocumentFileAPI(t *testing.T, env *testutil.IntegrationEnv, token string, fileID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/document/file/%d", fileID), token, nil)
}

// documentFileRowOf 读取租户 1 的文件归属行（含软删行）。
func documentFileRowOf(t *testing.T, env *testutil.IntegrationEnv, id int64) oamodel.DocumentFile {
	t.Helper()
	var row oamodel.DocumentFile
	if err := tenant.Skip(env.DB).Unscoped().First(&row, id).Error; err != nil {
		t.Fatalf("查询文档文件行 %d: %v", id, err)
	}
	return row
}

// TestIntegrationDocumentFileLifecycle 覆盖上传 → 列表 → 鉴权下载 →
// 软删除 → 目录删除约束的闭环。
func TestIntegrationDocumentFileLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	folder := createDocumentFolderAPI(t, env, admin, 0, "文件目录", documentTarget(documentACLTargetAll, 0))

	payload := []byte("quarterly report content")
	status, api := uploadDocumentFileAPI(t, env, admin, folder.ID, "季度报告.txt", payload)
	mustOK(t, "上传文件", status, api)
	uploaded := decodeDocumentFile(t, api)
	if uploaded.ID <= 0 || uploaded.FolderID != folder.ID || uploaded.FileID <= 0 {
		t.Fatalf("上传响应缺少归属字段: %+v", uploaded)
	}
	if uploaded.Name != "季度报告.txt" || uploaded.Size != int64(len(payload)) || uploaded.Type == "" {
		t.Fatalf("上传响应展示字段不符: %+v", uploaded)
	}
	if uploaded.UploaderID != 1 || uploaded.CreateTime == "" {
		t.Fatalf("上传人/创建时间不符: %+v", uploaded)
	}
	wantURL := fmt.Sprintf("/oa/document/file/%d/download", uploaded.ID)
	if !strings.Contains(uploaded.URL, wantURL) {
		t.Fatalf("文件 URL 应为鉴权下载端点 %q，实际 %q", wantURL, uploaded.URL)
	}
	if strings.Contains(uploaded.URL, "/infra/file/") || !strings.HasSuffix(uploaded.URL, "/download") {
		t.Fatalf("文件 URL 不应暴露公开/静态直链: %q", uploaded.URL)
	}

	// 归属行与 infra_file 本体同时落地。
	fileRow := documentFileRowOf(t, env, uploaded.ID)
	if fileRow.FolderID != folder.ID || fileRow.FileID != uploaded.FileID ||
		fileRow.Name != "季度报告.txt" || fileRow.Size != int64(len(payload)) ||
		fileRow.UploaderID != 1 || fileRow.DeletedAt.Valid {
		t.Fatalf("oa_document_file 行不符: %+v", fileRow)
	}
	var infraRow inframodel.File
	if err := tenant.Skip(env.DB).First(&infraRow, uploaded.FileID).Error; err != nil {
		t.Fatalf("查询 infra_file 本体: %v", err)
	}
	if infraRow.BizType != "oa_document" || infraRow.BizID != folder.ID || infraRow.Name != "季度报告.txt" {
		t.Fatalf("infra_file 归属不符: %+v", infraRow)
	}

	// 列表：1 条，URL 指向鉴权端点。
	status, api, rows := listDocumentFilesAPI(t, env, admin, folder.ID)
	mustOK(t, "文件列表", status, api)
	if len(rows) != 1 || rows[0].ID != uploaded.ID || rows[0].URL != uploaded.URL {
		t.Fatalf("文件列表不符: %+v", rows)
	}

	// 鉴权下载：内容一致。
	status, got := downloadFile(t, env, admin, uploaded.URL)
	if status != http.StatusOK || !bytes.Equal(got, payload) {
		t.Fatalf("下载文件: HTTP %d body=%q", status, got)
	}

	// 再传一个：新上传在前。
	status, api = uploadDocumentFileAPI(t, env, admin, folder.ID, "第二次.txt", []byte("second"))
	mustOK(t, "第二次上传", status, api)
	second := decodeDocumentFile(t, api)
	status, api, rows = listDocumentFilesAPI(t, env, admin, folder.ID)
	mustOK(t, "二次列表", status, api)
	if len(rows) != 2 || rows[0].ID != second.ID {
		t.Fatalf("新上传应排在列表最前: %+v", rows)
	}

	// 有未软删文件的目录不能删除（409）。
	status, api = deleteDocumentFolderAPI(t, env, admin, folder.ID)
	if status != http.StatusConflict {
		t.Fatalf("有文件目录删除: HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 软删第一个文件：列表少一条、下载 404、归属行 deleted_at 置位、
	// infra 本体保留（不做物理删除）。
	status, api = deleteDocumentFileAPI(t, env, admin, uploaded.ID)
	mustOK(t, "删除文件", status, api)
	status, api, rows = listDocumentFilesAPI(t, env, admin, folder.ID)
	mustOK(t, "删除后列表", status, api)
	if len(rows) != 1 || rows[0].ID != second.ID {
		t.Fatalf("软删后列表应只剩 1 条: %+v", rows)
	}
	status, _ = downloadFile(t, env, admin, uploaded.URL)
	if status != http.StatusNotFound {
		t.Fatalf("软删后下载: HTTP %d，期望 404", status)
	}
	if row := documentFileRowOf(t, env, uploaded.ID); !row.DeletedAt.Valid {
		t.Fatalf("软删后 deleted_at 应置位: %+v", row)
	}
	// infra 侧同步软删（磁盘与行保留，读取入口关闭）。
	var infraAfter inframodel.File
	if err := tenant.Skip(env.DB).Unscoped().First(&infraAfter, uploaded.FileID).Error; err != nil {
		t.Fatalf("软删文件本体不应被物理删除: %v", err)
	}
	if !infraAfter.DeletedAt.Valid {
		t.Fatalf("infra 文件行应同步软删: %+v", infraAfter)
	}
	status, api = deleteDocumentFileAPI(t, env, admin, uploaded.ID)
	if status != http.StatusNotFound {
		t.Fatalf("重复删除文件: HTTP %d code %d，期望 404", status, api.Code)
	}

	// 删除剩下的文件后目录可删（计数不包含软删行）。
	status, api = deleteDocumentFileAPI(t, env, admin, second.ID)
	mustOK(t, "删除第二个文件", status, api)
	status, api = deleteDocumentFolderAPI(t, env, admin, folder.ID)
	mustOK(t, "清空文件后删除目录", status, api)
}

// TestIntegrationDocumentFileACLAndPermissions 覆盖目录管理权（owner /
// oa:document:manage）与读权限（ACL）对文件四个动作的收口。
func TestIntegrationDocumentFileACLAndPermissions(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	queryRole := createRoleWithMenus(t, env, admin, "docfile_query", []int64{documentMineMenuID})
	manageRole := createRoleWithMenus(t, env, admin, "docfile_manage", []int64{documentManageBtn})
	createRole := createRoleWithMenus(t, env, admin, "docfile_create", []int64{documentCreateBtn})

	readerID := createUser(t, env, admin, "docfile_reader", 102)
	managerID := createUser(t, env, admin, "docfile_manager", 102)
	ownerID := createUser(t, env, admin, "docfile_owner", 102)
	outsiderID := createUser(t, env, admin, "docfile_outsider", 102)
	bindDocumentRoles(t, env, admin, "docfile_reader", readerID, 102, []int64{queryRole})
	bindDocumentRoles(t, env, admin, "docfile_manager", managerID, 102, []int64{queryRole, manageRole})
	bindDocumentRoles(t, env, admin, "docfile_owner", ownerID, 102, []int64{queryRole, createRole})
	bindDocumentRoles(t, env, admin, "docfile_outsider", outsiderID, 102, []int64{queryRole})

	reader := env.Login(t, "docfile_reader", "123456")
	manager := env.Login(t, "docfile_manager", "123456")
	owner := env.Login(t, "docfile_owner", "123456")
	outsider := env.Login(t, "docfile_outsider", "123456")

	// 管理员目录：ACL 只给 reader（manager 靠 oa:document:manage 旁路）。
	shared := createDocumentFolderAPI(t, env, admin, 0, "共享目录",
		documentTarget(documentACLTargetUser, readerID))
	status, api := uploadDocumentFileAPI(t, env, admin, shared.ID, "共享.txt", []byte("shared"))
	mustOK(t, "管理员上传共享文件", status, api)
	sharedFile := decodeDocumentFile(t, api)

	// reader：可列表/下载，上传/删除 403（可见但无管理权）。
	status, api, rows := listDocumentFilesAPI(t, env, reader, shared.ID)
	mustOK(t, "reader 列表", status, api)
	if len(rows) != 1 || rows[0].ID != sharedFile.ID {
		t.Fatalf("reader 文件列表不符: %+v", rows)
	}
	status, got := downloadFile(t, env, reader, sharedFile.URL)
	if status != http.StatusOK || string(got) != "shared" {
		t.Fatalf("reader 下载: HTTP %d body=%q", status, got)
	}
	status, api = uploadDocumentFileAPI(t, env, reader, shared.ID, "越权.txt", []byte("x"))
	if status != http.StatusForbidden {
		t.Fatalf("reader 上传: HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	status, api = deleteDocumentFileAPI(t, env, reader, sharedFile.ID)
	if status != http.StatusForbidden {
		t.Fatalf("reader 删除: HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 无 ACL 命中用户：列表/下载均 404（不泄露文件存在性）。
	status, api, _ = listDocumentFilesAPI(t, env, outsider, shared.ID)
	if status != http.StatusNotFound {
		t.Fatalf("outsider 列表: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, _ = downloadFile(t, env, outsider, sharedFile.URL)
	if status != http.StatusNotFound {
		t.Fatalf("outsider 下载: HTTP %d，期望 404", status)
	}

	// 管理码（5262）用户可上传/删除非自己 owner 的可见目录。
	status, api = uploadDocumentFileAPI(t, env, manager, shared.ID, "管理码.txt", []byte("managed"))
	mustOK(t, "管理码上传", status, api)
	managerFile := decodeDocumentFile(t, api)
	status, api = deleteDocumentFileAPI(t, env, manager, managerFile.ID)
	mustOK(t, "管理码删除", status, api)

	// owner 旁路：只有创建码/查询码的员工创建目录后，无需管理码即可上传/删除。
	own := createDocumentFolderAPI(t, env, owner, 0, "owner 目录",
		documentTarget(documentACLTargetUser, ownerID))
	status, api = uploadDocumentFileAPI(t, env, owner, own.ID, "owner.txt", []byte("owner"))
	mustOK(t, "owner 上传", status, api)
	ownerFile := decodeDocumentFile(t, api)
	if ownerFile.UploaderID != ownerID {
		t.Fatalf("owner 上传人 = %d，期望 %d", ownerFile.UploaderID, ownerID)
	}
	status, api = deleteDocumentFileAPI(t, env, owner, ownerFile.ID)
	mustOK(t, "owner 删除", status, api)

	// 文件 ID 不能绕过 ACL：outsider 可读自己的空目录，但直接拿隐藏目录的
	// 文件 ID 下载仍按「文件所属目录不可读」返回 404。
	readable := createDocumentFolderAPI(t, env, admin, 0, "outsider 可读目录",
		documentTarget(documentACLTargetUser, outsiderID))
	hidden := createDocumentFolderAPI(t, env, admin, 0, "隐藏目录",
		documentTarget(documentACLTargetUser, managerID))
	status, api = uploadDocumentFileAPI(t, env, admin, hidden.ID, "隐藏.txt", []byte("hidden"))
	mustOK(t, "隐藏目录上传", status, api)
	hiddenFile := decodeDocumentFile(t, api)

	status, api, rows = listDocumentFilesAPI(t, env, outsider, readable.ID)
	mustOK(t, "可读目录列表", status, api)
	if len(rows) != 0 {
		t.Fatalf("可读空目录不应出现文件: %+v", rows)
	}
	status, api = env.Request(t, http.MethodGet,
		fmt.Sprintf("/oa/document/file/%d/download", hiddenFile.ID), outsider, nil)
	if status != http.StatusNotFound {
		t.Fatalf("文件 ID 绕过目录 ACL: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = listDocumentFilesAPI(t, env, outsider, hidden.ID)
	if status != http.StatusNotFound {
		t.Fatalf("不可读目录列表: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, _ = downloadFile(t, env, outsider,
		fmt.Sprintf("/admin-api/oa/document/file/%d/download", hiddenFile.ID))
	if status != http.StatusNotFound {
		t.Fatalf("不可读目录下载: HTTP %d，期望 404", status)
	}

	// 未登录：401。
	status, api, _ = listDocumentFilesAPI(t, env, "", readable.ID)
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录列表: HTTP %d code %d，期望 401", status, api.Code)
	}
	status, api = uploadDocumentFileAPI(t, env, "", readable.ID, "匿名.txt", []byte("x"))
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录上传: HTTP %d code %d，期望 401", status, api.Code)
	}
}

// TestIntegrationDocumentFileTenantIsolation 跨租户文件读写一律 404。
func TestIntegrationDocumentFileTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	folder := createDocumentFolderAPI(t, env, admin, 0, "租户一文件目录",
		documentTarget(documentACLTargetAll, 0))
	status, api := uploadDocumentFileAPI(t, env, admin, folder.ID, "租户一.txt", []byte("t1"))
	mustOK(t, "租户一上传", status, api)
	file := decodeDocumentFile(t, api)

	// 建租户 2 并以租户 2 admin 登录。
	status, api = env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": "租户二文件", "contactName": "联系人", "status": 0, "domain": "docfile-t2.example.com",
	})
	mustOK(t, "创建租户二", status, api)
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"tenantId": 2, "username": "admin", "password": "123456",
	})
	mustOK(t, "租户二登录", status, api)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &login); err != nil || login.AccessToken == "" {
		t.Fatalf("解析租户二登录: %v（data=%s）", err, string(api.Data))
	}
	t2 := login.AccessToken

	// 跨租户读/写/删全部 404。
	status, api, _ = listDocumentFilesAPI(t, env, t2, folder.ID)
	if status != http.StatusNotFound {
		t.Fatalf("租户二列表跨租户目录: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, _ = downloadFile(t, env, t2,
		fmt.Sprintf("/admin-api/oa/document/file/%d/download", file.ID))
	if status != http.StatusNotFound {
		t.Fatalf("租户二下载跨租户文件: HTTP %d，期望 404", status)
	}
	status, api = uploadDocumentFileAPI(t, env, t2, folder.ID, "越界.txt", []byte("x"))
	if status != http.StatusNotFound {
		t.Fatalf("租户二上传到跨租户目录: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api = deleteDocumentFileAPI(t, env, t2, file.ID)
	if status != http.StatusNotFound {
		t.Fatalf("租户二删除跨租户文件: HTTP %d code %d，期望 404", status, api.Code)
	}

	// 租户二自己的目录/文件正常，且不影响租户一。
	t2Folder := createDocumentFolderAPI(t, env, t2, 0, "租户二文件目录",
		documentTarget(documentACLTargetAll, 0))
	status, api = uploadDocumentFileAPI(t, env, t2, t2Folder.ID, "租户二.txt", []byte("t2"))
	mustOK(t, "租户二上传", status, api)

	status, api, rows := listDocumentFilesAPI(t, env, admin, folder.ID)
	mustOK(t, "租户一列表", status, api)
	if len(rows) != 1 || rows[0].ID != file.ID {
		t.Fatalf("租户一文件列表被跨租户污染: %+v", rows)
	}
	status, api, _ = listDocumentFilesAPI(t, env, t2, t2Folder.ID)
	mustOK(t, "租户二列表", status, api)
}
