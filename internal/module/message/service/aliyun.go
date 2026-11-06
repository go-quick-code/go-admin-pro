package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
)

// 阿里云短信通道实现（issue #74，ADR 0010）：stdlib net/http + 阿里云 RPC 签名
// （HMAC-SHA1），不引入云厂商 SDK。配置项（message_channel.config JSON）：
//
//	{
//	  "accessKeyId":     "LTAI...",                        // 必填
//	  "accessKeySecret": "******",                         // 必填；查询时已由 service 脱敏
//	  "signName":        "签名名称",                        // 必填，已报备的短信签名
//	  "regionId":        "cn-hangzhou",                    // 缺省 cn-hangzhou
//	  "endpoint":        "https://dysmsapi.aliyuncs.com"   // 缺省官方接入点
//	}
//
// 约定：message_template.code 直接使用阿里云控制台的模板编码（SMS_ 开头），
// 发送时原样作为 TemplateCode；模板声明的参数以 JSON 对象作为 TemplateParam。
//
// 超时：单次请求受 ctx deadline 与 sender.timeout 的较小值约束（spec §4.3.2：
// 超时 10s）；错误详情只落 message_log，HTTP 层统一返回 502 中文提示，
// 不回显配置与密钥（spec §4.3.5）。
//
// 已知限制（本轮不做，见 spec §4.3.8）：手动重发不带原始模板参数（日志不存
// params），声明了变量的阿里云模板重发会被阿里云拒绝并按失败记录。

const (
	// aliyunSMSAction 是 dysmsapi 的发送动作。
	aliyunSMSAction = "SendSms"
	// aliyunSMSAPIVersion 是 dysmsapi 的 API 版本号。
	aliyunSMSAPIVersion = "2017-05-25"
	// aliyunSMSOKCode 是阿里云短信的成功业务码。
	aliyunSMSOKCode = "OK"
	// 缺省 region 与官方接入点。
	aliyunSMSDefaultRegion   = "cn-hangzhou"
	aliyunSMSDefaultEndpoint = "https://dysmsapi.aliyuncs.com"
	// aliyunResponseLimit 限制读取的响应体大小，防御异常端点返回超大内容。
	aliyunResponseLimit = 64 << 10
)

// aliyunSMSConfig 是阿里云短信渠道配置的结构化形态。
type aliyunSMSConfig struct {
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	SignName        string `json:"signName"`
	RegionID        string `json:"regionId"`
	Endpoint        string `json:"endpoint"`
}

// parseAliyunSMSConfig 把渠道配置 JSON 解析为结构化配置并做完整性校验：
// accessKeyId/accessKeySecret/signName 必填、endpoint 必须是不带路径的
// http(s) 地址。错误会进入失败日志（含细节），由 service 层统一转成
// 不含细节的 502。
func parseAliyunSMSConfig(raw string) (aliyunSMSConfig, error) {
	var cfg aliyunSMSConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("阿里云短信配置必须是 JSON 对象: %w", err)
	}
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.AccessKeySecret = strings.TrimSpace(cfg.AccessKeySecret)
	cfg.SignName = strings.TrimSpace(cfg.SignName)
	cfg.RegionID = strings.TrimSpace(cfg.RegionID)
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	if cfg.AccessKeyID == "" {
		return cfg, errors.New("阿里云短信配置缺少 accessKeyId")
	}
	if cfg.AccessKeySecret == "" {
		return cfg, errors.New("阿里云短信配置缺少 accessKeySecret")
	}
	if cfg.SignName == "" {
		return cfg, errors.New("阿里云短信配置缺少 signName")
	}
	if cfg.RegionID == "" {
		cfg.RegionID = aliyunSMSDefaultRegion
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = aliyunSMSDefaultEndpoint
	}
	// RPC 风格请求固定发到根路径；带路径/查询串的 endpoint 直接拒绝（fail-closed）。
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return cfg, fmt.Errorf("阿里云短信配置 endpoint 不合法：%q（期望形如 https://dysmsapi.aliyuncs.com）", cfg.Endpoint)
	}
	parsed.Path = ""
	cfg.Endpoint = parsed.String()
	return cfg, nil
}

// aliyunPercentEncode 是阿里云 RPC 签名要求的 RFC 3986 百分号编码：
// 在 net/url 的 QueryEscape 基础上按官方样例修正三处差异——空格编码为 %20
// （而非 +）、* 显式转义为 %2A、~ 保留原样（官方样例面向 Java URLEncoder，
// Go 对后两者本就一致，保留替换是为了与文档语义逐字对应）。
func aliyunPercentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

