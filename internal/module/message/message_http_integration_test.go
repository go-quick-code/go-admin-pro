//go:build integration

// 消息模块 HTTP 集成测试（issue #72）：走真实 router + 生产中间件（JWT/权限/
// 租户插件）+ 真实 MySQL，在 HTTP 契约上验证：
//   - 渠道/模板仅平台租户可管理（非平台 403），密钥查询脱敏、****** 更新保留原值；
//   - 发送成功/失败都写日志，失败返回 502 且详情不回显；
//   - 参数缺失/多余、收件人格式、模板/渠道停用一律 400；
//   - 发送日志租户隔离（跨租户 404）、手动重发只允许失败行、删除/清空。
package message_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

const (
	seededSMSChannelCode  = "sms-log"
	seededMailChannelCode = "mail-log"
	seededTemplateCode    = "welcome"
)

// failingSender 是 provider 失败的测试替身（经 env.App.Message 注册）。
type failingSender struct{}

func (failingSender) Provider() string { return "failing" }
func (failingSender) Send(context.Context, service.SendRequest) (service.SendResult, error) {
	return service.SendResult{}, errors.New("provider boom")
}

func mustOK(t *testing.T, label string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", label, status, api.Code, api.Msg)
	}
}

func decodeMap(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatalf("解析响应对象: %v（data=%s）", err, string(data))
	}
	return row
}

func decodePage(t *testing.T, data json.RawMessage) ([]map[string]any, int64) {
	t.Helper()
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("解析分页响应: %v（data=%s）", err, string(data))
	}
	return page.List, page.Total
}

// loginTenant 以指定租户登录（登录契约带 tenantId）。
func loginTenant(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	mustOK(t, fmt.Sprintf("登录租户 %d", tenantID), status, api)
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}

// createTenantByAPI 创建租户并回读 ID（新租户内置 admin/123456）。
func createTenantByAPI(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	mustOK(t, "创建租户 "+name, status, api)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10&name="+name, token, nil)
	mustOK(t, "查询租户 "+name, status, api)
	list, total := decodePage(t, api.Data)
	if total != 1 || len(list) != 1 {
		t.Fatalf("租户 %s 应唯一命中，实际 total=%d list=%d", name, total, len(list))
	}
	return int64(list[0]["id"].(float64))
}

func createChannel(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/message/channel", token, payload)
	mustOK(t, "创建渠道 "+fmt.Sprint(payload["code"]), status, api)
	return decodeMap(t, api.Data)
}

func pageChannels(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/message/channel/page?pageNo=1&pageSize=50"+query, token, nil)
	mustOK(t, "查询渠道分页"+query, status, api)
	return decodePage(t, api.Data)
}

func createTemplate(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/message/template", token, payload)
	mustOK(t, "创建模板 "+fmt.Sprint(payload["code"]), status, api)
	return decodeMap(t, api.Data)
}

func pageTemplates(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/message/template/page?pageNo=1&pageSize=50"+query, token, nil)
	mustOK(t, "查询模板分页"+query, status, api)
	return decodePage(t, api.Data)
}

func pageLogs(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/message/log/page?pageNo=1&pageSize=50"+query, token, nil)
	mustOK(t, "查询发送日志"+query, status, api)
	return decodePage(t, api.Data)
}

func sendMessage(t *testing.T, env *testutil.IntegrationEnv, token, path string, payload map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, path, token, payload)
}

// channelConfigFromDB 直读平台表配置，验证脱敏只发生在 API 层。
func channelConfigFromDB(t *testing.T, env *testutil.IntegrationEnv, id int64) string {
	t.Helper()
	var cfg string
	if err := env.DB.Raw("SELECT config FROM message_channel WHERE id = ?", id).Scan(&cfg).Error; err != nil {
		t.Fatalf("读取渠道 %d 配置: %v", id, err)
	}
	return cfg
}

