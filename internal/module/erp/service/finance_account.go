// finance_account.go 是结算账户的业务方法（issue #94，docs/erp-mvp-spec.md
// §3.6/§4.7/§4.10）：分页 CRUD + owner 数据范围过滤；默认账户租户内唯一互斥
// （置默认时先清原默认，事务内行锁 + 库层生成列唯一键双保险）；余额列 =
// 该账户已审核收付款净额实时汇总（不落库）；被收付款单引用禁删（409）。
package service

import (
	"context"
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// financeStatusNames 是主数据状态中文展示（0 启用 / 1 停用，与 00029 口径一致）。
var financeStatusNames = map[int8]string{
	model.StatusEnable:  "启用",
	model.StatusDisable: "停用",
}

// ListFinanceAccounts 分页查询结算账户（筛选 + 数据范围），行内带实时余额。
func (s *Service) ListFinanceAccounts(ctx context.Context, req *dto.FinanceAccountPageReq) ([]dto.FinanceAccountRow, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.financeAccountFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListFinanceAccounts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	balances, err := s.financeAccountBalances(ctx, financeAccountIDs(list))
	if err != nil {
		return nil, 0, err
	}
	rows := make([]dto.FinanceAccountRow, 0, len(list))
	for _, row := range list {
		rows = append(rows, dto.FinanceAccountRow{
			ID:            row.ID,
			Name:          row.Name,
			No:            row.No,
			Remark:        row.Remark,
			Status:        row.Status,
			StatusName:    financeStatusNames[row.Status],
			Sort:          row.Sort,
			DefaultStatus: row.DefaultStatus,
			Balance:       balances[row.ID],
			CreateTime:    row.CreatedAt,
		})
	}
	return rows, total, nil
}

// financeAccountFilter 构造结算账户列表筛选与数据范围谓词。
func (s *Service) financeAccountFilter(ctx context.Context, req *dto.FinanceAccountPageReq) (repository.FinanceAccountFilter, error) {
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		return repository.FinanceAccountFilter{}, xerr.BadRequest("账户状态只支持 0 启用 / 1 停用")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.FinanceAccountFilter{}, err
	}
	filter := repository.FinanceAccountFilter{Name: req.Name, Status: req.Status}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetFinanceAccount 结算账户详情（含实时余额；不存在/数据范围外 404）。
func (s *Service) GetFinanceAccount(ctx context.Context, id int64) (*dto.FinanceAccountDetail, error) {
	row, err := s.findFinanceAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	balances, err := s.financeAccountBalances(ctx, []int64{row.ID})
	if err != nil {
		return nil, err
	}
	return &dto.FinanceAccountDetail{
		ID:            row.ID,
		Name:          row.Name,
		No:            row.No,
		Remark:        row.Remark,
		Status:        row.Status,
		StatusName:    financeStatusNames[row.Status],
		Sort:          row.Sort,
		DefaultStatus: row.DefaultStatus,
		Balance:       balances[row.ID],
		CreateTime:    row.CreatedAt,
	}, nil
}

// CreateFinanceAccount 创建结算账户；defaultStatus=1 时事务内取消原默认。
func (s *Service) CreateFinanceAccount(ctx context.Context, req *dto.FinanceAccountCreateReq) (*model.FinanceAccount, error) {
	status, sort, err := statusAndSort(req.Status, req.Sort)
	if err != nil {
		return nil, err
	}
	defaultStatus, err := financeDefaultStatus(req.DefaultStatus)
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	row := &model.FinanceAccount{
		Name:          req.Name,
		No:            req.No,
		Remark:        req.Remark,
		Sort:          sort,
		Status:        status,
		DefaultStatus: defaultStatus,
		OwnerUserID:   ownerID,
		OwnerDeptID:   deptID,
	}
	if defaultStatus != model.FinanceDefaultYes {
		return row, s.repo.CreateFinanceAccount(ctx, row)
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.LockFinanceAccounts(ctx); err != nil {
			return err
		}
		if err := tx.ClearFinanceAccountDefaults(ctx, 0); err != nil {
			return err
		}
		if err := tx.CreateFinanceAccount(ctx, row); err != nil {
			return financeDuplicateDefaultError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateFinanceAccount 修改结算账户；置默认时事务内取消原默认，取消默认直接
// 写 0（不强制存在默认账户）；defaultStatus 缺省保持现状，避免误清除默认。
func (s *Service) UpdateFinanceAccount(ctx context.Context, req *dto.FinanceAccountUpdateReq) error {
	row, err := s.findFinanceAccount(ctx, req.ID)
	if err != nil {
		return err
	}
	status, sort, err := statusAndSort(req.Status, req.Sort)
	if err != nil {
		return err
	}
	defaultStatus := row.DefaultStatus
	if req.DefaultStatus != nil {
		if defaultStatus, err = financeDefaultStatus(req.DefaultStatus); err != nil {
			return err
		}
	}
	row.Name = req.Name
	row.No = req.No
	row.Remark = req.Remark
	row.Sort = sort
	row.Status = status
	row.DefaultStatus = defaultStatus
	if defaultStatus != model.FinanceDefaultYes {
		return s.repo.SaveFinanceAccount(ctx, row)
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.LockFinanceAccounts(ctx); err != nil {
			return err
		}
		if err := tx.ClearFinanceAccountDefaults(ctx, row.ID); err != nil {
			return err
		}
		if err := tx.SaveFinanceAccount(ctx, row); err != nil {
			return financeDuplicateDefaultError(err)
		}
		return nil
	})
}

// DeleteFinanceAccount 删除结算账户：被收款/付款单引用禁删（409，spec §4.9）；
// 默认账户删除前清标记（库层唯一键不残留默认影子值），软删除保留历史。
func (s *Service) DeleteFinanceAccount(ctx context.Context, id int64) error {
	row, err := s.findFinanceAccount(ctx, id)
	if err != nil {
		return err
	}
	receipts, err := s.repo.CountFinanceReceiptsByAccount(ctx, row.ID)
	if err != nil {
		return err
	}
	payments, err := s.repo.CountFinancePaymentsByAccount(ctx, row.ID)
	if err != nil {
		return err
	}
	if receipts > 0 || payments > 0 {
		return xerr.Conflict("结算账户被收付款单引用，禁止删除")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if row.DefaultStatus == model.FinanceDefaultYes {
			if err := tx.UpdateFinanceAccountDefaultStatus(ctx, row.ID, model.FinanceDefaultNo); err != nil {
				return err
			}
		}
		return tx.DeleteFinanceAccount(ctx, row.ID)
	})
}

// findFinanceAccount 取账户并做可见性校验（不存在/数据范围外一律 404）。
func (s *Service) findFinanceAccount(ctx context.Context, id int64) (*model.FinanceAccount, error) {
	row, err := s.repo.FindFinanceAccountByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("结算账户不存在")
	}
	if err := s.checkRowVisible(ctx, "结算账户", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// financeAccountBalances 计算账户实时余额（已审核收款 − 已审核付款，spec §4.7）。
func (s *Service) financeAccountBalances(ctx context.Context, accountIDs []int64) (map[int64]money.Money, error) {
	receiptSums, err := s.repo.SumApprovedFinanceReceiptPricesByAccounts(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	paymentSums, err := s.repo.SumApprovedFinancePaymentPricesByAccounts(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	return foldAccountBalances(receiptSums, paymentSums)
}

// financeDefaultStatus 归一默认标记：缺省 0（非默认），非法 400。
func financeDefaultStatus(v *int8) (int8, error) {
	if v == nil {
		return model.FinanceDefaultNo, nil
	}
	if !model.ValidFinanceDefaultStatus(*v) {
		return 0, xerr.BadRequest("默认标记只支持 0 非默认 / 1 默认")
	}
	return *v, nil
}

// financeDuplicateDefaultError 把库层默认账户唯一键冲突翻译成 409（并发置默认
// 时另一请求已成功，提示刷新重试）。
func financeDuplicateDefaultError(err error) error {
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return xerr.Conflict("默认账户已存在，请刷新后重试")
	}
	return err
}

// financeAccountIDs 提取账户 ID 切片（余额批量汇总入参）。
func financeAccountIDs(list []model.FinanceAccount) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.ID)
	}
	return ids
}
