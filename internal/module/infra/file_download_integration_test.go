//go:build integration

// 文件鉴权下载（PR #39 残余风险 A）：公开 /files 静态目录关闭后，
// 上传文件的唯一读取入口是 GET /infra/file/:id/download（挂 infra:file:query），
// 匿名直链 404、未登录 401、无权限 403、跨租户 404，内容与上传一致。
// 合同附件继续走自己的鉴权下载端点，不受本次收口影响。
package infra_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// infraFile 是 infra 文件上传/分页返回的文件视图（只取用例需要的字段）。
type infraFile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	URL  string `json:"url"`
	Type string `json:"type"`
}

// uploadInfraFile 以 multipart 上传一个 infra 文件并断言成功，返回文件视图。
func uploadInfraFile(t *testing.T, env *testutil.IntegrationEnv, token, filename string, content []byte) infraFile {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/infra/file/upload"), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传文件: %v", err)
	}
	defer resp.Body.Close()

	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应（HTTP %d）: %v", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || api.Code != 0 {
		t.Fatalf("上传文件：HTTP %d code %d msg %q", resp.StatusCode, api.Code, api.Msg)
	}
	var file infraFile
	if err := json.Unmarshal(api.Data, &file); err != nil {
		t.Fatalf("解析上传文件视图: %v（data=%s）", err, string(api.Data))
	}
	return file
}

// downloadInfraFileRaw 请求 infra 鉴权下载端点，返回状态、响应头与原始字节。
func downloadInfraFileRaw(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, http.Header, []byte) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, env.APIPrefixURL(fmt.Sprintf("/infra/file/%d/download", id)), nil)
	if err != nil {
		t.Fatalf("构造下载请求: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载文件: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取下载内容: %v", err)
	}
	return resp.StatusCode, resp.Header, body
}

// createTenantForFileTest 通过平台管理员创建租户，返回租户 ID。
func createTenantForFileTest(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()

	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询租户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析租户分页: %v（data=%s）", err, string(api.Data))
	}
	for _, row := range page.List {
		if row.ID > 1 {
			return row.ID
		}
	}
	t.Fatalf("未找到新建租户 %s：%+v", name, page.List)
	return 0
}

