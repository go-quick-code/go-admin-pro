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
	now := s.businessNow()
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

// CreateReceivablePlan 新建回款计划：合同须可见且未作废（PR #39 审查 P2），
// 金额不变量按合同金额校验。
//
// 并发保护（PR #39 审查 P1）：金额裁决、SUM 与插入在同一事务内完成，事务先对
// 合同行 SELECT ... FOR UPDATE 加锁——同一合同的并发计划创建在锁上排队，
// 后到者读到的是先到者已提交的计划，不会各自按旧合计通过校验造成超限。
func (s *Service) CreateReceivablePlan(ctx context.Context, req *dto.ReceivablePlanCreateReq) (*model.ReceivablePlan, error) {
	if err := ValidateReceivablePeriodNo(req.PeriodNo); err != nil {
		return nil, err
	}
	dueDate, err := s.requireReceivableDate("到期日", req.DueDate)
	if err != nil {
		return nil, err
	}
	// 可见性预检在事务外完成：范围外/不存在直接 404，不泄露存在性，
	// 也不让无权限请求占用合同行锁。
	if _, err := s.findVisibleContract(ctx, req.ContractID); err != nil {
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
	var contract *model.Contract
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		locked, err := tx.LockContractByID(ctx, req.ContractID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("合同不存在")
		}
		// 锁内复核状态：预检之后合同可能已被并发作废。
		if err := ensureContractAcceptsReceivable(locked, "新增回款计划"); err != nil {
			return err
		}
		otherTotal, err := tx.SumReceivablePlansByContract(ctx, req.ContractID, 0)
		if err != nil {
			return err
		}
		if err := ValidateReceivablePlanAmount(req.Amount, locked.Amount, otherTotal); err != nil {
			return err
		}
		if err := tx.CreateReceivablePlan(ctx, plan); err != nil {
			return err
		}
		contract = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.fillReceivablePlan(ctx, plan, contract, s.businessNow())
	return plan, nil
}

// UpdateReceivablePlan 修改回款计划：合同归属不可改；金额校验排除自身。
// 与创建同样在事务内先锁合同行再裁决合计（同一合同的并发创建/修改串行化），
// 只写请求列（period_no/amount/due_date/remark），不回写 contract_id。
func (s *Service) UpdateReceivablePlan(ctx context.Context, req *dto.ReceivablePlanUpdateReq) error {
	plan, _, err := s.findVisibleReceivablePlan(ctx, req.ID)
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
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		contract, err := tx.LockContractByID(ctx, plan.ContractID)
		if err != nil {
			return err
		}
		if contract == nil {
			return xerr.NotFound("回款计划不存在")
		}
		locked, err := tx.LockReceivablePlanByID(ctx, req.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("回款计划不存在")
		}
		otherTotal, err := tx.SumReceivablePlansByContract(ctx, plan.ContractID, plan.ID)
		if err != nil {
			return err
		}
		if err := ValidateReceivablePlanAmount(req.Amount, contract.Amount, otherTotal); err != nil {
			return err
		}
		// 金额不变量在更新路径同样成立：计划金额不得改到低于已回款金额，
		// 否则 SUM(记录金额) ≤ 计划金额 被破坏（spec §4.3）。
		received, err := tx.SumReceivableRecordsByPlan(ctx, plan.ID)
		if err != nil {
			return err
		}
		if received.Cents() > req.Amount.Cents() {
			return xerr.BadRequest(fmt.Sprintf(
				"回款计划金额 %s 不能低于已回款金额 %s：请先删除相关回款记录",
				req.Amount.String(), received.String()))
		}
		_, err = tx.UpdateReceivablePlanFields(ctx, plan.ID, map[string]any{
			"period_no": req.PeriodNo,
			"amount":    req.Amount,
			"due_date":  *dueDate,
			"remark":    req.Remark,
		})
		return err
	})
}

// DeleteReceivablePlan 删除回款计划：存在回款记录时禁止删除（spec §4.5）。
//
// 并发保护（PR #39 审查 P1）：与登记回款同一锁序（先合同、后计划），计数与
// 软删在同一事务内完成——并发登记在合同/计划行锁上排队，不会删出「已删计划
// + 新增回款记录」。
func (s *Service) DeleteReceivablePlan(ctx context.Context, id int64) error {
	plan, _, err := s.findVisibleReceivablePlan(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		contract, err := tx.LockContractByID(ctx, plan.ContractID)
		if err != nil {
			return err
		}
		if contract == nil {
			return xerr.NotFound("回款计划不存在")
		}
		locked, err := tx.LockReceivablePlanByID(ctx, id)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("回款计划不存在")
		}
		count, err := tx.CountReceivableRecordsByPlan(ctx, locked.ID)
		if err != nil {
			return err
		}
		if count > 0 {
			return xerr.BadRequest("回款计划存在回款记录，禁止删除：请先删除相关回款记录")
		}
		return tx.DeleteReceivablePlan(ctx, locked.ID)
	})
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

