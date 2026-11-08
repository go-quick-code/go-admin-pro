// finance_receipt.go 是收款单（SK）的业务方法（issue #94，docs/erp-mvp-spec.md
// §3.6/§4.7/§5）：明细只挂已审核销售出库（收）/销售退货（退），客户必须一致；
// 审核时校验核销上限并回写 sale_out.receipt_price / sale_return.refund_price，
// 反审核重算释放额度。
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

// ListFinanceReceipts 收款单分页（筛选 + owner 数据范围，spec §5）。
func (s *Service) ListFinanceReceipts(ctx context.Context, req *dto.FinanceReceiptPageReq) ([]dto.FinanceReceiptRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.financeReceiptFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListFinanceReceipts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return s.assembleFinanceReceiptRows(ctx, list, total)
}

// financeReceiptFilter 构造收款单列表/导出筛选（数据范围 + 时间按业务时区解析）。
func (s *Service) financeReceiptFilter(ctx context.Context, req *dto.FinanceReceiptPageReq) (repository.FinanceReceiptFilter, error) {
	if req.Status != nil && !model.ValidDocStatus(*req.Status) {
		return repository.FinanceReceiptFilter{}, xerr.BadRequest("审核状态只支持 10 未审核 / 20 已审核")
	}
	start, end, err := s.parseDocTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return repository.FinanceReceiptFilter{}, err
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.FinanceReceiptFilter{}, err
	}
	filter := repository.FinanceReceiptFilter{
		No:         req.No,
		CustomerId: req.CustomerId,
		AccountId:  req.AccountId,
		Status:     req.Status,
		StartTime:  start,
		EndTime:    end,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// assembleFinanceReceiptRows 批量解析客户/账户名与明细核销合计并组装分页行。
func (s *Service) assembleFinanceReceiptRows(ctx context.Context, list []model.FinanceReceipt, total int64) ([]dto.FinanceReceiptRow, int64, error) {
	receiptIDs := make([]int64, 0, len(list))
	customerIDs := make([]int64, 0, len(list))
	accountIDs := make([]int64, 0, len(list))
	for _, row := range list {
		receiptIDs = append(receiptIDs, row.ID)
		customerIDs = append(customerIDs, row.CustomerID)
		accountIDs = append(accountIDs, row.AccountID)
	}
	totals, err := s.repo.SumFinanceReceiptItemPrices(ctx, receiptIDs)
	if err != nil {
		return nil, 0, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, customerIDs)
	if err != nil {
		return nil, 0, err
	}
	accountNames, err := s.repo.FindFinanceAccountNamesIn(ctx, accountIDs)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]dto.FinanceReceiptRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.FinanceReceiptRow{
			ID:            row.ID,
			No:            row.No,
			CustomerId:    row.CustomerID,
			CustomerName:  customerNames[row.CustomerID],
			AccountId:     row.AccountID,
			AccountName:   accountNames[row.AccountID],
			ReceiptUserId: row.ReceiptUserID,
			ReceiptTime:   row.ReceiptTime,
			Status:        row.Status,
			StatusName:    docStatusNames[row.Status],
			TotalPrice:    totals[row.ID],
			Remark:        row.Remark,
			CreateTime:    row.CreatedAt,
		})
	}
	return rows, total, nil
}

