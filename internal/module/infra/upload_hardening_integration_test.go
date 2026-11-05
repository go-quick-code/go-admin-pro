//go:build integration

// 上传请求体在 multipart 解析前限制（PR #39 审查加固）：
// 整个请求体（文件 + 其他表单字段）超过 20MB + 1MB 开销时返回中文 400，
// 不解析出 FileHeader、不写 infra_file 行。
package infra_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// uploadBodyWithPadding 构造一个「小文件 + 超大非文件字段」的 multipart 请求体：
// 文件本身合法，超限来自请求体填充，只有解析前的 MaxBytesReader 能拦住
// （仅靠 fh.Size 校验会放行并落库）。
func uploadBodyWithPadding(t *testing.T, field, filename string, content, paddingSize int) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write([]byte(strings.Repeat("a", content))); err != nil {
		t.Fatalf("写入文件内容: %v", err)
	}
	if err := w.WriteField("padding", strings.Repeat("x", paddingSize)); err != nil {
		t.Fatalf("写入填充字段: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// pageFileTotal 查询 infra 文件分页总数。
func pageFileTotal(t *testing.T, env *testutil.IntegrationEnv, token string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/infra/file/page?pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询文件分页：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析文件分页: %v（data=%s）", err, string(api.Data))
	}
	return page.Total
}

// TestIntegrationUploadBodyLimitBeforeParse 通用文件上传：小文件 + 21MB 填充字段
// 的请求体超限 → 400（文案含 20MB），且不产生 infra_file 行。
func TestIntegrationUploadBodyLimitBeforeParse(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	body, contentType := uploadBodyWithPadding(t, "file", "ok.pdf", 5, 21<<20)
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/infra/file/upload"), body)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传超限请求: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(api.Msg, "20MB") {
		t.Fatalf("超限上传：HTTP %d code %d msg %q，期望 400 且文案含 20MB", resp.StatusCode, api.Code, api.Msg)
	}
	if total := pageFileTotal(t, env, admin); total != 0 {
		t.Fatalf("超限上传不应产生 infra_file 行，实际 %d 条", total)
	}
}

// TestIntegrationUploadSmallFileStillWorks 加固不误伤正常上传（5 字节 PDF 成功落库）。
func TestIntegrationUploadSmallFileStillWorks(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	body, contentType := uploadBodyWithPadding(t, "file", "ok.pdf", 5, 0)
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/infra/file/upload"), body)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传正常文件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应（HTTP %d）: %v", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || api.Code != 0 {
		t.Fatalf("正常上传：HTTP %d code %d msg %q", resp.StatusCode, api.Code, api.Msg)
	}
	if total := pageFileTotal(t, env, admin); total != 1 {
		t.Fatalf("正常上传应产生 1 条 infra_file，实际 %d 条", total)
	}
}

// createCustomerForInfraTest 创建合同附件用例的宿主客户，返回 ID。
func createCustomerForInfraTest(t *testing.T, env *testutil.IntegrationEnv, token string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/customer", token, map[string]any{
		"name": "附件上传加固客户",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建客户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析客户响应: %v（data=%s）", err, string(api.Data))
	}
	return row.ID
}

// createContractForInfraTest 创建合同附件用例的宿主合同，返回 ID。
func createContractForInfraTest(t *testing.T, env *testutil.IntegrationEnv, token string, customerID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/contract", token, map[string]any{
		"customerId": customerID, "name": "附件上传加固合同", "amount": "100.00",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析合同响应: %v（data=%s）", err, string(api.Data))
	}
	return row.ID
}

// TestIntegrationContractFileBodyLimitBeforeParse 合同附件上传同样在解析前限制：
// 小文件 + 21MB 填充字段 → 400 且不产生附件记录。
func TestIntegrationContractFileBodyLimitBeforeParse(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomerForInfraTest(t, env, admin)
	contract := createContractForInfraTest(t, env, admin, customer)

	body, contentType := uploadBodyWithPadding(t, "file", "ok.pdf", 5, 21<<20)
	req, err := http.NewRequest(http.MethodPost,
		env.APIPrefixURL(fmt.Sprintf("/crm/contract/%d/files", contract)), body)
	if err != nil {
		t.Fatalf("构造合同附件上传请求: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
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
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(api.Msg, "20MB") {
		t.Fatalf("超限附件上传：HTTP %d code %d msg %q，期望 400 且文案含 20MB", resp.StatusCode, api.Code, api.Msg)
	}

	status, api := env.Request(t, http.MethodGet,
		fmt.Sprintf("/crm/contract/%d/files", contract), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询合同附件：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var files []map[string]any
	if err := json.Unmarshal(api.Data, &files); err != nil {
		t.Fatalf("解析附件列表: %v（data=%s）", err, string(api.Data))
	}
	if len(files) != 0 {
		t.Fatalf("超限附件上传不应产生附件记录，实际 %d 条", len(files))
	}
}