// CreateReceivableRecord 登记回款记录：计划须可见、父合同未作废（PR #39 审查
// P2），金额不变量按计划金额校验，HK 编号与记录落库同事务（失败回滚、不消耗序号）。
//
// 并发保护（PR #39 审查 P1）：统一「先合同、后计划」锁序——事务内先对父合同行
// SELECT ... FOR UPDATE 并复核未作废，再锁计划行，然后 SUM/校验/取号/插入。
// 合同作废路径同样先锁合同行，因此「作废守卫计数通过 → 记录插入 → 合同置作废」
// 的旧竞态被串行化：两种顺序必有一方失败。
func (s *Service) CreateReceivableRecord(ctx context.Context, req *dto.ReceivableRecordCreateReq) (*model.ReceivableRecord, error) {
	visiblePlan, _, err := s.findVisibleReceivablePlan(ctx, req.PlanID)
	if err != nil {
		return nil, err
	}
	receiptDate, err := s.requireReceivableDate("到账日期", req.ReceiptDate)
	if err != nil {
		return nil, err
	}

	row := &model.ReceivableRecord{
		TenantID:    currentTenantID(ctx),
		PlanID:      req.PlanID,
		Amount:      req.Amount,
		ReceiptDate: *receiptDate,
		Method:      req.Method,
		Remark:      req.Remark,
	}
	var plan *model.ReceivablePlan
	var contract *model.Contract
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 锁序固定：合同在前、计划在后（与作废/删除合同、计划创建/修改一致）。
		lockedContract, err := tx.LockContractByID(ctx, visiblePlan.ContractID)
		if err != nil {
			return err
		}
		if lockedContract == nil {
			return xerr.NotFound("回款计划不存在")
		}
		// 锁内复核：预检之后合同可能已被并发作废（守卫在锁内计数，见作废路径）。
		if err := ensureContractAcceptsReceivable(lockedContract, "登记回款记录"); err != nil {
			return err
		}
		lockedPlan, err := tx.LockReceivablePlanByID(ctx, req.PlanID)
		if err != nil {
			return err
		}
		if lockedPlan == nil || lockedPlan.ContractID != lockedContract.ID {
			return xerr.NotFound("回款计划不存在")
		}
		otherTotal, err := tx.SumReceivableRecordsByPlan(ctx, lockedPlan.ID)
		if err != nil {
			return err
		}
		if err := ValidateReceivableRecordAmount(req.Amount, lockedPlan.Amount, otherTotal); err != nil {
			return err
		}
		now := s.businessNow()
		dateKey := DateKey(now)
		seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeReceivableRecord, dateKey)
		if err != nil {
			return err
		}
		row.RecordNo = FormatReceivableRecordNo(dateKey, seq)
		row.ContractID = lockedPlan.ContractID
		if err := tx.CreateReceivableRecord(ctx, row); err != nil {
			return err
		}
		plan, contract = lockedPlan, lockedContract
		return nil
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
	ok, err := s.contractPermitted(ctx, contract)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, xerr.NotFound("回款计划不存在")
	}
	return plan, contract, nil
}

// contractPermitted 判断合同是否在当前用户的数据范围内（ADR 0004）。
// 纯读校验，供逐个可见性入口复用，避免各入口各写一份谓词翻译。
func (s *Service) contractPermitted(ctx context.Context, c *model.Contract) (bool, error) {
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return false, err
	}
	return scope.Permits(c.OwnerDeptID, c.OwnerUserID), nil
}

// ensureContractAcceptsReceivable 校验合同是否允许新增回款（计划/记录）：
// 作废合同是失效凭据，不再接受新的应收或到账（PR #39 审查 P2），返回中文 400。
func ensureContractAcceptsReceivable(c *model.Contract, action string) error {
	if c.Status == model.ContractStatusVoid {
		return xerr.BadRequest("合同已作废，不能" + action)
	}
	return nil
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
