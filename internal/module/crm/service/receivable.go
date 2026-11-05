package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 回款计划 + 回款记录（issue #18）----------
// 可见性跟随父合同（spec §4.1：回款 join 合同后套用合同 owner 谓词）：
// 列表按谓词过滤，范围外计划/记录的修改/删除/登记一律 404（看不见即不存在）。
//
// 金额不变量（spec §4.3）：SUM(计划金额) ≤ 合同金额（更新排除自身）；
// SUM(记录金额) ≤ 计划金额。违反返回用户可读 400，不静默截断。
// 派生状态不落库：列表 SQL 扫出已收金额，状态用 DeriveReceivableStatus 纯函数重算。
// 删除约束（spec §4.5）：计划存在回款记录时禁止删除；合同存在回款记录时
// 禁止作废/删除（VoidContractGuard 真实实现，见 receivable_guard.go）。

// ListReceivablePlans 分页查询回款计划（合同/派生状态/逾期筛选 + 数据范围）。
func (s *Service) ListReceivablePlans(ctx context.Context, req *dto.ReceivablePlanPageReq) ([]model.ReceivablePlan, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	now := time.Now()
	filter, err := s.receivablePlanFilter(ctx, req, now)
	if err != nil {
		return nil, 0, err
	}

	list, total, err := s.repo.ListReceivablePlans(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillReceivablePlans(ctx, list, now)
	return list, total, nil
}

// receivablePlanFilter 构造回款计划列表/导出的筛选与数据范围谓词
// （导出复用同一口径；Today 由调用方传入，保证筛选与派生状态用同一个「今天」）。
func (s *Service) receivablePlanFilter(ctx context.Context, req *dto.ReceivablePlanPageReq, now time.Time) (repository.ReceivablePlanFilter, error) {
	if req.Status != nil {
		if err := ValidateReceivableStatusFilter(*req.Status); err != nil {
			return repository.ReceivablePlanFilter{}, err
		}
	}
	filter := repository.ReceivablePlanFilter{
		ContractID: req.ContractID,
		Status:     req.Status,
		Overdue:    req.Overdue,
		Today:      now.Format("2006-01-02"),
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.ReceivablePlanFilter{}, err
	}
	applyCustomerScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// CreateReceivablePlan 新建回款计划：合同须可见，金额不变量按合同金额校验。
func (s *Service) CreateReceivablePlan(ctx context.Context, req *dto.ReceivablePlanCreateReq) (*model.ReceivablePlan, error) {
	if err := ValidateReceivablePeriodNo(req.PeriodNo); err != nil {
		return nil, err
	}
	dueDate, err := s.requireReceivableDate("到期日", req.DueDate)
	if err != nil {
		return nil, err
	}
	contract, err := s.findVisibleContract(ctx, req.ContractID)
	if err != nil {
		return nil, err
	}
	otherTotal, err := s.repo.SumReceivablePlansByContract(ctx, req.ContractID, 0)
	if err != nil {
		return nil, err
	}
	if err := ValidateReceivablePlanAmount(req.Amount, contract.Amount, otherTotal); err != nil {
		return nil, err
	}

	plan := &model.ReceivablePlan{
		TenantID:   currentTenantID(ctx),
		ContractID: req.ContractID,
		PeriodNo:   req.PeriodNo,
		Amount:     req.Amount,
		DueDate:    *dueDate,
		Remark:     req.Remark,
	}
	if err := s.repo.CreateReceivablePlan(ctx, plan); err != nil {
		return nil, err
	}
	s.fillReceivablePlan(ctx, plan, contract, time.Now())
	return plan, nil
}

// UpdateReceivablePlan 修改回款计划：合同归属不可改；金额校验排除自身。
func (s *Service) UpdateReceivablePlan(ctx context.Context, req *dto.ReceivablePlanUpdateReq) error {
	plan, contract, err := s.findVisibleReceivablePlan(ctx, req.ID)
	if err != nil {
		return err
	}
	if err := ValidateReceivablePeriodNo(req.PeriodNo); err != nil {
		return err
	}
	dueDate, err := s.requireReceivableDate("到期日", req.DueDate)
	if err != nil {
		return err
	}
	otherTotal, err := s.repo.SumReceivablePlansByContract(ctx, plan.ContractID, plan.ID)
	if err != nil {
		return err
	}
	if err := ValidateReceivablePlanAmount(req.Amount, contract.Amount, otherTotal); err != nil {
		return err
	}
	// 金额不变量在更新路径同样成立：计划金额不得改到低于已回款金额，
	// 否则 SUM(记录金额) ≤ 计划金额 被破坏（spec §4.3）。
	received, err := s.repo.SumReceivableRecordsByPlan(ctx, plan.ID)
	if err != nil {
		return err
	}
	if received.Cents() > req.Amount.Cents() {
		return xerr.BadRequest(fmt.Sprintf(
			"回款计划金额 %s 不能低于已回款金额 %s：请先删除相关回款记录",
			req.Amount.String(), received.String()))
	}

	plan.PeriodNo = req.PeriodNo
	plan.Amount = req.Amount
	plan.DueDate = *dueDate
	plan.Remark = req.Remark
	return s.repo.SaveReceivablePlan(ctx, plan)
}

// DeleteReceivablePlan 删除回款计划：存在回款记录时禁止删除（spec §4.5）。
func (s *Service) DeleteReceivablePlan(ctx context.Context, id int64) error {
	plan, _, err := s.findVisibleReceivablePlan(ctx, id)
	if err != nil {
		return err
	}
	count, err := s.repo.CountReceivableRecordsByPlan(ctx, plan.ID)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.BadRequest("回款计划存在回款记录，禁止删除：请先删除相关回款记录")
	}
	return s.repo.DeleteReceivablePlan(ctx, plan.ID)
}

// ListReceivableRecords 分页查询回款记录（合同/计划筛选 + 数据范围）。
func (s *Service) ListReceivableRecords(ctx context.Context, req *dto.ReceivableRecordPageReq) ([]model.ReceivableRecord, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.ReceivableRecordFilter{
		ContractID: req.ContractID,
		PlanID:     req.PlanID,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	applyCustomerScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)

	list, total, err := s.repo.ListReceivableRecords(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillReceivableRecordContractNames(ctx, list)
	return list, total, nil
}

// CreateReceivableRecord 登记回款记录：计划须可见，金额不变量按计划金额校验，
// HK 编号与记录落库同事务（失败回滚、不消耗序号）。
func (s *Service) CreateReceivableRecord(ctx context.Context, req *dto.ReceivableRecordCreateReq) (*model.ReceivableRecord, error) {
	plan, contract, err := s.findVisibleReceivablePlan(ctx, req.PlanID)
	if err != nil {
		return nil, err
	}
	receiptDate, err := s.requireReceivableDate("到账日期", req.ReceiptDate)
	if err != nil {
		return nil, err
	}
	otherTotal, err := s.repo.SumReceivableRecordsByPlan(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	if err := ValidateReceivableRecordAmount(req.Amount, plan.Amount, otherTotal); err != nil {
		return nil, err
	}

	row := &model.ReceivableRecord{
		TenantID:    currentTenantID(ctx),
		PlanID:      plan.ID,
		ContractID:  plan.ContractID,
		Amount:      req.Amount,
		ReceiptDate: *receiptDate,
		Method:      req.Method,
		Remark:      req.Remark,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		now := time.Now()
		dateKey := DateKey(now)
		seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeReceivableRecord, dateKey)
		if err != nil {
			return err
		}
		row.RecordNo = FormatReceivableRecordNo(dateKey, seq)
		return tx.CreateReceivableRecord(ctx, row)
	})
	if err != nil {
		return nil, err
	}
	row.PlanPeriodNo = plan.PeriodNo
	row.ContractNo = contract.ContractNo
	row.ContractName = contract.Name
	return row, nil
}

// DeleteReceivableRecord 删除回款记录（数据范围外 404）：删除后计划的已收金额
// 与派生状态读时自然回退，不做补偿写入。
func (s *Service) DeleteReceivableRecord(ctx context.Context, id int64) error {
	row, err := s.repo.FindReceivableRecordByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("回款记录不存在")
	}
	if _, _, err := s.findVisibleReceivablePlan(ctx, row.PlanID); err != nil {
		if e, ok := xerr.As(err); ok && e.Status == 404 {
			return xerr.NotFound("回款记录不存在")
		}
		return err
	}
	return s.repo.DeleteReceivableRecord(ctx, row.ID)
}

