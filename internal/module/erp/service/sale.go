// sale.go 是销售三单的共享规则（issue #92，docs/erp-mvp-spec.md §3.4/§4.2/§4.5/
// §4.8）：编号前缀与序列 biz_type、占用校验（订单剩余可出 / 出库剩余可退）、
// 进度回写重算、往来单位校验。库存变动只经唯一入账入口 ApplyStockChange
// （#90 确立），本文件不直接碰 erp_stock。
package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 单号前缀（spec §4.8：销售订单 XS / 销售出库 CK / 销售退货 ST）。
const (
	SaleOrderNoPrefix  = "XS"
	SaleOutNoPrefix    = "CK"
	SaleReturnNoPrefix = "ST"
)

// erp_sequence 的 biz_type（spec §4.8：与单据表名一致）。
const (
	SeqBizTypeSaleOrder  = "sale_order"
	SeqBizTypeSaleOut    = "sale_out"
	SeqBizTypeSaleReturn = "sale_return"
)

// saleStockRecordBizNames 是销售链路流水的中文展示（追加进 #90 的
// stockRecordBizNames，spec §4.3 biz_type 全集随票扩展；init 合并避免改动
// #90 既有行——#91 采购以同样方式追加自己的名字）。
var saleStockRecordBizNames = map[string]string{
	model.StockRecordBizSaleOut:          "销售出库",
	model.StockRecordBizSaleOutCancel:    "销售出库（反审核）",
	model.StockRecordBizSaleReturn:       "销售退货",
	model.StockRecordBizSaleReturnCancel: "销售退货（反审核）",
}

func init() {
	for k, v := range saleStockRecordBizNames {
		stockRecordBizNames[k] = v
	}
}

// ---------- 占用校验（spec §4.5 镜像 §4.4：超量 409，未审核也占用） ----------

// countSumByProduct 把聚合行折叠成 产品→数量和 映射。
func countSumByProduct(rows []repository.ProductCountSum) map[int64]qty.Qty {
	out := make(map[int64]qty.Qty, len(rows))
	for _, row := range rows {
		out[row.ProductID] = row.Total
	}
	return out
}

// draftCountByProduct 把明细草稿按产品聚合（同一产品多行合并占用）。
func draftCountByProduct(drafts []docItemDraft) map[int64]qty.Qty {
	out := make(map[int64]qty.Qty, len(drafts))
	for _, d := range drafts {
		out[d.ProductID] = qty.FromMilli(out[d.ProductID].Milli() + d.Count.Milli())
	}
	return out
}

// itemCountByProduct 把单据明细按产品聚合（订单/出库行数量）。
func itemCountByProduct[T any](items []T, product func(T) int64, count func(T) qty.Qty) map[int64]qty.Qty {
	out := make(map[int64]qty.Qty, len(items))
	for _, it := range items {
		p := product(it)
		out[p] = qty.FromMilli(out[p].Milli() + count(it).Milli())
	}
	return out
}

// checkSaleOutOccupancy 校验出库草稿不超订单剩余可出（spec §4.5）：
// 剩余可出（按产品聚合）= 订单数量 −（未审核+已审核出库）−（未审核+已审核退货）。
// excludeOutID > 0 表示编辑自检（聚合时排除自己，不重复计占用）。
// 调用方必须已在同一事务内锁住订单行（FindSaleOrderByIDForUpdate），并发创建
// 的占用变更由此串行化。
func (s *Service) checkSaleOutOccupancy(ctx context.Context, tx *repository.Repository, order *model.SaleOrder, drafts []docItemDraft, excludeOutID int64) error {
	tenantID := erpTenantID(ctx)
	orderCounts, err := tx.FindSaleOrderItemsByOrder(ctx, order.ID)
	if err != nil {
		return err
	}
	need := itemCountByProduct(orderCounts,
		func(it model.SaleOrderItem) int64 { return it.ProductID },
		func(it model.SaleOrderItem) qty.Qty { return it.Count })
	outRows, err := mustSumSaleOutByOrder(ctx, tx, tenantID, order.ID, excludeOutID)
	if err != nil {
		return err
	}
	retRows, err := mustSumSaleReturnByOrder(ctx, tx, tenantID, order.ID)
	if err != nil {
		return err
	}
	return s.assertSaleRemaining(ctx, tx, need,
		countSumByProduct(outRows), countSumByProduct(retRows),
		draftCountByProduct(drafts), "可出")
}

