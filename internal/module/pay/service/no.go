package service

import (
	"context"
	"fmt"
	"time"

	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
)

// no.go 是单号发放（docs/pay-mvp-spec.md §4.9）：单号 = 前缀 + yyyyMMdd
//（业务时区）+ 至少 4 位序号（超 9999 扩位），pay_sequence 事务内发放，
// 客户端提交的 no 一律不采纳；merchant_* 单号由消费方提供并作幂等键。
// 前缀：支付订单 ZF、退款单 TK、转账单 ZZ、钱包流水 LS；biz_type 与表名一致。

// 序列 biz_type（与业务表名一致）与单号前缀。
const (
	SeqOrder             = "pay_order"
	SeqRefund            = "pay_refund"
	SeqTransfer          = "pay_transfer"
	SeqWalletTransaction = "pay_wallet_transaction"

	NoPrefixOrder             = "ZF"
	NoPrefixRefund            = "TK"
	NoPrefixTransfer          = "ZZ"
	NoPrefixWalletTransaction = "LS"
)

// dateKey 返回业务时区下的 yyyyMMdd 日期键（纯函数；跨日自然重置序号）。
func dateKey(now time.Time) string {
	return now.Format("20060102")
}

// formatNo 把前缀、日期键与序号格式化为单号（纯函数）：前缀 + yyyyMMdd +
// 至少 4 位序号，序号超过 9999 自然扩位（唯一性优先于固定位数，绝不截断）。
func formatNo(prefix, dateKey string, seq int64) string {
	return prefix + dateKey + fmt.Sprintf("%04d", seq)
}

// NextOrderNo / NextRefundNo / NextTransferNo / NextWalletTxNo 是集成测试专用
// 的发号入口（刻意保留的测试 seam——internal/app.App.Pay 暴露给
// pay_sequence 并发发放用例，见 app.go 对 Pay 字段的注释）：生产写路径一律
// 经 nextNoInTx 在业务事务内发号，不走这组自开事务的入口；非测试代码调用
// 即绕过了「发号与单据落库同事务」的 spec §4.9 保证（误用自证）。

// NextOrderNo 发放支付订单号（ZF 前缀）。仅集成测试使用（见上组注释）。
func (s *Service) NextOrderNo(ctx context.Context) (string, error) {
	return s.nextNo(ctx, SeqOrder, NoPrefixOrder)
}

// NextRefundNo 发放退款单号（TK 前缀）。
func (s *Service) NextRefundNo(ctx context.Context) (string, error) {
	return s.nextNo(ctx, SeqRefund, NoPrefixRefund)
}

// NextTransferNo 发放转账单号（ZZ 前缀）。
func (s *Service) NextTransferNo(ctx context.Context) (string, error) {
	return s.nextNo(ctx, SeqTransfer, NoPrefixTransfer)
}

// NextWalletTxNo 发放钱包流水号（LS 前缀）。
func (s *Service) NextWalletTxNo(ctx context.Context) (string, error) {
	return s.nextNo(ctx, SeqWalletTransaction, NoPrefixWalletTransaction)
}

// nextNo 在 pay_sequence 上原子发放下一个单号：序号发放与后续单据落库
// 同事务由后续票的写路径保证；本票先提供独立发放入口（自己开事务）。
func (s *Service) nextNo(ctx context.Context, bizType, prefix string) (string, error) {
	return s.nextNoInTx(ctx, s.repo, payTenantID(ctx), bizType, prefix)
}

// nextNoInTx 在传入事务的 pay_sequence 上发放下一个单号（spec §4.9 的统一
// 形状：dateKey(businessNow) → tx.NextSequenceValue → formatNo）：日期键在
// 事务内取一次口径，避免跨日翻转错位；序号发放与调用方的单据落库同事务
// （tx 为 payrepo.Transaction 绑定事务的 Repository 或 s.repo 本体）。写路径
// （订单/退款/转账/钱包流水/充值挂单/示例挂单）与事务外发放入口 nextNo 共用。
func (s *Service) nextNoInTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, bizType, prefix string) (string, error) {
	key := dateKey(s.businessNow())
	seq, err := tx.NextSequenceValue(ctx, tenantID, bizType, key)
	if err != nil {
		return "", err
	}
	return formatNo(prefix, key, seq), nil
}
