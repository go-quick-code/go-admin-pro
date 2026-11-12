// Package channel 是 pay 的渠道 SPI 与注册表（docs/pay-mvp-spec.md §4.1），
// 概念等价芋道 PayClient：统一下单、支付回调解析、退款、退款回调解析四个
// 动作 + 转账（#137 以 Transfer 发起动作增补，见 TransferDecision）。
//
// v1 实现 mock 与 wallet 两个渠道（#133 定形，#135/#137 补退款/转账）；
// 14 个渠道码常量全部保留为概念（对齐芋道枚举），微信/支付宝实现待商户
// 资源另立工单（ADR 0021）。补渠道实现不改本 SPI：实现 Client 后在装配处
// Register 即可。
//
// 「渠道未实现」（#133 生效）：注册表中无 Client 的渠道（wx_*/alipay_*）
// 在应用绑定渠道与 submit 处一律 409——渠道码常量仍是唯一合法码源
// （IsKnownCode），实现与否由 Registry.Known 判定，两查相与。
package channel

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 渠道码常量（对齐芋道 pay 渠道枚举 14 个，spec §3.1/§4.1）。
const (
	CodeWxPub      = "wx_pub"      // 微信公众号
	CodeWxLite     = "wx_lite"     // 微信小程序
	CodeWxApp      = "wx_app"      // 微信 App
	CodeWxNative   = "wx_native"   // 微信扫码
	CodeWxWap      = "wx_wap"      // 微信 H5
	CodeWxBar      = "wx_bar"      // 微信条码
	CodeAliPayPC   = "alipay_pc"   // 支付宝 PC 网站支付
	CodeAliPayWap  = "alipay_wap"  // 支付宝手机网站
	CodeAliPayApp  = "alipay_app"  // 支付宝 App
	CodeAliPayQR   = "alipay_qr"   // 支付宝扫码
	CodeAliPayBar  = "alipay_bar"  // 支付宝条码
	CodeAliPayLite = "alipay_lite" // 支付宝小程序
	CodeMock       = "mock"        // 模拟渠道（本地演练回调全链路）
	CodeWallet     = "wallet"      // 钱包渠道（余额扣减/回加）
)

// codeNames 是渠道码 → 中文名称（种子与前端展示用；渠道行由种子预插，
// 平台不可新建，故名称表常驻编译期常量）。
var codeNames = map[string]string{
	CodeWxPub:      "微信公众号",
	CodeWxLite:     "微信小程序",
	CodeWxApp:      "微信 App",
	CodeWxNative:   "微信扫码",
	CodeWxWap:      "微信 H5",
	CodeWxBar:      "微信条码",
	CodeAliPayPC:   "支付宝 PC 网站支付",
	CodeAliPayWap:  "支付宝手机网站",
	CodeAliPayApp:  "支付宝 App",
	CodeAliPayQR:   "支付宝扫码",
	CodeAliPayBar:  "支付宝条码",
	CodeAliPayLite: "支付宝小程序",
	CodeMock:       "模拟渠道",
	CodeWallet:     "钱包渠道",
}

// Name 返回渠道码的中文名称；未知码返回空串（绑定校验前置已拦截未知码）。
func Name(code string) string { return codeNames[code] }

// AllCodes 返回全部 14 个渠道码（固定顺序，种子与测试用）。
func AllCodes() []string {
	return []string{
		CodeWxPub, CodeWxLite, CodeWxApp, CodeWxNative, CodeWxWap, CodeWxBar,
		CodeAliPayPC, CodeAliPayWap, CodeAliPayApp, CodeAliPayQR, CodeAliPayBar, CodeAliPayLite,
		CodeMock, CodeWallet,
	}
}

// IsKnownCode 报告 code 是否为 14 个渠道码常量之一。
func IsKnownCode(code string) bool {
	_, ok := codeNames[code]
	return ok
}

// DisplayMode 是统一下单的展示模式（spec §4.1）：none 表示等待渠道异步回调
// （mock），qrcode/url 预留给扫码/H5 类渠道实现。
const (
	DisplayModeNone   = "none"
	DisplayModeQRCode = "qrcode"
	DisplayModeURL    = "url"
)