// aliyunSign 计算阿里云 RPC 签名（spec：HMAC-SHA1）：
//  1. 参数按名称 ASCII 升序拼成 canonicalizedQueryString；
//  2. StringToSign = GET&%2F&percentEncode(canonicalizedQueryString)；
//  3. 以 accessKeySecret + "&" 为密钥做 HMAC-SHA1，结果 base64。
//
// params 不应包含 Signature 本身；Timestamp 的 ISO8601 冒号由编码步骤转义。
func aliyunSign(params url.Values, accessKeySecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for i, k := range keys {
		if i > 0 {
			canonical.WriteByte('&')
		}
		canonical.WriteString(aliyunPercentEncode(k))
		canonical.WriteByte('=')
		canonical.WriteString(aliyunPercentEncode(params.Get(k)))
	}
	stringToSign := "GET&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(canonical.String())
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// aliyunSignatureNonce 生成一次性的签名字符串随机数（16 字节 hex）；
// crypto/rand 失败时退化为纳秒时间戳，保证请求仍可发出。
func aliyunSignatureNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// buildAliyunTemplateParam 把模板参数序列化为阿里云要求的 JSON 对象字符串；
// 关闭 HTML 转义保证参数值原样投递。空参数返回空串（请求省略 TemplateParam）。
func buildAliyunTemplateParam(params map[string]string) (string, error) {
	if len(params) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(params); err != nil {
		return "", fmt.Errorf("序列化短信模板参数: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// aliyunSMSResponse 是 dysmsapi SendSms 的响应子集（只取用到的字段）。
type aliyunSMSResponse struct {
	RequestID string `json:"RequestId"`
	BizID     string `json:"BizId"`
	Code      string `json:"Code"`
	Message   string `json:"Message"`
}

// AliyunSMSSender 是内置的 aliyun_sms provider（issue #74）。
type AliyunSMSSender struct {
	// timeout 是无 ctx deadline 时的请求上限；service 层的发送流程会用
	// sendTimeout（10s）包裹 ctx，两者取较小值生效。
	timeout time.Duration
	// now/nonce 仅供测试固定 Timestamp 与 SignatureNonce（已知答案比对用）。
	now   func() time.Time
	nonce func() string
	// client 复用连接；不设全局 Timeout，单请求超时由 ctx deadline 控制。
	client *http.Client
}

// NewAliyunSMSSender 创建 aliyun_sms provider（10s 请求超时，spec §4.3.2）。
func NewAliyunSMSSender() *AliyunSMSSender {
	return &AliyunSMSSender{
		timeout: sendTimeout,
		now:     time.Now,
		nonce:   aliyunSignatureNonce,
		client:  &http.Client{},
	}
}

// Provider 返回注册键（与 message_channel.provider 对应）。
func (s *AliyunSMSSender) Provider() string { return model.ProviderAliyunSMS }

// Send 通过 dysmsapi 发送一条短信。Recipient 已由 service 校验为大陆手机号，
// TemplateCode/模板参数来自平台模板（code 即阿里云模板编码）。
func (s *AliyunSMSSender) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	cfg, err := parseAliyunSMSConfig(req.Channel.Config)
	if err != nil {
		return SendResult{}, err
	}
	templateParam, err := buildAliyunTemplateParam(req.Params)
	if err != nil {
		return SendResult{}, err
	}

	params := url.Values{}
	params.Set("AccessKeyId", cfg.AccessKeyID)
	params.Set("Action", aliyunSMSAction)
	params.Set("Format", "JSON")
	params.Set("PhoneNumbers", req.Recipient)
	params.Set("RegionId", cfg.RegionID)
	params.Set("SignName", cfg.SignName)
	params.Set("SignatureMethod", "HMAC-SHA1")
	params.Set("SignatureNonce", s.nonce())
	params.Set("SignatureVersion", "1.0")
	params.Set("TemplateCode", req.TemplateCode)
	if templateParam != "" {
		params.Set("TemplateParam", templateParam)
	}
	// 阿里云要求 UTC ISO8601（yyyy-MM-ddTHH:mm:ssZ）。
	params.Set("Timestamp", s.now().UTC().Format("2006-01-02T15:04:05Z"))
	params.Set("Version", aliyunSMSAPIVersion)
	params.Set("Signature", aliyunSign(params, cfg.AccessKeySecret))

	body, err := s.do(ctx, cfg, params)
	if err != nil {
		return SendResult{}, err
	}
	var out aliyunSMSResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return SendResult{}, fmt.Errorf("解析阿里云短信响应: %w（响应=%s）", err, truncateRunes(string(body), 200))
	}
	// HTTP 200 但业务码非 OK：阿里云把业务错误也放在 200 里返回。
	if out.Code != aliyunSMSOKCode {
		return SendResult{}, fmt.Errorf("阿里云短信发送失败：错误码 %s：%s", out.Code, out.Message)
	}
	// provider_message_id 优先 BizId（发送流水号），缺失回退 RequestId。
	if out.BizID != "" {
		return SendResult{ProviderMessageID: out.BizID}, nil
	}
	return SendResult{ProviderMessageID: out.RequestID}, nil
}

// do 发起签名后的 RPC 请求并读取响应体。错误信息只保留原因，剥离 url.Error
// 外壳（其 Error() 含完整 URL，会带出 AccessKeyId 与 Signature）；
// 超时统一标注「超时」，其余标注「请求失败」。
func (s *AliyunSMSSender) do(ctx context.Context, cfg aliyunSMSConfig, params url.Values) ([]byte, error) {
	deadline := time.Now().Add(s.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	reqCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, cfg.Endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("构造阿里云短信请求: %w", err)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		cause := err
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			cause = urlErr.Err
		}
		if isAliyunTimeout(cause) {
			return nil, fmt.Errorf("阿里云短信请求超时：%w", cause)
		}
		return nil, fmt.Errorf("阿里云短信请求失败：%w", cause)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, aliyunResponseLimit))
	if err != nil {
		if isAliyunTimeout(err) {
			return nil, fmt.Errorf("阿里云短信请求超时：%w", err)
		}
		return nil, fmt.Errorf("读取阿里云短信响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("阿里云短信接口异常：HTTP 状态 %d，响应 %s",
			resp.StatusCode, truncateRunes(string(body), 500))
	}
	return body, nil
}

// isAliyunTimeout 判断错误是否为超时（ctx deadline 与 net 超时都算）。
func isAliyunTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr) && netErr.Timeout()
}
