//go:build integration

// 合同附件 HTTP 集成测试（issue #17）：真实 router + 生产中间件 + 真实 MySQL，
// 在 HTTP 契约上验证上传-列表-下载-删除全链路、文件归属（跨合同 fileId 404）、
// 合同数据范围与租户隔离、权限跟随合同（不要求 infra:file:*）、作废合同上传 400、
// 大小/扩展名限制与 multipart 缺字段 400。
package crm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// uploadContractFile 以 multipart/form-data 上传合同附件，返回 HTTP 状态与统一响应。
// field 为空时构造一个不含文件字段的表单（校验「请选择要上传的文件」）。
func uploadContractFile(t *testing.T, env *testutil.IntegrationEnv, token string, contractID int64, field, filename string, content []byte) (int, testutil.APIResponse) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if field != "" {
		part, err := w.CreateFormFile(field, filename)
		if err != nil {
			t.Fatalf("构造 multipart 文件字段: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("写入 multipart 内容: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL(fmt.Sprintf("/crm/contract/%d/files", contractID)), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传合同附件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	return resp.StatusCode, api
}

// listContractFilesRaw 查询合同附件列表（原始响应，便于断言 404/403）。
func listContractFilesRaw(t *testing.T, env *testutil.IntegrationEnv, token string, contractID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/crm/contract/%d/files", contractID), token, nil)
}

// listContractFiles 查询合同附件列表并断言成功，返回附件数组。
func listContractFiles(t *testing.T, env *testutil.IntegrationEnv, token string, contractID int64) []map[string]any {
	t.Helper()
	status, api := listContractFilesRaw(t, env, token, contractID)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询合同附件列表：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var files []map[string]any
	if err := json.Unmarshal(api.Data, &files); err != nil {
		t.Fatalf("解析附件列表: %v（data=%s）", err, string(api.Data))
	}
	return files
}

// deleteContractFileRaw 删除合同附件（原始响应）。
func deleteContractFileRaw(t *testing.T, env *testutil.IntegrationEnv, token string, contractID, fileID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d/files/%d", contractID, fileID), token, nil)
}

// downloadContractFileRaw 走鉴权下载端点取原始响应：返回状态、响应头与字节内容
// （附件是二进制，不能按统一 JSON 信封解码）。
func downloadContractFileRaw(t *testing.T, env *testutil.IntegrationEnv, token string, contractID, fileID int64) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		env.APIPrefixURL(fmt.Sprintf("/crm/contract/%d/files/%d/download", contractID, fileID)), nil)
	if err != nil {
		t.Fatalf("构造附件下载请求: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载合同附件: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取附件内容: %v", err)
	}
	return resp.StatusCode, resp.Header, body
}