// OrderResp.Status 取值（字符串形态对齐芋道；wallet 同步成功用 10）。
const (
	OrderStatusPending = "0"  // 处理中（订单停在待支付，等回调）
	OrderStatusSuccess = "10" // 成功（wallet 同步扣减成功；mock 不返回）
)

// OrderReq 统一下单请求（金额一律 money.Money 分定点，spec §2，由消费方服务端定）。
type OrderReq struct {
	OutTradeNo  string // 业务支付单号（ZF 前缀）
	Subject     string
	Body        string
	Price       money.Money // 实付金额（服务端定，客户端不可覆盖业务价）
	BizID       int64       // 业务单据 ID（pay_order.id；wallet 渠道流水 biz_id 用）
	NotifyURL   string      // 渠道回调地址（平台级公开端点）
	ReturnURL   string      // 支付后跳转地址（预留）
	ExpireTime  string      // 订单过期时间（RFC3339，预留）
	ChannelUser string      // 渠道用户标识（wallet 渠道为下单用户 ID；条码类预留）
}

// OrderResp 统一下单响应：displayMode=none 等回调，qrcode/url 携带 content。
type OrderResp struct {
	Status      string // 0 处理中 / 10 成功（wallet 同步成功用 10）
	DisplayMode string // none / qrcode / url
	Content     string // qrcode 内容或跳转 URL（预留）
}

// OrderNotify 渠道支付回调解析结果（验签 + 解析）。
type OrderNotify struct {
	Success        bool
	ChannelOrderNo string
	ChannelUserID  string
	Price          money.Money // 回调金额（渠道对账用）
	Raw            string      // 回调原文（落 extension.channel_notify_data）
}

// RefundResp.Status 取值（字符串形态对齐芋道；wallet/mock 同步完成用 10/20，
// 异步渠道（微信/支付宝）pending 等退款回调）。
const (
	RefundStatusPending = "0"  // 处理中（退款单停 0 待处理，等渠道回调）
	RefundStatusSuccess = "10" // 成功（wallet/mock 同步退款成功）
	RefundStatusFail    = "20" // 失败（mock 失败分支供测试，spec §4.4）
)

// RefundReq 退款请求（原路退回；金额一律退款单快照，渠道请求不含任何客户端
// 输入金额）。
type RefundReq struct {
	OutRefundNo    string // 业务退款单号（TK 前缀）
	ChannelOrderNo string
	RefundPrice    money.Money // 本次退款金额
	Reason         string
	BizID          int64 // 业务退款单据 ID（pay_refund.id；wallet 渠道回加定位原下单用户与流水 biz_id 用）
}

// RefundResp 退款响应：Raw 为渠道回调原文/末次响应摘要（落
// pay_refund.channel_notify_data；同步渠道的 mock 携带模拟回调原文）。
type RefundResp struct {
	Status          string // 0 处理中 / 10 成功 / 20 失败
	ChannelRefundNo string
	Raw             string
}

// RefundNotify 渠道退款回调解析结果（验签 + 解析）。
type RefundNotify struct {
	Success         bool
	ChannelRefundNo string
	Raw             string
}

// NotifyReq 渠道回调原文（渠道回调端点解析用；具体结构随渠道实现定形）。
type NotifyReq struct {
	Body []byte
}

// TransferResp.Status 取值（字符串形态对齐芋道；v1 两渠道同步完成用 10，
// 异步渠道（微信/支付宝商家转账）pending 等转账回调）。
const (
	TransferStatusPending = "0"  // 处理中（转账单停 0 等待，等渠道回调）
	TransferStatusSuccess = "10" // 成功（wallet/mock 同步转账成功）
	TransferStatusFail    = "20" // 失败（v1 无失败分支：mock 模拟成功，wallet 入账失败即发起回滚）
)

