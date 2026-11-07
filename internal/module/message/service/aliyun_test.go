package service

import (
	"context"
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
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
)

// ---------- 用例辅助 ----------

type aliyunConfigOpt func(m map[string]any)

// aliyunConfigJSON 构造阿里云短信渠道配置 JSON：固定测试密钥（仅测试用）。
func aliyunConfigJSON(endpoint string, opts ...aliyunConfigOpt) string {
	m := map[string]any{
		"accessKeyId":     "testKeyId",
		"accessKeySecret": "testSecret",
		"signName":        "签名名称",
		"endpoint":        endpoint,
	}
	for _, opt := range opts {
		opt(m)
	}
	data, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func aliyunChannelWith(cfgJSON string) model.Channel {
	return model.Channel{
		BaseEntity:  model.BaseEntity{ID: 2},
		ChannelType: model.ChannelTypeSMS,
		Provider:    model.ProviderAliyunSMS,
		Config:      cfgJSON,
	}
}

func aliyunSendRequest(ch model.Channel) SendRequest {
	return SendRequest{
		Channel:      ch,
		TemplateCode: "SMS_123456789",
		Recipient:    "13800001234",
		Title:        "验证码",
		Content:      "您好 张三，您的验证码是 1234",
		Params:       map[string]string{"name": "张三"},
	}
}

// tpEncode 是测试侧独立的 RFC 3986 百分号编码（阿里云 RPC 签名要求）：
// 保留 A-Za-z0-9 与 -_.~，其余逐字节 %XX 大写。不与实现代码共享任何辅助函数，
// 作为签名的独立真值来源。
func tpEncode(s string) string {
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

// testSideSign 用测试侧独立实现重算阿里云 RPC 签名（不含 Signature 参数）：
// 参数按名称 ASCII 升序逐个编码拼 canonical → StringToSign =
// GET&%2F&encode(canonical)（规范要求对 canonical 再整体编码一次，双重编码）→
// HMAC-SHA1(key = secret + "&") → base64。
func testSideSign(values url.Values, secret string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, tpEncode(k)+"="+tpEncode(values.Get(k)))
	}
	stringToSign := "GET&" + tpEncode("/") + "&" + tpEncode(strings.Join(pairs, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// fakeAliyun 是模拟阿里云 SendSms 的 httptest 端点：捕获请求供断言。
// mode 决定响应行为：ok（Code=OK）/ bizId 缺省回退 / 错误码 / HTTP 500 / 慢响应。
type fakeAliyun struct {
	server *httptest.Server
	mu     chan *http.Request
	stall  time.Duration
}

func startFakeAliyun(t *testing.T, respond func(w http.ResponseWriter)) *fakeAliyun {
	t.Helper()
	f := &fakeAliyun{mu: make(chan *http.Request, 8)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu <- r
		if f.stall > 0 {
			select {
			case <-time.After(f.stall):
			case <-r.Context().Done():
				return
			}
		}
		if respond != nil {
			respond(w)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAliyun) endpoint() string { return f.server.URL }

// lastRequest 取最近一次请求（带超时保护，避免用例卡死）。
func (f *fakeAliyun) lastRequest(t *testing.T) *http.Request {
	t.Helper()
	select {
	case r := <-f.mu:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("假阿里云端点未收到请求")
		return nil
	}
}

// ---------- 配置解析 ----------

func TestParseAliyunSMSConfig(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string // 空＝期望成功
	}{
		{name: "完整配置", json: aliyunConfigJSON("https://dysmsapi.aliyuncs.com")},
		{name: "缺 accessKeyId", json: aliyunConfigJSON("", func(m map[string]any) { delete(m, "accessKeyId") }), wantErr: "accessKeyId"},
		{name: "缺 accessKeySecret", json: aliyunConfigJSON("", func(m map[string]any) { delete(m, "accessKeySecret") }), wantErr: "accessKeySecret"},
		{name: "缺 signName", json: aliyunConfigJSON("", func(m map[string]any) { delete(m, "signName") }), wantErr: "signName"},
		{name: "accessKeyId 空白", json: aliyunConfigJSON("", func(m map[string]any) { m["accessKeyId"] = "  " }), wantErr: "accessKeyId"},
		{name: "endpoint 非 http 协议", json: aliyunConfigJSON("ftp://dysmsapi.aliyuncs.com"), wantErr: "endpoint"},
		{name: "endpoint 带路径", json: aliyunConfigJSON("https://dysmsapi.aliyuncs.com/sms"), wantErr: "endpoint"},
		{name: "endpoint 带查询串", json: aliyunConfigJSON("https://dysmsapi.aliyuncs.com/?x=1"), wantErr: "endpoint"},
		{name: "endpoint 非法", json: aliyunConfigJSON("://bad"), wantErr: "endpoint"},
		{name: "非对象", json: `["x"]`, wantErr: "JSON 对象"},
		{name: "非 JSON", json: `not-json`, wantErr: "JSON 对象"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseAliyunSMSConfig(tc.json)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("期望错误包含 %q，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错：%v", err)
			}
			if cfg.Endpoint != "https://dysmsapi.aliyuncs.com" {
				t.Fatalf("endpoint 解析不符：%q", cfg.Endpoint)
			}
		})
	}

	// 缺省值：regionId 缺省 cn-hangzhou，endpoint 缺省官方接入点。
	cfg, err := parseAliyunSMSConfig(`{"accessKeyId":"k","accessKeySecret":"s","signName":"签名"}`)
	if err != nil {
		t.Fatalf("最小配置不应报错：%v", err)
	}
	if cfg.RegionID != "cn-hangzhou" {
		t.Fatalf("缺省 regionId 应为 cn-hangzhou，实际 %q", cfg.RegionID)
	}
	if cfg.Endpoint != "https://dysmsapi.aliyuncs.com" {
		t.Fatalf("缺省 endpoint 不符：%q", cfg.Endpoint)
	}
}

// ---------- 签名 ----------

// TestAliyunSignKnownAnswer 已知答案：规范化请求串（第一层编码，含特殊字符）
// 在测试里手写，StringToSign 按 RPC 规范再整体编码一次（双重编码），HMAC-SHA1
// 用测试侧独立实现计算——实现函数只允许给出相同结果。
func TestAliyunSignKnownAnswer(t *testing.T) {
	cases := []struct {
		name      string
		params    url.Values
		secret    string
		canonical string // 手写的 canonicalizedQueryString（已完成第一层编码的真值）
	}{
		{
			name:      "基本参数",
			params:    url.Values{"Action": {"SendSms"}, "PhoneNumbers": {"13800001234"}},
			secret:    "testSecret",
			canonical: "Action=SendSms&PhoneNumbers=13800001234",
		},
		{
			name: "中文/空格/星号在 StringToSign 中双重编码",
			params: url.Values{
				"Action":        {"SendSms"},
				"TemplateParam": {`{"name":"张三 a*b~c"}`},
			},
			secret: "testSecret",
			// 第一层编码真值：{ %7B、" %22、: %3A、中文逐字节、空格 %20、* %2A、~ 保留。
			canonical: `Action=SendSms&TemplateParam=%7B%22name%22%3A%22%E5%BC%A0%E4%B8%89%20a%2Ab~c%22%7D`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stringToSign := "GET&" + tpEncode("/") + "&" + tpEncode(tc.canonical)
			mac := hmac.New(sha1.New, []byte(tc.secret+"&"))
			mac.Write([]byte(stringToSign))
			expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

			got := aliyunSign(tc.params, tc.secret)
			if got != expected {
				t.Fatalf("签名不符：got=%q expected=%q（StringToSign=%q）", got, expected, stringToSign)
			}
		})
	}
}

// TestAliyunPercentEncode 编码规则：空格 → %20、* → %2A、~ 保留、保留字符转义。
func TestAliyunPercentEncode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a b", "a%20b"},
		{"a*b~c", "a%2Ab~c"},
		{"a+b", "a%2Bb"},
		{"a=b&c", "a%3Db%26c"},
		{"签名", tpEncode("签名")},
		{"ABCabc012-_.~", "ABCabc012-_.~"},
	} {
		if got := aliyunPercentEncode(tc.in); got != tc.want {
			t.Fatalf("percentEncode(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// ---------- 发送成功 ----------

func okAliyunResponse(bizID, requestID string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Code": "OK", "Message": "OK", "BizId": bizID, "RequestId": requestID,
		})
	}
}