// checkSaleReturnOccupancy 校验退货草稿不超出库剩余可退（spec §4.5/§8.2）：
// 剩余可退（按产品聚合）= 出库数量 −（未审核+已审核退货）。
// 调用方必须已在同一事务内锁住出库单行。
func (s *Service) checkSaleReturnOccupancy(ctx context.Context, tx *repository.Repository, out *model.SaleOut, drafts []docItemDraft, excludeReturnID int64) error {
	tenantID := erpTenantID(ctx)
	outItems, err := tx.FindSaleOutWithItems(ctx, out.ID)
	if err != nil {
		return err
	}
	need := itemCountByProduct(outItems.Items,
		func(it model.SaleOutItem) int64 { return it.ProductID },
		func(it model.SaleOutItem) qty.Qty { return it.Count })
	retRows, err := mustSumSaleReturnByOut(ctx, tx, tenantID, out.ID, excludeReturnID)
	if err != nil {
		return err
	}
	// 出库方向无扣减项：空映射表示剩余可退只被退货占用。
	return s.assertSaleRemaining(ctx, tx, need, map[int64]qty.Qty{},
		countSumByProduct(retRows), draftCountByProduct(drafts), "可退")
}

// assertSaleRemaining 通用占用裁决：对提交产品集合，检查 增量 ≤ 基数 − 已出占用
// − 已退占用，超量 409（文案带产品名/剩余量/提交量，spec §8.2 超订单数量 409）。
// 基数里没有该产品（订单不含此产品）也在这里以剩余 0 拦下。
func (s *Service) assertSaleRemaining(ctx context.Context, tx *repository.Repository, base, outOcc, retOcc, add map[int64]qty.Qty, label string) error {
	if len(add) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(add))
	for id := range add {
		ids = append(ids, id)
	}
	names, err := tx.FindProductNamesIn(ctx, ids)
	if err != nil {
		return err
	}
	for id, addQty := range add {
		remaining := base[id].Milli() - outOcc[id].Milli() - retOcc[id].Milli()
		if addQty.Milli() > remaining {
			name := names[id]
			if name == "" {
				name = fmt.Sprintf("产品 %d", id)
			}
			return xerr.Conflict(fmt.Sprintf("%s剩余%s %s，本次提交 %s，超出订单可用数量",
				name, label, qty.FromMilli(maxInt64(remaining, 0)), addQty))
		}
	}
	return nil
}

// recheckSaleOrderOccupancy 订单编辑自检（未审核可改，但占用不变量必须维持）：
// 新订单数量必须 ≥ 该订单现有的出库+退货占用（含未审核，spec §4.5 占用口径），
// 否则删减订单行会让既有单据超量。409。
func (s *Service) recheckSaleOrderOccupancy(ctx context.Context, tx *repository.Repository, order *model.SaleOrder, drafts []docItemDraft) error {
	tenantID := erpTenantID(ctx)
	newCounts := draftCountByProduct(drafts)
	outRows, err := mustSumSaleOutByOrder(ctx, tx, tenantID, order.ID, 0)
	if err != nil {
		return err
	}
	retRows, err := mustSumSaleReturnByOrder(ctx, tx, tenantID, order.ID)
	if err != nil {
		return err
	}
	outSum := countSumByProduct(outRows)
	retSum := countSumByProduct(retRows)
	// 联合产品域：新数量为 0 的产品若有占用同样违约（订单行被删光）。
	products := make(map[int64]struct{}, len(newCounts)+len(outSum)+len(retSum))
	for id := range newCounts {
		products[id] = struct{}{}
	}
	for id := range outSum {
		products[id] = struct{}{}
	}
	for id := range retSum {
		products[id] = struct{}{}
	}
	add := make(map[int64]qty.Qty, len(products))
	zero := qty.FromMilli(0)
	for id := range products {
		add[id] = zero // 增量为 0：只要求 基数 ≥ 占用
	}
	if err := s.assertSaleRemaining(ctx, tx, newCounts, outSum, retSum, add, "可出"); err != nil {
		return err
	}
	// assertSaleRemaining 比较 add ≤ base − occ；这里 add=0、base=新订单数量，
	// 语义即「新订单数量 ≥ 出库占用 + 退货占用」。
	return nil
}

// mustSumSaleOutByOrder / mustSumSaleReturnByOrder / mustSumSaleReturnByOut
// 是占用聚合的薄封装（占用查询失败直接中止事务，不吞错）。
func mustSumSaleOutByOrder(ctx context.Context, tx *repository.Repository, tenantID, orderID, excludeOutID int64) ([]repository.ProductCountSum, error) {
	return tx.SumSaleOutCountByOrder(ctx, tenantID, orderID, excludeOutID)
}

func mustSumSaleReturnByOrder(ctx context.Context, tx *repository.Repository, tenantID, orderID int64) ([]repository.ProductCountSum, error) {
	return tx.SumSaleReturnCountByOrder(ctx, tenantID, orderID)
}

