package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
)

// SMTP 通道实现（issue #73，ADR 0010）：stdlib net/smtp + STARTTLS，
// 不引入第三方邮件库。配置项（message_channel.config JSON）：
//
//	{
//	  "host": "smtp.example.com",      // 必填，不带端口
//	  "port": 587,                     // 缺省 25
//	  "username": "mailer",            // 缺省匿名发送（不执行 AUTH）
//	  "password": "******",            // 与 username 配套；查询时已由 service 脱敏
//	  "from": "noreply@example.com",   // 必填，信封发件人
//	  "fromName": "邮件机器人",         // 可选，显示名（RFC 2047 编码）
//	  "tls": "starttls"                // starttls（缺省）/ none（明文，仅调试）
//	}
//
// 超时：整体会话（连接+握手+认证+投递）受 ctx deadline 与 sender.timeout 的
// 较小值约束（spec §4.3.2：超时 10s）；错误详情只落 message_log，HTTP 层
// 统一返回 502 中文提示，不回显配置与密钥（spec §4.3.5）。

const (
	// smtpTLSStartTLS 是缺省加密模式：先明文连接，再 STARTTLS 升级。
	smtpTLSStartTLS = "starttls"
	// smtpTLSNone 表示全程明文（仅内网/调试使用）。
	smtpTLSNone = "none"
)

// smtpConfig 是 SMTP 渠道配置的结构化形态。
type smtpConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	FromName string `json:"fromName"`
	TLS      string `json:"tls"`
}

// useStartTLS 报告是否需要 STARTTLS 升级（tls 缺省按 starttls 处理）。
func (c smtpConfig) useStartTLS() bool { return c.TLS != smtpTLSNone }

var hostPortRe = regexp.MustCompile(`^[^:\[\]]+$`)

// parseSMTPConfig 把渠道配置 JSON 解析为结构化配置并做完整性校验：
// host/from 必填、端口范围、tls 枚举。错误会进入失败日志（含细节），
// 由 service 层统一转成不含细节的 502。
func parseSMTPConfig(raw string) (smtpConfig, error) {
	var cfg smtpConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("SMTP 配置必须是 JSON 对象: %w", err)
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.TLS = strings.TrimSpace(strings.ToLower(cfg.TLS))
	if cfg.Host == "" {
		return cfg, errors.New("SMTP 配置缺少 host")
	}
	// host 带端口/IPv6 括号会让 JoinHostPort 产生畸形地址，fail-closed。
	if !hostPortRe.MatchString(cfg.Host) {
		return cfg, errors.New("SMTP 配置 host 不应包含端口或括号")
	}
	if cfg.Port == 0 {
		cfg.Port = 25
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("SMTP 配置端口不合法：%d（期望 1–65535）", cfg.Port)
	}
	switch cfg.TLS {
	case smtpTLSNone:
	case "", smtpTLSStartTLS:
		// 缺省与显式 starttls 归一，保证配置回读语义一致。
		cfg.TLS = smtpTLSStartTLS
	default:
		return cfg, fmt.Errorf("SMTP 配置 tls 不合法：%q（期望 starttls 或 none）", cfg.TLS)
	}
	from, err := mail.ParseAddress(strings.TrimSpace(cfg.From))
	if err != nil || from.Address == "" || from.Address != strings.TrimSpace(cfg.From) {
		return cfg, fmt.Errorf("SMTP 配置 from 不合法：%q", cfg.From)
	}
	cfg.From = from.Address
	return cfg, nil
}

// SMTPSender 是内置的 smtp provider（issue #73）。
type SMTPSender struct {
	// timeout 是无 ctx deadline 时的会话上限；service 层的发送流程会用
	// sendTimeout（10s）包裹 ctx，两者取较小值生效。
	timeout time.Duration
	now     func() time.Time
	// tlsRoots 仅供测试注入假服务器的自签证书信任池（可替换 transport 的
	// 注入缝）；生产保持 nil，使用系统根证书。
	tlsRoots *x509.CertPool
}

// NewSMTPSender 创建 smtp provider（10s 会话超时，spec §4.3.2）。
func NewSMTPSender() *SMTPSender {
	return &SMTPSender{timeout: sendTimeout, now: time.Now}
}