// TestAliyunSMSSenderSendSuccess 成功路径：请求含正确的 Action/SignName/
// TemplateCode/PhoneNumbers/TemplateParam/Signature，签名用测试侧实现重算比对；
// 响应 BizId 作为 provider_message_id；密钥不出现在请求 URL。
func TestAliyunSMSSenderSendSuccess(t *testing.T) {
	fake := startFakeAliyun(t, okAliyunResponse("8906000000000000^12345678", "req-id-abcd"))
	sender := NewAliyunSMSSender()
	sender.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	sender.nonce = func() string { return "fixed-nonce-0001" }

	cfg := aliyunConfigJSON(fake.endpoint())
	result, err := sender.Send(context.Background(), aliyunSendRequest(aliyunChannelWith(cfg)))
	if err != nil {
		t.Fatalf("发送不应失败：%v", err)
	}
	if result.ProviderMessageID != "8906000000000000^12345678" {
		t.Fatalf("provider_message_id 应为 BizId：%q", result.ProviderMessageID)
	}

	r := fake.lastRequest(t)
	if r.Method != http.MethodGet {
		t.Fatalf("RPC 请求应为 GET，实际 %s", r.Method)
	}
	if r.URL.Path != "/" {
		t.Fatalf("RPC 请求路径应为 /，实际 %q", r.URL.Path)
	}
	q := r.URL.Query()
	for key, want := range map[string]string{
		"Action":           "SendSms",
		"Version":          "2017-05-25",
		"Format":           "JSON",
		"AccessKeyId":      "testKeyId",
		"RegionId":         "cn-hangzhou",
		"SignName":         "签名名称",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   "fixed-nonce-0001",
		"TemplateCode":     "SMS_123456789",
		"PhoneNumbers":     "13800001234",
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("参数 %s=%q，期望 %q", key, got, want)
		}
	}
	// Timestamp：UTC ISO8601（yyyy-MM-ddTHH:mm:ssZ）。
	if got := q.Get("Timestamp"); got != "2026-01-02T03:04:05Z" {
		t.Fatalf("Timestamp 应为 UTC ISO8601：%q", got)
	}
	// TemplateParam：参数按 JSON 对象投递，值原样。
	var tplParam map[string]string
	if err := json.Unmarshal([]byte(q.Get("TemplateParam")), &tplParam); err != nil {
		t.Fatalf("TemplateParam 应为 JSON 对象：%v（%q）", err, q.Get("TemplateParam"))
	}
	if tplParam["name"] != "张三" {
		t.Fatalf("TemplateParam 值不符：%v", tplParam)
	}
	// Signature：测试侧独立重算比对。
	if got, want := q.Get("Signature"), testSideSign(q, "testSecret"); got != want {
		t.Fatalf("签名不符：got=%q expected=%q", got, want)
	}
	// 密钥与签名原料不得出现在 URL 之外的头里。
	for _, header := range []string{"Authorization", "X-Acs-Access-Key"} {
		if v := r.Header.Get(header); strings.Contains(v, "testSecret") {
			t.Fatalf("请求头 %s 不应包含密钥：%q", header, v)
		}
	}
}