// GetFinanceReceipt 收款单详情（头 + 明细 + 被核销单据单号）。
func (s *Service) GetFinanceReceipt(ctx context.Context, id int64) (*dto.FinanceReceiptDetail, error) {
	row, err := s.findFinanceReceipt(ctx, id)
	if err != nil {
		return nil, err
	}
	full, err := s.repo.FindFinanceReceiptWithItems(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	total, err := sumFinanceDraftTotal(financeReceiptDrafts(full.Items))
	if err != nil {
		return nil, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, []int64{full.CustomerID})
	if err != nil {
		return nil, err
	}
	accountNames, err := s.repo.FindFinanceAccountNamesIn(ctx, []int64{full.AccountID})
	if err != nil {
		return nil, err
	}
	bizNos, err := s.financeReceiptBizNos(ctx, full.Items)
	if err != nil {
		return nil, err
	}
	items := make([]dto.FinanceReceiptItemRow, 0, len(full.Items))
	for _, item := range full.Items {
		key := financeBizKey{BizType: item.BizType, BizID: item.BizID}
		items = append(items, dto.FinanceReceiptItemRow{
			ID:          item.ID,
			BizType:     item.BizType,
			BizTypeName: financeBizTypeNames[item.BizType],
			BizId:       item.BizID,
			BizNo:       bizNos[key],
			Price:       item.Price,
		})
	}
	return &dto.FinanceReceiptDetail{
		ID:            full.ID,
		No:            full.No,
		CustomerId:    full.CustomerID,
		CustomerName:  customerNames[full.CustomerID],
		AccountId:     full.AccountID,
		AccountName:   accountNames[full.AccountID],
		ReceiptUserId: full.ReceiptUserID,
		ReceiptTime:   full.ReceiptTime,
		Status:        full.Status,
		StatusName:    docStatusNames[full.Status],
		TotalPrice:    total,
		Remark:        full.Remark,
		Items:         items,
		CreateTime:    full.CreatedAt,
	}, nil
}

// CreateFinanceReceipt 创建收款单（未审核）：明细校验 → 客户/账户/经手人存在 →
// 明细引用只挂已审核销售出库/退货且客户一致 → 事务内发放单号（SK）→ 落主表 +
// 明细。不动核销（只有审核动作回写，spec §4.7）。
func (s *Service) CreateFinanceReceipt(ctx context.Context, req *dto.FinanceReceiptCreateReq) (*model.FinanceReceipt, error) {
	drafts, err := buildFinanceReceiptDrafts(req.Items)
	if err != nil {
		return nil, err
	}
	receiptTime, err := s.parseDocTime(req.ReceiptTime, "收款时间")
	if err != nil {
		return nil, err
	}
	if err := s.checkSaleCustomer(ctx, req.CustomerId); err != nil {
		return nil, err
	}
	if err := s.checkFinanceAccount(ctx, req.AccountId); err != nil {
		return nil, err
	}
	if err := s.checkFinanceHandlerUser(ctx, req.ReceiptUserId); err != nil {
		return nil, err
	}
	if err := s.checkFinanceItemRefs(ctx, financeSideReceipt, req.CustomerId, drafts); err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var doc *model.FinanceReceipt
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		no, err := s.nextStockDocNo(ctx, tx, SeqBizTypeFinanceReceipt, FinanceReceiptNoPrefix)
		if err != nil {
			return err
		}
		doc = &model.FinanceReceipt{
			No:            no,
			CustomerID:    req.CustomerId,
			AccountID:     req.AccountId,
			ReceiptUserID: req.ReceiptUserId,
			ReceiptTime:   receiptTime,
			Status:        model.DocStatusUnaudited,
			Remark:        req.Remark,
			OwnerUserID:   ownerID,
			OwnerDeptID:   deptID,
		}
		if err := tx.CreateFinanceReceipt(ctx, doc); err != nil {
			return err
		}
		return tx.CreateFinanceReceiptItems(ctx, financeReceiptItemRows(doc.ID, drafts))
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// UpdateFinanceReceipt 修改收款单（仅未审核，已审核 409）：字段全量覆盖，明细
// 整体替换并重新校验引用（可换客户，明细随新客户重验）。
func (s *Service) UpdateFinanceReceipt(ctx context.Context, req *dto.FinanceReceiptUpdateReq) error {
	row, err := s.findFinanceReceipt(ctx, req.ID)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可修改")
	}
	drafts, err := buildFinanceReceiptDrafts(req.Items)
	if err != nil {
		return err
	}
	receiptTime, err := s.parseDocTime(req.ReceiptTime, "收款时间")
	if err != nil {
		return err
	}
	if err := s.checkSaleCustomer(ctx, req.CustomerId); err != nil {
		return err
	}
	if err := s.checkFinanceAccount(ctx, req.AccountId); err != nil {
		return err
	}
	if err := s.checkFinanceHandlerUser(ctx, req.ReceiptUserId); err != nil {
		return err
	}
	if err := s.checkFinanceItemRefs(ctx, financeSideReceipt, req.CustomerId, drafts); err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 状态不变量绑定到写入路径（安全 F1）：事务内锁自身行并复核 status=10，
		// 与审核的 UPDATE ... WHERE status=10 认领在同一行锁上串行化。
		locked, err := tx.FindFinanceReceiptByIDForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("收款单不存在")
		}
		if locked.Status != model.DocStatusUnaudited {
			return xerr.Conflict("已审核单据不可修改")
		}
		locked.CustomerID = req.CustomerId
		locked.AccountID = req.AccountId
		locked.ReceiptUserID = req.ReceiptUserId
		locked.ReceiptTime = receiptTime
		locked.Remark = req.Remark
		if err := tx.SaveFinanceReceipt(ctx, locked); err != nil {
			return err
		}
		if err := tx.DeleteFinanceReceiptItems(ctx, row.ID); err != nil {
			return err
		}
		return tx.CreateFinanceReceiptItems(ctx, financeReceiptItemRows(row.ID, drafts))
	})
}