// Provider 返回注册键（与 message_channel.provider 对应）。
func (s *SMTPSender) Provider() string { return model.ProviderSMTP }

// Send 投递一封邮件。Title 作为 Subject（RFC 2047 编码），Content 按 HTML/
// 纯文本自适应（检测典型标签）并 base64 编码，收件人已由 service 校验过
// RFC 5322 基础格式。
func (s *SMTPSender) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	cfg, err := parseSMTPConfig(req.Channel.Config)
	if err != nil {
		return SendResult{}, err
	}

	now := s.now()
	messageID := fmt.Sprintf("<smtp-%d@%s>", now.UnixNano(), cfg.Host)
	msg := buildMailMessage(cfg, req, messageID, now)
	if err := s.deliver(ctx, cfg, req.Recipient, msg); err != nil {
		return SendResult{}, err
	}
	return SendResult{ProviderMessageID: messageID}, nil
}

// deliver 执行 SMTP 会话：拨号 → STARTTLS（可选）→ AUTH（可选）→ 信封 → DATA。
// 每一步的错误都带步骤前缀，供失败日志定位；超时统一标注「超时」。
func (s *SMTPSender) deliver(ctx context.Context, cfg smtpConfig, to string, msg []byte) error {
	deadline := time.Now().Add(s.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return wrapSMTPStep("连接", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return wrapSMTPStep("连接", err)
	}
	defer client.Close()

	if cfg.useStartTLS() {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			// fail-closed：要求加密但服务器不支持时拒绝明文回退。
			return errors.New("SMTP 服务器不支持 STARTTLS")
		}
		tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
		if s.tlsRoots != nil {
			tlsCfg.RootCAs = s.tlsRoots
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return wrapSMTPStep("STARTTLS", err)
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return wrapSMTPStep("认证", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return wrapSMTPStep("发送", err)
	}
	if err := client.Rcpt(to); err != nil {
		return wrapSMTPStep("发送", err)
	}
	writer, err := client.Data()
	if err != nil {
		return wrapSMTPStep("发送", err)
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return wrapSMTPStep("发送", err)
	}
	if err := writer.Close(); err != nil {
		return wrapSMTPStep("发送", err)
	}
	// 邮件已被服务器接收（250 queued），QUIT 失败不影响发送结果。
	_ = client.Quit()
	return nil
}

// wrapSMTPStep 给错误加步骤前缀；net 超时统一标注「超时」，
// 让失败日志能区分 连接失败/认证失败/超时 三类（issue #73 验收标准）。
func wrapSMTPStep(step string, err error) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("SMTP %s超时：%w", step, err)
	}
	return fmt.Errorf("SMTP %s失败：%w", step, err)
}

// htmlTagRe 匹配常见 HTML 标签：命中即按 text/html 投递（spec：邮件模板
// content 支持 HTML 与纯文本）。纯文本里孤立的 < > 不构成标签，不受影响。
var htmlTagRe = regexp.MustCompile(`(?i)<(p|div|span|br|html|body|table|thead|tbody|tr|td|th|h[1-6]|a|b|i|u|strong|em|ul|ol|li|img|section|article|font)\b[^>]*/?>`)

// buildMailMessage 组装 RFC 5322 邮件（CRLF 换行，正文 base64 编码避免
// 行长度与 8bit 兼容问题）。
func buildMailMessage(cfg smtpConfig, req SendRequest, messageID string, now time.Time) []byte {
	var b strings.Builder
	from := cfg.From
	if cfg.FromName != "" {
		from = mime.QEncoding.Encode("utf-8", cfg.FromName) + " <" + cfg.From + ">"
	}
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + req.Recipient + "\r\n")
	if title := strings.TrimSpace(req.Title); title != "" {
		b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", title) + "\r\n")
	}
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	if htmlTagRe.MatchString(req.Content) {
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	} else {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	}
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(encodeMailBody(req.Content))
	return []byte(b.String())
}

// encodeMailBody 把正文按 76 列折行成 base64（RFC 2045）。
func encodeMailBody(content string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	var b strings.Builder
	for i, r := 0, len(encoded); i < r; i += 76 {
		end := i + 76
		if end > r {
			end = r
		}
		b.WriteString(encoded[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}
