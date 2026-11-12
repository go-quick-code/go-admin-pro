package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// notify.go 是商户回调任务与超时关单的执行层（issue #134，
// docs/pay-mvp-spec.md §4.5/§4.6 demo 回调/§7 job 种子）。
//
// 结构（照公海回收 pool_recycle.go 模式）：
//   - infra_job handler 入口（payNotifyRetry / payOrderTimeoutClose）→
//     系统级遍历启用租户 → 逐租户处理（单租户失败不阻塞其他租户）；
//   - 任务并发认领：条件 UPDATE（status IN (0,21,22) 且 next_notify_time
//     到期，认领即推进 next_notify_time）——0 行 = 已被并发方认领，跳过不重发；
//   - 每次执行追加 pay_notify_log（append-only，响应摘要截断）；
//   - HMAC 密钥来自 configs（pay.notify-hmac-key）：缺失时拒发（fail-closed，
//     照阿里云 endpoint 先例），任务保持待重试并记日志。
//
// demo 回调三端点（order/refund/transfer，公开路由）是「模拟商户服务器」：
// 验签（Pay-Signature = HMAC-SHA256(rawBody, 服务端回调密钥)）→ 解析 →
// 业务联动扩展点（#137 注册 demo 表联动）→ 回业务码信封。

// NotifyRetryJobHandlerName 是商户回调重试任务的处理器名
// （infra_job.handler_name，种子行与 internal/app 注册处共用）。
const NotifyRetryJobHandlerName = "payNotifyRetry"

// OrderTimeoutCloseJobHandlerName 是订单超时关闭任务的处理器名。
const OrderTimeoutCloseJobHandlerName = "payOrderTimeoutClose"

// notifyRetryIntervals 是重试间隔序列（秒，spec §4.5 照芋道 NOTIFY_FREQUENCY）：
// 第 N 次失败后取第 N 个间隔；首次 + 8 重试共 9 次，第 9 次仍失败置 20 彻底失败。
var notifyRetryIntervals = [8]int{15, 15, 30, 180, 1800, 1800, 1800, 3600}

// notifyScanBatch 是单租户单轮扫描的任务数上限（防御性分批：单轮积压过大时
// 留给下一分钟，避免单次执行拖垮 HTTP 客户端预算）。
const notifyScanBatch = 100

// notifyRetryIntervalSeconds 返回第 n 次失败后的重试间隔秒数；
// n 越界（>=9 或 <=0）返回 0 表示不再重试。
func notifyRetryIntervalSeconds(failures int) int {
	if failures <= 0 || failures > len(notifyRetryIntervals) {
		return 0
	}
	return notifyRetryIntervals[failures-1]
}

// notifyResponseCodes 是商户回调响应的业务码信封（{"code":0} 为成功）。
type notifyResponseEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// notifyResponseSummaryMaxBytes 是日志 response 摘要的最大 rune 数
// （列 varchar(1024)：按 rune 截断保证不撕裂多字节字符、必然可入库；
// ASCII 响应下与「1KB」等价，spec §3.3）。
const notifyResponseSummaryMaxRunes = 1024

// classifyNotifyResult 把一次回调执行的 HTTP 结果翻译为任务状态与日志摘要
// （spec §4.5 响应语义）：
//   - execErr != nil（超时/连接错误）→ 22；
//   - HTTP 非 2xx → 22；
//   - 2xx 且响应体 {"code":0} → 10 成功；
//   - 2xx 但业务码非 0（含非 JSON/空体——无法确认成功，保守继续重试）→ 21。
func classifyNotifyResult(statusCode int, body []byte, execErr error) (status int8, summary string) {
	switch {
	case execErr != nil:
		return paymodel.NotifyTaskStatusRequestFail, truncateRunes(execErr.Error(), notifyResponseSummaryMaxRunes)
	case statusCode < 200 || statusCode > 299:
		return paymodel.NotifyTaskStatusRequestFail,
			truncateRunes(fmt.Sprintf("HTTP %d：%s", statusCode, string(body)), notifyResponseSummaryMaxRunes)
	}
	var envelope notifyResponseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code != 0 {
		// 非 JSON 或业务码非 0：请求成功但结果失败（21，继续重试）。
		return paymodel.NotifyTaskStatusResultFail, truncateRunes(string(body), notifyResponseSummaryMaxRunes)
	}
	return paymodel.NotifyTaskStatusSuccess, truncateRunes(string(body), notifyResponseSummaryMaxRunes)
}

