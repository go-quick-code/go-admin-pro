package money

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// MulQty 返回单价 × 数量的行金额（明细金额 = 单价(Money) × 数量(qty)，
// ADR 0017：ERP 单据明细不变量，128 位中间量防 int64 溢出）。
//
// 分 × 毫的精确乘积经无符号 128 位中间量计算，除以 1000（毫 → 分）时按
// 绝对值四舍五入到分（余数 ≥ 500 进一）；结果须落在 decimal(12,2) 范围内，
// 超出返回 ErrOutOfRange。数量的正负语义由调用方决定（负数量出现在
// ERP 退货/反向流水场景），符号按代数规则处理。
func (m Money) MulQty(q qty.Qty) (Money, error) {
	hi, lo := bits.Mul64(uabs(m.cents), uabs(q.Milli()))
	// 128 位无符号积 ÷ 1000：1000 < 2³²，按 32 位字做长除法（教科书算法），
	// 余数 < 1000 供四舍五入。
	qhi, qlo, rem := divRem1000(hi, lo)
	if rem >= 500 {
		// 128 位加一：qlo 进位到 qhi。
		qlo++
		if qlo == 0 {
			qhi++
		}
	}
	// 结果超出 int64（必然同时超出 decimal(12,2)）或超出金额域时拒绝。
	if qhi != 0 || qlo > math.MaxInt64 {
		return Money{}, fmt.Errorf("%w: 单价 × 数量结果超出范围", ErrOutOfRange)
	}
	product := int64(qlo)
	if (m.cents < 0) != (q.Milli() < 0) {
		product = -product
	}
	return checked(product)
}

// divRem1000 计算 128 位无符号数 (hi,lo) ÷ 1000 的商与余数。
// 1000 < 2³²，按 4 个 32 位字从高位到低位长除：每步 cur = rem<<32 | word
// 不会溢出 uint64（rem < 1000）。
func divRem1000(hi, lo uint64) (qhi, qlo, rem uint64) {
	words := [4]uint32{uint32(lo), uint32(lo >> 32), uint32(hi), uint32(hi >> 32)}
	var quo [4]uint32
	rem = 0
	for i := 3; i >= 0; i-- {
		cur := rem<<32 | uint64(words[i])
		quo[i] = uint32(cur / 1000)
		rem = cur % 1000
	}
	return uint64(quo[3])<<32 | uint64(quo[2]), uint64(quo[1])<<32 | uint64(quo[0]), rem
}
