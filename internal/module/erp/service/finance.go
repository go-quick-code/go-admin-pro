// finance.go 是财务域的共享规则与工具（issue #94，docs/erp-mvp-spec.md
// §3.6/§4.7/§4.8）：编号前缀与序列 biz_type、核销明细校验（只挂已审核单据 +
// 往来单位一致）、目标单据加锁读取、审核上限裁决入口、回写重算。
//
// 核销只在审核时落账：审核先乐观认领状态，再按（biz_type,biz_id）字典序对
// 目标单据行加锁（多单据稳定锁序防死锁），用加锁聚合校验「本单核销额 + 其它
// 已审核核销额 ≤ 单据 total_price」，通过后重算回写被核销单据的已收/已付
// 金额；反审核认领后按同一锁序重算（释放额度）。目标单据存在已审核收付款
// 引用时其反审核路径 409（在 #91/#92 的 unapprove 事务内加检查）。
package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 单号前缀（spec §4.8：收款单 SK / 付款单 FK）。
const (
	FinanceReceiptNoPrefix = "SK"
	FinancePaymentNoPrefix = "FK"
)

// erp_sequence 的 biz_type（spec §4.8：与单据表名一致）。
const (
	SeqBizTypeFinanceReceipt = "finance_receipt"
	SeqBizTypeFinancePayment = "finance_payment"
)

// financeSide 区分收款/付款两个镜像方向（决定合法 biz_type 与回写列）。
type financeSide int

const (
	// financeSideReceipt 收款单：挂销售出库（收）/销售退货（退）。
	financeSideReceipt financeSide = iota + 1
	// financeSidePayment 付款单：挂采购入库（付）/采购退货（退）。
	financeSidePayment
)

// financeTargetDoc 是被核销单据的核销相关快照（加锁读取后用于上限/回写）。
type financeTargetDoc struct {
	BizType    string
	ID         int64
	No         string
	TotalPrice money.Money
	Status     int8
	// PartyID 是单据的往来单位 ID（sale_out/sale_return 为客户，
	// purchase_in/purchase_return 为供应商），用于与收付款单一致性校验。
	PartyID int64
}

// financeBizLabel 返回核销业务类型的中文单据名（错误文案用）。
func financeBizLabel(bizType string) string {
	switch bizType {
	case model.FinanceBizSaleOut:
		return "销售出库单"
	case model.FinanceBizSaleReturn:
		return "销售退货单"
	case model.FinanceBizPurchaseIn:
		return "采购入库单"
	case model.FinanceBizPurchaseReturn:
		return "采购退货单"
	}
	return "单据"
}

// financeSideDocNoun 返回单据类型中文名（收款单/付款单）。
func financeSideDocNoun(side financeSide) string {
	if side == financeSideReceipt {
		return "收款单"
	}
	return "付款单"
}

// financeSidePartyNoun 返回往来单位中文名（客户/供应商）。
func financeSidePartyNoun(side financeSide) string {
	if side == financeSideReceipt {
		return "客户"
	}
	return "供应商"
}

// buildFinanceReceiptDrafts 校验并换算收款单明细：biz_type 只能 sale_out /
// sale_return，biz_id 必填，price 必须 > 0（spec §3.6/§4.7）。
func buildFinanceReceiptDrafts(items []dto.FinanceReceiptItemPayload) ([]financeItemDraft, error) {
	if len(items) == 0 {
		return nil, xerr.BadRequest("收款单至少一条明细")
	}
	drafts := make([]financeItemDraft, 0, len(items))
	for i, item := range items {
		if !model.ValidFinanceReceiptBizType(item.BizType) {
			return nil, xerr.BadRequest(fmt.Sprintf("收款单第 %d 行业务类型不合法：只支持销售出库（sale_out）/销售退货（sale_return）", i+1))
		}
		if item.BizId <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("收款单第 %d 行缺少被核销单据", i+1))
		}
		if item.Price.Cents() <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("收款单第 %d 行核销金额必须大于 0", i+1))
		}
		drafts = append(drafts, financeItemDraft{BizType: item.BizType, BizID: item.BizId, Price: item.Price})
	}
	return drafts, nil
}