// notifySignature 计算 Pay-Signature：HMAC-SHA256(rawBody, 服务端回调密钥) hex。
func notifySignature(rawBody []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyNotifySignature 校验 demo 回调请求签名（恒时比较防时序侧信道）：
// 密钥为空一律拒绝（fail-closed——空密钥下任何「有效签名」都不存在）。
func verifyNotifySignature(rawBody []byte, signature, key string) bool {
	if key == "" {
		return false
	}
	return hmac.Equal([]byte(notifySignature(rawBody, key)), []byte(signature))
}

// truncateRunes 按 rune 截断（同 social 先例 truncateRunes 语义）：保证截断后
// 仍是合法 UTF-8，可安全落 varchar 列。
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// ---------- 商户回调报文（发送端与 demo 解析端共用同一结构） ----------

// NotifyPayload 是商户回调 POST 的 JSON 报文（spec §4.5）：job 发送端与
// demo 回调端点解析端共用同一结构（demo 商户即本服务自身模拟，密钥同源）。
// status 固定 "success"（v1 只在成功时建任务，spec §4.5）。
type NotifyPayload struct {
	Type               int8   `json:"type"` // 1 支付 / 2 退款 / 3 转账
	Status             string `json:"status"`
	MerchantOrderID    string `json:"merchantOrderId,omitempty"`
	MerchantRefundID   string `json:"merchantRefundId,omitempty"`
	MerchantTransferID string `json:"merchantTransferId,omitempty"`
	DataID             int64  `json:"dataId"` // 对应单据 ID（type=1 为 pay_order.id）
	OrderNo            string `json:"orderNo,omitempty"`
	ChannelOrderNo     string `json:"channelOrderNo,omitempty"`
	Subject            string `json:"subject,omitempty"`     // 单据标题（#137 type=3 转账 enricher）
	Price              string `json:"price,omitempty"`       // decimal 字符串（不经 float）
	SuccessTime        string `json:"successTime,omitempty"` // RFC3339
}

// NotifyPayloadEnricher 按任务类型补充单据详情的扩展点：#134 内置 type=1
// （支付订单）实现；#135 退款 / #136 转账在装配处以 SetNotifyPayloadEnricher
// 追加各自的详情（pay_refund/pay_transfer 无 #134 的模型依赖）。
type NotifyPayloadEnricher func(ctx context.Context, task *paymodel.NotifyTask, payload *NotifyPayload) error

// DemoNotifyKind 是 demo 回调端点种类（path 段）。
const (
	DemoNotifyKindOrder    = "order"
	DemoNotifyKindRefund   = "refund"
	DemoNotifyKindTransfer = "transfer"
)

// DemoNotifyHandler 是 demo 商户回调业务联动的扩展点（#137 注册真实实现：
// 按 payload.MerchantOrderID 联动 pay_demo_order/pay_demo_withdraw 状态）。
// kind 为端点种类（order/refund/transfer），payload 已验签解析。
// 返回 error 时端点回非 0 业务码（任务转 21 继续重试）。
type DemoNotifyHandler func(ctx context.Context, kind string, payload *NotifyPayload) error

// demo 回调验签/解析失败的哨兵错误（HTTP 层翻译为 4xx；其余错误按业务失败
// 回非 0 业务码信封）。
var (
	// ErrDemoSignatureInvalid 签名缺失/伪造。
	ErrDemoSignatureInvalid = errors.New("回调签名无效")
	// ErrDemoPayloadInvalid 报文非法（非 JSON/类型不符）。
	ErrDemoPayloadInvalid = errors.New("回调报文无效")
)

// notifyHTTP 是回调 job 的出站 HTTP 客户端（照 socialHTTP 先例：10s 单请求
// 超时、1MB 响应体上限；错误剥离 url.Error 外壳——其 Error() 含完整 URL）。
type notifyHTTP struct {
	client *http.Client
}

// notifyHTTPTimeout 是单次回调 POST 的超时（spec §4.5 照 social 先例）。
const notifyHTTPTimeout = 10 * time.Second

// notifyHTTPBodyLimit 是回调响应体的读取上限（防御性，正常应答远小于此）。
const notifyHTTPBodyLimit = 1 << 20

func newNotifyHTTP() *notifyHTTP {
	return &notifyHTTP{client: &http.Client{Timeout: notifyHTTPTimeout}}
}

// postJSON 发起 JSON POST（附带 header）并返回状态码与响应体：
// 与 socialHTTP 不同，非 2xx 不视为 transport 错误——状态码与响应体原样返回，
// 由 classifyNotifyResult 按 spec §4.5 语义分类（21/22 均继续重试）。
func (h *notifyHTTP) postJSON(ctx context.Context, endpoint string, body []byte, header map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("构造回调请求: %w", err)
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		// 剥离 url.Error 外壳：其 Error() 含完整 URL（不向日志泄内部拓扑）。
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		msg := "请求商户回调地址失败"
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			msg = fmt.Sprintf("请求商户回调地址超时（%s）", notifyHTTPTimeout)
		}
		return 0, nil, fmt.Errorf("%s: %w", msg, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, notifyHTTPBodyLimit))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取回调响应: %w", err)
	}
	return resp.StatusCode, data, nil
}