// TestIntegrationMessageChannelTemplatePlatformOnly 覆盖渠道/模板 CRUD、
// 密钥脱敏与「仅平台租户」收口。
func TestIntegrationMessageChannelTemplatePlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 创建渠道：响应中的密钥必须已脱敏。
	created := createChannel(t, env, admin, map[string]any{
		"channelType": "sms", "code": "sms-crud-1", "name": "测试短信渠道", "provider": "log",
		"config": `{"username":"u","password":"p","secret":"s","accessKeySecret":"a"}`,
		"status": 0, "remark": "crud",
	})
	channelID := int64(created["id"].(float64))
	configText, _ := created["config"].(string)
	if !strings.Contains(configText, "******") {
		t.Fatalf("创建响应未脱敏：%s", configText)
	}
	for _, secret := range []string{`"password":"p"`, `"secret":"s"`, `"accessKeySecret":"a"`} {
		if strings.Contains(configText, secret) {
			t.Fatalf("创建响应泄露密钥 %s：%s", secret, configText)
		}
	}

	// 列表同样脱敏。
	list, total := pageChannels(t, env, admin, "&channelType=sms&code=sms-crud-1")
	if total != 1 || len(list) != 1 {
		t.Fatalf("渠道列表 total=%d len=%d，期望 1", total, len(list))
	}
	if cfg, _ := list[0]["config"].(string); !strings.Contains(cfg, "******") {
		t.Fatalf("列表响应未脱敏：%s", cfg)
	}

	// 库里仍是明文（本轮不做 KMS）。
	stored := channelConfigFromDB(t, env, channelID)
	for _, secret := range []string{`"password":"p"`, `"secret":"s"`, `"accessKeySecret":"a"`} {
		if !strings.Contains(stored, secret) {
			t.Fatalf("库内应保留明文密钥 %s：%s", secret, stored)
		}
	}

	// 更新：****** 保留原值，非密钥字段按提交值覆盖。
	status, api := env.Request(t, http.MethodPut, "/message/channel", admin, map[string]any{
		"id": channelID, "channelType": "sms", "code": "sms-crud-1", "name": "测试短信渠道改名",
		"provider": "log",
		"config":   `{"username":"u2","password":"******","secret":"******","accessKeySecret":"******"}`,
		"status":   1, "remark": "crud2",
	})
	mustOK(t, "修改渠道", status, api)
	stored = channelConfigFromDB(t, env, channelID)
	if !strings.Contains(stored, `"username":"u2"`) ||
		!strings.Contains(stored, `"password":"p"`) ||
		!strings.Contains(stored, `"secret":"s"`) ||
		!strings.Contains(stored, `"accessKeySecret":"a"`) {
		t.Fatalf("****** 更新未保留原密钥或未覆盖普通字段：%s", stored)
	}

	// 状态端点只改 status。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/message/channel/%d/status", channelID), admin, map[string]any{"status": 0})
	mustOK(t, "修改渠道状态", status, api)
	list, _ = pageChannels(t, env, admin, "&code=sms-crud-1")
	if len(list) != 1 || list[0]["status"] != float64(0) {
		t.Fatalf("渠道状态未更新：%v", list)
	}

	// 模板 CRUD：params 以数组往返。
	tpl := createTemplate(t, env, admin, map[string]any{
		"channelType": "sms", "code": "crud_tpl", "name": "CRUD 模板",
		"content": "您好 {{name}}", "params": []string{"name"}, "status": 0,
	})
	templateID := int64(tpl["id"].(float64))
	params, _ := tpl["params"].([]any)
	if len(params) != 1 || params[0] != "name" {
		t.Fatalf("模板参数回读不符：%v", tpl["params"])
	}
	status, api = env.Request(t, http.MethodPut, "/message/template", admin, map[string]any{
		"id": templateID, "channelType": "sms", "code": "crud_tpl", "name": "CRUD 模板改名",
		"content": "您好 {{name}}，编号 {{no}}", "params": []string{"name", "no"}, "status": 1,
	})
	mustOK(t, "修改模板", status, api)
	list, total = pageTemplates(t, env, admin, "&channelType=sms&code=crud_tpl")
	if total != 1 || len(list) != 1 || list[0]["status"] != float64(1) {
		t.Fatalf("模板列表/状态不符：total=%d list=%v", total, list)
	}

	// 未声明占位符的模板必须 400。
	status, _ = env.Request(t, http.MethodPost, "/message/template", admin, map[string]any{
		"channelType": "sms", "code": "bad_tpl", "name": "坏模板",
		"content": "您好 {{name}}", "params": []string{}, "status": 0,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("未声明占位符应 400，实际 %d", status)
	}

	// 删除模板与渠道。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/message/template/%d", templateID), admin, nil)
	mustOK(t, "删除模板", status, api)
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/message/channel/%d", channelID), admin, nil)
	mustOK(t, "删除渠道", status, api)
	if _, total = pageTemplates(t, env, admin, "&code=crud_tpl"); total != 0 {
		t.Fatalf("删除模板后 total=%d，期望 0", total)
	}
	if _, total = pageChannels(t, env, admin, "&code=sms-crud-1"); total != 0 {
		t.Fatalf("删除渠道后 total=%d，期望 0", total)
	}

	// 非平台租户：渠道/模板的所有入口一律 403（即使持有通配权限）。
	tenant2 := createTenantByAPI(t, env, admin, "消息平台收口租户")
	tenantToken := loginTenant(t, env, tenant2)
	validChannel := map[string]any{
		"channelType": "sms", "code": "tenant-try", "name": "越权渠道", "provider": "log",
		"config": "{}", "status": 0,
	}
	validTemplate := map[string]any{
		"channelType": "sms", "code": "tenant_try", "name": "越权模板",
		"content": "hi", "params": []string{}, "status": 0,
	}
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{name: "渠道列表", method: http.MethodGet, path: "/message/channel/page?pageNo=1&pageSize=10"},
		{name: "渠道新增", method: http.MethodPost, path: "/message/channel", body: validChannel},
		{name: "渠道修改", method: http.MethodPut, path: "/message/channel", body: map[string]any{
			"id": 1, "channelType": "sms", "code": "tenant-try", "name": "越权渠道", "provider": "log", "config": "{}",
		}},
		{name: "渠道改状态", method: http.MethodPut, path: "/message/channel/1/status", body: map[string]any{"status": 0}},
		{name: "渠道删除", method: http.MethodDelete, path: "/message/channel/1"},
		{name: "模板列表", method: http.MethodGet, path: "/message/template/page?pageNo=1&pageSize=10"},
		{name: "模板新增", method: http.MethodPost, path: "/message/template", body: validTemplate},
		{name: "模板修改", method: http.MethodPut, path: "/message/template", body: map[string]any{
			"id": 1, "channelType": "sms", "code": "tenant_try", "name": "越权模板", "content": "hi", "params": []string{},
		}},
		{name: "模板删除", method: http.MethodDelete, path: "/message/template/1"},
	} {
		status, api := env.Request(t, tc.method, tc.path, tenantToken, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("非平台租户%s：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}
}

// TestIntegrationMessageSendAndLogIsolation 覆盖发送成功落日志、参数/收件人
// 校验、模板/渠道停用与租户日志隔离。
func TestIntegrationMessageSendAndLogIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 短信发送成功（未指定 channelCode，使用种子的 log 渠道）。
	status, api := sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": "13800000000", "params": map[string]string{"name": "张三"},
	})
	mustOK(t, "发送短信", status, api)
	smsLogID := int64(decodeMap(t, api.Data)["logId"].(float64))
	if smsLogID <= 0 {
		t.Fatalf("发送响应缺少 logId：%s", string(api.Data))
	}

	list, total := pageLogs(t, env, admin, "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("发送后日志 total=%d len=%d，期望 1", total, len(list))
	}
	logRow := list[0]
	if logRow["channelType"] != "sms" || logRow["templateCode"] != seededTemplateCode ||
		logRow["recipient"] != "13800000000" || logRow["status"] != float64(0) {
		t.Fatalf("短信日志字段不符：%v", logRow)
	}
	if content, _ := logRow["content"].(string); !strings.Contains(content, "张三") {
		t.Fatalf("日志正文未渲染参数：%q", content)
	}
	if pmID, _ := logRow["providerMessageId"].(string); !strings.HasPrefix(pmID, "log-") {
		t.Fatalf("provider message id 不符：%q", pmID)
	}
	if logRow["sentAt"] == nil {
		t.Fatalf("成功日志应记录 sentAt：%v", logRow)
	}

	// 邮件发送成功（主题/正文都渲染）。
	status, api = sendMessage(t, env, admin, "/message/mail/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": "user@example.com", "params": map[string]string{"name": "李四"},
	})
	mustOK(t, "发送邮件", status, api)
	mailLogs, mailTotal := pageLogs(t, env, admin, "&channelType=mail")
	if mailTotal != 1 || len(mailLogs) != 1 {
		t.Fatalf("邮件日志 total=%d len=%d，期望 1", mailTotal, len(mailLogs))
	}
	if mailLogs[0]["recipient"] != "user@example.com" || mailLogs[0]["title"] != "欢迎使用 go-admin-pro" {
		t.Fatalf("邮件日志字段不符：%v", mailLogs[0])
	}
	if content, _ := mailLogs[0]["content"].(string); !strings.Contains(content, "李四") {
		t.Fatalf("邮件正文未渲染参数：%q", content)
	}

	// 租户 2 看不到租户 1 的日志，跨租户重发/删除 404。
	tenant2 := createTenantByAPI(t, env, admin, "消息日志隔离租户")
	tenantToken := loginTenant(t, env, tenant2)
	if _, tenantTotal := pageLogs(t, env, tenantToken, ""); tenantTotal != 0 {
		t.Fatalf("租户 2 初始日志 total=%d，期望 0", tenantTotal)
	}
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "重发", method: http.MethodPost, path: fmt.Sprintf("/message/log/%d/resend", smsLogID)},
		{name: "删除", method: http.MethodDelete, path: fmt.Sprintf("/message/log/%d", smsLogID)},
	} {
		status, _ := env.Request(t, tc.method, tc.path, tenantToken, nil)
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 %s租户 1 日志：HTTP %d，期望 404", tc.name, status)
		}
	}

	// 租户 2 自己发送后只看到自己的日志；清空不影响租户 1。
	status, api = sendMessage(t, env, tenantToken, "/message/sms/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": "13900000000", "params": map[string]string{"name": "王五"},
	})
	mustOK(t, "租户 2 发送短信", status, api)
	if _, tenantTotal := pageLogs(t, env, tenantToken, ""); tenantTotal != 1 {
		t.Fatalf("租户 2 发送后 total=%d，期望 1", tenantTotal)
	}
	status, api = env.Request(t, http.MethodDelete, "/message/log/clear", tenantToken, nil)
	mustOK(t, "租户 2 清空日志", status, api)
	if _, tenantTotal := pageLogs(t, env, tenantToken, ""); tenantTotal != 0 {
		t.Fatalf("租户 2 清空后 total=%d，期望 0", tenantTotal)
	}
	if _, adminTotal := pageLogs(t, env, admin, ""); adminTotal != 2 {
		t.Fatalf("租户 2 清空不应影响租户 1：admin total=%d，期望 2", adminTotal)
	}

	// 参数缺失/多余、收件人格式 400。
	for _, tc := range []struct {
		name    string
		path    string
		payload map[string]any
		wantMsg string
	}{
		{
			name: "缺少参数", path: "/message/sms/send",
			payload: map[string]any{"templateCode": seededTemplateCode, "recipient": "13800000000", "params": map[string]string{}},
			wantMsg: "缺少模板参数",
		},
		{
			name: "多余参数", path: "/message/sms/send",
			payload: map[string]any{"templateCode": seededTemplateCode, "recipient": "13800000000", "params": map[string]string{"name": "x", "extra": "y"}},
			wantMsg: "多余的模板参数",
		},
		{
			name: "手机号非法", path: "/message/sms/send",
			payload: map[string]any{"templateCode": seededTemplateCode, "recipient": "12345", "params": map[string]string{"name": "x"}},
			wantMsg: "手机号格式不正确",
		},
		{
			name: "邮箱非法", path: "/message/mail/send",
			payload: map[string]any{"templateCode": seededTemplateCode, "recipient": "not-an-email", "params": map[string]string{"name": "x"}},
			wantMsg: "邮箱格式不正确",
		},
	} {
		status, api := sendMessage(t, env, admin, tc.path, tc.payload)
		if status != http.StatusBadRequest || !strings.Contains(api.Msg, tc.wantMsg) {
			t.Fatalf("%s：HTTP %d msg %q，期望 400 且包含 %q", tc.name, status, api.Msg, tc.wantMsg)
		}
	}

	// 模板停用 / 渠道停用 / 渠道类型不匹配 400。
	createTemplate(t, env, admin, map[string]any{
		"channelType": "sms", "code": "disabled_tpl", "name": "停用模板",
		"content": "您好 {{name}}", "params": []string{"name"}, "status": 1,
	})
	createChannel(t, env, admin, map[string]any{
		"channelType": "sms", "code": "sms-off", "name": "停用渠道", "provider": "log",
		"config": "{}", "status": 1,
	})
	createTemplate(t, env, admin, map[string]any{
		"channelType": "mail", "code": "mail_only", "name": "仅邮件模板",
		"title": "主题", "content": "您好 {{name}}", "params": []string{"name"}, "status": 0,
	})
	for _, tc := range []struct {
		name    string
		payload map[string]any
	}{
		{
			name: "模板停用",
			payload: map[string]any{
				"templateCode": "disabled_tpl", "recipient": "13800000000", "params": map[string]string{"name": "x"},
			},
		},
		{
			name: "渠道停用",
			payload: map[string]any{
				"channelCode": "sms-off", "templateCode": seededTemplateCode, "recipient": "13800000000",
				"params": map[string]string{"name": "x"},
			},
		},
		{
			name: "渠道类型不匹配",
			payload: map[string]any{
				"channelCode": seededMailChannelCode, "templateCode": seededTemplateCode, "recipient": "13800000000",
				"params": map[string]string{"name": "x"},
			},
		},
		{
			name: "模板类型不匹配",
			payload: map[string]any{
				"templateCode": "mail_only", "recipient": "13800000000", "params": map[string]string{"name": "x"},
			},
		},
	} {
		status, api := sendMessage(t, env, admin, "/message/sms/send", tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d msg %q，期望 400", tc.name, status, api.Msg)
		}
	}

	// 单条删除（租户 1 的日志仍可删除）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/message/log/%d", smsLogID), admin, nil)
	mustOK(t, "删除单条日志", status, api)
	if _, adminTotal := pageLogs(t, env, admin, ""); adminTotal != 1 {
		t.Fatalf("删除单条后 admin total=%d，期望 1", adminTotal)
	}
}

