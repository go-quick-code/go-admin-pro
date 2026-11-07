//go:build integration

// 短信/邮件月度配额（issue #75，spec §4.2.4）：
//   - 发送前统计当月 message_log 对应 channel_type 行数（成功+失败都算），
//     >= Max*Monthly 返回 409「本月短信/邮件额度已用尽」，且不写任何日志行；
//   - 短信/邮件各自独立计数；不同租户互不影响；平台租户不限；
//   - 月窗口按应用时区（config.Location()）划分，跨月零点自动重置。
package message_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createMessageQuotaTenant 创建「套餐（指定短信/邮件月度上限）+ 绑定租户」，
// 返回租户 ID。菜单 601/602/603 为短信/邮件/发送日志（按钮随父菜单展开，
// 租户 admin 因此持有 message:sms:send 与 message:mail:send）。
func createMessageQuotaTenant(t *testing.T, env *testutil.IntegrationEnv, name string, maxSMS, maxMail int64) int64 {
	t.Helper()

	admin := env.Login(t, "admin", "123456")
	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": name + "-套餐", "status": 0, "menuIds": []int64{601, 602, 603},
		"maxUsers": 5, "maxSmsMonthly": maxSMS, "maxMailMonthly": maxMail,
	})
	mustOK(t, "创建消息配额套餐", status, api)

	var pkg struct {
		ID int64 `json:"id"`
	}
	if err := env.DB.Raw("SELECT id FROM system_tenant_package WHERE name = ?", name+"-套餐").Scan(&pkg).Error; err != nil || pkg.ID == 0 {
		t.Fatalf("查询套餐 ID: %v（id=%d）", err, pkg.ID)
	}

	status, api = env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": name, "contactName": "配额联系人", "status": 0, "packageId": pkg.ID,
	})
	mustOK(t, "创建消息配额租户", status, api)

	var tenant struct {
		ID int64 `json:"id"`
	}
	if err := env.DB.Raw("SELECT id FROM system_tenant WHERE name = ?", name).Scan(&tenant).Error; err != nil || tenant.ID == 0 {
		t.Fatalf("查询租户 ID: %v（id=%d）", err, tenant.ID)
	}
	return tenant.ID
}

// sendSMSAs 以租户身份发一条短信（种子 log 渠道 + welcome 模板）。
func sendSMSAs(t *testing.T, env *testutil.IntegrationEnv, token, recipient string) (int, testutil.APIResponse) {
	t.Helper()
	return sendMessage(t, env, token, "/message/sms/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": recipient, "params": map[string]string{"name": "配额"},
	})
}

// insertMessageLog 直接插一条 message_log（显式 created_at/tenant_id，
// 绕过租户插件），用于构造月边界与他租户数据。
func insertMessageLog(t *testing.T, env *testutil.IntegrationEnv, tenantID int64, channelType, createdAt string) {
	t.Helper()
	if err := env.DB.Exec(
		"INSERT INTO message_log (created_at, tenant_id, channel_type, channel_id, template_code, recipient, content, status) "+
			"VALUES (?, ?, ?, 1, 'welcome', ?, 'x', 0)",
		createdAt, tenantID, channelType, fmt.Sprintf("user-%s@t.cn", createdAt),
	).Error; err != nil {
		t.Fatalf("插入 message_log（tenant=%d %s %s）: %v", tenantID, channelType, createdAt, err)
	}
}