// ---------- 依赖注入（装配期） ----------

// TenantLister 是启用租户清单的窄接口（job 跨租户迭代的数据源）：由
// internal/app 装配处适配 system Service（同公海回收的 crm sys 网关模式）。
type TenantLister interface {
	ListEnabledTenantIDs(ctx context.Context) ([]int64, error)
}

// SetTenantLister 注入启用租户清单来源（装配期）。未注入时 job 报错拒跑
// （fail-closed：静默空转会掩盖装配缺陷）。
func (s *Service) SetTenantLister(l TenantLister) {
	s.tenants = l
}

// SetNotifyHMACKey 注入服务端回调密钥（configs pay.notify-hmac-key）：
// 空/缺失时回调 job 拒发（fail-closed）、demo 回调端点拒绝一切验签。
func (s *Service) SetNotifyHMACKey(key string) {
	s.notifyHMACKey = key
}

// SetNotifyPayloadEnricher 注册任务类型的报文详情扩展（#135 退款 / #136 转账
// 在装配处追加；后注册覆盖先注册）。type=1 支付的内置实现见 enrichPaymentPayload。
func (s *Service) SetNotifyPayloadEnricher(taskType int8, fn NotifyPayloadEnricher) {
	if s.payloadEnrichers == nil {
		s.payloadEnrichers = make(map[int8]NotifyPayloadEnricher, 3)
	}
	s.payloadEnrichers[taskType] = fn
}

// SetDemoNotifyHandler 注册 demo 商户回调业务联动（#137 接入点：按
// payload.MerchantOrderID 联动 pay_demo_order/pay_demo_withdraw 状态）。
// 未注册时 demo 端点验签通过即回 {"code":0}（任务 10 成功）。
func (s *Service) SetDemoNotifyHandler(fn DemoNotifyHandler) {
	s.demoHandler = fn
}

// ---------- payNotifyRetry（商户回调重试 job） ----------

// NotifyRetryResult 汇总一轮回调重试的执行结果（handler 日志与调用方展示用）。
type NotifyRetryResult struct {
	Tenants    int   // 扫过的租户数
	Executed   int64 // 实际发送次数（认领成功的执行）
	Success    int64 // 其中任务转 10 成功的次数
	FailClosed int64 // 密钥缺失拒发的次数
}

// RunNotifyRetry 是商户回调重试任务入口（系统级动作，不依赖调用方租户上下文
// ——照公海回收模式：先列启用租户，再逐租户携带该租户上下文执行；租户插件
// fail-closed 保证每条语句只碰本租户数据）。单租户失败不阻塞其他租户，
// 错误聚合返回首个。
func (s *Service) RunNotifyRetry(ctx context.Context) (NotifyRetryResult, error) {
	tenantIDs, err := s.listEnabledTenants(ctx)
	if err != nil {
		return NotifyRetryResult{}, err
	}
	result := NotifyRetryResult{Tenants: len(tenantIDs)}
	var errs []error
	for _, id := range tenantIDs {
		r, err := s.retryTenantNotify(tenant.WithTenantID(ctx, id), s.businessNow())
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 回调重试失败: %w", id, err))
			continue
		}
		result.Executed += r.Executed
		result.Success += r.Success
		result.FailClosed += r.FailClosed
	}
	if len(errs) > 0 {
		return result, errs[0]
	}
	return result, nil
}