// TestIntegrationMessageLogBatchDelete 覆盖批量删除端点（spec §5.2
// DELETE /message/log 按 ids，#85 审查补齐）：正常批量删除、跨租户 ids
// 404 且不删他租户行、all-or-nothing（混入不可见 ID 一行不删）、空 ids 400。
func TestIntegrationMessageLogBatchDelete(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	sendOne := func(recipient string) int64 {
		t.Helper()
		status, api := sendMessage(t, env, admin, "/message/sms/send", map[string]any{
			"templateCode": seededTemplateCode, "recipient": recipient, "params": map[string]string{"name": "批量"},
		})
		mustOK(t, "发送短信 "+recipient, status, api)
		return int64(decodeMap(t, api.Data)["logId"].(float64))
	}

	id1 := sendOne("13700000001")
	id2 := sendOne("13700000002")
	id3 := sendOne("13700000003")

	// 空 ids / 缺 ids → 400。
	status, api := env.Request(t, http.MethodDelete, "/message/log", admin, map[string]any{"ids": []int64{}})
	if status != http.StatusBadRequest {
		t.Fatalf("空 ids：HTTP %d msg %q，期望 400", status, api.Msg)
	}
	status, _ = env.Request(t, http.MethodDelete, "/message/log", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("缺 ids：HTTP %d，期望 400", status)
	}

	// 不存在的 ID → 404，已有行不受影响。
	status, api = env.Request(t, http.MethodDelete, "/message/log", admin, map[string]any{"ids": []int64{id1, 999999999}})
	if status != http.StatusNotFound {
		t.Fatalf("混入不存在 ID：HTTP %d msg %q，期望 404", status, api.Msg)
	}
	if _, total := pageLogs(t, env, admin, ""); total != 3 {
		t.Fatalf("404 后不应删除任何行：total=%d，期望 3", total)
	}

	// 正常批量删除 id1/id2。
	status, api = env.Request(t, http.MethodDelete, "/message/log", admin, map[string]any{"ids": []int64{id1, id2}})
	mustOK(t, "批量删除", status, api)
	if _, total := pageLogs(t, env, admin, ""); total != 1 {
		t.Fatalf("批量删除后 total=%d，期望 1", total)
	}

	// 跨租户：租户 2 批量删除含租户 1 的 id3 → 404，且租户 1 的行原样保留。
	tenant2 := createTenantByAPI(t, env, admin, "批量删除隔离租户")
	tenantToken := loginTenant(t, env, tenant2)
	status, _ = env.Request(t, http.MethodDelete, "/message/log", tenantToken, map[string]any{"ids": []int64{id3}})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删租户 1 日志：HTTP %d，期望 404", status)
	}
	if _, total := pageLogs(t, env, admin, ""); total != 1 {
		t.Fatalf("跨租户 404 后租户 1 行不应被删：total=%d，期望 1", total)
	}
}

