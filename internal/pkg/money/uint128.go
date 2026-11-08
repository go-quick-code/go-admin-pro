package money

import (
	"math"
	"math/bits"
)

// uint128.go 是金额乘法共用的 128 位无符号原语（内部 helper）：Money.Mul 与
// Money.MulQty 都以「绝对值的 128 位乘积 + 符号」为中间量，避免 int64 溢出；
// 重采样路径（毫 → 分）共用 add128One / uint128ToInt64 的溢出裁决。
//
// 约定：乘积一律按绝对值计算、符号最后还原；结果量级超过 MaxInt64 时拒绝
//（decimal(12,2) 上限远小于 int64 极值，无需为 MinInt64 保留负向特例）。

// mulAbs128 返回 |a| × |b| 的无符号 128 位乘积（hi, lo）与代数符号。
func mulAbs128(a, b int64) (hi, lo uint64, negative bool) {
	hi, lo = bits.Mul64(uabs(a), uabs(b))
	return hi, lo, (a < 0) != (b < 0)
}

// add128One 返回 128 位无符号数 (hi, lo) + 1（qlo 进位到 qhi）。
func add128One(hi, lo uint64) (uint64, uint64) {
	lo++
	if lo == 0 {
		hi++
	}
	return hi, lo
}

// uint128ToInt64 把无符号 128 位量级与符号还原为 int64；量级超过 MaxInt64
// （hi != 0 或 lo > MaxInt64）返回 ok=false（视为越界，沿用既有乘法口径）。
func uint128ToInt64(hi, lo uint64, negative bool) (int64, bool) {
	if hi != 0 || lo > math.MaxInt64 {
		return 0, false
	}
	product := int64(lo)
	if negative {
		product = -product
	}
	return product, true
}