// TestIntegrationMessageSendQuotaLimit 套餐 maxSms=3/maxMail=1：
// 已用=上限-1 可发、=上限 409 且无新日志行；短信/邮件独立计数；平台租户不限。
func TestIntegrationMessageSendQuotaLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	tenantID := createMessageQuotaTenant(t, env, "短信受限租户", 3, 1)
	tenantToken := loginTenant(t, env, tenantID)

	// 前 3 条可发：第 3 条发送前已用=2=上限-1。
	for i := 1; i <= 3; i++ {
		status, api := sendSMSAs(t, env, tenantToken, fmt.Sprintf("138000000%02d", i))
		if status != http.StatusOK {
			t.Fatalf("第 %d 条短信（发送前已用 %d）应成功，实际 HTTP %d msg %q", i, i-1, status, api.Msg)
		}
	}
	if _, total := pageLogs(t, env, tenantToken, "&channelType=sms"); total != 3 {
		t.Fatalf("3 条发送后短信日志 total=%d，期望 3", total)
	}

	// 第 4 条：发送前已用=3=上限 → 409「本月短信额度已用尽」。
	status, api := sendSMSAs(t, env, tenantToken, "13800000099")
	if status != http.StatusConflict {
		t.Fatalf("超限发送：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	if !strings.Contains(api.Msg, "本月短信额度已用尽") {
		t.Fatalf("409 文案不符：%q", api.Msg)
	}
	// 配额校验失败不得写入业务数据（spec §4.2.6）：日志仍 3 行。
	if _, total := pageLogs(t, env, tenantToken, "&channelType=sms"); total != 3 {
		t.Fatalf("409 后短信日志 total=%d，期望仍 3（不写日志行）", total)
	}

	// 邮件独立计数（maxMail=1）：第 1 封可发，第 2 封 409——不受短信已超限影响。
	status, api = sendMessage(t, env, tenantToken, "/message/mail/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": "q1@example.com", "params": map[string]string{"name": "配额"},
	})
	if status != http.StatusOK {
		t.Fatalf("第 1 封邮件应成功，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = sendMessage(t, env, tenantToken, "/message/mail/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": "q2@example.com", "params": map[string]string{"name": "配额"},
	})
	if status != http.StatusConflict || !strings.Contains(api.Msg, "本月邮件额度已用尽") {
		t.Fatalf("邮件超限：HTTP %d msg %q，期望 409「本月邮件额度已用尽」", status, api.Msg)
	}

	// 平台租户（ID=1）不限：不受套餐上限约束，可继续发送。
	platformToken := env.Login(t, "admin", "123456")
	status, api = sendSMSAs(t, env, platformToken, "13800000111")
	if status != http.StatusOK {
		t.Fatalf("平台租户发送不应受限：HTTP %d msg %q", status, api.Msg)
	}
}

// TestIntegrationMessageSendQuotaTenantIsolation 相同套餐的两个租户各自计数：
// 租户 A 打满后，租户 B 仍可发送且打满后同样 409。
func TestIntegrationMessageSendQuotaTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	tenantA := createMessageQuotaTenant(t, env, "配额隔离A", 1, 0)
	tenantB := createMessageQuotaTenant(t, env, "配额隔离B", 1, 0)
	tokenA := loginTenant(t, env, tenantA)
	tokenB := loginTenant(t, env, tenantB)

	status, api := sendSMSAs(t, env, tokenA, "13800000001")
	if status != http.StatusOK {
		t.Fatalf("租户 A 第 1 条应成功：HTTP %d msg %q", status, api.Msg)
	}
	status, api = sendSMSAs(t, env, tokenA, "13800000002")
	if status != http.StatusConflict {
		t.Fatalf("租户 A 第 2 条应 409：HTTP %d msg %q", status, api.Msg)
	}

	// 租户 A 已打满不影响租户 B。
	status, api = sendSMSAs(t, env, tokenB, "13800000003")
	if status != http.StatusOK {
		t.Fatalf("租户 B 第 1 条不应受租户 A 影响：HTTP %d msg %q", status, api.Msg)
	}
	status, _ = sendSMSAs(t, env, tokenB, "13800000004")
	if status != http.StatusConflict {
		t.Fatalf("租户 B 第 2 条应 409：HTTP %d", status)
	}
}

// TestIntegrationMessageQuotaCountsProviderFailure provider 失败也计入当月用量：
// maxSms=1 时第 1 条发送失败（502，写失败日志），第 2 条即被 409 拦截，
// 且 409 不新增日志行。
func TestIntegrationMessageQuotaCountsProviderFailure(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	tenantID := createMessageQuotaTenant(t, env, "失败计数租户", 1, 0)
	tenantToken := loginTenant(t, env, tenantID)

	admin := env.Login(t, "admin", "123456")
	env.App.Message.RegisterSender(failingSender{})
	createChannel(t, env, admin, map[string]any{
		"channelType": "sms", "code": "sms-quota-fail", "name": "配额失败渠道", "provider": "failing",
		"config": "{}", "status": 0,
	})

	// 第 1 条：provider 失败 → 502，失败日志落库（计入用量）。
	status, api := sendMessage(t, env, tenantToken, "/message/sms/send", map[string]any{
		"channelCode": "sms-quota-fail", "templateCode": seededTemplateCode,
		"recipient": "13800000001", "params": map[string]string{"name": "配额"},
	})
	if status != http.StatusBadGateway {
		t.Fatalf("provider 失败应 502，实际 HTTP %d msg %q", status, api.Msg)
	}
	if _, total := pageLogs(t, env, tenantToken, "&channelType=sms"); total != 1 {
		t.Fatalf("失败日志应落库：total=%d，期望 1", total)
	}

	// 第 2 条：已用=1=上限（失败也计入）→ 409。
	status, api = sendMessage(t, env, tenantToken, "/message/sms/send", map[string]any{
		"channelCode": "sms-quota-fail", "templateCode": seededTemplateCode,
		"recipient": "13800000002", "params": map[string]string{"name": "配额"},
	})
	if status != http.StatusConflict {
		t.Fatalf("失败计入后应 409：HTTP %d msg %q", status, api.Msg)
	}
	if _, total := pageLogs(t, env, tenantToken, "&channelType=sms"); total != 1 {
		t.Fatalf("409 不应新增日志行：total=%d，期望仍 1", total)
	}
}

