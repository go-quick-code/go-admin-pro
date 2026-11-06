// testutil.FakeSMTP 是共用的最小 TCP SMTP 假服务器（issue #73）：
// 单元测试与集成测试都用它验证 smtp provider 的成功/认证失败/超时路径，
// 全程只监听 127.0.0.1 随机端口，不依赖外网 SMTP。
//
// 协议覆盖：220 问候 → EHLO →（可选 STARTTLS 真升级）→ AUTH PLAIN →
// MAIL/RCPT/DATA → QUIT；捕获信封与 DATA 供断言。
package fakesmtp

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// FakeSMTPConfig 是假服务器的行为开关。
type FakeSMTPConfig struct {
	WithTLS    bool // 支持 STARTTLS 真升级（自签证书）
	AuthFail   bool // AUTH 后回 535
	NoStartTLS bool // EHLO 广告不含 STARTTLS（拒绝 starttls 模式用例）
	Stall      bool // 接受连接后不回问候（超时用例）
}

// FakeSMTP 是运行中的假 SMTP 服务器。
type FakeSMTP struct {
	ln   net.Listener
	cert *tls.Certificate

	mu         sync.Mutex
	authFail   bool
	noStartTLS bool
	stall      bool
	mailFrom   string
	rcptTo     string
	data       []byte
	quit       bool
	done       chan struct{}
}

// StartFakeSMTP 在 127.0.0.1 随机端口启动假服务器并注册清理。
func StartFakeSMTP(tb testing.TB, opt FakeSMTPConfig) *FakeSMTP {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("启动假 SMTP 服务器: %v", err)
	}
	f := &FakeSMTP{
		ln:         ln,
		done:       make(chan struct{}),
		authFail:   opt.AuthFail,
		noStartTLS: opt.NoStartTLS,
		stall:      opt.Stall,
	}
	if opt.WithTLS {
		f.cert = newSelfSignedCert(tb)
	}
	go f.acceptLoop()
	tb.Cleanup(func() {
		close(f.done)
		_ = ln.Close()
	})
	return f
}

// Addr 返回监听地址（host:port）。
func (f *FakeSMTP) Addr() string { return f.ln.Addr().String() }

// Port 返回监听端口。
func (f *FakeSMTP) Port(tb testing.TB) int {
	tb.Helper()
	_, portStr, err := net.SplitHostPort(f.Addr())
	if err != nil {
		tb.Fatalf("解析假 SMTP 地址 %s: %v", f.Addr(), err)
	}
	port := 0
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		tb.Fatalf("解析假 SMTP 端口 %s: %v", portStr, err)
	}
	return port
}

// TLSCert 返回自签证书（WithTLS 时非 nil）。
func (f *FakeSMTP) TLSCert() *tls.Certificate { return f.cert }