// DeleteFinanceReceipt 删除收款单（仅未审核，已审核 409）：主表 + 明细软删除
// （未审核单不产生核销回写，无需释放额度）。
func (s *Service) DeleteFinanceReceipt(ctx context.Context, id int64) error {
	row, err := s.findFinanceReceipt(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != model.DocStatusUnaudited {
		return xerr.Conflict("已审核单据不可删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 条件软删 WHERE id=? AND status=10（安全 F1）：影响行数为 0 → 409。
		deleted, err := tx.DeleteFinanceReceiptIfUnaudited(ctx, row.ID)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return xerr.Conflict("已审核单据不可删除")
		}
		return tx.DeleteFinanceReceiptItems(ctx, row.ID)
	})
}

// UpdateFinanceReceiptStatus 审核（10→20）/反审核（20→10），幂等目标态。
func (s *Service) UpdateFinanceReceiptStatus(ctx context.Context, req *dto.FinanceDocUpdateStatusReq) error {
	row, err := s.findFinanceReceipt(ctx, req.ID)
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
		return s.approveFinanceReceipt(ctx, row)
	}
	return s.unapproveFinanceReceipt(ctx, row)
}

// approveFinanceReceipt 审核落账（spec §4.7）：事务内先乐观认领状态 → 按稳定
// 键序锁定并复核目标单据（已审核 + 客户一致）→ 核销上限（本单合计 + 其它
// 已审核核销 ≤ 单据 total_price，超限 409）→ 重算回写 receipt_price/refund_price。
func (s *Service) approveFinanceReceipt(ctx context.Context, row *model.FinanceReceipt) error {
	// READ COMMITTED：持有目标单据行锁后的核销聚合必须是当前读（见 repository
	// TransactionReadCommitted 注释），上限不变量才不受并发已提交核销影响。
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateFinanceReceiptStatusIf(ctx, row.ID, model.DocStatusUnaudited, model.DocStatusApproved)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindFinanceReceiptWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		drafts := financeReceiptDrafts(doc.Items)
		own, err := groupFinanceDrafts(drafts)
		if err != nil {
			return err
		}
		if err := s.checkFinanceApproveRefs(ctx, tx, financeSideReceipt, doc.CustomerID, own, doc.ID); err != nil {
			return err
		}
		return s.lockAndRecomputeFinanceWritebacks(ctx, tx, financeSideReceipt, own)
	})
}

