// finance_payment.go 是付款单（FK）的业务方法（issue #94，docs/erp-mvp-spec.md
// §3.6/§4.7/§5）：收款单的镜像——明细只挂已审核采购入库（付）/采购退货（退），
// 供应商必须一致；审核时校验核销上限并回写 purchase_in.payment_price /
// purchase_return.refund_price，反审核重算释放额度。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListFinancePayments 付款单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListFinancePayments(ctx context.Context, req *dto.FinancePaymentPageReq) ([]dto.FinancePaymentRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.financePaymentFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListFinancePayments(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return s.assembleFinancePaymentRows(ctx, list, total)
}

// financePaymentFilter 构造付款单列表/导出筛选。
func (s *Service) financePaymentFilter(ctx context.Context, req *dto.FinancePaymentPageReq) (repository.FinancePaymentFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.FinancePaymentFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.FinancePaymentFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.FinancePaymentFilter{}, err
	}
	filter := repository.FinancePaymentFilter{
		No:         req.No,
		SupplierId: req.SupplierId,
		AccountId:  req.AccountId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assembleFinancePaymentRows 批量解析供应商/账户名与明细核销合计并组装分页行。
func (s *Service) assembleFinancePaymentRows(ctx context.Context, list []model.FinancePayment, total int64) ([]dto.FinancePaymentRow, int64, error) {
	paymentIDs := make([]int64, 0, len(list))
	supplierIDs := make([]int64, 0, len(list))
	accountIDs := make([]int64, 0, len(list))
	for _, row := range list {
		paymentIDs = append(paymentIDs, row.ID)
		supplierIDs = append(supplierIDs, row.SupplierID)
		accountIDs = append(accountIDs, row.AccountID)
	}
	totals, err := s.repo.SumFinancePaymentItemPrices(ctx, paymentIDs)
	if err != nil {
		return nil, 0, err
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, supplierIDs)
	if err != nil {
		return nil, 0, err
	}
	accountNames, err := s.repo.FindFinanceAccountNamesIn(ctx, accountIDs)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]dto.FinancePaymentRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.FinancePaymentRow{
			ID:            row.ID,
			No:            row.No,
			SupplierId:    row.SupplierID,
			SupplierName:  supplierNames[row.SupplierID],
			AccountId:     row.AccountID,
			AccountName:   accountNames[row.AccountID],
			PaymentUserId: row.PaymentUserID,
			PaymentTime:   row.PaymentTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			TotalPrice:    totals[row.ID],
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return rows, total, nil
}

// GetFinancePayment 付款单详情（头 + 明细 + 被核销单据单号）。
func (s *Service) GetFinancePayment(ctx context.Context, id int64) (*dto.FinancePaymentDetail, error) {
	row, err := s.findFinancePayment(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindFinancePaymentWithItems(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	total, err := sumFinanceDraftTotal(financePaymentDrafts(full.Items))
	if err != nil {
		return nil, err
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, []int64{full.SupplierID})
	if err != nil {
		return nil, err
	}
	accountNames, err := s.repo.FindFinanceAccountNamesIn(ctx, []int64{full.AccountID})
	if err != nil {
		return nil, err
	}
	bizNos, err := s.financePaymentBizNos(ctx, full.Items)
	if err != nil {
		return nil, err
	}
	items := make([]dto.FinancePaymentItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		key := financeBizKey{BizType: item.BizType, BizID: item.BizID}
		items = append(items, dto.FinancePaymentItemRow{
			ID:          item.ID,
			BizType:     item.BizType,
			BizTypeName: financeBizTypeNames[item.BizType],
			BizId:       item.BizID,
			BizNo:       bizNos[key],
			Price:       item.Price,
		})
	}
	return &dto.FinancePaymentDetail{
		ID:            full.ID,
		No:            full.No,
		SupplierId:    full.SupplierID,
		SupplierName:  supplierNames[full.SupplierID],
		AccountId:     full.AccountID,
		AccountName:   accountNames[full.AccountID],
		PaymentUserId: full.PaymentUserID,
		PaymentTime:   full.PaymentTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		TotalPrice:    total,
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateFinancePayment 创建付款单（未审核）：明细校验 → 供应商/账户/经手人存在
// → 明细引用只挂已审核采购入库/退货且供应商一致 → 事务内发放单号（FK）→ 落库。
func (s *Service) CreateFinancePayment(ctx context.Context, req *dto.FinancePaymentCreateReq) (*model.FinancePayment, error) {
	drafts, err := buildFinancePaymentDrafts(req.Items)
	if err != nil {
		return nil, err
	}
	paymentTime, err := s.parseDocTime(req.PaymentTime, "付款时间")
	if err != nil {
		return nil, err
	}
	if err := s.checkPurchaseSupplier(ctx, req.SupplierId); err != nil {
		return nil, err
	}
	if err := s.checkFinanceAccount(ctx, req.AccountId); err != nil {
		return nil, err
	}
	if err := s.checkFinanceHandlerUser(ctx, req.PaymentUserId); err != nil {
		return nil, err
	}
	if err := s.checkFinanceItemRefs(ctx, financeSidePayment, req.SupplierId, drafts); err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.FinancePayment
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeFinancePayment, FinancePaymentNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.FinancePayment{
			No:            no,
			SupplierID:    req.SupplierId,
			AccountID:     req.AccountId,
			PaymentUserID: req.PaymentUserId,
			PaymentTime:   paymentTime,
			Status:        model.DocStatusUnaudited,
			Remark:        req.Remark,
			OwnerUserID:   ownerID,
			OwnerDeptID:   deptID,
		}
		if err := tx.CreateFinancePayment(ctx, doc); err != nil {
			return err
		}
		return tx.CreateFinancePaymentItems(ctx, financePaymentItemRows(doc.ID, drafts))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdateFinancePayment 修改付款单（仅未审核，已审核 409）。
func (s *Service) UpdateFinancePayment(ctx context.Context, req *dto.FinancePaymentUpdateReq) error {
	row, err := s.findFinancePayment(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	drafts, err := buildFinancePaymentDrafts(req.Items)
	if err != nil {
		return err
	}
	paymentTime, err := s.parseDocTime(req.PaymentTime, "付款时间")
	if err != nil {
		return err
	}
	if err := s.checkPurchaseSupplier(ctx, req.SupplierId); err != nil {
		return err
	}
	if err := s.checkFinanceAccount(ctx, req.AccountId); err != nil {
		return err
	}
	if err := s.checkFinanceHandlerUser(ctx, req.PaymentUserId); err != nil {
		return err
	}
	if err := s.checkFinanceItemRefs(ctx, financeSidePayment, req.SupplierId, drafts); err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row.SupplierID = req.SupplierId
		row.AccountID = req.AccountId
		row.PaymentUserID = req.PaymentUserId
		row.PaymentTime = paymentTime
		row.Remark = req.Remark
		if err := tx.SaveFinancePayment(ctx, row); err != nil {
			return err
		}
		if err := tx.DeleteFinancePaymentItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateFinancePaymentItems(ctx, financePaymentItemRows(row.ID, drafts))
	})
}

// DeleteFinancePayment 删除付款单（仅未审核，已审核 409）。
func (s *Service) DeleteFinancePayment(ctx context.Context, id int64) error {
	row, err := s.findFinancePayment(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.DeleteFinancePayment(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteFinancePaymentItems(ctx, row.ID)
	})
}

// UpdateFinancePaymentStatus 审核（10→20）/反审核（20→10），幂等目标态。
func (s *Service) UpdateFinancePaymentStatus(ctx context.Context, req *dto.FinanceDocUpdateStatusReq) error {
	row, err := s.findFinancePayment(ctx, req.ID)
	if err != nil {
		return err
	}
	idempotent, err := checkDocStatusTransition(row.Status, req.Status)
	if err != nil {
		return err
	}
	if idempotent {
		return nil
	}
	if req.Status == model.DocStatusApproved {
		return s.approveFinancePayment(ctx, row)
	}
	return s.unapproveFinancePayment(ctx, row)
}

// approveFinancePayment 审核落账（spec §4.7）：认领状态 → 锁定并复核采购单据
// （已审核 + 供应商一致）→ 核销上限 → 重算回写 payment_price/refund_price。
func (s *Service) approveFinancePayment(ctx context.Context, row *model.FinancePayment) error {
	// READ COMMITTED：持有目标单据行锁后的核销聚合必须是当前读（见 repository
	// TransactionReadCommitted 注释），上限不变量才不受并发已提交核销影响。
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateFinancePaymentStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindFinancePaymentWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		own, err := groupFinanceDrafts(financePaymentDrafts(doc.Items))
		if err != nil {
			return err
		}
		if err := s.checkFinanceApproveRefs(ctx, tx, financeSidePayment, doc.SupplierID, own, doc.ID); err != nil {
			return err
		}
		return s.lockAndRecomputeFinanceWritebacks(ctx, tx, financeSidePayment, own)
	})
}

// unapproveFinancePayment 反审核：认领状态 → 重算回写（额度释放）。
func (s *Service) unapproveFinancePayment(ctx context.Context, row *model.FinancePayment) error {
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateFinancePaymentStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindFinancePaymentWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		own, err := groupFinanceDrafts(financePaymentDrafts(doc.Items))
		if err != nil {
			return err
		}
		return s.lockAndRecomputeFinanceWritebacks(ctx, tx, financeSidePayment, own)
	})
}

// findFinancePayment 取付款单并做可见性校验（数据范围外一律 404）。
func (s *Service) findFinancePayment(ctx context.Context, id int64) (*model.FinancePayment, error) {
	row, err := s.repo.FindFinancePaymentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("付款单不存在")
	}
	if err := s.checkRowVisible(ctx, "付款单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// financePaymentBizNos 批量解析付款明细指向的被核销单据单号。
func (s *Service) financePaymentBizNos(ctx context.Context, items []model.FinancePaymentItem) (map[financeBizKey]string, error) {
	out := make(map[financeBizKey]string, len(items))
	inIDs := make([]int64, 0, len(items))
	retIDs := make([]int64, 0, len(items))
	for _, item := range items {
		switch item.BizType {
		case model.FinanceBizPurchaseIn:
			inIDs = append(inIDs, item.BizID)
		case model.FinanceBizPurchaseReturn:
			retIDs = append(retIDs, item.BizID)
		}
	}
	inNos, err := s.repo.FindPurchaseInNosIn(ctx, inIDs)
	if err != nil {
		return nil, err
	}
	retNos, err := s.repo.FindPurchaseReturnNosIn(ctx, retIDs)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		switch item.BizType {
		case model.FinanceBizPurchaseIn:
			out[financeBizKey{BizType: item.BizType, BizID: item.BizID}] = inNos[item.BizID]
		case model.FinanceBizPurchaseReturn:
			out[financeBizKey{BizType: item.BizType, BizID: item.BizID}] = retNos[item.BizID]
		}
	}
	return out, nil
}

// ExportFinancePayments 导出付款单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportFinancePayments(ctx context.Context, req *dto.FinancePaymentPageReq) ([]byte, error) {
	filter, err := s.financePaymentFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllFinancePayments(ctx, filter)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.SumFinancePaymentItemPrices(ctx, financePaymentIDs(list))
	if err != nil {
		return nil, err
	}
	supplierNames, err := s.repo.FindSupplierNamesIn(ctx, financePaymentSupplierIDs(list))
	if err != nil {
		return nil, err
	}
	accountNames, err := s.repo.FindFinanceAccountNamesIn(ctx, financePaymentAccountIDs(list))
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.FinancePayment) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.FinancePaymentExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.FinancePaymentExportRow{
			No:            row.No,
			Supplier:      supplierNames[row.SupplierID],
			Account:       accountNames[row.AccountID],
			PaymentTime:   row.PaymentTime,
			Status:        docStatusNames[row.Status],
			TotalPrice:    totals[row.ID],
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

func financePaymentIDs(list []model.FinancePayment) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.ID)
	}
	return ids
}

func financePaymentSupplierIDs(list []model.FinancePayment) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.SupplierID)
	}
	return ids
}

func financePaymentAccountIDs(list []model.FinancePayment) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.AccountID)
	}
	return ids
}
