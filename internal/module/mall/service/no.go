package service

import (
	"context"
	"fmt"
	"time"

	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
)

// no.go 是 mall 订单号发放（docs/mall-mvp-spec.md §2/§3.3，issue #179）：
// 单号 = 前缀 + yyyyMMdd（业务时区）+ 至少 4 位序号（超 9999 扩位），
// mall_sequence 事务内发放（internal/pkg/sequence 单源，ADR 0022，照
// pay/service/no.go 形状）；客户端提交的 no 一律不采纳。
// 前缀：订单 SO（spec §2「编号：biz_type=订单编号，前缀 SO」）；biz_type
// 与业务表名一致（pay/service/no.go 先例）。

// 序列 biz_type（与业务表名一致）与单号前缀。
const (
	// SeqOrder 是订单序号的 biz_type（mall_sequence 计数行的业务类型键）。
	SeqOrder = "trade_order"

	// NoPrefixOrder 是订单号前缀（spec §2：前缀 SO）。
	NoPrefixOrder = "SO"
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

// nextOrderNoInTx 在传入事务的 mall_sequence 上发放下一个订单号（spec §3.3
// 的统一形状：dateKey(businessNow) → tx.NextSequenceValue → formatNo("SO",
// dateKey, seq)）：日期键在事务内取一次口径，避免跨日翻转错位；序号发放与
// 订单落库同事务（tx 为 mallrepo.Transaction 绑定事务的 Repository）。
// 锁序约定照 internal/pkg/sequence 包注释：先取序列行锁、再写业务行。
func (s *Service) nextOrderNoInTx(ctx context.Context, tx *mallrepo.Repository, tenantID int64) (string, error) {
	key := dateKey(s.businessNow())
	seq, err := tx.NextSequenceValue(ctx, tenantID, SeqOrder, key)
	if err != nil {
		return "", err
	}
	return formatNo(NoPrefixOrder, key, seq), nil
}