// unapproveFinanceReceipt 反审核（spec §4.1/§4.7）：乐观认领状态 → 按稳定键序
// 锁定目标单据并重算回写（本单不再计入已审核核销，额度自然释放）。
func (s *Service) unapproveFinanceReceipt(ctx context.Context, row *model.FinanceReceipt) error {
	return s.repo.TransactionReadCommitted(ctx, func(tx *repository.Repository) error {
		claimed, err := tx.UpdateFinanceReceiptStatusIf(ctx, row.ID, model.DocStatusApproved, model.DocStatusUnaudited)
		if err != nil {
			return err
		}
		if claimed == 0 {
			return xerr.Conflict("单据状态已变化，请刷新后重试")
		}
		doc, err := tx.FindFinanceReceiptWithItems(ctx, row.ID)
		if err != nil {
			return err
		}
		own, err := groupFinanceDrafts(financeReceiptDrafts(doc.Items))
		if err != nil {
			return err
		}
		return s.lockAndRecomputeFinanceWritebacks(ctx, tx, financeSideReceipt, own)
	})
}

// findFinanceReceipt 取收款单并做可见性校验（数据范围外一律 404）。
func (s *Service) findFinanceReceipt(ctx context.Context, id int64) (*model.FinanceReceipt, error) {
	row, err := s.repo.FindFinanceReceiptByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("收款单不存在")
	}
	if err := s.checkRowVisible(ctx, "收款单", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// financeReceiptBizNos 批量解析收款明细指向的被核销单据单号。
func (s *Service) financeReceiptBizNos(ctx context.Context, items []model.FinanceReceiptItem) (map[financeBizKey]string, error) {
	out := make(map[financeBizKey]string, len(items))
	outIDs := make([]int64, 0, len(items))
	retIDs := make([]int64, 0, len(items))
	for _, item := range items {
		switch item.BizType {
		case model.FinanceBizSaleOut:
			outIDs = append(outIDs, item.BizID)
		case model.FinanceBizSaleReturn:
			retIDs = append(retIDs, item.BizID)
		}
	}
	outNos, err := s.repo.FindSaleOutNosIn(ctx, outIDs)
	if err != nil {
		return nil, err
	}
	retNos, err := s.repo.FindSaleReturnNosIn(ctx, retIDs)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		switch item.BizType {
		case model.FinanceBizSaleOut:
			out[financeBizKey{BizType: item.BizType, BizID: item.BizID}] = outNos[item.BizID]
		case model.FinanceBizSaleReturn:
			out[financeBizKey{BizType: item.BizType, BizID: item.BizID}] = retNos[item.BizID]
		}
	}
	return out, nil
}

// ExportFinanceReceipts 导出收款单（筛选含数据范围，spec §4.11）。
func (s *Service) ExportFinanceReceipts(ctx context.Context, req *dto.FinanceReceiptPageReq) ([]byte, error) {
	filter, err := s.financeReceiptFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllFinanceReceipts(ctx, filter)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.SumFinanceReceiptItemPrices(ctx, financeReceiptIDs(list))
	if err != nil {
		return nil, err
	}
	customerNames, err := s.repo.FindCustomerNamesIn(ctx, financeReceiptCustomerIDs(list))
	if err != nil {
		return nil, err
	}
	accountNames, err := s.repo.FindFinanceAccountNamesIn(ctx, financeReceiptAccountIDs(list))
	if err != nil {
		return nil, err
	}
	ownerNames, err := s.userNicknames(ctx, collectOwnerIDs(list, func(row model.FinanceReceipt) int64 { return row.OwnerUserID }))
	if err != nil {
		return nil, err
	}
	rows := make([]dto.FinanceReceiptExportRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.FinanceReceiptExportRow{
			No:            row.No,
			Customer:      customerNames[row.CustomerID],
			Account:       accountNames[row.AccountID],
			ReceiptTime:   row.ReceiptTime,
			Status:        docStatusNames[row.Status],
			TotalPrice:    totals[row.ID],
			OwnerUserName: ownerNames[row.OwnerUserID],
			CreateTime:    row.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

func financeReceiptIDs(list []model.FinanceReceipt) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.ID)
	}
	return ids
}

func financeReceiptCustomerIDs(list []model.FinanceReceipt) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.CustomerID)
	}
	return ids
}

func financeReceiptAccountIDs(list []model.FinanceReceipt) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.AccountID)
	}
	return ids
}
