//go:build integration

// 阿里云短信通道集成测试（issue #74）：在真实 router + MySQL 上验证——
//   - 请求包含正确的 Action/SignName/TemplateCode/PhoneNumbers/TemplateParam/
//     Signature（签名用测试侧独立实现按已知 secret 重算比对）；
//   - 成功响应写成功日志（provider_message_id=BizId、sent_at 正确）；
//   - 错误码/超时/连接失败写失败日志并返回 502 中文提示，细节与密钥不回显；
//   - 手机号非法 400；手动重发只允许失败行、retry_count 递增、原行更新；
//   - accessKeySecret 查询脱敏、库内明文、****** 更新保留原值。
//
// 全程使用本机 httptest 模拟阿里云 dysmsapi 端点，不依赖外网与真实凭据。
package message_test

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

const aliyunTestSecret = "aliyun-secret-74"

// sigEncode 是测试侧独立的 RFC 3986 百分号编码（保留 A-Za-z0-9 与 -_.~）。
func sigEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// recomputeAliyunSignature 按阿里云 RPC 规范重算签名（排除 Signature 自身）：
// 参数名升序逐个编码拼 canonical → 对 canonical 再整体编码一次（双重编码）→
// HMAC-SHA1(key = secret + "&") → base64。
func recomputeAliyunSignature(q url.Values, secret string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, sigEncode(k)+"="+sigEncode(q.Get(k)))
	}
	stringToSign := "GET&" + sigEncode("/") + "&" + sigEncode(strings.Join(pairs, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// fakeAliyunMode 是模拟端点的行为开关。
type fakeAliyunMode int

const (
	aliyunModeOK fakeAliyunMode = iota
	aliyunModeErrorCode
	aliyunModeHTTP500
	aliyunModeStall
)

// capturedAliyunRequest 记录模拟端点收到的最近一次请求。
type capturedAliyunRequest struct {
	method string
	path   string
	query  url.Values
}

// fakeAliyunEndpoint 模拟阿里云 dysmsapi SendSms 端点（仅本机随机端口）。
type fakeAliyunEndpoint struct {
	server *httptest.Server

	mu   sync.Mutex
	mode fakeAliyunMode
	last *capturedAliyunRequest
}

func startFakeAliyunEndpoint(t *testing.T, mode fakeAliyunMode) *fakeAliyunEndpoint {
	t.Helper()
	f := &fakeAliyunEndpoint{mode: mode}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.last = &capturedAliyunRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
		currentMode := f.mode
		f.mu.Unlock()

		switch currentMode {
		case aliyunModeOK:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Code": "OK", "Message": "OK",
				"BizId": "7406000000000000^74000001", "RequestId": "req-74-ok",
			})
		case aliyunModeErrorCode:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Code": "isv.MOBILE_NUMBER_ILLEGAL", "Message": "非法手机号",
				"RequestId": "req-74-err",
			})
		case aliyunModeHTTP500:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"Code":"InternalError","Message":"内部错误"}`))
		case aliyunModeStall:
			select {
			case <-time.After(30 * time.Second):
			case <-r.Context().Done():
			}
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// snapshot 取最近一次请求（带超时保护）。
func (f *fakeAliyunEndpoint) snapshot(t *testing.T) *capturedAliyunRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		last := f.last
		f.mu.Unlock()
		if last != nil {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("模拟阿里云端点未收到请求")
	return nil
}

// aliyunChannelPayload 构造指向模拟端点的阿里云短信渠道创建/更新请求。
func aliyunChannelPayload(code, endpoint string, opts map[string]any) map[string]any {
	payload := map[string]any{
		"channelType": "sms",
		"code":        code,
		"name":        "阿里云渠道 " + code,
		"provider":    "aliyun_sms",
		"config": fmt.Sprintf(
			`{"accessKeyId":"aliyun-key-74","accessKeySecret":"%s","signName":"平台测试签名","regionId":"cn-hangzhou","endpoint":"%s"}`,
			aliyunTestSecret, endpoint),
		"status": 0,
		"remark": "issue #74 集成测试",
	}
	for k, v := range opts {
		payload[k] = v
	}
	return payload
}

// createAliyunChannel 创建阿里云短信渠道并断言创建响应已脱敏。
func createAliyunChannel(t *testing.T, env *testutil.IntegrationEnv, token, code string, endpoint string, opts map[string]any) int64 {
	t.Helper()
	created := createChannel(t, env, token, aliyunChannelPayload(code, endpoint, opts))
	configText, _ := created["config"].(string)
	if strings.Contains(configText, aliyunTestSecret) {
		t.Fatalf("创建响应泄露 accessKeySecret：%s", configText)
	}
	if !strings.Contains(configText, "******") {
		t.Fatalf("创建响应未脱敏密钥：%s", configText)
	}
	return int64(created["id"].(float64))
}

// TestIntegrationAliyunSMSSendSuccess 覆盖成功发送：RPC 参数与签名正确、
// 成功日志含 BizId 与 sent_at、密钥库内明文/查询脱敏、手机号非法 400。
func TestIntegrationAliyunSMSSendSuccess(t *testing.T) {
	fake := startFakeAliyunEndpoint(t, aliyunModeOK)
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	channelID := createAliyunChannel(t, env, admin, "aliyun-ok", fake.server.URL, nil)

	status, api := sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode":  "aliyun-ok",
		"templateCode": seededTemplateCode,
		"recipient":    "13800001234",
		"params":       map[string]string{"name": "张三"},
	})
	mustOK(t, "阿里云发送短信", status, api)
	logID := int64(decodeMap(t, api.Data)["logId"].(float64))
	if logID <= 0 {
		t.Fatalf("发送响应缺少 logId：%s", string(api.Data))
	}

	// 模拟端点收到的 RPC 请求：方法/路径与全部业务参数。
	req := fake.snapshot(t)
	if req.method != http.MethodGet || req.path != "/" {
		t.Fatalf("RPC 请求应为 GET /：实际 %s %s", req.method, req.path)
	}
	q := req.query
	for key, want := range map[string]string{
		"Action":           "SendSms",
		"Version":          "2017-05-25",
		"Format":           "JSON",
		"RegionId":         "cn-hangzhou",
		"SignName":         "平台测试签名",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"PhoneNumbers":     "13800001234",
		"TemplateCode":     seededTemplateCode,
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("参数 %s=%q，期望 %q", key, got, want)
		}
	}
	if q.Get("SignatureNonce") == "" {
		t.Fatal("请求应携带 SignatureNonce")
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", q.Get("Timestamp")); err != nil {
		t.Fatalf("Timestamp 应为 UTC ISO8601：%q（%v）", q.Get("Timestamp"), err)
	}
	var tplParam map[string]string
	if err := json.Unmarshal([]byte(q.Get("TemplateParam")), &tplParam); err != nil || tplParam["name"] != "张三" {
		t.Fatalf("TemplateParam 应为 {\"name\":\"张三\"}：%q（err=%v）", q.Get("TemplateParam"), err)
	}
	// 签名：测试侧独立重算比对。
	if got, want := q.Get("Signature"), recomputeAliyunSignature(q, aliyunTestSecret); got != want {
		t.Fatalf("Signature 校验不符：got=%q expected=%q", got, want)
	}
	// 密钥不得出现在请求的任何位置。
	if strings.Contains(req.query.Encode(), aliyunTestSecret) {
		t.Fatal("请求不得携带 accessKeySecret")
	}

	// 成功日志：状态/provider_message_id=BizId/sent_at。
	list, total := pageLogs(t, env, admin, "&channelType=sms&recipient=13800001234")
	if total != 1 || len(list) != 1 {
		t.Fatalf("短信日志 total=%d len=%d，期望 1", total, len(list))
	}
	row := list[0]
	if row["status"] != float64(0) {
		t.Fatalf("成功日志状态不符：%v", row)
	}
	if pmID, _ := row["providerMessageId"].(string); pmID != "7406000000000000^74000001" {
		t.Fatalf("provider_message_id 应为 BizId：%q", pmID)
	}
	if row["sentAt"] == nil {
		t.Fatalf("成功日志应记录 sent_at：%v", row)
	}
	if msg, _ := row["errorMessage"].(string); msg != "" {
		t.Fatalf("成功日志不应有失败原因：%q", msg)
	}
	if row["id"].(float64) != float64(logID) {
		t.Fatalf("日志行与 logId 不符：%v vs %d", row["id"], logID)
	}

	// 密钥脱敏：库内明文、列表脱敏；accessKeyId 非密钥字段原样展示。
	stored := channelConfigFromDB(t, env, channelID)
	if !strings.Contains(stored, aliyunTestSecret) {
		t.Fatalf("库内应保留明文密钥：%s", stored)
	}
	channels, _ := pageChannels(t, env, admin, "&code=aliyun-ok")
	if len(channels) != 1 {
		t.Fatalf("渠道列表应只有 aliyun-ok：%d", len(channels))
	}
	if cfg, _ := channels[0]["config"].(string); strings.Contains(cfg, aliyunTestSecret) {
		t.Fatalf("列表响应泄露密钥：%s", cfg)
	}

	// 手机号非法 400（渠道已就绪，错误发生在参数校验层）。
	status, api = sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "aliyun-ok", "templateCode": seededTemplateCode,
		"recipient": "12345", "params": map[string]string{"name": "x"},
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "手机号格式不正确") {
		t.Fatalf("手机号非法：HTTP %d msg %q，期望 400 且提示格式", status, api.Msg)
	}
	// 非法请求不写日志、不触达 provider。
	if _, total = pageLogs(t, env, admin, "&channelType=sms&recipient=12345"); total != 0 {
		t.Fatalf("手机号非法不应写日志：total=%d", total)
	}
}

// TestIntegrationAliyunSMSErrorsAndResend 覆盖错误码/超时/连接失败：一律
// 502 + 中文提示（不回显细节与密钥）+ 失败日志；渠道修复后重发成功——
// 原行翻转、retry_count 递增、不新增行。
func TestIntegrationAliyunSMSErrorsAndResend(t *testing.T) {
	errFake := startFakeAliyunEndpoint(t, aliyunModeErrorCode)
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 无变量模板：重发路径不携带 TemplateParam，与阿里云无变量模板语义一致。
	createTemplate(t, env, admin, map[string]any{
		"channelType": "sms", "code": "aliyun_fixed_tpl", "name": "固定短信模板",
		"content": "您的验证请求已受理", "params": []string{}, "status": 0,
	})
	channelID := createAliyunChannel(t, env, admin, "aliyun-err", errFake.server.URL, nil)

	status, api := sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "aliyun-err", "templateCode": "aliyun_fixed_tpl",
		"recipient": "13800001234",
	})
	if status != http.StatusBadGateway {
		t.Fatalf("错误码：HTTP %d msg %q，期望 502", status, api.Msg)
	}
	if !strings.Contains(api.Msg, "短信发送失败") || strings.Contains(api.Msg, "isv.") ||
		strings.Contains(api.Msg, aliyunTestSecret) {
		t.Fatalf("502 文案不应回显细节/密钥：%q", api.Msg)
	}

	// 失败日志落库且带错误码细节；该行用于后续重发。
	logs, _ := pageLogs(t, env, admin, "&channelType=sms&status=1&recipient=13800001234")
	if len(logs) != 1 {
		t.Fatalf("失败日志应 1 行：%d", len(logs))
	}
	failLogID := int64(logs[0]["id"].(float64))
	if msg, _ := logs[0]["errorMessage"].(string); !strings.Contains(msg, "isv.MOBILE_NUMBER_ILLEGAL") {
		t.Fatalf("失败日志应含错误码细节：%q", msg)
	}
	if logs[0]["retryCount"] != float64(0) || logs[0]["channelId"] != float64(channelID) {
		t.Fatalf("失败日志 retry_count/渠道不符：%v", logs[0])
	}

	// 渠道仍指向错误码端点：重发失败 → 502，原行 retry_count 自增、不新增行。
	status, _ = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", failLogID), admin, nil)
	if status != http.StatusBadGateway {
		t.Fatalf("重发失败应 502，实际 %d", status)
	}
	logs, _ = pageLogs(t, env, admin, "&channelType=sms&status=1&recipient=13800001234")
	if len(logs) != 1 || logs[0]["retryCount"] != float64(1) {
		t.Fatalf("重发失败后应仍 1 行且 retry_count=1：%v", logs)
	}

	// 超时：stall 端点 + 缩短发送超时（用例内改回原值）。
	stallFake := startFakeAliyunEndpoint(t, aliyunModeStall)
	createAliyunChannel(t, env, admin, "aliyun-stall", stallFake.server.URL, nil)
	originalTimeout := service.SendTimeout()
	service.SetSendTimeout(500 * time.Millisecond)
	status, _ = sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "aliyun-stall", "templateCode": "aliyun_fixed_tpl",
		"recipient": "13800001234",
	})
	service.SetSendTimeout(originalTimeout)
	if status != http.StatusBadGateway {
		t.Fatalf("超时应 502，实际 %d", status)
	}
	stallLogs, _ := pageLogs(t, env, admin, "&channelType=sms&status=1")
	timeoutLogged := false
	for _, row := range stallLogs {
		if msg, _ := row["errorMessage"].(string); strings.Contains(msg, "超时") {
			timeoutLogged = true
		}
	}
	if !timeoutLogged {
		t.Fatal("超时应写失败日志且错误带「超时」")
	}

	// 连接失败：端点指向必然未监听的端口。
	createAliyunChannel(t, env, admin, "aliyun-conn-fail", "http://127.0.0.1:1", nil)
	status, _ = sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "aliyun-conn-fail", "templateCode": "aliyun_fixed_tpl",
		"recipient": "13800001234",
	})
	if status != http.StatusBadGateway {
		t.Fatalf("连接失败应 502，实际 %d", status)
	}

	// 修复渠道（端点换成 OK 模拟端点，密钥提交 ****** 保留原值）后重发成功：
	// 原行翻转成功、retry_count 再自增、provider_message_id=BizId。
	okFake := startFakeAliyunEndpoint(t, aliyunModeOK)
	status, api = env.Request(t, http.MethodPut, "/message/channel", admin, map[string]any{
		"id": channelID, "channelType": "sms", "code": "aliyun-err",
		"name": "阿里云渠道 aliyun-err", "provider": "aliyun_sms",
		"config": fmt.Sprintf(
			`{"accessKeyId":"aliyun-key-74","accessKeySecret":"******","signName":"平台测试签名","regionId":"cn-hangzhou","endpoint":"%s"}`,
			okFake.server.URL),
		"status": 0,
	})
	mustOK(t, "修复阿里云渠道", status, api)
	stored := channelConfigFromDB(t, env, channelID)
	if !strings.Contains(stored, aliyunTestSecret) {
		t.Fatalf("****** 更新未保留原密钥：%s", stored)
	}

	before, _ := pageLogs(t, env, admin, "&channelType=sms&recipient=13800001234")
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", failLogID), admin, nil)
	mustOK(t, "重发成功", status, api)

	list, _ := pageLogs(t, env, admin, "&channelType=sms&recipient=13800001234")
	if len(list) != len(before) {
		t.Fatalf("重发不应新增行：after=%d before=%d", len(list), len(before))
	}
	resendRow := (map[string]any)(nil)
	for _, row := range list {
		if int64(row["id"].(float64)) == failLogID {
			resendRow = row
		}
	}
	if resendRow == nil {
		t.Fatal("重发后应保留原日志行")
	}
	if resendRow["status"] != float64(0) || resendRow["retryCount"] != float64(2) {
		t.Fatalf("重发成功后原行应翻转成功且 retry_count=2：%v", resendRow)
	}
	if pmID, _ := resendRow["providerMessageId"].(string); pmID != "7406000000000000^74000001" {
		t.Fatalf("重发成功应写 BizId：%q", pmID)
	}
	if msg, _ := resendRow["errorMessage"].(string); msg != "" {
		t.Fatalf("重发成功后应清空失败原因：%q", msg)
	}
	req := okFake.snapshot(t)
	if req.query.Get("PhoneNumbers") != "13800001234" {
		t.Fatalf("重发应收件人沿用原日志：%q", req.query.Get("PhoneNumbers"))
	}

	// 成功行不允许重发。
	status, _ = env.Request(t, http.MethodPost, fmt.Sprintf("/message/log/%d/resend", failLogID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("重发成功行应 400，实际 %d", status)
	}
}

// TestIntegrationAliyunSMSHTTP500 覆盖非 200 响应：502 + 失败日志含状态码。
func TestIntegrationAliyunSMSHTTP500(t *testing.T) {
	failFake := startFakeAliyunEndpoint(t, aliyunModeHTTP500)
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	createAliyunChannel(t, env, admin, "aliyun-500", failFake.server.URL, nil)
	status, api := sendMessage(t, env, admin, "/message/sms/send", map[string]any{
		"channelCode": "aliyun-500", "templateCode": seededTemplateCode,
		"recipient": "13800001234", "params": map[string]string{"name": "张三"},
	})
	if status != http.StatusBadGateway || !strings.Contains(api.Msg, "短信发送失败") {
		t.Fatalf("HTTP 500：HTTP %d msg %q，期望 502", status, api.Msg)
	}
	logs, _ := pageLogs(t, env, admin, "&channelType=sms&status=1&recipient=13800001234")
	if len(logs) != 1 {
		t.Fatalf("HTTP 500 应写失败日志：%d 行", len(logs))
	}
	if msg, _ := logs[0]["errorMessage"].(string); !strings.Contains(msg, "500") {
		t.Fatalf("失败日志应含 HTTP 状态：%q", msg)
	}
}
