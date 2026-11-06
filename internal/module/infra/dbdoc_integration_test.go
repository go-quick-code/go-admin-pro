//go:build integration

// 数据库文档集成测试（issue #83，spec §4.9/§5.8）：走真实 router + JWT/权限/
// 租户中间件 + 真实 MySQL，在 HTTP 契约上验证：
//   - 表清单覆盖 system_*/infra_* 业务表，排除 goose_db_version，关键字过滤；
//   - 表详情列完整（主键/自增/默认值/序号），不存在 404、非法表名 400；
//   - Markdown 导出内容完整、文件名 db-doc-YYYYMMDD-HHmmss.md；
//   - 非平台租户即使持码也 403；无权限码（普通角色）403。
package infra_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

func dbDocMustOK(t *testing.T, label string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", label, status, api.Code, api.Msg)
	}
}

// loginTenantDBDoc 以指定租户登录（登录契约带 tenantId）。
func loginTenantDBDoc(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	dbDocMustOK(t, fmt.Sprintf("登录租户 %d", tenantID), status, api)
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}

// createTenantDBDoc 创建租户并回读 ID（新租户内置 admin/123456）。
func createTenantDBDoc(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	dbDocMustOK(t, "创建租户 "+name, status, api)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10&name="+name, token, nil)
	dbDocMustOK(t, "查询租户 "+name, status, api)
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("租户 %s 应唯一命中: %v（data=%s）", name, err, string(api.Data))
	}
	return int64(page.List[0]["id"].(float64))
}