// TestAliyunSMSSenderBizIdFallbackRequestId 响应缺 BizId 时回退 RequestId。
func TestAliyunSMSSenderBizIdFallbackRequestId(t *testing.T) {
	fake := startFakeAliyun(t, okAliyunResponse("", "only-request-id"))
	sender := NewAliyunSMSSender()
	cfg := aliyunConfigJSON(fake.endpoint())
	result, err := sender.Send(context.Background(), aliyunSendRequest(aliyunChannelWith(cfg)))
	if err != nil {
		t.Fatalf("发送不应失败：%v", err)
	}
	if result.ProviderMessageID != "only-request-id" {
		t.Fatalf("缺 BizId 应回退 RequestId：%q", result.ProviderMessageID)
	}
}

// TestAliyunSMSSenderEmptyParams 模板未声明参数时不携带 TemplateParam。
func TestAliyunSMSSenderEmptyParams(t *testing.T) {
	fake := startFakeAliyun(t, okAliyunResponse("biz", "req"))
	sender := NewAliyunSMSSender()
	req := aliyunSendRequest(aliyunChannelWith(aliyunConfigJSON(fake.endpoint())))
	req.Params = nil
	if _, err := sender.Send(context.Background(), req); err != nil {
		t.Fatalf("发送不应失败：%v", err)
	}
	if got := fake.lastRequest(t).URL.Query().Get("TemplateParam"); got != "" {
		t.Fatalf("空参数不应携带 TemplateParam：%q", got)
	}
}

// ---------- 失败路径 ----------

