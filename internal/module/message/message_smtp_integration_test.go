//go:build integration

// SMTP 邮件通道集成测试（issue #73）：在真实 router + MySQL 上验证——
//   - SMTP 成功发送写成功日志（provider_message_id / sent_at 正确）；
//   - 连接失败/认证失败/超时写失败日志并返回 502，错误细节与密钥不回显；
//   - 手动重发只允许失败行，retry_count 递增（复用 #72 的原行更新语义）；
//   - 渠道测试发送接口仅平台租户可用（非平台 403）；
//   - SMTP 渠道配置查询脱敏、库内明文（§4.11）。
//
// 全程使用本机 fakesmtp 假服务器，不依赖外网 SMTP。
package message_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	fakesmtp "github.com/go-quick-code/go-admin-pro/internal/testutil/fakesmtp"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// smtpChannelPayload 构造指向假服务器的 SMTP 邮件渠道创建请求。
func smtpChannelPayload(code string, port int, opts map[string]any) map[string]any {
	payload := map[string]any{
		"channelType": "mail",
		"code":        code,
		"name":        "SMTP 渠道 " + code,
		"provider":    "smtp",
		"config": fmt.Sprintf(
			`{"host":"127.0.0.1","port":%d,"username":"mailer","password":"smtp-pass-123","from":"noreply@example.com","fromName":"邮件机器人","tls":"none"}`,
			port),
		"status": 0,
		"remark": "issue #73 集成测试",
	}
	for k, v := range opts {
		payload[k] = v
	}
	return payload
}

// createSMTPChannel 创建 SMTP 渠道并断言创建响应已脱敏。
func createSMTPChannel(t *testing.T, env *testutil.IntegrationEnv, token, code string, port int, opts map[string]any) int64 {
	t.Helper()
	created := createChannel(t, env, token, smtpChannelPayload(code, port, opts))
	configText, _ := created["config"].(string)
	if strings.Contains(configText, "smtp-pass-123") {
		t.Fatalf("创建响应泄露 SMTP 密码：%s", configText)
	}
	if !strings.Contains(configText, "******") {
		t.Fatalf("创建响应未脱敏密码：%s", configText)
	}
	return int64(created["id"].(float64))
}

// TestIntegrationSMTPSendSuccess 覆盖 SMTP 成功发送：信封/正文按模板渲染投递，
// 成功日志含 provider_message_id 与 sent_at；配置查询脱敏、库内明文。
func TestIntegrationSMTPSendSuccess(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{})
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	channelID := createSMTPChannel(t, env, admin, "smtp-ok", fake.Port(t), nil)

	status, api := sendMessage(t, env, admin, "/message/mail/send", map[string]any{
		"channelCode":  "smtp-ok",
		"templateCode": seededTemplateCode,
		"recipient":    "user@example.com",
		"params":       map[string]string{"name": "张三"},
	})
	mustOK(t, "SMTP 发送邮件", status, api)
	logID := int64(decodeMap(t, api.Data)["logId"].(float64))
	if logID <= 0 {
		t.Fatalf("发送响应缺少 logId：%s", string(api.Data))
	}

	// 假服务器应收到投递：信封收件人、HTML 正文渲染了参数。
	mailFrom, rcptTo, data, quit := fake.Snapshot()
	if mailFrom != "noreply@example.com" || rcptTo != "user@example.com" {
		t.Fatalf("信封不符：from=%q to=%q", mailFrom, rcptTo)
	}
	if !quit {
		t.Fatal("发送成功应完成 QUIT")
	}
	if ct := fakesmtp.DecodeMailHeader(data, "Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("种子模板是 HTML，应按 text/html 投递：%q", ct)
	}
	if subject := fakesmtp.DecodeMailHeader(data, "Subject"); !strings.Contains(subject, "utf-8") {
		t.Fatalf("主题应 RFC2047 编码：%q", subject)
	}
	if body := fakesmtp.DecodeMailBody(t, data); !strings.Contains(body, "张三") {
		t.Fatalf("正文未渲染参数：%q", body)
	}

	// 成功日志：状态/渠道/provider_message_id/sent_at。
	list, total := pageLogs(t, env, admin, "&channelType=mail&recipient=user@example.com")
	if total != 1 || len(list) != 1 {
		t.Fatalf("SMTP 日志 total=%d len=%d，期望 1", total, len(list))
	}
	row := list[0]
	if row["status"] != float64(0) || row["channelId"] != float64(channelID) {
		t.Fatalf("成功日志状态/渠道不符：%v", row)
	}
	if pmID, _ := row["providerMessageId"].(string); !strings.HasPrefix(pmID, "<smtp-") {
		t.Fatalf("provider_message_id 形态不符：%q", pmID)
	}
	if row["sentAt"] == nil {
		t.Fatalf("成功日志应记录 sent_at：%v", row)
	}
	if msg, _ := row["errorMessage"].(string); msg != "" {
		t.Fatalf("成功日志不应有失败原因：%q", msg)
	}

	// 密钥脱敏：库内明文、列表脱敏（§4.11）。
	stored := channelConfigFromDB(t, env, channelID)
	if !strings.Contains(stored, `"password":"smtp-pass-123"`) {
		t.Fatalf("库内应保留明文密钥：%s", stored)
	}
	channels, _ := pageChannels(t, env, admin, "&code=smtp-ok")
	if len(channels) != 1 {
		t.Fatalf("渠道列表应只有 smtp-ok：%d", len(channels))
	}
	if cfg, _ := channels[0]["config"].(string); strings.Contains(cfg, "smtp-pass-123") {
		t.Fatalf("列表响应泄露密码：%s", cfg)
	}
}

