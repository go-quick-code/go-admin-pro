package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// mock.go 是 mock 渠道实现（issue #133 支付 / #135 退款，
// docs/pay-mvp-spec.md §4.1/§4.2/§4.4）：本地演练渠道——Order 不动资金，返回
// displayMode=none、订单停在待支付；收银台展示「模拟回调」按钮，触发
// POST /pay/mock/notify 走与真实渠道回调完全相同的解析 → 入账 → 建任务链路
//（ParseOrderNotify 验「签」+ 解析）。退款（#135）：Refund 按 mock 渠道
// config 模拟成功/失败两分支（失败分支供测试，demo 退款默认走成功；
// spec §4.4），内部构造回调原文并经 ParseRefundNotify 解析——mock 退款路径
// 与真实渠道回调共用同一解析入口。
//
// mockConfig 是渠道配置多态结构（spec §4.1）：mock 渠道无需凭证，config 为
// 空对象 {}（演练参数在此扩展，如 refundResult）。

// mockConfig 是 mock 渠道配置（pay_channel.config JSON 反序列化目标）：
// refundResult 决定 Refund 模拟结果（success 成功 / failure 失败，缺省 success）。
type mockConfig struct {
	RefundResult string `json:"refundResult,omitempty"`
}

// parseMockConfig 解析 mock 渠道配置：空串/空对象/仅 refundResult 键合法
// （值 success/failure），其他键一律拒绝（配置打错字宁可拒绝也不静默忽略）。
// 未实现渠道的种子 config={} 直接可用（退款缺省走成功分支）。
func parseMockConfig(raw string) (mockConfig, error) {
	var cfg mockConfig
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return cfg, fmt.Errorf("mock 渠道配置必须是 JSON 对象: %w", err)
	}
	for key, value := range obj {
		if key != "refundResult" {
			return cfg, errors.New("mock 渠道仅支持 refundResult 配置键（success/failure）")
		}
		var result string
		if err := json.Unmarshal(value, &result); err != nil {
			return cfg, fmt.Errorf("mock 渠道 refundResult 必须是字符串: %w", err)
		}
		switch strings.TrimSpace(result) {
		case "success", "failure":
			cfg.RefundResult = strings.TrimSpace(result)
		default:
			return cfg, fmt.Errorf("mock 渠道 refundResult 不合法：%q（期望 success/failure）", result)
		}
	}
	return cfg, nil
}

// MockClient 是 mock 渠道（注册表主键 CodeMock）。
type MockClient struct {
	code         string
	refundResult string // Refund 模拟结果（success/failure，缺省 success）
}

// NewMockClient 创建 mock 渠道实现；configJSON 为 pay_channel.config 原文。
func NewMockClient(configJSON string) (*MockClient, error) {
	cfg, err := parseMockConfig(configJSON)
	if err != nil {
		return nil, err
	}
	result := cfg.RefundResult
	if result == "" {
		result = "success"
	}
	return &MockClient{refundResult: result}, nil
}

// Code 渠道码。
func (c *MockClient) Code() string { return CodeMock }

// Order 统一下单：mock 渠道不动资金，订单停在待支付等「模拟回调」触发
// （spec §4.2：displayMode=none）。
func (c *MockClient) Order(ctx context.Context, req OrderReq) (OrderResp, error) {
	return OrderResp{Status: OrderStatusPending, DisplayMode: DisplayModeNone}, nil
}

// mockNotifyBody 是 mock 回调原文结构（收银台模拟按钮构造，真实渠道回调的
// 原文形态由各渠道定义——mock 用最小 JSON 演练同一解析链路）。
type mockNotifyBody struct {
	OutTradeNo string `json:"outTradeNo"`
	Result     string `json:"result"` // success / failure
}

// ParseOrderNotify 验「签」并解析 mock 回调：演练真实渠道回调的解析入口
// （spec §4.2——模拟回调走完全相同的解析→入账→建任务链路）。result 仅接受
// success/failure 两值，其他一律解析失败（回调伪造 4xx 语义的 mock 对应）。
func (c *MockClient) ParseOrderNotify(ctx context.Context, req NotifyReq) (OrderNotify, error) {
	var body mockNotifyBody
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return OrderNotify{}, xerr.BadRequest("mock 回调报文不是合法 JSON：" + err.Error())
	}
	outTradeNo := strings.TrimSpace(body.OutTradeNo)
	if outTradeNo == "" {
		return OrderNotify{}, xerr.BadRequest("mock 回调缺少 outTradeNo")
	}
	switch strings.TrimSpace(body.Result) {
	case "success":
		return OrderNotify{
			Success:        true,
			ChannelOrderNo: "MOCK-" + outTradeNo,
			Price:          money.Money{}, // mock 不携带对账金额（金额以订单为准）
			Raw:            string(req.Body),
		}, nil
	case "failure":
		return OrderNotify{Success: false, ChannelOrderNo: "MOCK-" + outTradeNo, Raw: string(req.Body)}, nil
	default:
		return OrderNotify{}, xerr.BadRequest(fmt.Sprintf("mock 回调 result 不合法：%q（期望 success/failure）", body.Result))
	}
}