// loginTenantForFileTest 用租户 ID 登录该租户管理员。
func loginTenantForFileTest(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()

	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("登录租户 %d 管理员：HTTP %d code %d msg %q", tenantID, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	if data.AccessToken == "" {
		t.Fatalf("登录响应缺少 accessToken: %s", string(api.Data))
	}
	return data.AccessToken
}

// TestIntegrationFileDownloadAuthorization 文件读取收口到鉴权下载端点：
// 公开静态直链 404；未登录 401；无 infra:file:query 403；有权限 200 且字节一致；
// 返回的 URL 是鉴权下载地址（上传/分页一致）；跨租户文件 404。
func TestIntegrationFileDownloadAuthorization(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	body := []byte("authenticated-file-body")
	file := uploadInfraFile(t, env, admin, "机密文件.txt", body)

	wantURL := fmt.Sprintf("/admin-api/infra/file/%d/download", file.ID)
	if file.URL != wantURL {
		t.Fatalf("上传返回的 URL 应为鉴权下载地址 %q，实际 %q", wantURL, file.URL)
	}
	if strings.HasPrefix(file.URL, "/files/") {
		t.Fatalf("上传返回的 URL 不应再指向公开静态目录，实际 %q", file.URL)
	}

	// 分页列表同样返回鉴权下载地址（存储列的旧公开值由 service 覆盖）。
	status, api := env.Request(t, http.MethodGet, "/infra/file/page?pageNo=1&pageSize=10", admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询文件分页：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		List []infraFile `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析文件分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 || page.List[0].URL != wantURL {
		t.Fatalf("分页应返回 1 条且 URL=%q，实际 %+v", wantURL, page.List)
	}

	// 公开静态目录已移除：匿名直链 404。
	req, err := http.NewRequest(http.MethodGet, env.Server.URL+"/files/"+file.Path, nil)
	if err != nil {
		t.Fatalf("构造公开直链请求: %v", err)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("请求公开直链: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("匿名 GET /files/%s：HTTP %d，期望 404（静态目录应已移除）", file.Path, resp.StatusCode)
	}

	// 未登录下载：401。
	if status, _, _ := downloadInfraFileRaw(t, env, "", file.ID); status != http.StatusUnauthorized {
		t.Fatalf("未登录下载：HTTP %d，期望 401", status)
	}

	// 无 infra:file:query 权限码（演示账号只有普通角色）：403。
	demo := env.Login(t, "demo", "123456")
	if status, _, _ := downloadInfraFileRaw(t, env, demo, file.ID); status != http.StatusForbidden {
		t.Fatalf("无权限码下载：HTTP %d，期望 403", status)
	}

	// 有权限：200，内容与上传一致，Content-Type/Content-Disposition 正确。
	status, header, downloaded := downloadInfraFileRaw(t, env, admin, file.ID)
	if status != http.StatusOK || !bytes.Equal(downloaded, body) {
		t.Fatalf("鉴权下载：HTTP %d body=%q，期望 200 且内容一致", status, string(downloaded))
	}
	if ct := header.Get("Content-Type"); ct == "" {
		t.Fatal("鉴权下载应返回 Content-Type")
	}
	disp := header.Get("Content-Disposition")
	if !strings.HasPrefix(disp, "attachment;") ||
		!strings.Contains(disp, fmt.Sprintf(`filename="file-%d.txt"`, file.ID)) ||
		!strings.Contains(disp, "filename*=UTF-8''%E6%9C%BA%E5%AF%86%E6%96%87%E4%BB%B6.txt") {
		t.Fatalf("Content-Disposition 应带安全 ASCII 回退名与 RFC 5987 中文名，实际 %q", disp)
	}

	// 不存在的文件：404。
	if status, _, _ := downloadInfraFileRaw(t, env, admin, 999999); status != http.StatusNotFound {
		t.Fatalf("下载不存在的文件：HTTP %d，期望 404", status)
	}

	// 跨租户：租户 2 管理员持有 super_admin（*），但文件按租户过滤 → 404。
	tenant2 := createTenantForFileTest(t, env, admin, "文件下载隔离租户")
	admin2 := loginTenantForFileTest(t, env, tenant2)
	if status, _, _ := downloadInfraFileRaw(t, env, admin2, file.ID); status != http.StatusNotFound {
		t.Fatalf("跨租户下载：HTTP %d，期望 404", status)
	}
}

// TestIntegrationContractFileDownloadUnaffected 合同附件仍走自己的鉴权下载端点：
// 上传返回的 URL 指向合同下载地址，下载内容与上传一致。
func TestIntegrationContractFileDownloadUnaffected(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomerForInfraTest(t, env, admin)
	contract := createContractForInfraTest(t, env, admin, customer)

	body := []byte("contract-attachment-body")
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
	wantURL := fmt.Sprintf("/admin-api/crm/contract/%d/files/%d/download", contract, file.ID)
	if file.URL != wantURL {
		t.Fatalf("合同附件 URL 应保持合同下载地址 %q，实际 %q", wantURL, file.URL)
	}

	// 附件下载走合同端点：内容一致（不依赖已移除的 /files 静态目录）。
	dreq, err := http.NewRequest(http.MethodGet, env.Server.URL+file.URL, nil)
	if err != nil {
		t.Fatalf("构造附件下载请求: %v", err)
	}
	dreq.Header.Set("Authorization", "Bearer "+admin)
	dresp, err := env.Server.Client().Do(dreq)
	if err != nil {
		t.Fatalf("下载合同附件: %v", err)
	}
	defer dresp.Body.Close()
	downloaded, err := io.ReadAll(dresp.Body)
	if err != nil {
		t.Fatalf("读取附件内容: %v", err)
	}
	if dresp.StatusCode != http.StatusOK || !bytes.Equal(downloaded, body) {
		t.Fatalf("合同附件下载：HTTP %d body=%q，期望 200 且内容一致", dresp.StatusCode, string(downloaded))
	}
}
