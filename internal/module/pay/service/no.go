package service

import (
	"context"
	"fmt"
	"time"
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

// NextOrderNo 发放支付订单号（ZF 前缀）。
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
	key := dateKey(s.businessNow())
	seq, err := s.repo.NextSequenceValue(ctx, payTenantID(ctx), bizType, key)
	if err != nil {
		return "", err
	}
	return formatNo(prefix, key, seq), nil
}