// findVisibleReceivablePlan 取计划、父合同并做数据范围校验：
// 不存在、合同不可见或已删除都返回 404（看不见的即不存在，不泄露存在性）。
func (s *Service) findVisibleReceivablePlan(ctx context.Context, id int64) (*model.ReceivablePlan, *model.Contract, error) {
	plan, err := s.repo.FindReceivablePlanByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if plan == nil {
		return nil, nil, xerr.NotFound("回款计划不存在")
	}
	contract, err := s.repo.FindContractByID(ctx, plan.ContractID)
	if err != nil {
		return nil, nil, err
	}
	if contract == nil {
		return nil, nil, xerr.NotFound("回款计划不存在")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, nil, err
	}
	if !scope.Permits(contract.OwnerDeptID, contract.OwnerUserID) {
		return nil, nil, xerr.NotFound("回款计划不存在")
	}
	return plan, contract, nil
}

// requireReceivableDate 解析必填的回款日期字段（到期日/到账日期）。
func (s *Service) requireReceivableDate(field, value string) (*time.Time, error) {
	parsed, err := ParseReceivableDate(field, value, s.businessLocation())
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, xerr.BadRequest(field + "不能为空")
	}
	return parsed, nil
}

// fillReceivablePlans 批量补齐合同展示字段并派生状态（列表路径，避免逐行查合同）。
func (s *Service) fillReceivablePlans(ctx context.Context, list []model.ReceivablePlan, now time.Time) {
	if len(list) == 0 {
		return
	}
	contractIDs := make([]int64, 0, len(list))
	for _, p := range list {
		contractIDs = append(contractIDs, p.ContractID)
	}
	contracts, err := s.repo.FindContractsByIDs(ctx, contractIDs)
	if err != nil {
		// 合同名称补齐失败不阻塞列表（与负责人名称补齐同一降级口径）。
		for i := range list {
			s.deriveReceivablePlanStatus(&list[i], now)
		}
		return
	}
	byID := make(map[int64]model.Contract, len(contracts))
	customerIDs := make([]int64, 0, len(contracts))
	for _, c := range contracts {
		byID[c.ID] = c
		if c.CustomerID > 0 {
			customerIDs = append(customerIDs, c.CustomerID)
		}
	}
	customerNames := s.receivableCustomerNames(ctx, customerIDs)
	for i := range list {
		if c, ok := byID[list[i].ContractID]; ok {
			fillPlanContractFields(&list[i], &c, customerNames[c.CustomerID])
		}
		s.deriveReceivablePlanStatus(&list[i], now)
	}
}