// TrustPool 用自签证书构造信任池，供测试注入 smtp provider（可替换
// transport 注入缝；生产代码不传，走系统根证书）。
func (f *FakeSMTP) TrustPool(tb testing.TB) *x509.CertPool {
	tb.Helper()
	if f.cert == nil || len(f.cert.Certificate) == 0 {
		tb.Fatal("假 SMTP 服务器未启用 TLS")
	}
	parsed, err := x509.ParseCertificate(f.cert.Certificate[0])
	if err != nil {
		tb.Fatalf("解析假 SMTP 证书: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return pool
}

// Snapshot 返回捕获的信封与 DATA 正文。
func (f *FakeSMTP) Snapshot() (mailFrom, rcptTo string, data []byte, quit bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mailFrom, f.rcptTo, append([]byte(nil), f.data...), f.quit
}

// SelfSignedCert 生成仅测试用的自签证书（CN/IP 均为 127.0.0.1）。
func newSelfSignedCert(tb testing.TB) *tls.Certificate {
	tb.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatalf("生成 RSA 密钥: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		tb.Fatalf("生成自签证书: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (f *FakeSMTP) acceptLoop() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *FakeSMTP) writeRaw(conn net.Conn, s string) {
	_, _ = conn.Write([]byte(s))
}

func (f *FakeSMTP) writeLine(conn net.Conn, s string) {
	f.writeRaw(conn, s+"\r\n")
}

// handle 实现协议循环；cert 非 nil 时支持 STARTTLS 升级。
func (f *FakeSMTP) handle(conn net.Conn) {
	defer func() {
		if f.stall {
			return // 超时用例：连接留给客户端超时，测试结束统一回收
		}
		_ = conn.Close()
	}()
	br := bufio.NewReader(conn)
	if f.stall {
		select {
		case <-f.done:
		case <-time.After(60 * time.Second):
		}
		return
	}
	f.writeLine(conn, "220 test ESMTP ready")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(strings.ToUpper(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO"):
			f.mu.Lock()
			supports := !f.noStartTLS && f.cert != nil
			f.mu.Unlock()
			if supports {
				f.writeRaw(conn, "250-test\r\n250-8BITMIME\r\n250-PIPELINING\r\n250 STARTTLS\r\n")
			} else {
				f.writeRaw(conn, "250-test\r\n250-8BITMIME\r\n250 PIPELINING\r\n")
			}
		case cmd == "STARTTLS":
			if f.cert == nil {
				f.writeLine(conn, "502 Command not implemented")
				return
			}
			f.writeLine(conn, "220 Ready to start TLS")
			conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*f.cert}})
			br = bufio.NewReader(conn)
		case strings.HasPrefix(cmd, "AUTH"):
			f.mu.Lock()
			fail := f.authFail
			f.mu.Unlock()
			if fail {
				f.writeLine(conn, "535 5.7.8 Error: authentication failed")
				return
			}
			f.writeLine(conn, "235 2.7.0 Authentication successful")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.mailFrom = extractEnvelopeAddr(line[len("MAIL FROM:"):])
			f.mu.Unlock()
			f.writeLine(conn, "250 2.1.0 OK")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.rcptTo = extractEnvelopeAddr(line[len("RCPT TO:"):])
			f.mu.Unlock()
			f.writeLine(conn, "250 2.1.0 OK")
		case cmd == "DATA":
			f.writeLine(conn, "354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dataLine, err := br.ReadString('\n')
				if err != nil {
					return
				}
				trimmed := strings.TrimRight(dataLine, "\r\n")
				if trimmed == "." {
					break
				}
				// 去掉 SMTP 点填充（行首 ".." 转义为 "."）。
				if strings.HasPrefix(trimmed, "..") {
					trimmed = trimmed[1:]
				}
				body.WriteString(trimmed + "\r\n")
			}
			f.mu.Lock()
			f.data = []byte(body.String())
			f.mu.Unlock()
			f.writeLine(conn, "250 2.0.0 OK: queued")
		case cmd == "QUIT":
			f.mu.Lock()
			f.quit = true
			f.mu.Unlock()
			f.writeLine(conn, "221 2.0.0 Bye")
			return
		default:
			f.writeLine(conn, "250 2.0.0 OK")
		}
	}
}

// envelopeAddrRe 取 "MAIL FROM:<a@b> BODY=8BITMIME" 尖括号内的地址；
// 无尖括号时按原样返回。net/smtp 会附带 8BITMIME/SMTPUTF8 参数。
var envelopeAddrRe = regexp.MustCompile(`<([^>]*)>`)

func extractEnvelopeAddr(arg string) string {
	if m := envelopeAddrRe.FindStringSubmatch(arg); m != nil {
		return m[1]
	}
	return strings.TrimSpace(arg)
}

// DecodeMailHeader 从捕获的 DATA 中取指定头部（大小写不敏感）。
func DecodeMailHeader(data []byte, name string) string {
	for _, line := range strings.Split(string(data), "\r\n") {
		if line == "" {
			break // 头部区结束
		}
		if idx := strings.Index(line, ":"); idx > 0 && strings.EqualFold(strings.TrimSpace(line[:idx]), name) {
			return strings.TrimSpace(line[idx+1:])
		}
	}
	return ""
}

// DecodeMailBody 解码 base64 正文（Content-Transfer-Encoding: base64）。
func DecodeMailBody(tb testing.TB, data []byte) string {
	tb.Helper()
	parts := strings.SplitN(string(data), "\r\n\r\n", 2)
	if len(parts) != 2 {
		tb.Fatalf("邮件缺少正文分隔：%q", string(data))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(parts[1], "\r\n", ""))
	if err != nil {
		tb.Fatalf("正文 base64 解码: %v（body=%q）", err, parts[1])
	}
	return string(decoded)
}