// TestAliyunSMSSenderErrorCode 业务错误码（HTTP 200 但 Code != OK）：
// 错误含错误码与描述，落失败日志用；不回显密钥。
func TestAliyunSMSSenderErrorCode(t *testing.T) {
	fake := startFakeAliyun(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Code": "isv.BUSINESS_LIMIT_CONTROL", "Message": "触发分钟级流控",
			"RequestId": "req-err",
		})
	})
	sender := NewAliyunSMSSender()
	cfg := aliyunConfigJSON(fake.endpoint())
	_, err := sender.Send(context.Background(), aliyunSendRequest(aliyunChannelWith(cfg)))
	if err == nil {
		t.Fatal("业务错误码应返回错误")
	}
	if !strings.Contains(err.Error(), "isv.BUSINESS_LIMIT_CONTROL") || !strings.Contains(err.Error(), "触发分钟级流控") {
		t.Fatalf("错误应含错误码与描述：%v", err)
	}
	if strings.Contains(err.Error(), "testSecret") {
		t.Fatalf("错误不得回显密钥：%v", err)
	}
}

// TestAliyunSMSSenderHTTP500 非 200 状态：错误含状态码与截断的响应体，不含密钥。
func TestAliyunSMSSenderHTTP500(t *testing.T) {
	fake := startFakeAliyun(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"Code":"InternalError","Message":"内部错误"}`))
	})
	sender := NewAliyunSMSSender()
	cfg := aliyunConfigJSON(fake.endpoint())
	_, err := sender.Send(context.Background(), aliyunSendRequest(aliyunChannelWith(cfg)))
	if err == nil {
		t.Fatal("HTTP 500 应返回错误")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("错误应含状态码与响应摘要：%v", err)
	}
	if strings.Contains(err.Error(), "testSecret") || strings.Contains(err.Error(), "testKeyId") {
		t.Fatalf("错误不得回显密钥/AccessKeyId：%v", err)
	}
}

// TestAliyunSMSSenderTimeout 慢响应 + 短超时 ctx：错误标注「超时」并快速返回。
func TestAliyunSMSSenderTimeout(t *testing.T) {
	fake := startFakeAliyun(t, okAliyunResponse("biz", "req"))
	fake.stall = 5 * time.Second
	sender := NewAliyunSMSSender()
	cfg := aliyunConfigJSON(fake.endpoint())

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := sender.Send(ctx, aliyunSendRequest(aliyunChannelWith(cfg)))
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应标注超时：%v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时应快速返回，实际耗时 %v", elapsed)
	}
}

// TestAliyunSMSSenderConnectionFailure 连接失败：错误带「请求失败」，不回显
// URL（含 AccessKeyId）与密钥。
func TestAliyunSMSSenderConnectionFailure(t *testing.T) {
	sender := NewAliyunSMSSender()
	// 127.0.0.1:1 几乎必然未监听，触发 dial 失败。
	cfg := aliyunConfigJSON("http://127.0.0.1:1")
	_, err := sender.Send(context.Background(), aliyunSendRequest(aliyunChannelWith(cfg)))
	if err == nil {
		t.Fatal("连接失败应返回错误")
	}
	if !strings.Contains(err.Error(), "请求失败") {
		t.Fatalf("错误文案不符：%v", err)
	}
	if strings.Contains(err.Error(), "testKeyId") || strings.Contains(err.Error(), "testSecret") {
		t.Fatalf("错误不得回显 AccessKeyId/密钥：%v", err)
	}
}

// TestAliyunSMSSenderInvalidConfig 配置缺失/非法 fail-closed（错误详情落日志）。
func TestAliyunSMSSenderInvalidConfig(t *testing.T) {
	sender := NewAliyunSMSSender()
	for _, cfg := range []string{
		`{"accessKeySecret":"s","signName":"签名"}`,                                       // 缺 accessKeyId
		`{"accessKeyId":"k","signName":"签名"}`,                                           // 缺 accessKeySecret
		`{"accessKeyId":"k","accessKeySecret":"s"}`,                                     // 缺 signName
		`{"accessKeyId":"k","accessKeySecret":"s","signName":"名","endpoint":"ftp://x"}`, // endpoint 非法
	} {
		req := aliyunSendRequest(aliyunChannelWith(cfg))
		if _, err := sender.Send(context.Background(), req); err == nil {
			t.Fatalf("配置 %s 应返回错误", cfg)
		}
	}
}

// TestAliyunSMSSenderProvider 注册键与渠道 provider 对应。
func TestAliyunSMSSenderProvider(t *testing.T) {
	if got := NewAliyunSMSSender().Provider(); got != model.ProviderAliyunSMS {
		t.Fatalf("Provider()=%q，期望 %q", got, model.ProviderAliyunSMS)
	}
}