// TestIntegrationSMTPFailuresAndResend 覆盖连接失败/认证失败/超时：
// 一律 502 + 中文提示（不回显细节与密钥）+ 失败日志；渠道修复后重发成功。
func TestIntegrationSMTPFailuresAndResend(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 连接失败：指向必然未监听的端口。
	connFailChannelID := createSMTPChannel(t, env, admin, "smtp-conn-fail", 1, nil)
	// 认证失败：假服务器回 535。
	authFake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{AuthFail: true})
	createSMTPChannel(t, env, admin, "smtp-auth-fail", authFake.Port(t), nil)

	for _, tc := range []struct {
		name        string
		channelCode string
	}{
		{name: "连接失败", channelCode: "smtp-conn-fail"},
		{name: "认证失败", channelCode: "smtp-auth-fail"},
	} {
		status, api := sendMessage(t, env, admin, "/message/mail/send", map[string]any{
			"channelCode":  tc.channelCode,
			"templateCode": seededTemplateCode,
			"recipient":    "user@example.com",
			"params":       map[string]string{"name": "李四"},
		})
		if status != http.StatusBadGateway {
			t.Fatalf("%s：HTTP %d msg %q，期望 502", tc.name, status, api.Msg)
		}
		if !strings.Contains(api.Msg, "邮件发送失败") || strings.Contains(api.Msg, "smtp") ||
			strings.Contains(api.Msg, "pass") || strings.Contains(api.Msg, "127.0.0.1") {
			t.Fatalf("%s：502 文案不应回显细节/密钥：%q", tc.name, api.Msg)
		}
	}

	// 两类失败的日志都落库且带细节；连接失败行后续用于重发。
	connFailLogs, _ := pageLogs(t, env, admin, "&channelType=mail&status=1&recipient=user@example.com")
	foundConnFail := false
	foundAuthFail := false
	var connFailLogID int64
	for _, row := range connFailLogs {
		msg, _ := row["errorMessage"].(string)
		if strings.Contains(msg, "SMTP 连接失败") {
			foundConnFail = true
			connFailLogID = int64(row["id"].(float64))
			if row["retryCount"] != float64(0) {
				t.Fatalf("首次失败 retry_count 应为 0：%v", row)
			}
		}
		if strings.Contains(msg, "认证失败") && strings.Contains(msg, "535") {
			foundAuthFail = true
		}
	}
	if !foundConnFail || !foundAuthFail {
		t.Fatalf("失败日志缺失：连接失败=%v 认证失败=%v rows=%d", foundConnFail, foundAuthFail, len(connFailLogs))
	}

	// 超时：stall 服务器 + 缩短发送超时（用例内改回原值）。
	stallFake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{Stall: true})
	createSMTPChannel(t, env, admin, "smtp-stall", stallFake.Port(t), nil)
	originalTimeout := service.SendTimeout()
	service.SetSendTimeout(500 * time.Millisecond)
	status, api := sendMessage(t, env, admin, "/message/mail/send", map[string]any{
		"channelCode":  "smtp-stall",
		"templateCode": seededTemplateCode,
		"recipient":    "user@example.com",
		"params":       map[string]string{"name": "王五"},
	})
	service.SetSendTimeout(originalTimeout)
	if status != http.StatusBadGateway {
		t.Fatalf("超时：HTTP %d msg %q，期望 502", status, api.Msg)
	}
	timeoutLogged := false
	for _, row := range connFailLogs {
		if msg, _ := row["errorMessage"].(string); strings.Contains(msg, "超时") {
			timeoutLogged = true
		}
	}
	stallLogs, _ := pageLogs(t, env, admin, "&channelType=mail&status=1&recipient=user@example.com")
	for _, row := range stallLogs {
		if msg, _ := row["errorMessage"].(string); strings.Contains(msg, "超时") {
			timeoutLogged = true
		}
	}
	if !timeoutLogged {
		t.Fatalf("超时应写失败日志且错误带「超时」：%d 行", len(stallLogs))
	}

	// 手动重发：把连接失败渠道修复（密码提交 ****** 保留原值）后重发成功——
	// 原行翻转、retry_count 递增、不新增行。
	okFake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{})
	status, api = env.Request(t, http.MethodPut, "/message/channel", admin, map[string]any{
		"id": connFailChannelID, "channelType": "mail", "code": "smtp-conn-fail",
		"name": "SMTP 渠道 smtp-conn-fail", "provider": "smtp",
		"config": fmt.Sprintf(
			`{"host":"127.0.0.1","port":%d,"username":"mailer","password":"******","from":"noreply@example.com","fromName":"邮件机器人","tls":"none"}`,
			okFake.Port(t)),
		"status": 0,
	})
	mustOK(t, "修复连接失败渠道", status, api)
	stored := channelConfigFromDB(t, env, connFailChannelID)
	if !strings.Contains(stored, `"password":"smtp-pass-123"`) {
		t.Fatalf("****** 更新未保留原密钥：%s", stored)
	}

	before, _ := pageLogs(t, env, admin, "&channelType=mail&recipient=user@example.com")
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", connFailLogID), admin, nil)
	mustOK(t, "重发成功", status, api)

	// 重发更新原行，不新增行（收件人命中的总行数不变）。
	list, _ := pageLogs(t, env, admin, "&channelType=mail&recipient=user@example.com")
	if len(list) != len(before) {
		t.Fatalf("重发不应新增行：after=%d before=%d", len(list), len(before))
	}
	resendRow := (map[string]any)(nil)
	for _, row := range list {
		if int64(row["id"].(float64)) == connFailLogID {
			resendRow = row
		}
	}
	if resendRow == nil {
		t.Fatal("重发后应保留原日志行")
	}
	if resendRow["status"] != float64(0) || resendRow["retryCount"] != float64(1) {
		t.Fatalf("重发成功后原行应翻转成功且 retry_count=1：%v", resendRow)
	}
	if pmID, _ := resendRow["providerMessageId"].(string); !strings.HasPrefix(pmID, "<smtp-") {
		t.Fatalf("重发成功应写 provider_message_id：%q", pmID)
	}
	if msg, _ := resendRow["errorMessage"].(string); msg != "" {
		t.Fatalf("重发成功后应清空失败原因：%q", msg)
	}
	if _, rcpt, _, _ := okFake.Snapshot(); rcpt != "user@example.com" {
		t.Fatalf("重发应收件人沿用原日志：%q", rcpt)
	}

	// 成功行不允许重发。
	status, _ = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", connFailLogID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("重发成功行应 400，实际 %d", status)
	}
}

