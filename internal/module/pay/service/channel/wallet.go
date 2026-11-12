package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// wallet.go 是 wallet 渠道实现（issue #133 支付 / #135 退款 / #137 转账，
// docs/pay-mvp-spec.md §4.1/§4.2/§4.3/§4.4/§4.8）：余额扣减渠道——Order 在
// 发起支付的同一事务内经唯一入账入口扣减下单用户余额（biz=3 负价入账），
// 成功即支付成功（OrderResp.Status=10，同步完成，无异步回调）。余额不足返回
// 409 语义错误，事务整体回滚——无部分扣减（spec §4.2，集成测试断言流水不变）。
// 退款（#135）：Refund 在发起退款的同一事务内回加原下单用户余额（biz=4 正价
// 入账），成功即退款成功——与支付同锁路径（唯一入账入口行锁），并发支付
// 扣减串行化（spec §4.4 回加并发安全）。转账（#137）：Transfer 在发起转账的
// 同一事务内入账目标用户钱包（biz=6 正价入账，目标钱包懒创建），成功即转账
// 成功，回执取目标钱包流水号。
//
// 依赖倒置：channel 包不 import service，扣减/回加后端由装配处注入
//（service 层实现 WalletPayer/WalletRefunder，从 ctx 取 payrepo.WithTx 绑定
// 的事务）。

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

// WalletRefunder 是 wallet 渠道的退款回加后端（由 pay service 层实现、装配期
// 注入）：在 ctx 绑定的当前事务内经唯一入账入口回加原下单用户钱包
// （biz=4，+req.RefundPrice，biz_id=req.BizID=pay_refund.id）。req.BizID 必须
// 是本事务内已落库的退款单（回加对象取退款单的 user_id/user_type 快照——
// 原路退回）。失败按入账函数语义返回（调用方整体回滚，无部分入账）；实现方
// 不做退款单/订单状态变更（认领/累计/建任务由发起方在回加成功后统一走退款
// 成功入账函数）。
type WalletRefunder interface {
	RefundByWallet(ctx context.Context, req RefundReq) error
}

// WalletTransferer 是 wallet 渠道的转账入账后端（issue #137，spec §4.8，由
// pay service 层实现、装配期注入）：在 ctx 绑定的当前事务内经唯一入账入口
// 入账 req.UserAccount 指向的目标用户钱包（biz=6 转账，正价入账，
// biz_id=req.BizID=pay_transfer.id；目标钱包懒创建，spec §4.3）。返回渠道
// 转账回执（目标钱包流水 LS 单号——资金在本租户账本内移动，流水即回执）。
// 失败按入账函数语义返回（调用方整体回滚，无部分入账）；实现方不做转账单
// 状态变更（认领/建任务由发起方在入账成功后统一走转账成功入账函数）。
type WalletTransferer interface {
	TransferByWallet(ctx context.Context, req TransferReq) (string, error)
}

// WalletClient 是钱包渠道（注册表主键 CodeWallet）。
type WalletClient struct {
	payer      WalletPayer
	refunder   WalletRefunder
	transferer WalletTransferer
}

// NewWalletClient 创建 wallet 渠道实现；configJSON 为 pay_channel.config 原文，
// payer 为余额扣减后端、refunder 为退款回加后端、transferer 为转账入账后端
// （均为 service 层；transferer 兼容旧调用方缺省？不——转账后端必填，三后端
// 同一 service 实例）。
func NewWalletClient(configJSON string, payer WalletPayer, refunder WalletRefunder, transferer WalletTransferer) (*WalletClient, error) {
	if _, err := parseWalletConfig(configJSON); err != nil {
		return nil, err
	}
	if payer == nil {
		return nil, errors.New("wallet 渠道缺少余额扣减后端")
	}
	if refunder == nil {
		return nil, errors.New("wallet 渠道缺少退款回加后端")
	}
	if transferer == nil {
		return nil, errors.New("wallet 渠道缺少转账入账后端")
	}
	return &WalletClient{payer: payer, refunder: refunder, transferer: transferer}, nil
}

// Code 渠道码。
func (c *WalletClient) Code() string { return CodeWallet }

// PublicNotifyCallback wallet 渠道不承接公开回调（PR #140 安全审查 P1）：
// 同步完成无异步回调（ParseOrderNotify 恒错误），公开端点对其无可承接的
// 合法报文——404 同答防探测。
func (c *WalletClient) PublicNotifyCallback() bool { return false }

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

// Refund 发起退款（issue #135，spec §4.3/§4.4）：同事务经唯一入账入口回加
// 原下单用户余额（biz=4 支付退款，正价入账，biz_id=退款单 ID）。成功即退款
// 成功（同步完成，无异步回调——ParseRefundNotify 恒错误）。金额一律退款单
// 快照，渠道请求不含任何客户端输入金额。
func (c *WalletClient) Refund(ctx context.Context, req RefundReq) (RefundResp, error) {
	if err := c.refunder.RefundByWallet(ctx, req); err != nil {
		return RefundResp{}, err
	}
	return RefundResp{Status: RefundStatusSuccess}, nil
}

// ParseRefundNotify 钱包为同步退款（本租户账本内回加即终态），无异步回调，
// 不存在退款回调解析场景。
func (c *WalletClient) ParseRefundNotify(ctx context.Context, req NotifyReq) (RefundNotify, error) {
	return RefundNotify{}, errors.New("wallet 渠道为同步退款，无异步回调")
}

// Transfer 发起转账（issue #137，spec §4.8）：同事务经唯一入账入口入账
// req.UserAccount 指向的目标用户钱包（biz=6 转账，正价入账，biz_id=转账单
// ID；目标钱包懒创建，spec §4.3）。成功即转账成功（同步完成，无异步回调）；
// 回执取目标钱包流水 LS 单号。金额一律转账单快照，渠道请求不含任何客户端
// 输入金额。
func (c *WalletClient) Transfer(ctx context.Context, req TransferReq) (TransferResp, error) {
	receipt, err := c.transferer.TransferByWallet(ctx, req)
	if err != nil {
		return TransferResp{}, err
	}
	return TransferResp{Status: TransferStatusSuccess, ChannelTransferNo: receipt}, nil
}
