package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/testutil/fakesmtp"
)

// ---------- 用例辅助 ----------

type smtpConfigOpt func(m map[string]any)

// smtpConfigJSON 构造 SMTP 渠道配置 JSON：默认 tls=none 指向明文假服务器。
func smtpConfigJSON(host string, port int, opts ...smtpConfigOpt) string {
	m := map[string]any{
		"host":     host,
		"port":     port,
		"username": "mailer",
		"password": "pass-123",
		"from":     "noreply@example.com",
		"fromName": "邮件机器人",
		"tls":      "none",
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

func smtpChannelWith(cfgJSON string) model.Channel {
	return model.Channel{
		BaseEntity:  model.BaseEntity{ID: 1},
		ChannelType: model.ChannelTypeMail,
		Provider:    model.ProviderSMTP,
		Config:      cfgJSON,
	}
}

func smtpSendRequest(ch model.Channel, recipient, title, content string) SendRequest {
	return SendRequest{
		Channel:      ch,
		TemplateCode: "welcome",
		Recipient:    recipient,
		Title:        title,
		Content:      content,
	}
}

func TestParseSMTPConfig(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string // 空＝期望成功
	}{
		{name: "完整配置", json: smtpConfigJSON("smtp.example.com", 587)},
		{name: "缺 host", json: smtpConfigJSON("", 587), wantErr: "host"},
		{name: "host 带端口", json: smtpConfigJSON("smtp.example.com:25", 587), wantErr: "host"},
		{name: "缺 from", json: smtpConfigJSON("smtp.example.com", 587, func(m map[string]any) { delete(m, "from") }), wantErr: "from"},
		{name: "from 非法", json: smtpConfigJSON("smtp.example.com", 587, func(m map[string]any) { m["from"] = "not-an-email" }), wantErr: "from"},
		{name: "端口超范围", json: smtpConfigJSON("smtp.example.com", 70000), wantErr: "端口"},
		{name: "端口负数", json: smtpConfigJSON("smtp.example.com", -1), wantErr: "端口"},
		{name: "tls 非法", json: smtpConfigJSON("smtp.example.com", 587, func(m map[string]any) { m["tls"] = "ssl2" }), wantErr: "tls"},
		{name: "非对象", json: `["x"]`, wantErr: "JSON 对象"},
		{name: "非 JSON", json: `not-json`, wantErr: "JSON 对象"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseSMTPConfig(tc.json)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("期望错误包含 %q，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错：%v", err)
			}
			if cfg.Port != 587 {
				t.Fatalf("端口解析不符：%d", cfg.Port)
			}
		})
	}

	// 缺省值：port 缺省 25，tls 缺省 starttls。
	cfg, err := parseSMTPConfig(`{"host":"smtp.example.com","from":"a@b.com"}`)
	if err != nil {
		t.Fatalf("最小配置不应报错：%v", err)
	}
	if cfg.Port != 25 {
		t.Fatalf("缺省端口应为 25，实际 %d", cfg.Port)
	}
	if cfg.TLS != smtpTLSStartTLS {
		t.Fatalf("缺省 tls 应为 starttls，实际 %q", cfg.TLS)
	}
	if !cfg.useStartTLS() {
		t.Fatal("缺省应启用 STARTTLS")
	}
	if cfg, err := parseSMTPConfig(`{"host":"h","from":"a@b.com","tls":"none"}`); err != nil || cfg.useStartTLS() {
		t.Fatalf("tls=none 不应启用 STARTTLS（cfg=%v err=%v）", cfg, err)
	}
}

// TestSMTPSenderSendPlaintext 明文假服务器（tls=none）成功发送：
// 信封与头部正确、HTML 正文按 base64 投递、Message-ID 返回。
func TestSMTPSenderSendPlaintext(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{})
	sender := NewSMTPSender()
	cfg := smtpConfigJSON("127.0.0.1", fake.Port(t), func(m map[string]any) { m["tls"] = "none" })
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "欢迎使用 go-admin-pro", "<p>您好 张三</p>")

	result, err := sender.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("发送不应失败：%v", err)
	}
	if !strings.HasPrefix(result.ProviderMessageID, "<smtp-") ||
		!strings.Contains(result.ProviderMessageID, "@127.0.0.1>") {
		t.Fatalf("provider message id 形态不符：%q", result.ProviderMessageID)
	}

	mailFrom, rcptTo, data, quit := fake.Snapshot()
	if mailFrom != "noreply@example.com" {
		t.Fatalf("信封 MAIL FROM 不符：%q", mailFrom)
	}
	if rcptTo != "user@example.com" {
		t.Fatalf("信封 RCPT TO 不符：%q", rcptTo)
	}
	if !quit {
		t.Fatal("发送成功后应发送 QUIT")
	}
	if subject := fakesmtp.DecodeMailHeader(data, "Subject"); !strings.HasPrefix(subject, "=?utf-8?") {
		t.Fatalf("中文主题应 RFC2047 编码：%q", subject)
	}
	if from := fakesmtp.DecodeMailHeader(data, "From"); !strings.Contains(from, "noreply@example.com") {
		t.Fatalf("From 头不符（含 RFC2047 显示名）：%q", from)
	}
	if to := fakesmtp.DecodeMailHeader(data, "To"); to != "user@example.com" {
		t.Fatalf("To 头不符：%q", to)
	}
	if msgID := fakesmtp.DecodeMailHeader(data, "Message-ID"); msgID != result.ProviderMessageID {
		t.Fatalf("Message-ID 头应与返回值一致：%q vs %q", msgID, result.ProviderMessageID)
	}
	if ct := fakesmtp.DecodeMailHeader(data, "Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("HTML 内容应按 text/html 投递：%q", ct)
	}
	if body := fakesmtp.DecodeMailBody(t, data); body != "<p>您好 张三</p>" {
		t.Fatalf("正文解码不符：%q", body)
	}
	// 密钥不得外泄到投递内容里（密钥与安全 §4.11）。
	if strings.Contains(string(data), "pass-123") {
		t.Fatal("邮件内容不应包含 SMTP 密码")
	}
}

