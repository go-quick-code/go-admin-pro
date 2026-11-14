package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/sequence"
)

// sequence.go 是 ERP 单号序列发放（issue #90，docs/erp-mvp-spec.md §3.7/§4.8）：
// erp_sequence 上的薄包装，机制与并发说明见 internal/pkg/sequence（ADR 0022）。

// NextSequenceValue 在 (tenantID, bizType, dateKey) 上原子发放下一个序号；机制与
// 并发说明见 internal/pkg/sequence（唯一实现，本方法只是表名固定的薄包装）。
func (r *Repository) NextSequenceValue(ctx context.Context, tenantID int64, bizType, dateKey string) (int64, error) {
	return sequence.Next(ctx, r.db, "erp_sequence", tenantID, bizType, dateKey)
}