// TestIntegrationMessageQuotaMonthBoundary 月窗口按应用时区划分并跨月重置：
// 注入固定时钟 2026-11-15 12:00 +08，手工构造月初/月末/他租户/他渠道数据，
// 只有「当月本租户本渠道」的行计入；时钟拨到下月零点后计数归零（自动重置）。
// 期望值全部按 Asia/Shanghai 手算，不随运行日期漂移。
func TestIntegrationMessageQuotaMonthBoundary(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	shanghai := env.Config.Location()

	fixed := time.Date(2026, 11, 15, 12, 0, 0, 0, shanghai)
	prev := service.SetQuotaClock(func() time.Time { return fixed })
	t.Cleanup(func() { service.SetQuotaClock(prev) })

	tenantID := createMessageQuotaTenant(t, env, "月边界租户", 5, 5)
	otherTenantID := createMessageQuotaTenant(t, env, "月边界他租户", 5, 5)

	// 行 A：当月 1 日 00:00.000 +08（窗口下界，含）→ 计入。
	insertMessageLog(t, env, tenantID, "sms", "2026-11-01 00:00:00.000")
	// 行 B：上月最后一毫秒 2026-10-31 23:59:59.999 +08 → 不计入。
	insertMessageLog(t, env, tenantID, "sms", "2026-10-31 23:59:59.999")
	// 行 C：下月 1 日 00:00.000 +08（窗口上界，不含）→ 不计入。
	insertMessageLog(t, env, tenantID, "sms", "2026-12-01 00:00:00.000")
	// 行 D：他租户当月 → 不计入本租户。
	insertMessageLog(t, env, otherTenantID, "sms", "2026-11-05 08:00:00.000")
	// 行 E：本租户当月但渠道为 mail → 不计入短信。
	insertMessageLog(t, env, tenantID, "mail", "2026-11-05 08:00:00.000")

	if got, err := env.App.Message.MonthlyUsage(context.Background(), tenantID, "sms"); err != nil || got != 1 {
		t.Fatalf("当月短信用量应 1（仅行 A），实际 %d err %v", got, err)
	}
	if got, err := env.App.Message.MonthlyUsage(context.Background(), tenantID, "mail"); err != nil || got != 1 {
		t.Fatalf("当月邮件用量应 1（行 E），实际 %d err %v", got, err)
	}
	if got, err := env.App.Message.MonthlyUsage(context.Background(), otherTenantID, "sms"); err != nil || got != 1 {
		t.Fatalf("他租户当月短信用量应 1（行 D），实际 %d err %v", got, err)
	}

	// 时钟拨到 2026-12-05：行 A 已属上月不再计入，行 C（12 月 1 日）进入当月窗口。
	service.SetQuotaClock(func() time.Time { return time.Date(2026, 12, 5, 0, 0, 0, 0, shanghai) })
	if got, err := env.App.Message.MonthlyUsage(context.Background(), tenantID, "sms"); err != nil || got != 1 {
		t.Fatalf("12 月窗口短信用量应 1（仅行 C），实际 %d err %v", got, err)
	}
	// 再拨到 2027-01-01：行 C 也滚出窗口 → 归零。这是「窗口随月份移动」的
	// 唯一可区分证据（若窗口卡死在 11 月，此处仍会数到行 A 的 1）。
	service.SetQuotaClock(func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, shanghai) })
	if got, err := env.App.Message.MonthlyUsage(context.Background(), tenantID, "sms"); err != nil || got != 0 {
		t.Fatalf("2027 年 1 月窗口短信用量应 0（跨月自动重置），实际 %d err %v", got, err)
	}
	// 拨回当月最后一毫秒：行 A 仍在 11 月窗口内（若上界误含，行 C 会数成 2）。
	service.SetQuotaClock(func() time.Time {
		return time.Date(2026, 11, 30, 23, 59, 59, int(999*time.Millisecond), shanghai)
	})
	if got, err := env.App.Message.MonthlyUsage(context.Background(), tenantID, "sms"); err != nil || got != 1 {
		t.Fatalf("月末最后一毫秒行 A 仍应计入，实际 %d err %v", got, err)
	}
}

