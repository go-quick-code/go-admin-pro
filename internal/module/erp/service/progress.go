package service

import "github.com/go-quick-code/go-admin-pro/internal/pkg/qty"

// progress.go 是采购/销售共用的订单进度分摊纯函数（issue #91/#92，spec §4.4/§4.5）：
// 审批/反审核时把「订单×产品」维度的已审核聚合量按订单明细 ID 序分摊到各行，
// 同产品跨多行时前面的行先填满，总量精确等于聚合值。采购的 in_count/return_count
// 与销售的 out_count/return_count 共用同一套分摊口径，避免同产品多行时把聚合量
// 写给每一行导致 Σ 进度虚高。
//
// 调用方必须传入按明细 ID 升序排列的 items（采购 FindPurchaseOrderItems /
// 销售 FindSaleOrderItemsByOrder 均以 id ASC 查询）；函数不修改输入切片以外
// 的状态，是纯函数（§8.1 次 seam：进度计算表驱动测试）。
func fillProgressByItemOrder[T any](
	items []T,
	productID func(T) int64,
	count func(T) qty.Qty,
	primarySums, secondarySums map[int64]qty.Qty,
	set func(index int, primary, secondary qty.Qty),
) {
	primaryLeft := remainingMilli(primarySums)
	secondaryLeft := remainingMilli(secondarySums)
	for i := range items {
		item := items[i]
		set(i, takeProgress(primaryLeft, productID(item), count(item)), takeProgress(secondaryLeft, productID(item), count(item)))
	}
}

// remainingMilli 把产品聚合量复制成可扣减的毫单位余量（避免修改调用方 map）。
func remainingMilli(sums map[int64]qty.Qty) map[int64]int64 {
	left := make(map[int64]int64, len(sums))
	for id, sum := range sums {
		left[id] = sum.Milli()
	}
	return left
}

// takeProgress 从产品余量中取本行可占用的数量：不超过行数量与剩余量，负数归零。
func takeProgress(left map[int64]int64, productID int64, want qty.Qty) qty.Qty {
	avail := left[productID]
	taken := want.Milli()
	if avail < taken {
		taken = avail
	}
	if taken < 0 {
		taken = 0
	}
	left[productID] = avail - taken
	return qty.FromMilli(taken)
}