// fillReceivablePlan 补齐单条计划的合同字段并派生状态（创建/详情响应）。
func (s *Service) fillReceivablePlan(ctx context.Context, plan *model.ReceivablePlan, contract *model.Contract, now time.Time) {
	if contract != nil {
		name := ""
		if contract.CustomerID > 0 {
			if customers, err := s.repo.FindCustomersByIDs(ctx, []int64{contract.CustomerID}); err == nil && len(customers) > 0 {
				name = customers[0].Name
			}
		}
		fillPlanContractFields(plan, contract, name)
	}
	s.deriveReceivablePlanStatus(plan, now)
}

// fillPlanContractFields 写入合同展示字段（编号/名称/金额/客户名）。
func fillPlanContractFields(plan *model.ReceivablePlan, contract *model.Contract, customerName string) {
	plan.ContractNo = contract.ContractNo
	plan.ContractName = contract.Name
	plan.ContractAmount = contract.Amount
	plan.CustomerName = customerName
}

// deriveReceivablePlanStatus 用纯函数重算派生状态（列表 SQL 的筛选口径与之一致）。
func (s *Service) deriveReceivablePlanStatus(plan *model.ReceivablePlan, now time.Time) {
	plan.Status, plan.Overdue = DeriveReceivableStatus(plan.Amount, plan.ReceivedAmount, plan.DueDate, now)
	plan.StatusName = ReceivableStatusNames[plan.Status]
}

// fillReceivableRecordContractNames 批量补齐记录的合同编号/名称（列表路径）。
func (s *Service) fillReceivableRecordContractNames(ctx context.Context, list []model.ReceivableRecord) {
	if len(list) == 0 {
		return
	}
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.ContractID)
	}
	contracts, err := s.repo.FindContractsByIDs(ctx, ids)
	if err != nil {
		return
	}
	byID := make(map[int64]model.Contract, len(contracts))
	for _, c := range contracts {
		byID[c.ID] = c
	}
	for i := range list {
		if c, ok := byID[list[i].ContractID]; ok {
			list[i].ContractNo = c.ContractNo
			list[i].ContractName = c.Name
		}
	}
}

// receivableCustomerNames 批量取客户名称（计划列表展示用；失败降级为空）。
func (s *Service) receivableCustomerNames(ctx context.Context, customerIDs []int64) map[int64]string {
	names := map[int64]string{}
	if len(customerIDs) == 0 {
		return names
	}
	customers, err := s.repo.FindCustomersByIDs(ctx, customerIDs)
	if err != nil {
		return names
	}
	for _, c := range customers {
		names[c.ID] = c.Name
	}
	return names
}
