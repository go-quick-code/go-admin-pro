package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// 三方 OAuth2 HTTP 调用的共用底座（issue #77，spec §4.4.8）：
//   - 单请求超时 10s（三方端点无响应时快速失败，回调统一 400，不吊死浏览器）；
//   - 错误信息剥离 url.Error 外壳（其 Error() 含完整 URL，可能带 client_secret，
//     参照 #74 阿里云短信的脱敏处理）；
//   - 响应体读取上限 1MB，防止异常端点把内存打爆；raw_info 落库另有 4096
//     字节截断（socialRawInfoMaxBytes）。

// socialHTTPTimeout 是三方 HTTP 调用的单请求超时（spec §4.4.8）。
const socialHTTPTimeout = 10 * time.Second

// socialHTTPBodyLimit 是三方响应体的读取上限（防御性，正常 userinfo 远小于此）。
const socialHTTPBodyLimit = 1 << 20

// socialHTTP 是三家 provider 共用的 HTTP 客户端；测试用例在同一个包内可以把
// client 换成短超时实例来验证超时行为。
type socialHTTP struct {
	client *http.Client
}

func newSocialHTTP() *socialHTTP {
	return &socialHTTP{client: &http.Client{Timeout: socialHTTPTimeout}}
}

// get 发起 GET 请求并返回响应体。ctx 已含超时控制（client.Timeout 兜底）。
func (h *socialHTTP) get(ctx context.Context, endpoint string, query url.Values) ([]byte, error) {
	if len(query) > 0 {
		endpoint = endpoint + "?" + query.Encode()
	}
	return h.do(ctx, http.MethodGet, endpoint, nil, nil)
}

// postJSON 发起 JSON POST 请求并返回响应体。
func (h *socialHTTP) postJSON(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("编码三方请求体: %w", err)
	}
	header := map[string]string{"Content-Type": "application/json"}
	return h.do(ctx, http.MethodPost, endpoint, body, header)
}

// do 发起请求并读取响应体；非 200 视为失败（错误里带状态码与响应摘要，
// 摘要经 truncateRunes 截断，不含 query 里的密钥）。
func (h *socialHTTP) do(ctx context.Context, method, endpoint string, body []byte, header map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造三方请求: %w", err)
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		// 剥离 url.Error 外壳：其 Error() 含完整 URL（可能携带 client_secret）。
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		msg := "请求三方服务失败"
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			msg = fmt.Sprintf("请求三方服务超时（%s）", socialHTTPTimeout)
		}
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, socialHTTPBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("读取三方响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("三方服务响应异常：HTTP %d：%s",
			resp.StatusCode, truncateRunes(string(data), 200))
	}
	return data, nil
}

// socialErrcode 是三家平台错误信封的并集：
//   - 微信/企微：errcode != 0 表示失败（成功时可能省略或为 0）；
//   - 钉钉 v1.0 新版接口：HTTP 非 200 + 字符串 code/message 表示失败。
type socialErrcode struct {
	Errcode int    `json:"errcode"`
	Errmsg  string `json:"errmsg"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// failed 判断业务层是否失败；钉钉成功响应的 code 字段（如 corpId 场景）不会
// 误判：userAccessToken 成功响应没有 code 字段。
func (e socialErrcode) failed() bool {
	return e.Errcode != 0 || (e.Code != "" && e.Code != "0")
}

// err 把错误信封转成带错误码与描述的错误。
func (e socialErrcode) err() error {
	if e.Errmsg != "" || e.Errcode != 0 {
		return fmt.Errorf("三方接口返回错误：错误码 %d：%s", e.Errcode, e.Errmsg)
	}
	return fmt.Errorf("三方接口返回错误：%s：%s", e.Code, e.Message)
}

// checkErrcode 解析响应体并判断业务错误；解析失败（非 JSON）也按错误处理。
func checkErrcode(body []byte) error {
	var envelope socialErrcode
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("解析三方响应: %w（响应=%s）", err, truncateRunes(string(body), 200))
	}
	if envelope.failed() {
		return envelope.err()
	}
	return nil
}