// buildFinancePaymentDrafts 校验并换算付款单明细：biz_type 只能 purchase_in /
// purchase_return，biz_id 必填，price 必须 > 0。
func buildFinancePaymentDrafts(items []dto.FinancePaymentItemPayload) ([]financeItemDraft, error) {
	if len(items) == 0 {
		return nil, xerr.BadRequest("付款单至少一条明细")
	}
	drafts := make([]financeItemDraft, 0, len(items))
	for i, item := range items {
		if !model.ValidFinancePaymentBizType(item.BizType) {
			return nil, xerr.BadRequest(fmt.Sprintf("付款单第 %d 行业务类型不合法：只支持采购入库（purchase_in）/采购退货（purchase_return）", i+1))
		}
		if item.BizId <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("付款单第 %d 行缺少被核销单据", i+1))
		}
		if item.Price.Cents() <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("付款单第 %d 行核销金额必须大于 0", i+1))
		}
		drafts = append(drafts, financeItemDraft{BizType: item.BizType, BizID: item.BizId, Price: item.Price})
	}
	return drafts, nil
}

// checkFinanceItemRefs 校验核销明细引用（创建/编辑路径）：单据存在（400）→
// 已审核（409，spec §4.7）→ 往来单位与收付款单一致（400，保证回写口径）。
func (s *Service) checkFinanceItemRefs(ctx context.Context, side financeSide, partyID int64, drafts []financeItemDraft) error {
	for _, draft := range drafts {
		key := financeBizKey{BizType: draft.BizType, BizID: draft.BizID}
		doc, err := s.findFinanceTargetDoc(ctx, s.repo, key, false)
		if err != nil {
			return err
		}
		label := financeBizLabel(draft.BizType)
		if doc == nil {
			return xerr.BadRequest(label + "不存在")
		}
		if doc.Status != model.DocStatusApproved {
			return xerr.Conflict(label + "未审核，" + financeSideDocNoun(side) + "必须挂已审核单据")
		}
		if doc.PartyID != partyID {
			return xerr.BadRequest(financeSideDocNoun(side) + financeSidePartyNoun(side) + "必须与" + label + financeSidePartyNoun(side) + "一致")
		}
	}
	return nil
}

// findFinanceTargetDoc 按 biz_type 读取被核销单据（forUpdate=true 时行锁读取，
// 只能在事务内调用）；不存在返回 (nil, nil)。
func (s *Service) findFinanceTargetDoc(ctx context.Context, repo *repository.Repository, key financeBizKey, forUpdate bool) (*financeTargetDoc, error) {
	switch key.BizType {
	case model.FinanceBizSaleOut:
		var doc *model.SaleOut
		var err error
		if forUpdate {
			doc, err = repo.FindSaleOutByIDForUpdate(ctx, key.BizID)
		} else {
			doc, err = repo.FindSaleOutByID(ctx, key.BizID)
		}
		if err != nil || doc == nil {
			return nil, err
		}
		return &financeTargetDoc{BizType: key.BizType, ID: doc.ID, No: doc.No,
			TotalPrice: doc.TotalPrice, Status: doc.Status, PartyID: doc.CustomerID}, nil
	case model.FinanceBizSaleReturn:
		var doc *model.SaleReturn
		var err error
		if forUpdate {
			doc, err = repo.LockSaleReturnByID(ctx, key.BizID)
		} else {
			doc, err = repo.FindSaleReturnByID(ctx, key.BizID)
		}
		if err != nil || doc == nil {
			return nil, err
		}
		return &financeTargetDoc{BizType: key.BizType, ID: doc.ID, No: doc.No,
			TotalPrice: doc.TotalPrice, Status: doc.Status, PartyID: doc.CustomerID}, nil
	case model.FinanceBizPurchaseIn:
		var doc *model.PurchaseIn
		var err error
		if forUpdate {
			doc, err = repo.LockPurchaseIn(ctx, key.BizID)
		} else {
			doc, err = repo.FindPurchaseInByID(ctx, key.BizID)
		}
		if err != nil || doc == nil {
			return nil, err
		}
		return &financeTargetDoc{BizType: key.BizType, ID: doc.ID, No: doc.No,
			TotalPrice: doc.TotalPrice, Status: doc.Status, PartyID: doc.SupplierID}, nil
	case model.FinanceBizPurchaseReturn:
		var doc *model.PurchaseReturn
		var err error
		if forUpdate {
			doc, err = repo.LockPurchaseReturnByID(ctx, key.BizID)
		} else {
			doc, err = repo.FindPurchaseReturnByID(ctx, key.BizID)
		}
		if err != nil || doc == nil {
			return nil, err
		}
		return &financeTargetDoc{BizType: key.BizType, ID: doc.ID, No: doc.No,
			TotalPrice: doc.TotalPrice, Status: doc.Status, PartyID: doc.SupplierID}, nil
	}
	return nil, xerr.BadRequest("核销业务类型不合法：" + key.BizType)
}