// mockRefundNotifyBody 是 mock 退款回调原文结构（Refund 内部构造——mock 退款
// 路径与真实渠道回调共用同一解析入口，原文形态由各渠道定义，mock 用最小
// JSON 演练）。
type mockRefundNotifyBody struct {
	OutRefundNo string `json:"outRefundNo"`
	Result      string `json:"result"` // success / failure
}

// ParseRefundNotify 验「签」并解析 mock 退款回调（与 ParseOrderNotify 同口径，
// spec §4.4）：result 仅接受 success/failure 两值，其他一律解析失败（回调
// 伪造 4xx 语义的 mock 对应）。
func (c *MockClient) ParseRefundNotify(ctx context.Context, req NotifyReq) (RefundNotify, error) {
	var body mockRefundNotifyBody
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return RefundNotify{}, xerr.BadRequest("mock 退款回调报文不是合法 JSON：" + err.Error())
	}
	outRefundNo := strings.TrimSpace(body.OutRefundNo)
	if outRefundNo == "" {
		return RefundNotify{}, xerr.BadRequest("mock 退款回调缺少 outRefundNo")
	}
	switch strings.TrimSpace(body.Result) {
	case "success":
		return RefundNotify{
			Success:         true,
			ChannelRefundNo: "MOCK-" + outRefundNo,
			Raw:             string(req.Body),
		}, nil
	case "failure":
		return RefundNotify{Success: false, ChannelRefundNo: "MOCK-" + outRefundNo, Raw: string(req.Body)}, nil
	default:
		return RefundNotify{}, xerr.BadRequest(fmt.Sprintf("mock 退款回调 result 不合法：%q（期望 success/failure）", body.Result))
	}
}

// Refund 发起退款（issue #135，spec §4.4）：mock 渠道不动资金，按渠道配置
// refundResult 模拟成功/失败两分支（缺省 success——demo 退款默认走成功分支；
// failure 失败分支供测试）。内部构造回调原文并经 ParseRefundNotify 解析后
// 返回（成功带渠道回执 MOCK-<TK 单号>，失败带回调原文落错误分支）。
func (c *MockClient) Refund(ctx context.Context, req RefundReq) (RefundResp, error) {
	raw, err := json.Marshal(mockRefundNotifyBody{OutRefundNo: req.OutRefundNo, Result: c.refundResult})
	if err != nil {
		return RefundResp{}, err
	}
	notify, err := c.ParseRefundNotify(ctx, NotifyReq{Body: raw})
	if err != nil {
		return RefundResp{}, err
	}
	if !notify.Success {
		return RefundResp{Status: RefundStatusFail, ChannelRefundNo: notify.ChannelRefundNo, Raw: notify.Raw}, nil
	}
	return RefundResp{Status: RefundStatusSuccess, ChannelRefundNo: notify.ChannelRefundNo, Raw: notify.Raw}, nil
}

// mockTransferBody 是 mock 转账的模拟回执原文（转账无 Parse*Notify 动作——
// v1 同步完成，原文仅作 channel_notify_data 落库演练）。
type mockTransferBody struct {
	OutTransferNo string `json:"outTransferNo"`
	Result        string `json:"result"` // 恒 success（spec §4.8 模拟成功）
}

// Transfer 发起转账（issue #137，spec §4.8）：mock 渠道不动资金，模拟成功
// （spec §4.8 仅「模拟成功」，无失败分支——与 refund 的 refundResult 两分支
// 不同：转账失败分支无承接场景，演示链路里转账失败即发起异常，保持最小实现；
// 决策记录见包内 mock.go 头注释与 TransferDecision）。渠道回执
// MOCK-<ZZ 单号>，原文落 pay_transfer.channel_notify_data。
func (c *MockClient) Transfer(ctx context.Context, req TransferReq) (TransferResp, error) {
	raw, err := json.Marshal(mockTransferBody{OutTransferNo: req.OutTransferNo, Result: "success"})
	if err != nil {
		return TransferResp{}, err
	}
	return TransferResp{
		Status:            TransferStatusSuccess,
		ChannelTransferNo: "MOCK-" + req.OutTransferNo,
		Raw:               string(raw),
	}, nil
}