// TestIntegrationMessageQuotaUsageAPI 用量并入 GET /system/tenant/quota
// （issue #75，spec §4.2.5）：used.smsMonthly/mailMonthly 与当月日志行数一致，
// 平台可查任意租户（显式 tenantID，不依赖调用方租户上下文），平台自身 unlimited
// 且用量照常统计。
func TestIntegrationMessageQuotaUsageAPI(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	tenantID := createMessageQuotaTenant(t, env, "用量上报租户", 5, 2)
	tenantToken := loginTenant(t, env, tenantID)
	platformToken := env.Login(t, "admin", "123456")

	// 当月 2 短信 1 邮件（种子渠道 + welcome 模板）。
	for _, recipient := range []string{"13800000001", "13800000002"} {
		status, api := sendSMSAs(t, env, tenantToken, recipient)
		if status != http.StatusOK {
			t.Fatalf("租户发送 %s：HTTP %d msg %q", recipient, status, api.Msg)
		}
	}
	status, api := sendMessage(t, env, tenantToken, "/message/mail/send", map[string]any{
		"templateCode": seededTemplateCode, "recipient": "usage@example.com", "params": map[string]string{"name": "用量"},
	})
	if status != http.StatusOK {
		t.Fatalf("租户发送邮件：HTTP %d msg %q", status, api.Msg)
	}

	// 租户管理员查自己：limit 与套餐一致，used 与当月日志一致。
	status, api = env.Request(t, http.MethodGet, "/system/tenant/quota", tenantToken, nil)
	mustOK(t, "租户查自身配额", status, api)
	var tenantQuota struct {
		Unlimited bool `json:"unlimited"`
		Limit     struct {
			MaxSMSMonthly  int64 `json:"maxSmsMonthly"`
			MaxMailMonthly int64 `json:"maxMailMonthly"`
		} `json:"limit"`
		Used struct {
			SMSMonthly  int64 `json:"smsMonthly"`
			MailMonthly int64 `json:"mailMonthly"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &tenantQuota); err != nil {
		t.Fatalf("解析租户配额: %v（data=%s）", err, string(api.Data))
	}
	if tenantQuota.Unlimited || tenantQuota.Limit.MaxSMSMonthly != 5 || tenantQuota.Limit.MaxMailMonthly != 2 {
		t.Fatalf("套餐限额不符：%+v", tenantQuota)
	}
	if tenantQuota.Used.SMSMonthly != 2 || tenantQuota.Used.MailMonthly != 1 {
		t.Fatalf("用量不符：sms=%d mail=%d，期望 2/1", tenantQuota.Used.SMSMonthly, tenantQuota.Used.MailMonthly)
	}

	// 平台查该租户（平台上下文租户是 1，用量必须按目标租户统计）。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant/%d/quota", tenantID), platformToken, nil)
	mustOK(t, "平台查询租户配额", status, api)
	var viaPlatform struct {
		Used struct {
			SMSMonthly  int64 `json:"smsMonthly"`
			MailMonthly int64 `json:"mailMonthly"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &viaPlatform); err != nil {
		t.Fatalf("解析平台视角配额: %v", err)
	}
	if viaPlatform.Used.SMSMonthly != 2 || viaPlatform.Used.MailMonthly != 1 {
		t.Fatalf("平台视角用量不符：sms=%d mail=%d，期望 2/1", viaPlatform.Used.SMSMonthly, viaPlatform.Used.MailMonthly)
	}

	// 平台自身：unlimited=true，用量照常统计（平台自己的 0 条不算租户的）。
	status, api = env.Request(t, http.MethodGet, "/system/tenant/quota", platformToken, nil)
	mustOK(t, "平台查自身配额", status, api)
	var platformQuota struct {
		Unlimited bool `json:"unlimited"`
		Used      struct {
			SMSMonthly int64 `json:"smsMonthly"`
		} `json:"used"`
	}
	if err := json.Unmarshal(api.Data, &platformQuota); err != nil {
		t.Fatalf("解析平台配额: %v", err)
	}
	if !platformQuota.Unlimited {
		t.Fatal("平台租户应 unlimited=true")
	}
	if platformQuota.Used.SMSMonthly != 0 {
		t.Fatalf("平台自身短信用量应 0，实际 %d", platformQuota.Used.SMSMonthly)
	}
}