// checkFinanceApproveRefs 审核时的引用复核与上限裁决（事务内）：按稳定键序对
// 目标单据行加锁，重新确认已审核与往来单位一致，再按 spec §4.7 校验
// 「本单合计 + 其它已审核核销 ≤ 单据 total_price」；超限 409。
// excludeDocID 是本单 ID（统计「其它已审核核销」时排除自身）。
func (s *Service) checkFinanceApproveRefs(ctx context.Context, tx *repository.Repository, side financeSide, partyID int64, own map[financeBizKey]money.Money, excludeDocID int64) error {
	docTotals := make(map[financeBizKey]money.Money, len(own))
	docNos := make(map[financeBizKey]string, len(own))
	allocated := make(map[financeBizKey]money.Money, len(own))
	for _, key := range sortedFinanceKeys(own) {
		doc, err := s.findFinanceTargetDoc(ctx, tx, key, true)
		if err != nil {
			return err
		}
		label := financeBizLabel(key.BizType)
		if doc == nil {
			return xerr.Conflict(label + "不存在或已删除，" + financeSideDocNoun(side) + "不可审核")
		}
		if doc.Status != model.DocStatusApproved {
			return xerr.Conflict(label + "未审核，" + financeSideDocNoun(side) + "必须挂已审核单据")
		}
		if doc.PartyID != partyID {
			return xerr.BadRequest(financeSideDocNoun(side) + financeSidePartyNoun(side) + "必须与" + label + financeSidePartyNoun(side) + "一致")
		}
		docTotals[key] = doc.TotalPrice
		docNos[key] = doc.No
		sums, err := s.sumApprovedFinancePricesByBiz(ctx, tx, side, key.BizType, []int64{key.BizID}, excludeDocID)
		if err != nil {
			return err
		}
		allocated[key] = sums[key.BizID]
	}
	return checkFinanceLimit(own, docTotals, allocated, docNos)
}

// sumApprovedFinancePricesByBiz 按方向分派到收款/付款明细的已审核聚合。
func (s *Service) sumApprovedFinancePricesByBiz(ctx context.Context, tx *repository.Repository, side financeSide, bizType string, bizIDs []int64, excludeDocID int64) (map[int64]money.Money, error) {
	if side == financeSideReceipt {
		return tx.SumApprovedFinanceReceiptPricesByBiz(ctx, bizType, bizIDs, excludeDocID)
	}
	return tx.SumApprovedFinancePaymentPricesByBiz(ctx, bizType, bizIDs, excludeDocID)
}

// recomputeFinanceWriteback 重算并回写被核销单据的已收/已付金额（spec §4.7）：
// 收款 → sale_out.receipt_price / sale_return.refund_price；付款 →
// purchase_in.payment_price / purchase_return.refund_price。重算式（非增量）
// 保证审核与反审核幂等；调用方已持有目标单据行锁。
func (s *Service) recomputeFinanceWriteback(ctx context.Context, tx *repository.Repository, side financeSide, key financeBizKey) error {
	sums, err := s.sumApprovedFinancePricesByBiz(ctx, tx, side, key.BizType, []int64{key.BizID}, 0)
	if err != nil {
		return err
	}
	amount := sums[key.BizID]
	if side == financeSideReceipt {
		switch key.BizType {
		case model.FinanceBizSaleOut:
			return tx.UpdateSaleOutReceiptPrice(ctx, key.BizID, amount)
		case model.FinanceBizSaleReturn:
			return tx.UpdateSaleReturnRefundPrice(ctx, key.BizID, amount)
		}
		return xerr.BadRequest("收款单不支持的核销业务类型：" + key.BizType)
	}
	switch key.BizType {
	case model.FinanceBizPurchaseIn:
		return tx.UpdatePurchaseInPaymentPrice(ctx, key.BizID, amount)
	case model.FinanceBizPurchaseReturn:
		return tx.UpdatePurchaseReturnRefundPrice(ctx, key.BizID, amount)
	}
	return xerr.BadRequest("付款单不支持的核销业务类型：" + key.BizType)
}