// listEnabledTenants 列启用租户：listers 未注入时报错拒跑（fail-closed）。
func (s *Service) listEnabledTenants(ctx context.Context) ([]int64, error) {
	if s.tenants == nil {
		return nil, errors.New("pay 回调任务未配置租户清单来源（SetTenantLister）")
	}
	return s.tenants.ListEnabledTenantIDs(ctx)
}

// retryTenantNotify 对 ctx 租户执行一轮回调重试：扫描到期任务（status IN
// (0,21,22) 且 next_notify_time <= now）逐条执行；单任务执行错误跳过该任务
// （任务保持到期状态，下轮重试），仅数据访问层错误中断本轮。
func (s *Service) retryTenantNotify(ctx context.Context, now time.Time) (NotifyRetryResult, error) {
	result := NotifyRetryResult{Tenants: 1}
	tasks, err := s.repo.ListDueNotifyTasks(ctx, now, notifyScanBatch)
	if err != nil {
		return result, err
	}
	for i := range tasks {
		executed, success, failClosed, err := s.executeNotifyTask(ctx, &tasks[i], now)
		if err != nil {
			slogSkipTask(&tasks[i], err)
			continue
		}
		result.Executed += executed
		result.Success += success
		result.FailClosed += failClosed
	}
	return result, nil
}

// notifyNextIntervalSeconds 返回第 attempt 次尝试失败后的重试间隔秒数；
// attempt 越过间隔表时取最后一个间隔（第 9 次失败的终态由结果改写为 20，
// 间隔值只为认领时预推进 next_notify_time）。
func notifyNextIntervalSeconds(attempt int) int {
	if v := notifyRetryIntervalSeconds(attempt); v > 0 {
		return v
	}
	return notifyRetryIntervals[len(notifyRetryIntervals)-1]
}

// executeNotifyTask 执行单个回调任务（spec §4.5）：
//  1. 密钥缺失 → 拒发（fail-closed）：条件推进 next_notify_time（不计次数、
//     不改状态）+ 记日志；0 行 = 并发方已处理；
//  2. 构造报文（任务基础字段 + 类型扩展详情）；
//  3. 条件认领（notify_times+1、last_execute_time、next_notify_time 预推进到
//     本次失败后的重试点）——0 行 = 已被并发方认领，跳过不重发；
//  4. POST JSON（header 带 Pay-Signature = HMAC-SHA256(rawBody, 密钥)）；
//  5. 响应分类（10/21/22）→ 第 9 次失败改写 20 彻底失败 → 回写终态 + 追加日志。
func (s *Service) executeNotifyTask(ctx context.Context, task *paymodel.NotifyTask, now time.Time) (executed, success, failClosed int64, err error) {
	tenantID := tenant.ID(ctx)
	if s.notifyHMACKey == "" {
		// fail-closed（spec §4.5 照阿里云 endpoint 先例）：不发送、不消耗
		// 重试次数，任务保持待重试并在下个间隔重查（密钥补配后自动恢复）。
		next := now.Add(time.Duration(notifyNextIntervalSeconds(task.NotifyTimes+1)) * time.Second)
		rows, err := s.repo.AdvanceNotifyTaskFailClosed(ctx, tenantID, task.ID, next, now)
		if err != nil {
			return 0, 0, 0, err
		}
		if rows == 0 {
			return 0, 0, 0, nil
		}
		if err := s.repo.InsertNotifyLog(ctx, &paymodel.NotifyLog{
			TaskID:      task.ID,
			NotifyTimes: task.NotifyTimes,
			Response:    "服务端回调密钥未配置，拒发（fail-closed）",
			Status:      paymodel.NotifyTaskStatusRequestFail,
		}); err != nil {
			return 0, 0, 0, err
		}
		return 0, 0, 1, nil
	}

	raw, err := s.buildNotifyPayload(ctx, task)
	if err != nil {
		return 0, 0, 0, err
	}

	attempt := task.NotifyTimes + 1
	next := now.Add(time.Duration(notifyNextIntervalSeconds(attempt)) * time.Second)
	rows, err := s.repo.ClaimNotifyTask(ctx, tenantID, task.ID, now, next, now)
	if err != nil {
		return 0, 0, 0, err
	}
	if rows == 0 {
		// 已被并发方认领（同任务双触发只执行一次）。
		return 0, 0, 0, nil
	}

	status, summary := s.sendNotify(ctx, task.NotifyURL, raw)
	// 终态裁决：成功 → 10；第 max 次失败 → 20 彻底失败；其余 21/22 继续重试。
	final := status
	if final != paymodel.NotifyTaskStatusSuccess && attempt >= task.MaxNotifyTimes {
		final = paymodel.NotifyTaskStatusFail
	}
	if err := s.repo.UpdateNotifyTaskResult(ctx, tenantID, task.ID, final, now); err != nil {
		return 1, 0, 0, err
	}
	if err := s.repo.InsertNotifyLog(ctx, &paymodel.NotifyLog{
		TaskID:      task.ID,
		NotifyTimes: attempt,
		Response:    summary,
		Status:      status,
	}); err != nil {
		return 1, 0, 0, err
	}
	if final == paymodel.NotifyTaskStatusSuccess {
		return 1, 1, 0, nil
	}
	return 1, 0, 0, nil
}

