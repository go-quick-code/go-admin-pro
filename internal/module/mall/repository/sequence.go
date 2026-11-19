package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/sequence"
)

// sequence.go 是 mall 单号序列发放（docs/mall-mvp-spec.md §2/§3.3，issue #179）：
// mall_sequence 上的薄包装，机制与并发说明见 internal/pkg/sequence（ADR 0022，
// 机制复制 pay_sequence/erp_sequence，全库唯一实现 internal/pkg/sequence.Next）。

// NextSequenceValue 在 (tenantID, bizType, dateKey) 上原子发放下一个序号；
// 机制与并发说明见 internal/pkg/sequence（唯一实现，本方法只是表名固定的薄
// 包装）。必须在调用方业务事务内调用（发放与订单落库同事务，见 service 层）。
func (r *Repository) NextSequenceValue(ctx context.Context, tenantID int64, bizType, dateKey string) (int64, error) {
	return sequence.Next(ctx, r.db, "mall_sequence", tenantID, bizType, dateKey)
}