func mustSumSaleReturnByOut(ctx context.Context, tx *repository.Repository, tenantID, outID, excludeReturnID int64) ([]repository.ProductCountSum, error) {
	return tx.SumSaleReturnCountByOut(ctx, tenantID, outID, excludeReturnID)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ---------- 进度回写（out_count/return_count 只计已审核，spec §4.5） ----------

// recomputeSaleOrderProgress 重算订单进度（审批/反审核出库/退货后调用，事务内）：
// 取已审核出库/退货的订单×产品聚合，按明细 ID 序填充到各明细并回写。重算式
// （非增量）保证审批与反审核幂等，不产生漂移。
func (s *Service) recomputeSaleOrderProgress(ctx context.Context, tx *repository.Repository, orderID int64) error {
	tenantID := erpTenantID(ctx)
	items, err := tx.FindSaleOrderItemsByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	outRows, err := tx.SumApprovedSaleOutCountByOrder(ctx, tenantID, orderID)
	if err != nil {
		return err
	}
	retRows, err := tx.SumApprovedSaleReturnCountByOrder(ctx, tenantID, orderID)
	if err != nil {
		return err
	}
	fillOrderProgress(items, countSumByProduct(outRows), countSumByProduct(retRows))
	for _, item := range items {
		if err := tx.UpdateSaleOrderItemProgress(ctx, item.ID, item.OutCount, item.ReturnCount); err != nil {
			return err
		}
	}
	return nil
}

// fillOrderProgress 把订单×产品的已审核聚合量依明细 ID 序填充到明细行（纯函数）：
// 同产品跨多行时按行序依次占用（前面的行先填满），总量精确等于聚合值。
// 调用方传入的 items 已按明细 ID 升序（FindSaleOrderItemsByOrder）；分摊口径
// 与采购共用 fillProgressByItemOrder（见 progress.go）。
func fillOrderProgress(items []model.SaleOrderItem, outSums, returnSums map[int64]qty.Qty) {
	fillProgressByItemOrder(items,
		func(it model.SaleOrderItem) int64 { return it.ProductID },
		func(it model.SaleOrderItem) qty.Qty { return it.Count },
		outSums, returnSums,
		func(i int, outCount, returnCount qty.Qty) {
			items[i].OutCount = outCount
			items[i].ReturnCount = returnCount
		})
}

// ---------- 往来单位 / 销售员校验 ----------

// checkSaleCustomer 校验 ERP 客户存在（租户内；跨租户/不存在一律 400，与
// 仓库/产品引用校验同口径，不泄露存在性）。
func (s *Service) checkSaleCustomer(ctx context.Context, customerID int64) error {
	if customerID <= 0 {
		return xerr.BadRequest("缺少客户")
	}
	customer, err := s.repo.FindCustomerByID(ctx, customerID)
	if err != nil {
		return err
	}
	if customer == nil {
		return xerr.BadRequest("客户不存在")
	}
	return nil
}

// checkSaleUser 校验销售员（纯记录字段，spec §3.4）：> 0 时用户必须存在。
func (s *Service) checkSaleUser(ctx context.Context, saleUserID int64) error {
	if saleUserID <= 0 {
		return nil
	}
	_, ok, err := s.sys.UserDeptID(ctx, saleUserID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.BadRequest(fmt.Sprintf("销售员不存在：用户 %d", saleUserID))
	}
	return nil
}

// sumDraftTotal 明细合计（主表 total_price 冗余列，服务端权威）。行金额已在
// buildDocItems 校验范围，合计越界只可能来自极端构造，如实报 400。
func sumDraftTotal(drafts []docItemDraft) (money.Money, error) {
	total := money.FromCents(0)
	for _, d := range drafts {
		next, err := total.Add(d.TotalPrice)
		if err != nil {
			return money.Money{}, xerr.BadRequest("明细合计金额超出范围")
		}
		total = next
	}
	return total, nil
}

// buildSaleOrderItemRows 组装订单明细行（进度列由重算写入，创建时恒 0）。
func buildSaleOrderItemRows(docID int64, drafts []docItemDraft) []model.SaleOrderItem {
	rows := make([]model.SaleOrderItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.SaleOrderItem{
			SaleOrderID:  docID,
			ProductID:    d.ProductID,
			Count:        d.Count,
			ProductPrice: d.ProductPrice,
			TotalPrice:   d.TotalPrice,
		})
	}
	return rows
}

func buildSaleOutItemRows(docID int64, drafts []docItemDraft) []model.SaleOutItem {
	rows := make([]model.SaleOutItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.SaleOutItem{
			SaleOutID:    docID,
			ProductID:    d.ProductID,
			Count:        d.Count,
			ProductPrice: d.ProductPrice,
			TotalPrice:   d.TotalPrice,
		})
	}
	return rows
}

func buildSaleReturnItemRows(docID int64, drafts []docItemDraft) []model.SaleReturnItem {
	rows := make([]model.SaleReturnItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.SaleReturnItem{
			SaleReturnID: docID,
			ProductID:    d.ProductID,
			Count:        d.Count,
			ProductPrice: d.ProductPrice,
			TotalPrice:   d.TotalPrice,
		})
	}
	return rows
}

// 编译期无锚点：销售明细载荷 SaleItemPayload 在 dto 包内是 StockDocItemPayload
// 的类型别名，明细校验复用 buildDocItems（数量 > 0、单价非负、行金额服务端算）。