// lockAndRecomputeFinanceWritebacks 按稳定键序锁定目标单据并重算回写
// （审核通过后 / 反审核释放额度）；单据已不存在时跳过（不可写回）。
func (s *Service) lockAndRecomputeFinanceWritebacks(ctx context.Context, tx *repository.Repository, side financeSide, keys map[financeBizKey]money.Money) error {
	for _, key := range sortedFinanceKeys(keys) {
		doc, err := s.findFinanceTargetDoc(ctx, tx, key, true)
		if err != nil {
			return err
		}
		if doc == nil {
			continue
		}
		if err := s.recomputeFinanceWriteback(ctx, tx, side, key); err != nil {
			return err
		}
	}
	return nil
}

// checkFinanceAccount 校验结算账户存在（租户内，不存在 400）。
func (s *Service) checkFinanceAccount(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return xerr.BadRequest("缺少结算账户")
	}
	account, err := s.repo.FindFinanceAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account == nil {
		return xerr.BadRequest("结算账户不存在")
	}
	return nil
}

// checkFinanceHandlerUser 校验经手人（纯记录字段）：> 0 时用户必须存在。
func (s *Service) checkFinanceHandlerUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return nil
	}
	_, ok, err := s.sys.UserDeptID(ctx, userID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.BadRequest(fmt.Sprintf("经手人不存在：用户 %d", userID))
	}
	return nil
}

// checkPurchaseSupplier 校验供应商存在（租户内，不存在 400；付款单往来单位）。
func (s *Service) checkPurchaseSupplier(ctx context.Context, supplierID int64) error {
	if supplierID <= 0 {
		return xerr.BadRequest("缺少供应商")
	}
	supplier, err := s.repo.FindSupplierByID(ctx, supplierID)
	if err != nil {
		return err
	}
	if supplier == nil {
		return xerr.BadRequest("供应商不存在")
	}
	return nil
}

// financeReceiptItemRows 组装收款单明细行。
func financeReceiptItemRows(docID int64, drafts []financeItemDraft) []model.FinanceReceiptItem {
	rows := make([]model.FinanceReceiptItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.FinanceReceiptItem{
			ReceiptID: docID,
			BizType:   d.BizType,
			BizID:     d.BizID,
			Price:     d.Price,
		})
	}
	return rows
}

// financePaymentItemRows 组装付款单明细行。
func financePaymentItemRows(docID int64, drafts []financeItemDraft) []model.FinancePaymentItem {
	rows := make([]model.FinancePaymentItem, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, model.FinancePaymentItem{
			PaymentID: docID,
			BizType:   d.BizType,
			BizID:     d.BizID,
			Price:     d.Price,
		})
	}
	return rows
}

// financeReceiptDrafts 把已落库明细还原为草稿（审核/反审核统计口径）。
func financeReceiptDrafts(items []model.FinanceReceiptItem) []financeItemDraft {
	drafts := make([]financeItemDraft, 0, len(items))
	for _, item := range items {
		drafts = append(drafts, financeItemDraft{BizType: item.BizType, BizID: item.BizID, Price: item.Price})
	}
	return drafts
}

// financePaymentDrafts 把已落库明细还原为草稿。
func financePaymentDrafts(items []model.FinancePaymentItem) []financeItemDraft {
	drafts := make([]financeItemDraft, 0, len(items))
	for _, item := range items {
		drafts = append(drafts, financeItemDraft{BizType: item.BizType, BizID: item.BizID, Price: item.Price})
	}
	return drafts
}

// sumFinanceDraftTotal 汇总明细核销金额（列表/详情展示用；不落主表列）。
func sumFinanceDraftTotal(drafts []financeItemDraft) (money.Money, error) {
	total := money.FromCents(0)
	for _, draft := range drafts {
		next, err := total.Add(draft.Price)
		if err != nil {
			return money.Money{}, xerr.BadRequest("明细核销金额合计超出范围")
		}
		total = next
	}
	return total, nil
}