// sendNotify POST 报文到商户回调地址并按 spec §4.5 分类结果：
// header 带 Pay-Signature = HMAC-SHA256(rawBody, 服务端回调密钥)。
func (s *Service) sendNotify(ctx context.Context, notifyURL string, raw []byte) (int8, string) {
	statusCode, body, err := s.notifyHTTPClient.postJSON(ctx, notifyURL, raw, map[string]string{
		"Pay-Signature": notifySignature(raw, s.notifyHMACKey),
	})
	return classifyNotifyResult(statusCode, body, err)
}

// buildNotifyPayload 构造回调报文：任务基础字段 + 类型扩展详情
// （type=1 内置支付订单详情；#135/#136 经 SetNotifyPayloadEnricher 追加）。
func (s *Service) buildNotifyPayload(ctx context.Context, task *paymodel.NotifyTask) ([]byte, error) {
	payload := &NotifyPayload{
		Type:               task.Type,
		Status:             "success",
		MerchantOrderID:    task.MerchantOrderID,
		MerchantRefundID:   task.MerchantRefundID,
		MerchantTransferID: task.MerchantTransferID,
		DataID:             task.DataID,
	}
	if enricher := s.payloadEnrichers[task.Type]; enricher != nil {
		if err := enricher(ctx, task, payload); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("编码回调报文: %w", err)
	}
	return raw, nil
}

// enrichPaymentPayload 是 type=1（支付）的内置报文详情：订单号/渠道单号/
// 金额/成功时间（demo 商户按 merchant_order_id 或 data_id 联动本地状态）。
func (s *Service) enrichPaymentPayload(ctx context.Context, task *paymodel.NotifyTask, payload *NotifyPayload) error {
	order, err := s.repo.FindOrderByID(ctx, task.DataID)
	if err != nil {
		return err
	}
	if order == nil {
		return fmt.Errorf("回调任务 %d 对应的支付订单 %d 不存在", task.ID, task.DataID)
	}
	payload.OrderNo = order.No
	payload.ChannelOrderNo = order.ChannelOrderNo
	text, err := order.Price.MarshalText()
	if err != nil {
		return fmt.Errorf("编码订单金额: %w", err)
	}
	payload.Price = string(text)
	if order.SuccessTime != nil {
		payload.SuccessTime = order.SuccessTime.Format(time.RFC3339)
	}
	return nil
}

// slogSkipTask 记录单任务执行失败（任务保持到期状态，下轮重试）。
func slogSkipTask(task *paymodel.NotifyTask, err error) {
	// 与 infra job 日志同风格：slog 结构化，带任务定位字段。
	slog.Warn("pay notify task skipped", "taskId", task.ID, "type", task.Type, "err", err)
}

// ---------- payOrderTimeoutClose（订单超时关闭 job） ----------

// OrderTimeoutCloseResult 汇总一轮超时关单的执行结果。
type OrderTimeoutCloseResult struct {
	Tenants int   // 扫过的租户数
	Closed  int64 // 关闭的过期待支付订单数
}

// RunOrderTimeoutClose 是订单超时关闭任务入口（系统级动作，逐租户执行）：
// 扫 status=0 且 expire_time < now → 条件 UPDATE 置 30 已关闭（已支付/已退款/
// 已关闭不受影响，spec §7）。
func (s *Service) RunOrderTimeoutClose(ctx context.Context) (OrderTimeoutCloseResult, error) {
	tenantIDs, err := s.listEnabledTenants(ctx)
	if err != nil {
		return OrderTimeoutCloseResult{}, err
	}
	result := OrderTimeoutCloseResult{Tenants: len(tenantIDs)}
	var errs []error
	for _, id := range tenantIDs {
		closed, err := s.repo.CloseExpiredOrders(tenant.WithTenantID(ctx, id), id, s.businessNow())
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 超时关单失败: %w", id, err))
			continue
		}
		result.Closed += closed
	}
	if len(errs) > 0 {
		return result, errs[0]
	}
	return result, nil
}