// TestIntegrationSMTPChannelTestSend 覆盖渠道测试发送接口：
// 仅平台租户可用（非平台 403）、成功走假服务器、渠道/收件人/模板校验。
func TestIntegrationSMTPChannelTestSend(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{})
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	channelID := createSMTPChannel(t, env, admin, "smtp-test", fake.Port(t), nil)
	testPath := fmt.Sprintf("/message/channel/%d/test", channelID)

	// 平台租户：测试发送成功，返回 logId 且写日志。
	status, api := env.Request(t, http.MethodPost, testPath, admin, map[string]any{
		"recipient":    "user@example.com",
		"templateCode": seededTemplateCode,
		"params":       map[string]string{"name": "平台管理员"},
	})
	mustOK(t, "测试发送", status, api)
	logID := int64(decodeMap(t, api.Data)["logId"].(float64))
	if logID <= 0 {
		t.Fatalf("测试发送响应缺少 logId：%s", string(api.Data))
	}
	if _, rcpt, _, _ := fake.Snapshot(); rcpt != "user@example.com" {
		t.Fatalf("测试发送未到达假服务器：%q", rcpt)
	}
	list, _ := pageLogs(t, env, admin, fmt.Sprintf("&channelType=mail&channelId=%d", channelID))
	if len(list) != 1 || int64(list[0]["id"].(float64)) != logID {
		t.Fatalf("测试发送日志未按渠道落库：%d 行", len(list))
	}

	// 非平台租户 403（即使持有权限码也不放开）。
	tenant2 := createTenantByAPI(t, env, admin, "测试发送越权租户")
	tenantToken := loginTenant(t, env, tenant2)
	status, _ = env.Request(t, http.MethodPost, testPath, tenantToken, map[string]any{
		"recipient": "user@example.com", "templateCode": seededTemplateCode, "params": map[string]string{"name": "x"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("非平台租户测试发送：HTTP %d，期望 403", status)
	}

	// 渠道不存在 404。
	status, _ = env.Request(t, http.MethodPost, "/message/channel/999999/test", admin, map[string]any{
		"recipient": "user@example.com", "templateCode": seededTemplateCode,
	})
	if status != http.StatusNotFound {
		t.Fatalf("渠道不存在：HTTP %d，期望 404", status)
	}

	// 收件人非法 400；模板停用 400。
	createTemplate(t, env, admin, map[string]any{
		"channelType": "mail", "code": "disabled_mail_tpl", "name": "停用邮件模板",
		"title": "主题", "content": "您好 {{name}}", "params": []string{"name"}, "status": 1,
	})
	for _, tc := range []struct {
		name   string
		body   map[string]any
		wantST int
	}{
		{
			name:   "收件人非法",
			body:   map[string]any{"recipient": "not-an-email", "templateCode": seededTemplateCode},
			wantST: http.StatusBadRequest,
		},
		{
			name: "模板停用",
			body: map[string]any{
				"recipient": "user@example.com", "templateCode": "disabled_mail_tpl",
				"params": map[string]string{"name": "x"},
			},
			wantST: http.StatusBadRequest,
		},
	} {
		status, _ := env.Request(t, http.MethodPost, testPath, admin, tc.body)
		if status != tc.wantST {
			t.Fatalf("%s：HTTP %d，期望 %d", tc.name, status, tc.wantST)
		}
	}

	// 测试发送失败（认证失败）也 502 且按渠道写失败日志。
	authFake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{AuthFail: true})
	failChannelID := createSMTPChannel(t, env, admin, "smtp-test-fail", authFake.Port(t), nil)
	status, _ = env.Request(t, http.MethodPost, fmt.Sprintf("/message/channel/%d/test", failChannelID), admin, map[string]any{
		"recipient": "user@example.com", "templateCode": seededTemplateCode, "params": map[string]string{"name": "x"},
	})
	if status != http.StatusBadGateway {
		t.Fatalf("测试发送失败应 502，实际 %d", status)
	}
	failLogs, _ := pageLogs(t, env, admin, fmt.Sprintf("&channelType=mail&status=1&channelId=%d", failChannelID))
	if len(failLogs) != 1 {
		t.Fatalf("测试发送失败应写失败日志：%d 行", len(failLogs))
	}
	if msg, _ := failLogs[0]["errorMessage"].(string); !strings.Contains(msg, "535") {
		t.Fatalf("失败日志应含认证失败细节：%q", msg)
	}
}