// TestSMTPSenderSendStartTLS 真实 STARTTLS 握手（自签证书经注入的信任池校验）：
// tls 缺省 starttls，服务器按解密后的明文捕获正文。
func TestSMTPSenderSendStartTLS(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{WithTLS: true})
	sender := NewSMTPSender()
	sender.tlsRoots = fake.TrustPool(t)
	// tls 字段缺省 → 走 STARTTLS。
	cfg := smtpConfigJSON("127.0.0.1", fake.Port(t), func(m map[string]any) { delete(m, "tls") })
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "测试主题", "<p>加密正文</p>")

	result, err := sender.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("STARTTLS 发送不应失败：%v", err)
	}
	if result.ProviderMessageID == "" {
		t.Fatal("应返回 provider message id")
	}
	_, _, data, _ := fake.Snapshot()
	if body := fakesmtp.DecodeMailBody(t, data); body != "<p>加密正文</p>" {
		t.Fatalf("TLS 通道后正文不符：%q", body)
	}
}

// TestSMTPSenderPlainTextBody 纯文本内容按 text/plain 投递。
func TestSMTPSenderPlainTextBody(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{})
	sender := NewSMTPSender()
	cfg := smtpConfigJSON("127.0.0.1", fake.Port(t))
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "纯文本", "您好，这是纯文本邮件")

	if _, err := sender.Send(context.Background(), req); err != nil {
		t.Fatalf("发送不应失败：%v", err)
	}
	_, _, data, _ := fake.Snapshot()
	if ct := fakesmtp.DecodeMailHeader(data, "Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("纯文本内容应按 text/plain 投递：%q", ct)
	}
}

// TestSMTPSenderAuthFailure 认证失败：错误带「认证失败」与状态码，正文未投递。
func TestSMTPSenderAuthFailure(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{AuthFail: true})
	sender := NewSMTPSender()
	cfg := smtpConfigJSON("127.0.0.1", fake.Port(t))
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "主题", "内容")

	_, err := sender.Send(context.Background(), req)
	if err == nil {
		t.Fatal("认证失败应返回错误")
	}
	if !strings.Contains(err.Error(), "认证失败") || !strings.Contains(err.Error(), "535") {
		t.Fatalf("错误文案不符：%v", err)
	}
	if _, _, data, _ := fake.Snapshot(); data != nil {
		t.Fatalf("认证失败不应投递正文：%q", string(data))
	}
}

// TestSMTPSenderConnectionFailure 连接失败（端口未监听）。
func TestSMTPSenderConnectionFailure(t *testing.T) {
	sender := NewSMTPSender()
	// 127.0.0.1:1 几乎必然未监听，触发 dial 失败。
	cfg := smtpConfigJSON("127.0.0.1", 1)
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "主题", "内容")

	_, err := sender.Send(context.Background(), req)
	if err == nil {
		t.Fatal("连接失败应返回错误")
	}
	if !strings.Contains(err.Error(), "连接失败") {
		t.Fatalf("错误文案不符：%v", err)
	}
}

// TestSMTPSenderTimeout 假服务器接受连接后不响应：短超时 ctx 触发「超时」错误。
func TestSMTPSenderTimeout(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{Stall: true})
	sender := NewSMTPSender()
	cfg := smtpConfigJSON("127.0.0.1", fake.Port(t))
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "主题", "内容")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := sender.Send(ctx, req)
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误文案应标注超时：%v", err)
	}
}

// TestSMTPSenderStartTLSUnsupported 服务器不提供 STARTTLS：starttls 模式 fail-closed。
func TestSMTPSenderStartTLSUnsupported(t *testing.T) {
	fake := fakesmtp.StartFakeSMTP(t, fakesmtp.FakeSMTPConfig{NoStartTLS: true})
	sender := NewSMTPSender()
	cfg := smtpConfigJSON("127.0.0.1", fake.Port(t), func(m map[string]any) { m["tls"] = "starttls" })
	req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "主题", "内容")

	_, err := sender.Send(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("应报不支持 STARTTLS：%v", err)
	}
}

// TestSMTPSenderInvalidConfig 配置缺失/非法时 fail-closed（错误详情落日志，不回显）。
func TestSMTPSenderInvalidConfig(t *testing.T) {
	sender := NewSMTPSender()
	for _, cfg := range []string{
		`{"from":"a@b.com"}`,                        // 缺 host
		`{"host":"h","from":"bad"}`,                 // from 非法
		`{"host":"h","from":"a@b.com","tls":"ssl"}`, // tls 枚举外
	} {
		req := smtpSendRequest(smtpChannelWith(cfg), "user@example.com", "主题", "内容")
		if _, err := sender.Send(context.Background(), req); err == nil {
			t.Fatalf("配置 %s 应返回错误", cfg)
		}
	}
}

// TestWrapSMTPStep 超时错误必须带「超时」标记（服务层据此写失败日志）。
func TestWrapSMTPStep(t *testing.T) {
	dialErr := &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded, Net: "tcp"}
	if got := wrapSMTPStep("连接", dialErr); !strings.Contains(got.Error(), "连接超时") {
		t.Fatalf("超时包装不符：%v", got)
	}
	if got := wrapSMTPStep("连接", fmt.Errorf("connection refused")); !strings.Contains(got.Error(), "连接失败") {
		t.Fatalf("普通错误包装不符：%v", got)
	}
}
