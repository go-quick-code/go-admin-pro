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

// mock.go 是 mock 渠道实现（issue #133，docs/pay-mvp-spec.md §4.1/§4.2）：
// 本地演练渠道——Order 不动资金，返回 displayMode=none、订单停在待支付；
// 收银台展示「模拟回调」按钮，触发 POST /pay/mock/notify 走与真实渠道回调
// 完全相同的解析 → 入账 → 建任务链路（ParseOrderNotify 验「签」+ 解析）。
//
// mockConfig 是渠道配置多态结构（spec §4.1）：mock 渠道无需凭证，config 为
// 空对象 {}（占位结构，后续演练参数如「自动失败率」可在此扩展）。

// mockConfig 是 mock 渠道配置（pay_channel.config JSON 反序列化目标）。
type mockConfig struct{}

// parseMockConfig 解析 mock 渠道配置：空串/空对象合法，其他键一律拒绝
// （配置打错字宁可拒绝也不静默忽略）。未实现渠道的种子 config={} 直接可用。
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
	if len(obj) > 0 {
		return cfg, errors.New("mock 渠道无需配置，config 应为空对象 {}")
	}
	return cfg, nil
}

// MockClient 是 mock 渠道（注册表主键 CodeMock）。
type MockClient struct {
	code string
}

// NewMockClient 创建 mock 渠道实现；configJSON 为 pay_channel.config 原文。
func NewMockClient(configJSON string) (*MockClient, error) {
	if _, err := parseMockConfig(configJSON); err != nil {
		return nil, err
	}
	return &MockClient{}, nil
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

// Refund 发起退款（#135 落地 wallet/mock 退款分支，本票未实现）。
func (c *MockClient) Refund(ctx context.Context, req RefundReq) (RefundResp, error) {
	return RefundResp{}, errors.New("mock 渠道退款未实现（#135）")
}

// ParseRefundNotify 解析退款回调（#135 落地）。
func (c *MockClient) ParseRefundNotify(ctx context.Context, req NotifyReq) (RefundNotify, error) {
	return RefundNotify{}, errors.New("mock 渠道退款回调未实现（#135）")
}