// TransferReq 转账请求（issue #137，spec §4.8；金额一律转账单快照，渠道请求
// 不含任何客户端输入金额）。UserAccount 语义随渠道：wallet 渠道 = 目标用户
// 标识（user_id 字符串）；未来真实渠道 = 收款账号（open_id/登录账号等）。
type TransferReq struct {
	OutTransferNo string // 业务转账单号（ZZ 前缀）
	Subject       string
	Price         money.Money // 转账金额（服务端定）
	UserAccount   string      // 收款账号（wallet 渠道为目标用户标识）
	UserName      string      // 收款人姓名（预留，真实渠道实名校验用）
	BizID         int64       // 业务单据 ID（pay_transfer.id；wallet 渠道流水 biz_id 用）
}

// TransferResp 转账响应：Raw 为渠道回调原文/末次响应摘要（落
// pay_transfer.channel_notify_data）。ChannelTransferNo 为渠道转账回执
// （wallet 渠道 = 目标钱包流水 LS 单号——资金在本租户账本内移动，流水即回执）。
type TransferResp struct {
	Status            string // 0 处理中 / 10 成功 / 20 失败
	ChannelTransferNo string
	Raw               string
}

// Client 是渠道 SPI（概念等价芋道 PayClient 四动作 + 渠道码自述；#137 以
// Transfer 动作扩为五动作，见 TransferDecision 注释）。实现为各渠道的多态
// 结构（walletConfig{}、mockConfig{}），配置 JSON 存 pay_channel.config，
// 由渠道实现自行反序列化。
type Client interface {
	// Code 渠道码（14 常量之一）；注册表以此为主键。
	Code() string
	// Order 统一下单，返回展示模式（none 等回调 / qrcode|url 预留）。
	Order(ctx context.Context, req OrderReq) (OrderResp, error)
	// ParseOrderNotify 验签并解析支付回调，产出成功/失败与渠道单号。
	ParseOrderNotify(ctx context.Context, req NotifyReq) (OrderNotify, error)
	// Refund 发起退款（渠道原路退回）。
	Refund(ctx context.Context, req RefundReq) (RefundResp, error)
	// ParseRefundNotify 验签并解析退款回调。
	ParseRefundNotify(ctx context.Context, req NotifyReq) (RefundNotify, error)
	// Transfer 发起转账（issue #137，spec §4.8）：同步完成（wallet/mock）时
	// Status=10；异步渠道 pending 等回调（回调解析动作随真实渠道实现追加，
	// v1 两渠道无异步转账回调）。
	Transfer(ctx context.Context, req TransferReq) (TransferResp, error)
}

// TransferDecision 记录 SPI 扩展决策（issue #137）：spec §4.1 将 SPI 定形为
// 芋道 PayClient 四动作（Order/ParseOrderNotify/Refund/ParseRefundNotify），
// 未覆盖转账（§4.8 的渠道动作在 #137 落地时 SPI 尚无承接位）。本票按「四动作
// 的成对形态」（发起 + 回调解析）增补 Transfer 发起动作——与 Order/Refund 同构
// 的「消费方服务端发起」入口；ParseTransferNotify 不增补：v1 两渠道转账均
// 同步完成（wallet 入账即终态、mock 模拟成功），异步转账回调随真实渠道实现
// 另立工单再定形。增补为接口新方法（v1 仅 mock/wallet 两实现，同票内补齐，
// 不破坏「补渠道实现不改 SPI」——后续渠道实现 Transfer 与既有动作一并实现）。
const TransferDecision = "SPI Transfer 动作增补（#137）：四动作成对形态 + Transfer 发起；ParseTransferNotify 留给异步渠道工单"

// Registry 是渠道实现注册表：map[渠道码]Client。v1 在 #132 注册 mock 与
// wallet；未注册（未实现）的渠道在绑定与 submit 处 409「渠道未实现」
// （#133 生效，见包注释）。
type Registry struct {
	clients map[string]Client
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{clients: make(map[string]Client)}
}

// Register 注册一个渠道实现（按 Code() 覆盖，装配期或测试使用）。
func (r *Registry) Register(c Client) {
	if r.clients == nil {
		r.clients = make(map[string]Client)
	}
	r.clients[c.Code()] = c
}

// Get 按渠道码取实现；未注册返回 nil（渠道未实现）。
func (r *Registry) Get(code string) Client {
	return r.clients[code]
}

// Known 报告渠道是否已实现（注册表中有 Client）。
func (r *Registry) Known(code string) bool {
	return r.clients[code] != nil
}