// TestIntegrationDBDocTables 表清单：含 system_*/infra_*，排除 goose_db_version，
// 关键字按表名过滤。
func TestIntegrationDBDocTables(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/infra/db-doc/tables", token, nil)
	dbDocMustOK(t, "查询表清单", status, api)
	var tables []map[string]any
	if err := json.Unmarshal(api.Data, &tables); err != nil {
		t.Fatalf("解析表清单: %v（data=%s）", err, string(api.Data))
	}
	names := make(map[string]bool, len(tables))
	for _, item := range tables {
		name, _ := item["name"].(string)
		if name == "" {
			t.Fatalf("表清单存在空表名: %v", item)
		}
		names[name] = true
	}
	for _, want := range []string{"system_user", "system_role", "system_tenant", "infra_file", "infra_job", "crm_customer", "oa_leave_request", "message_channel", "notify_message", "bpm_process_definition"} {
		if !names[want] {
			t.Fatalf("表清单应包含业务表 %s（实际 %d 张）", want, len(names))
		}
	}
	if names["goose_db_version"] {
		t.Fatal("表清单不应包含 goose_db_version")
	}

	// 关键字过滤：命中表名（也按注释，但 attendance 只出现在表名里）。
	status, api = env.Request(t, http.MethodGet, "/infra/db-doc/tables?keyword=attendance", token, nil)
	dbDocMustOK(t, "关键字过滤 attendance", status, api)
	if err := json.Unmarshal(api.Data, &tables); err != nil {
		t.Fatalf("解析关键字结果: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("关键字 attendance 应命中 oa_attendance_* 表")
	}
	for _, item := range tables {
		name, _ := item["name"].(string)
		if !strings.Contains(name, "attendance") {
			t.Fatalf("关键字过滤后不应出现 %s", name)
		}
	}
}

// TestIntegrationDBDocTableDetail 表详情：列完整且按序号排列；不存在 404；
// 非法表名 400。
func TestIntegrationDBDocTableDetail(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/infra/db-doc/table/system_user", token, nil)
	dbDocMustOK(t, "查询 system_user 详情", status, api)
	var table struct {
		Name    string `json:"name"`
		Comment string `json:"comment"`
		Columns []struct {
			Name          string  `json:"name"`
			DataType      string  `json:"dataType"`
			Nullable      bool    `json:"nullable"`
			PrimaryKey    bool    `json:"primaryKey"`
			AutoIncrement bool    `json:"autoIncrement"`
			DefaultValue  *string `json:"defaultValue"`
			Ordinal       int     `json:"ordinal"`
		} `json:"columns"`
	}
	if err := json.Unmarshal(api.Data, &table); err != nil {
		t.Fatalf("解析表详情: %v（data=%s）", err, string(api.Data))
	}
	if table.Name != "system_user" || len(table.Columns) == 0 {
		t.Fatalf("表详情不完整: name=%q columns=%d", table.Name, len(table.Columns))
	}
	for i, col := range table.Columns {
		if col.Ordinal != i+1 {
			t.Fatalf("第 %d 列序号为 %d，期望 %d", i, col.Ordinal, i+1)
		}
	}
	var idCol *struct {
		Name          string  `json:"name"`
		DataType      string  `json:"dataType"`
		Nullable      bool    `json:"nullable"`
		PrimaryKey    bool    `json:"primaryKey"`
		AutoIncrement bool    `json:"autoIncrement"`
		DefaultValue  *string `json:"defaultValue"`
		Ordinal       int     `json:"ordinal"`
	}
	for i := range table.Columns {
		if table.Columns[i].Name == "id" {
			idCol = &table.Columns[i]
			break
		}
	}
	if idCol == nil || !idCol.PrimaryKey || !idCol.AutoIncrement {
		t.Fatalf("system_user 的 id 列应为主键自增: %+v", table.Columns)
	}

	// 不存在的表 → 404。
	status, api = env.Request(t, http.MethodGet, "/infra/db-doc/table/no_such_table_xx", token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("不存在的表应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 非法表名（含连字符）→ 400。
	status, api = env.Request(t, http.MethodGet, "/infra/db-doc/table/system-user", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法表名应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// TestIntegrationDBDocExport 导出：Markdown 内容完整、文件名 db-doc-YYYYMMDD-HHmmss.md。
func TestIntegrationDBDocExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, header, body := dbDocExportRaw(t, env, token)
	if status != http.StatusOK {
		t.Fatalf("导出应 200，实际 HTTP %d", status)
	}
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "text/markdown") {
		t.Fatalf("Content-Type 应为 text/markdown，实际 %q", ct)
	}
	disposition := header.Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") {
		t.Fatalf("Content-Disposition 应为 attachment，实际 %q", disposition)
	}
	m := regexp.MustCompile(`filename="?([^";]+)"?`).FindStringSubmatch(disposition)
	if m == nil {
		t.Fatalf("Content-Disposition 缺少 filename: %q", disposition)
	}
	if !regexp.MustCompile(`^db-doc-\d{8}-\d{6}\.md$`).MatchString(m[1]) {
		t.Fatalf("文件名应为 db-doc-YYYYMMDD-HHmmss.md，实际 %q", m[1])
	}

	text := string(body)
	dbName, err := testutil.ParseDSNDatabase(env.Config.Database.DSN)
	if err != nil {
		t.Fatalf("解析测试库名: %v", err)
	}
	for _, want := range []string{
		"# 数据库文档",
		"生成时间：",
		"数据库：" + dbName,
		"## system_user",
		"## infra_file",
		"| id ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("导出内容缺少 %q", want)
		}
	}
	if strings.Contains(text, "goose_db_version") {
		t.Fatal("导出内容不应包含 goose_db_version")
	}
}

// TestIntegrationDBDocPlatformOnly 非平台租户即使持 infra:db-doc:query 也 403。
func TestIntegrationDBDocPlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	tenantID := createTenantDBDoc(t, env, admin, "dbdoc-非平台租户")
	tenantToken := loginTenantDBDoc(t, env, tenantID)

	for _, tc := range []struct{ name, path string }{
		{"表清单", "/infra/db-doc/tables"},
		{"表详情", "/infra/db-doc/table/system_user"},
		{"导出", "/infra/db-doc/export"},
	} {
		status, api := env.Request(t, http.MethodGet, tc.path, tenantToken, nil)
		if status != http.StatusForbidden {
			t.Fatalf("非平台租户 %s 应 403，实际 HTTP %d msg %q", tc.name, status, api.Msg)
		}
	}

	// 导出端点是文件下载，用原始请求再核一遍状态码（上面的 JSON 信封解码
	// 对 403 同样成立，这里保证导出 403 不以文件形式泄漏内容）。
	if status, _, body := dbDocExportRaw(t, env, tenantToken); status != http.StatusForbidden {
		t.Fatalf("非平台租户导出应 403，实际 HTTP %d body=%q", status, truncateForLog(body))
	}
}

// TestIntegrationDBDocPermission 平台租户内无权限码的普通角色（demo）403。
func TestIntegrationDBDocPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "demo", "123456")
	status, api := env.Request(t, http.MethodGet, "/infra/db-doc/tables", token, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无权限码应 403，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// dbDocExportRaw 请求导出端点，返回状态、响应头与原始字节（导出是文件下载，
// 不走统一 JSON 信封）。
func dbDocExportRaw(t *testing.T, env *testutil.IntegrationEnv, token string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.APIPrefixURL("/infra/db-doc/export"), nil)
	if err != nil {
		t.Fatalf("构造导出请求: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("请求导出端点: %v", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 1<<16)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	return resp.StatusCode, resp.Header, body
}

func truncateForLog(b []byte) string {
	s := string(b)
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