// ---------- demo 商户回调端点（公开路由的公共链路） ----------

// ProcessDemoNotify 是 demo 回调三端点（order/refund/transfer）的公共链路：
// 验签（Pay-Signature = HMAC-SHA256(rawBody, 服务端回调密钥)，伪造/缺失/密钥
// 未配置一律 ErrDemoSignatureInvalid）→ 解析报文 → 业务联动扩展点
// （#137 注册真实实现 HandleDemoNotify）。
//
// 返回 ErrDemoSignatureInvalid / ErrDemoPayloadInvalid 时 HTTP 层回 4xx；
// 联动扩展点的错误原样返回（HTTP 层回 200 + 非 0 业务码信封——回调任务转
// 21 继续重试，spec §4.5 响应语义）。
func (s *Service) ProcessDemoNotify(ctx context.Context, kind, signature string, rawBody []byte) (*NotifyPayload, error) {
	if !verifyNotifySignature(rawBody, signature, s.notifyHMACKey) {
		return nil, ErrDemoSignatureInvalid
	}
	var payload NotifyPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return nil, fmt.Errorf("%w：报文必须是 JSON 对象", ErrDemoPayloadInvalid)
	}
	if s.demoHandler != nil {
		if err := s.demoHandler(ctx, kind, &payload); err != nil {
			return &payload, err
		}
	}
	return &payload, nil
}

// ---------- 回调通知管理端（权限码 pay:notify-task:query，spec §5） ----------

// ListNotifyTasks 分页查询回调任务（管理端）：pay 管理页不设 owner 行级数据
// 权限（spec §4.11），租户内全量可见。
func (s *Service) ListNotifyTasks(ctx context.Context, req *dto.NotifyTaskPageReq) ([]paymodel.NotifyTask, int64, error) {
	if err := validateNotifyTaskType(req.Type); err != nil {
		return nil, 0, err
	}
	if err := validateNotifyTaskStatus(req.Status); err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListNotifyTasks(ctx, payrepo.NotifyTaskFilter{
		Type:            req.Type,
		Status:          req.Status,
		AppID:           req.AppID,
		MerchantOrderID: req.MerchantOrderID,
	}, page.Offset(), page.PageSize)
}

// GetNotifyTask 回调任务详情（含执行日志时间线，append-only id ASC）。
func (s *Service) GetNotifyTask(ctx context.Context, id int64) (*dto.NotifyTaskDetail, error) {
	task, err := s.repo.FindNotifyTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, xerr.NotFound("回调任务不存在")
	}
	logs, err := s.repo.ListNotifyLogsByTaskID(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	return &dto.NotifyTaskDetail{NotifyTask: task, Logs: logs}, nil
}

// validateNotifyTaskType 校验任务类型过滤枚举（1 支付 / 2 退款 / 3 转账）。
func validateNotifyTaskType(t *int8) error {
	if t == nil {
		return nil
	}
	switch *t {
	case paymodel.NotifyTypePayment, paymodel.NotifyTypeRefund, paymodel.NotifyTypeTransfer:
		return nil
	}
	return xerr.BadRequest(fmt.Sprintf("回调任务类型不合法：%d（1 支付 / 2 退款 / 3 转账）", *t))
}

// validateNotifyTaskStatus 校验任务状态过滤枚举（0/10/20/21/22，spec §3.3）。
func validateNotifyTaskStatus(status *int8) error {
	if status == nil {
		return nil
	}
	switch *status {
	case paymodel.NotifyTaskStatusWaiting, paymodel.NotifyTaskStatusSuccess,
		paymodel.NotifyTaskStatusFail, paymodel.NotifyTaskStatusResultFail,
		paymodel.NotifyTaskStatusRequestFail:
		return nil
	}
	return xerr.BadRequest(fmt.Sprintf("回调任务状态不合法：%d（0 等待 / 10 成功 / 20 彻底失败 / 21 请求成功但结果失败 / 22 请求失败）", *status))
}