// TestIntegrationContractFileLifecycle 全链路：上传（落 infra_file 归属）→ 列表 →
// 通过返回的 URL 下载 → 再上传 → 删除 → 列表与磁盘同步；multipart 缺文件 400。
// 并使用只持有合同菜单/按钮（无 infra:file:*）的用户完成一次上传，验证权限跟随合同。
func TestIntegrationContractFileLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "附件生命周期宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "附件生命周期合同", map[string]any{"customerId": customerID})
	contractID := int64(contract["id"].(float64))

	body := []byte("contract-body-v1")
	status, api := uploadContractFile(t, env, admin, contractID, "file", "合同正文.pdf", body)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("上传合同附件：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var first map[string]any
	if err := json.Unmarshal(api.Data, &first); err != nil {
		t.Fatalf("解析上传响应: %v（data=%s）", err, string(api.Data))
	}
	if first["name"] != "合同正文.pdf" {
		t.Fatalf("附件名称应为原文件名，实际 %v", first["name"])
	}
	if first["size"] != float64(len(body)) {
		t.Fatalf("附件大小应为 %d，实际 %v", len(body), first["size"])
	}
	firstID := int64(first["id"].(float64))
	url, _ := first["url"].(string)
	wantURL := fmt.Sprintf("/admin-api/crm/contract/%d/files/%d/download", contractID, firstID)
	if url != wantURL {
		t.Fatalf("附件 URL 应为鉴权下载地址 %q，实际 %q", wantURL, url)
	}
	if first["createTime"] == nil || first["createTime"] == "" {
		t.Fatalf("附件应返回上传时间，实际 %v", first["createTime"])
	}

	files := listContractFiles(t, env, admin, contractID)
	if len(files) != 1 || int64(files[0]["id"].(float64)) != firstID {
		t.Fatalf("列表应返回刚上传的附件，实际 %d 条", len(files))
	}

	// 下载：鉴权端点返回上传内容，Content-Type 取存储值，Content-Disposition 为 attachment。
	dStatus, dHeader, downloaded := downloadContractFileRaw(t, env, admin, contractID, firstID)
	if dStatus != http.StatusOK || !bytes.Equal(downloaded, body) {
		t.Fatalf("下载附件内容不符：HTTP %d body=%q", dStatus, string(downloaded))
	}
	if ct := dHeader.Get("Content-Type"); ct != first["type"] {
		t.Fatalf("附件 Content-Type = %q，期望存储值 %q", ct, first["type"])
	}
	if disp := dHeader.Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") ||
		!strings.Contains(disp, "filename*=UTF-8''") {
		t.Fatalf("附件应带 attachment 与 filename*，实际 %q", disp)
	}

	// 再上传一个：列表新→旧。
	status, api = uploadContractFile(t, env, admin, contractID, "file", "补充协议.docx", []byte("appendix"))
	mustOK(t, "上传第二个附件", status, api)
	var second map[string]any
	if err := json.Unmarshal(api.Data, &second); err != nil {
		t.Fatalf("解析第二个附件: %v", err)
	}
	secondID := int64(second["id"].(float64))
	files = listContractFiles(t, env, admin, contractID)
	if len(files) != 2 || int64(files[0]["id"].(float64)) != secondID {
		t.Fatalf("附件列表应为 2 条且新上传在前，实际 %d 条首条 id=%v", len(files), files[0]["id"])
	}

	// 删除第一个：记录与磁盘文件都消失；重复删除 404。
	if status, api = deleteContractFileRaw(t, env, admin, contractID, firstID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除附件：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	files = listContractFiles(t, env, admin, contractID)
	if len(files) != 1 || int64(files[0]["id"].(float64)) != secondID {
		t.Fatalf("删除后列表应只剩第二个附件，实际 %d 条", len(files))
	}
	if dStatus, _, _ = downloadContractFileRaw(t, env, admin, contractID, firstID); dStatus != http.StatusNotFound {
		t.Fatalf("删除后旧下载链接应 404，实际 HTTP %d", dStatus)
	}
	if status, api = deleteContractFileRaw(t, env, admin, contractID, firstID); status != http.StatusNotFound {
		t.Fatalf("重复删除附件：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// multipart 缺文件字段：400。
	if status, api = uploadContractFile(t, env, admin, contractID, "", "", nil); status != http.StatusBadRequest {
		t.Fatalf("缺文件字段上传：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 权限跟随合同：只有合同菜单/按钮（4100/4101/4103）的用户可上传/删除，
	// 不需要 infra:file:* 权限码；合同在「全部」数据范围内可见。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "合同附件操作员角色", "code": "contract_file_writer", "dataScope": 1,
		"menuIds": []int64{crmContractMenuID, 4101, 4103},
	})
	createUserByAPI(t, env, admin, "contract_file_writer", 102, []int64{roleID})
	writer := env.Login(t, "contract_file_writer", "123456")
	status, api = uploadContractFile(t, env, writer, contractID, "file", "操作员上传.txt", []byte("writer"))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("无 infra:file 权限码的合同操作员上传：HTTP %d code %d msg %q，期望放行", status, api.Code, api.Msg)
	}
	var writerFile map[string]any
	if err := json.Unmarshal(api.Data, &writerFile); err != nil {
		t.Fatalf("解析操作员上传响应: %v", err)
	}
	if status, api = deleteContractFileRaw(t, env, writer, contractID, int64(writerFile["id"].(float64))); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("合同操作员删除附件：HTTP %d code %d msg %q，期望放行", status, api.Code, api.Msg)
	}
}

// TestIntegrationContractFileOwnershipAndScope 文件归属与合同数据范围：
// 跨合同 fileId 404（不误删）；数据范围外合同的列表/上传/删除一律 404。
func TestIntegrationContractFileOwnershipAndScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "附件归属宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contractA := createContract(t, env, admin, "附件归属合同A", map[string]any{"customerId": customerID})
	contractB := createContract(t, env, admin, "附件归属合同B", map[string]any{"customerId": customerID})
	aID := int64(contractA["id"].(float64))
	bID := int64(contractB["id"].(float64))

	status, api := uploadContractFile(t, env, admin, aID, "file", "A的附件.pdf", []byte("a"))
	mustOK(t, "给合同 A 上传附件", status, api)
	var file map[string]any
	if err := json.Unmarshal(api.Data, &file); err != nil {
		t.Fatalf("解析附件: %v", err)
	}
	fileID := int64(file["id"].(float64))

	// 跨合同删除：404，合同 A 的附件仍在，合同 B 列表为空。
	if status, api = deleteContractFileRaw(t, env, admin, bID, fileID); status != http.StatusNotFound {
		t.Fatalf("跨合同删除附件：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	if files := listContractFiles(t, env, admin, aID); len(files) != 1 {
		t.Fatalf("跨合同删除不应误删，合同 A 附件数=%d，期望 1", len(files))
	}
	if files := listContractFiles(t, env, admin, bID); len(files) != 0 {
		t.Fatalf("合同 B 不应看到合同 A 的附件，实际 %d 条", len(files))
	}

	// 数据范围外（仅本人）：他人合同的所有附件操作 404。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "附件范围观察者角色", "code": "contract_file_scope", "dataScope": 5,
		"menuIds": []int64{crmContractMenuID, 4101, 4103},
	})
	createUserByAPI(t, env, admin, "contract_file_scope", 102, []int64{roleID})
	observer := env.Login(t, "contract_file_scope", "123456")

	if status, _ := listContractFilesRaw(t, env, observer, aID); status != http.StatusNotFound {
		t.Fatalf("范围外附件列表：HTTP %d，期望 404", status)
	}
	if status, api := uploadContractFile(t, env, observer, aID, "file", "越权.pdf", []byte("x")); status != http.StatusNotFound {
		t.Fatalf("范围外上传附件：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	if status, api := deleteContractFileRaw(t, env, observer, aID, fileID); status != http.StatusNotFound {
		t.Fatalf("范围外删除附件：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// 不存在合同的附件列表同样 404（不泄露存在性）。
	if status, _ := listContractFilesRaw(t, env, admin, 999999); status != http.StatusNotFound {
		t.Fatalf("不存在合同的附件列表：HTTP %d，期望 404", status)
	}
}

// TestIntegrationContractFileVoidAndLimits 作废合同上传 400（已有附件仍可查看/删除）；
// 超大与非法扩展名 400，非法上传不落库。
func TestIntegrationContractFileVoidAndLimits(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "附件作废宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "附件作废合同", map[string]any{"customerId": customerID})
	contractID := int64(contract["id"].(float64))

	status, api := uploadContractFile(t, env, admin, contractID, "file", "作废前附件.pdf", []byte("before-void"))
	mustOK(t, "作废前上传附件", status, api)

	if status, api = changeContractStatus(t, env, admin, contractID, 4, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("作废合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = uploadContractFile(t, env, admin, contractID, "file", "作废后附件.pdf", []byte("after-void"))
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "作废") {
		t.Fatalf("作废合同上传附件：HTTP %d code %d msg %q，期望 400 且文案含「作废」", status, api.Code, api.Msg)
	}
	// 作废合同的既有附件仍可查看与删除（清理不受状态限制）。
	files := listContractFiles(t, env, admin, contractID)
	if len(files) != 1 {
		t.Fatalf("作废合同既有附件应仍可查看，实际 %d 条", len(files))
	}
	if status, api = deleteContractFileRaw(t, env, admin, contractID, int64(files[0]["id"].(float64))); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("作废合同删除既有附件：HTTP %d code %d msg %q，期望放行", status, api.Code, api.Msg)
	}

	// 大小/扩展名限制用一张非作废合同验证（作废合同会先被状态校验拦下）。
	limitsContract := createContract(t, env, admin, "附件限制合同", map[string]any{"customerId": customerID})
	limitsID := int64(limitsContract["id"].(float64))

	// 大小限制：20MB + 1 拒绝（内存构造，不落盘）。
	oversize := bytes.Repeat([]byte("x"), 20<<20+1)
	status, api = uploadContractFile(t, env, admin, limitsID, "file", "big.pdf", oversize)
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "20MB") {
		t.Fatalf("超大附件：HTTP %d code %d msg %q，期望 400 且文案含「20MB」", status, api.Code, api.Msg)
	}

	// 扩展名白名单：.exe 拒绝。
	status, api = uploadContractFile(t, env, admin, limitsID, "file", "evil.exe", []byte("x"))
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "不支持") {
		t.Fatalf("非法扩展名：HTTP %d code %d msg %q，期望 400 且文案含「不支持」", status, api.Code, api.Msg)
	}
	if files := listContractFiles(t, env, admin, limitsID); len(files) != 0 {
		t.Fatalf("非法上传不应落库，实际 %d 条", len(files))
	}
}

