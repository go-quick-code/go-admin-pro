package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// wallet.go 是 wallet 渠道实现（issue #133，docs/pay-mvp-spec.md §4.1/§4.2/
// §4.3）：余额扣减渠道——Order 在发起支付的同一事务内经唯一入账入口扣减
// 下单用户余额（biz=3 负价入账），成功即支付成功（OrderResp.Status=10，
// 同步完成，无异步回调）。余额不足返回 409 语义错误，事务整体回滚——
// 无部分扣减（spec §4.2，集成测试断言流水不变）。
//
// 依赖倒置：channel 包不 import service，扣减后端由装配处注入
//（service 层实现 WalletPayer，从 ctx 取 payrepo.WithTx 绑定的事务）。

// walletConfig 是 wallet 渠道配置（pay_channel.config JSON 反序列化目标）：
// 钱包即本租户账本，无需外部凭证，config 为空对象 {}。
type walletConfig struct{}

// parseWalletConfig 解析 wallet 渠道配置：空串/空对象合法，其他键拒绝。
func parseWalletConfig(raw string) (walletConfig, error) {
	var cfg walletConfig
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return cfg, fmt.Errorf("wallet 渠道配置必须是 JSON 对象: %w", err)
	}
	if len(obj) > 0 {
		return cfg, errors.New("wallet 渠道无需配置，config 应为空对象 {}")
	}
	return cfg, nil
}

// WalletPayer 是 wallet 渠道的余额扣减后端（由 pay service 层实现、装配期注入）：
// 在 ctx 绑定的当前事务内（payrepo.WithTx）经唯一入账入口 ApplyWalletChange
// 扣减 req.ChannelUser 的钱包（biz=3，-req.Price，biz_id=req.BizID）。
// 余额不足返回 409 语义错误（调用方整体回滚）；实现方不做订单状态变更
// （认领/extension/建任务由发起方在扣减成功后统一走支付成功入账函数）。
type WalletPayer interface {
	PayByWallet(ctx context.Context, req OrderReq) error
}

// WalletClient 是钱包渠道（注册表主键 CodeWallet）。
type WalletClient struct {
	payer WalletPayer
}

// NewWalletClient 创建 wallet 渠道实现；configJSON 为 pay_channel.config 原文，
// payer 为余额扣减后端（service 层）。
func NewWalletClient(configJSON string, payer WalletPayer) (*WalletClient, error) {
	if _, err := parseWalletConfig(configJSON); err != nil {
		return nil, err
	}
	if payer == nil {
		return nil, errors.New("wallet 渠道缺少余额扣减后端")
	}
	return &WalletClient{payer: payer}, nil
}

// Code 渠道码。
func (c *WalletClient) Code() string { return CodeWallet }

// Order 统一下单：同事务扣减余额（唯一入账入口），成功即支付成功。
func (c *WalletClient) Order(ctx context.Context, req OrderReq) (OrderResp, error) {
	if err := c.payer.PayByWallet(ctx, req); err != nil {
		return OrderResp{}, err
	}
	return OrderResp{Status: OrderStatusSuccess, DisplayMode: DisplayModeNone}, nil
}

// ParseOrderNotify 钱包无异步回调（同步完成），不存在回调解析场景。
func (c *WalletClient) ParseOrderNotify(ctx context.Context, req NotifyReq) (OrderNotify, error) {
	return OrderNotify{}, errors.New("wallet 渠道为同步支付，无异步回调")
}

// Refund 发起退款（#134/#135 落地 wallet 回充，本票未实现）。
func (c *WalletClient) Refund(ctx context.Context, req RefundReq) (RefundResp, error) {
	return RefundResp{}, errors.New("wallet 渠道退款未实现（#134）")
}

// ParseRefundNotify 解析退款回调（#134/#135 落地）。
func (c *WalletClient) ParseRefundNotify(ctx context.Context, req NotifyReq) (RefundNotify, error) {
	return RefundNotify{}, errors.New("wallet 渠道退款回调未实现（#134）")
}
