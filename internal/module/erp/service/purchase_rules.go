// purchase_rules.go 是采购三单的共享规则（issue #91，docs/erp-mvp-spec.md
// §4.1/§4.2/§4.4/§4.8）：明细不变量复用 #90 的 buildDocItems，本文件补采购域
// 的编号前缀/序列与流水 biz_type、占用校验与进度聚合的纯函数（次 seam，
// spec §8.1：占用/剩余计算表驱动锁定，表驱动用例见 purchase_rules_test.go）。
package service

import (
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 单号前缀（spec §4.8：采购订单 CG / 采购入库 RK / 采购退货 RT）。
const (
	PurchaseOrderNoPrefix  = "CG"
	PurchaseInNoPrefix     = "RK"
	PurchaseReturnNoPrefix = "RT"
)

// erp_sequence 的 biz_type（spec §4.8：与单据表名一致）。
const (
	SeqBizTypePurchaseOrder  = "purchase_order"
	SeqBizTypePurchaseIn     = "purchase_in"
	SeqBizTypePurchaseReturn = "purchase_return"
)

// sumDocItemQuantities 明细草稿按产品聚合数量（同一产品多行合并校验）。
func sumDocItemQuantities(drafts []docItemDraft) (map[int64]qty.Qty, error) {
	need := make(map[int64]qty.Qty)
	for _, d := range drafts {
		sum, err := need[d.ProductID].Add(d.Count)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("产品 %d 数量累计超出范围", d.ProductID))
		}
		need[d.ProductID] = sum
	}
	return need, nil
}

// checkPurchaseInOccupancy 订单余量的占用校验（spec §4.4，纯函数）：
// 剩余可入 = Σ订单行数量 −（未审核+已审核入库数量）−（未审核+已审核退货数量）；
// 未审核单也占量，超量 409。occupiedIn/occupiedReturn 是「除本次写入外」既有
// 占用按产品聚合（未审核 + 已审核全集）。
func checkPurchaseInOccupancy(orderItems []model.PurchaseOrderItem, occupiedIn, occupiedReturn map[int64]qty.Qty, drafts []docItemDraft, productNames map[int64]string) error {
	quota := make(map[int64]qty.Qty)
	for _, item := range orderItems {
		sum, err := quota[item.ProductID].Add(item.Count)
		if err != nil {
			return xerr.BadRequest(fmt.Sprintf("产品 %d 订单数量累计超出范围", item.ProductID))
		}
		quota[item.ProductID] = sum
	}
	need, err := sumDocItemQuantities(drafts)
	if err != nil {
		return err
	}
	for productID, want := range need {
		remaining, err := quota[productID].Sub(occupiedIn[productID])
		if err == nil {
			remaining, err = remaining.Sub(occupiedReturn[productID])
		}
		if err != nil {
			return xerr.BadRequest(fmt.Sprintf("产品 %s 订单数量累计超出范围", productNameOf(productNames, productID)))
		}
		if want.Milli() > remaining.Milli() {
			return xerr.Conflict(fmt.Sprintf(
				"超出采购订单可入库数量：产品 %s 订单余量 %s（已入/在途 %s，已退/在途 %s），本次 %s",
				productNameOf(productNames, productID), remaining,
				occupiedIn[productID], occupiedReturn[productID], want))
		}
	}
	return nil
}

// checkPurchaseReturnOccupancy 入库单余量的退货占用校验（spec §4.4/§8.2，
// 纯函数）：剩余可退 = 入库行数量 −（未审核+已审核退货数量），超量 409；
// 退货产品不在入库单中时入库量为 0，同样超量拒绝。
func checkPurchaseReturnOccupancy(inItems []model.PurchaseInItem, occupiedReturn map[int64]qty.Qty, drafts []docItemDraft, productNames map[int64]string) error {
	quota := make(map[int64]qty.Qty)
	for _, item := range inItems {
		sum, err := quota[item.ProductID].Add(item.Count)
		if err != nil {
			return xerr.BadRequest(fmt.Sprintf("产品 %d 入库数量累计超出范围", item.ProductID))
		}
		quota[item.ProductID] = sum
	}
	need, err := sumDocItemQuantities(drafts)
	if err != nil {
		return err
	}
	for productID, want := range need {
		remaining, err := quota[productID].Sub(occupiedReturn[productID])
		if err != nil {
			return xerr.BadRequest(fmt.Sprintf("产品 %s 入库数量累计超出范围", productNameOf(productNames, productID)))
		}
		if want.Milli() > remaining.Milli() {
			return xerr.Conflict(fmt.Sprintf(
				"超出采购入库单可退货数量：产品 %s 入库余量 %s（已退/在途 %s），本次 %s",
				productNameOf(productNames, productID), remaining, occupiedReturn[productID], want))
		}
	}
	return nil
}

// productNameOf 409 文案的产品名回退（缺名称时展示 ID）。
func productNameOf(names map[int64]string, productID int64) string {
	if name := names[productID]; name != "" {
		return name
	}
	return fmt.Sprintf("产品 %d", productID)
}