// TestIntegrationContractFilePermissionDenied 权限跟随合同：无合同查询码 403；
// 只有查询码时列表放行、上传/删除 403（不需要也不校验 infra:file:*）。
func TestIntegrationContractFilePermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "附件权限宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "附件权限合同", map[string]any{"customerId": customerID})
	contractID := int64(contract["id"].(float64))
	status, api := uploadContractFile(t, env, admin, contractID, "file", "权限附件.pdf", []byte("perm"))
	mustOK(t, "准备附件", status, api)
	var file map[string]any
	if err := json.Unmarshal(api.Data, &file); err != nil {
		t.Fatalf("解析附件: %v", err)
	}
	fileID := int64(file["id"].(float64))

	// 无任何合同权限码（只绑客户菜单 401）：统一 403。
	noPermRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "附件无权限角色", "code": "contract_file_noperm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "contract_file_noperm", 102, []int64{noPermRole})
	noPerm := env.Login(t, "contract_file_noperm", "123456")

	if status, _ := listContractFilesRaw(t, env, noPerm, contractID); status != http.StatusForbidden {
		t.Fatalf("无查询码查附件：HTTP %d，期望 403", status)
	}
	if status, _ := uploadContractFile(t, env, noPerm, contractID, "file", "越权.pdf", []byte("x")); status != http.StatusForbidden {
		t.Fatalf("无更新码上传附件：HTTP %d，期望 403", status)
	}
	if status, _ := deleteContractFileRaw(t, env, noPerm, contractID, fileID); status != http.StatusForbidden {
		t.Fatalf("无更新码删除附件：HTTP %d，期望 403", status)
	}

	// 只有查询码（4100 菜单 + 4101 查询按钮）：列表放行，上传/删除 403。
	queryRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "附件只读角色", "code": "contract_file_readonly", "dataScope": 1,
		"menuIds": []int64{crmContractMenuID, 4101},
	})
	createUserByAPI(t, env, admin, "contract_file_readonly", 102, []int64{queryRole})
	readonly := env.Login(t, "contract_file_readonly", "123456")
	if files := listContractFiles(t, env, readonly, contractID); len(files) != 1 {
		t.Fatalf("只读角色应能看到附件列表，实际 %d 条", len(files))
	}
	if status, _ := uploadContractFile(t, env, readonly, contractID, "file", "只读上传.pdf", []byte("x")); status != http.StatusForbidden {
		t.Fatalf("只读角色上传附件：HTTP %d，期望 403", status)
	}
	if status, _ := deleteContractFileRaw(t, env, readonly, contractID, fileID); status != http.StatusForbidden {
		t.Fatalf("只读角色删除附件：HTTP %d，期望 403", status)
	}
}