// TestIntegrationMessageProviderFailureAndResend 覆盖 provider 失败/未知 provider
// 的 502 + 失败日志，以及手动重发只更新原行、成功后状态翻转。
func TestIntegrationMessageProviderFailureAndResend(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	env.App.Message.RegisterSender(failingSender{})

	failChannel := createChannel(t, env, admin, map[string]any{
		"channelType": "sms", "code": "sms-fail", "name": "失败渠道", "provider": "failing",
		"config": "{}", "status": 0,
	})
	failChannelID := int64(failChannel["id"].(float64))
	createTemplate(t, env, admin, map[string]any{
		"channelType": "sms", "code": "fail_tpl", "name": "失败模板",
		"content": "您好 {{name}}", "params": []string{"name"}, "status": 0,
	})

	// provider 失败：502 + 中文提示，内部错误不回显；失败日志落库。
	status, api := sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "sms-fail", "templateCode": "fail_tpl",
		"recipient": "13800000000", "params": map[string]string{"name": "张三"},
	})
	if status != http.StatusBadGateway || api.Code != http.StatusBadGateway {
		t.Fatalf("provider 失败应返回 502，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if strings.Contains(api.Msg, "boom") || !strings.Contains(api.Msg, "短信发送失败") {
		t.Fatalf("502 文案不符（不应回显内部错误）：%q", api.Msg)
	}

	list, total := pageLogs(t, env, admin, "&templateCode=fail_tpl")
	if total != 1 || len(list) != 1 {
		t.Fatalf("失败发送后日志 total=%d len=%d，期望 1", total, len(list))
	}
	logID := int64(list[0]["id"].(float64))
	if list[0]["status"] != float64(1) || list[0]["retryCount"] != float64(0) {
		t.Fatalf("失败日志状态不符：%v", list[0])
	}
	if msg, _ := list[0]["errorMessage"].(string); !strings.Contains(msg, "boom") {
		t.Fatalf("失败日志应保存 provider 错误详情：%q", msg)
	}

	// 重发失败：仍 502，原行 retry_count 自增，不新增行。
	status, _ = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", logID), admin, nil)
	if status != http.StatusBadGateway {
		t.Fatalf("重发失败应 502，实际 %d", status)
	}
	list, total = pageLogs(t, env, admin, "&templateCode=fail_tpl")
	if total != 1 || len(list) != 1 {
		t.Fatalf("重发不应新增行：total=%d len=%d", total, len(list))
	}
	if list[0]["retryCount"] != float64(1) || list[0]["status"] != float64(1) {
		t.Fatalf("重发失败后字段不符：%v", list[0])
	}

	// 修复渠道 provider 后重发成功：同一行翻转成功并再自增 retry_count。
	status, api = env.Request(t, http.MethodPut, "/message/channel", admin, map[string]any{
		"id": failChannelID, "channelType": "sms", "code": "sms-fail", "name": "失败渠道",
		"provider": "log", "config": "{}", "status": 0,
	})
	mustOK(t, "修复渠道 provider", status, api)

	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", logID), admin, nil)
	mustOK(t, "重发成功", status, api)
	list, total = pageLogs(t, env, admin, "&templateCode=fail_tpl")
	if total != 1 || len(list) != 1 {
		t.Fatalf("重发成功后仍应只有原行：total=%d len=%d", total, len(list))
	}
	if list[0]["status"] != float64(0) || list[0]["retryCount"] != float64(2) {
		t.Fatalf("重发成功后字段不符：%v", list[0])
	}
	if msg, _ := list[0]["errorMessage"].(string); msg != "" {
		t.Fatalf("成功后应清空失败原因：%q", msg)
	}
	if pmID, _ := list[0]["providerMessageId"].(string); !strings.HasPrefix(pmID, "log-") {
		t.Fatalf("成功后应写入 provider message id：%q", pmID)
	}

	// 成功行不允许重发。
	status, _ = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", logID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("重发成功行应 400，实际 %d", status)
	}

	// 未知 provider fail-closed：502 + 失败日志。
	createChannel(t, env, admin, map[string]any{
		"channelType": "sms", "code": "sms-unknown", "name": "未知 provider 渠道",
		"provider": "missing-provider", "config": "{}", "status": 0,
	})
	createTemplate(t, env, admin, map[string]any{
		"channelType": "sms", "code": "unknown_tpl", "name": "未知 provider 模板",
		"content": "您好 {{name}}", "params": []string{"name"}, "status": 0,
	})
	status, api = sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "sms-unknown", "templateCode": "unknown_tpl",
		"recipient": "13800000000", "params": map[string]string{"name": "张三"},
	})
	if status != http.StatusBadGateway {
		t.Fatalf("未知 provider 应 502，实际 HTTP %d msg %q", status, api.Msg)
	}
	list, total = pageLogs(t, env, admin, "&templateCode=unknown_tpl")
	if total != 1 || len(list) != 1 || list[0]["status"] != float64(1) {
		t.Fatalf("未知 provider 应写失败日志：total=%d list=%v", total, list)
	}
	if msg, _ := list[0]["errorMessage"].(string); !strings.Contains(msg, "missing-provider") {
		t.Fatalf("失败日志应包含未知 provider 详情：%q", msg)
	}

	// 清空当前租户日志。
	status, api = env.Request(t, http.MethodDelete, "/message/log/clear", admin, nil)
	mustOK(t, "清空日志", status, api)
	if _, total = pageLogs(t, env, admin, ""); total != 0 {
		t.Fatalf("清空后 total=%d，期望 0", total)
	}
}