// TestIntegrationContractFileTenantIsolation 租户隔离：跨租户合同不可见（404），
// 跨租户删除他人附件 404 且不误删（infra_file 与合同都受租户插件约束）。
func TestIntegrationContractFileTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "附件隔离宿主客户一", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "租户一附件合同", map[string]any{"customerId": customerID})
	contractID := int64(contract["id"].(float64))
	status, api := uploadContractFile(t, env, admin, contractID, "file", "租户一附件.pdf", []byte("tenant1"))
	mustOK(t, "租户一上传附件", status, api)
	var t1File map[string]any
	if err := json.Unmarshal(api.Data, &t1File); err != nil {
		t.Fatalf("解析租户一附件: %v", err)
	}
	t1FileID := int64(t1File["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "附件隔离租户二")
	admin2 := loginTenant(t, env, "admin", "123456", tenant2)
	customer2 := createCustomer(t, env, admin2, "附件隔离宿主客户二", nil)
	customer2ID := int64(customer2["id"].(float64))
	contract2 := createContract(t, env, admin2, "租户二附件合同", map[string]any{"customerId": customer2ID})
	contract2ID := int64(contract2["id"].(float64))
	status, api = uploadContractFile(t, env, admin2, contract2ID, "file", "租户二附件.pdf", []byte("tenant2"))
	mustOK(t, "租户二上传附件", status, api)
	var t2File map[string]any
	if err := json.Unmarshal(api.Data, &t2File); err != nil {
		t.Fatalf("解析租户二附件: %v", err)
	}
	t2FileID := int64(t2File["id"].(float64))

	// 交叉可见性：各自的合同在对方租户不可见。
	if status, _ := listContractFilesRaw(t, env, admin2, contractID); status != http.StatusNotFound {
		t.Fatalf("租户二查租户一附件：HTTP %d，期望 404", status)
	}
	if status, _ := listContractFilesRaw(t, env, admin, contract2ID); status != http.StatusNotFound {
		t.Fatalf("租户一查租户二附件：HTTP %d，期望 404", status)
	}

	// 跨租户删除：用本租户合同 + 对方 fileId，归属与租户双重过滤后 404。
	if status, api := deleteContractFileRaw(t, env, admin2, contract2ID, t1FileID); status != http.StatusNotFound {
		t.Fatalf("租户二跨租户删除租户一附件：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	if status, api := deleteContractFileRaw(t, env, admin, contractID, t2FileID); status != http.StatusNotFound {
		t.Fatalf("租户一跨租户删除租户二附件：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// 双方附件都未被误删。
	if files := listContractFiles(t, env, admin, contractID); len(files) != 1 {
		t.Fatalf("租户一附件应仍为 1 条，实际 %d", len(files))
	}
	if files := listContractFiles(t, env, admin2, contract2ID); len(files) != 1 {
		t.Fatalf("租户二附件应仍为 1 条，实际 %d", len(files))
	}
}

// TestIntegrationContractFileDownloadAuthorization 鉴权下载（PR #39 审查 P2）：
// 未登录 401；无合同查询码 403；只读角色可下载；跨合同 fileId 404；
// 数据范围外合同 404；文件不存在 404。公开静态目录不再是附件的可用直链。
func TestIntegrationContractFileDownloadAuthorization(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "附件下载宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contractA := createContract(t, env, admin, "下载合同A", map[string]any{"customerId": customerID})
	contractB := createContract(t, env, admin, "下载合同B", map[string]any{"customerId": customerID})
	aID := int64(contractA["id"].(float64))
	bID := int64(contractB["id"].(float64))

	body := []byte("download-body")
	status, api := uploadContractFile(t, env, admin, aID, "file", "下载附件.txt", body)
	mustOK(t, "准备下载附件", status, api)
	var file map[string]any
	if err := json.Unmarshal(api.Data, &file); err != nil {
		t.Fatalf("解析附件: %v", err)
	}
	fileID := int64(file["id"].(float64))

	// 未登录：401（公开静态目录的旧直链不再适用）。
	if status, _, _ := downloadContractFileRaw(t, env, "", aID, fileID); status != http.StatusUnauthorized {
		t.Fatalf("未登录下载附件：HTTP %d，期望 401", status)
	}

	// 无合同查询码：403。
	noPermRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "下载无权限角色", "code": "contract_download_noperm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "contract_download_noperm", 102, []int64{noPermRole})
	noPerm := env.Login(t, "contract_download_noperm", "123456")
	if status, _, _ := downloadContractFileRaw(t, env, noPerm, aID, fileID); status != http.StatusForbidden {
		t.Fatalf("无查询码下载附件：HTTP %d，期望 403", status)
	}

	// 只读角色（合同菜单 + 查询按钮）：正常下载，内容与上传一致。
	readRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "下载只读角色", "code": "contract_download_readonly", "dataScope": 1,
		"menuIds": []int64{crmContractMenuID, 4101},
	})
	createUserByAPI(t, env, admin, "contract_download_readonly", 102, []int64{readRole})
	readonly := env.Login(t, "contract_download_readonly", "123456")
	dStatus, _, downloaded := downloadContractFileRaw(t, env, readonly, aID, fileID)
	if dStatus != http.StatusOK || !bytes.Equal(downloaded, body) {
		t.Fatalf("只读角色下载：HTTP %d body=%q，期望 200 且内容一致", dStatus, string(downloaded))
	}

	// 跨合同：合同 B 的路径访问合同 A 的文件 → 404（归属校验）。
	if status, _, _ := downloadContractFileRaw(t, env, admin, bID, fileID); status != http.StatusNotFound {
		t.Fatalf("跨合同下载：HTTP %d，期望 404", status)
	}

	// 数据范围外（仅本人）：合同不可见 → 404。
	scopeRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "下载范围外角色", "code": "contract_download_scope", "dataScope": 5,
		"menuIds": []int64{crmContractMenuID, 4101},
	})
	createUserByAPI(t, env, admin, "contract_download_scope", 102, []int64{scopeRole})
	scopeUser := env.Login(t, "contract_download_scope", "123456")
	if status, _, _ := downloadContractFileRaw(t, env, scopeUser, aID, fileID); status != http.StatusNotFound {
		t.Fatalf("范围外下载：HTTP %d，期望 404", status)
	}

	// 文件不存在、合同不存在：404。
	if status, _, _ := downloadContractFileRaw(t, env, admin, aID, 999999); status != http.StatusNotFound {
		t.Fatalf("下载不存在的文件：HTTP %d，期望 404", status)
	}
	if status, _, _ := downloadContractFileRaw(t, env, admin, 999999, fileID); status != http.StatusNotFound {
		t.Fatalf("下载不存在合同的文件：HTTP %d，期望 404", status)
	}
}
