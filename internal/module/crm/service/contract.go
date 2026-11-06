package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 合同（issue #16）----------
// owner-scoped 正体（与 #8 客户/#15 商机同构）：owner_user_id/owner_dept_id 承载
// 数据范围谓词，列表按谓词过滤、范围外详情/修改/删除/状态流转一律 404
//（看不见的即不存在，不泄露存在性）。
//
// 状态机口径（spec §4.4/§4.5）：状态变更用户入口是 PUT /crm/contract/status
//（条件更新防并发，0 行报 409）；非法流转 400。提交走 ApprovalService（bpm
// 实例与合同状态同事务，issue #47）；审批通过/驳回由 bpm 待办动作回调
// BusinessHandler 回写（见 contract_approval.go）；作废走 VoidContractGuard
// 守卫（#18 接回款约束），待审作废时同事务终止运行中实例。
//
// 编辑口径（决策，见提交说明）：草稿/待审可全量编辑（行项目随单整体替换，金额按
// 不变量重算）；已签合同只放行元数据修改（名称/签订/起止日期/备注）——金额与
// 行项目是审批通过的内容，事后可改会破坏「已成交」与回款拆分的基准，必须先作废
// 重签；已作废是终态，不可编辑。删除口径：草稿/作废可删，待审/已签不可删
// （审批中的单据应先驳回或作废，已签是生效凭据）。
//
// 金额不变量（spec §4.3）：有行项目时合同金额恒等于 SUM(行项目金额)，由服务端
// 计算（ResolveContractAmount），客户端提交的金额忽略；无行项目时手填且 ≥ 0。

// ListContracts 分页查询合同，按数据范围谓词过滤（owner-scoped 正体）。
func (s *Service) ListContracts(ctx context.Context, req *dto.ContractPageReq) ([]model.Contract, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.contractFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}

	list, total, err := s.repo.ListContracts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillContractNames(ctx, list)
	return list, total, nil
}

// contractFilter 构造合同列表/导出的筛选与数据范围谓词（导出复用同一口径）。
func (s *Service) contractFilter(ctx context.Context, req *dto.ContractPageReq) (repository.ContractFilter, error) {
	if req.Status != nil {
		if err := ValidateContractStatus(*req.Status); err != nil {
			return repository.ContractFilter{}, err
		}
	}

	filter := repository.ContractFilter{
		Name:       req.Name,
		Status:     req.Status,
		CustomerID: req.CustomerID,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.ContractFilter{}, err
	}
	// 复用 applyCustomerScope（与商机 join 路径同一翻译，ADR 0004 同构）。
	applyCustomerScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetContract 查询合同详情（含行项目、合计与历次审批时间线）。
// 数据范围外一律 404；审批时间线随合同可见性收口（业务读接口）。
// spec §4.3：同一合同保留全部历史实例（作废/驳回后重提会创建新实例），
// approvals 按 id 倒序返回，current 标记最新实例（合同表无
// approval_instance_id，最新实例即当前实例）。
func (s *Service) GetContract(ctx context.Context, id int64) (*dto.ContractDetail, error) {
	c, err := s.findVisibleContract(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.loadContractExtras(ctx, c); err != nil {
		return nil, err
	}
	detail := &dto.ContractDetail{Contract: *c}
	if s.bpm != nil {
		approvals, err := s.bpm.ListInstanceDetailsForBusiness(ctx, ContractApprovalBusinessType, c.ID)
		if err != nil {
			return nil, err
		}
		if len(approvals) > 0 {
			approvals[0].Current = true
			detail.Approvals = approvals
			detail.Approval = &approvals[0]
		}
	}
	return detail, nil
}

// CreateContract 创建合同：编号、合同行与行项目同事务（失败回滚、不消耗序号）。
// 状态缺省 1 草稿（提交/审批/作废都走状态端点，状态机入口唯一）；
// 负责人缺省当前登录用户；opportunityId 可空，传了则商机须可见且属于同一客户。
func (s *Service) CreateContract(ctx context.Context, req *dto.ContractCreateReq) (*model.Contract, error) {
	if _, err := s.findVisibleCustomer(ctx, req.CustomerID); err != nil {
		return nil, err
	}
	if req.OpportunityID > 0 {
		o, err := s.findVisibleOpportunity(ctx, req.OpportunityID)
		if err != nil {
			return nil, err
		}
		if o.CustomerID != req.CustomerID {
			return nil, xerr.BadRequest("来源商机不属于所选客户：合同挂商机时客户须与商机一致")
		}
	}
	if err := ValidateContractLineItems(contractLineItemsFromDTO(req.Products)); err != nil {
		return nil, err
	}
	products, err := s.buildContractProducts(ctx, 0, req.Products)
	if err != nil {
		return nil, err
	}
	amount, err := ResolveContractAmount(req.Amount, contractLineAmounts(products))
	if err != nil {
		return nil, err
	}
	signDate, startDate, endDate, err := parseContractDates(req.SignDate, req.StartDate, req.EndDate, s.businessLocation())
	if err != nil {
		return nil, err
	}
	ownerID := req.OwnerUserID
	if ownerID == 0 {
		ownerID = CurrentUserID(ctx)
	}
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	c := &model.Contract{
		CustomerID:    req.CustomerID,
		OpportunityID: req.OpportunityID,
		Name:          req.Name,
		Amount:        amount,
		SignDate:      signDate,
		StartDate:     startDate,
		EndDate:       endDate,
		Status:        model.ContractStatusDraft,
		OwnerUserID:   ownerID,
		OwnerDeptID:   deptID,
		Remark:        req.Remark,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		now := s.businessNow()
		dateKey := DateKey(now)
		seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeContract, dateKey)
		if err != nil {
			return err
		}
		c.ContractNo = FormatContractNo(dateKey, seq)
		if err := tx.CreateContract(ctx, c); err != nil {
			return err
		}
		return tx.ReplaceContractProducts(ctx, c.ID, products)
	})
	if err != nil {
		return nil, err
	}
	c.Products = products
	total := sumContractProducts(products)
	c.TotalAmount = &total
	s.fillContractName(ctx, c)
	return c, nil
}

// UpdateContract 修改合同：数据范围外一律 404；编号/客户/来源商机/状态不可改
// （状态走专门端点）。编辑口径见文件头注释：草稿/待审全量编辑，已签仅元数据，
// 已作废不可编辑。products 传 nil 保持行项目不变，传空数组清空（金额回退手填）。
//
// 并发保护（PR #39 审查 P1）：事务内先 SELECT ... FOR UPDATE 锁定合同行，
// 再按锁定后的最新状态决定编辑口径。审批通过（待审 → 已签）同样先锁合同行，
// 因此「编辑读待审 → 审批落已签 → 编辑写金额」的旧竞态被消除：后拿到锁的
// 请求看到的是已签状态，金额/行项目不会按旧快照写回。
func (s *Service) UpdateContract(ctx context.Context, req *dto.ContractUpdateReq) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		c, err := tx.LockContractByID(ctx, req.ID)
		if err != nil {
			return err
		}
		if c == nil {
			return xerr.NotFound("合同不存在")
		}
		ok, err := s.contractPermitted(ctx, c)
		if err != nil {
			return err
		}
		if !ok {
			return xerr.NotFound("合同不存在")
		}
		if c.Status == model.ContractStatusVoid {
			return xerr.BadRequest("已作废合同不可编辑")
		}
		signed := c.Status == model.ContractStatusSigned
		var products []model.ContractProduct
		var lineAmounts []money.Money
		if req.Products != nil {
			if signed {
				return xerr.BadRequest("已签合同不可修改行项目：如需调整请先作废后重新签订")
			}
			if err := ValidateContractLineItems(contractLineItemsFromDTO(req.Products)); err != nil {
				return err
			}
			if products, err = s.buildContractProducts(ctx, c.ID, req.Products); err != nil {
				return err
			}
			lineAmounts = contractLineAmounts(products)
		} else if !signed {
			// 行项目保持不变：金额不变量仍须成立，手填金额只在该合同无行项目时生效
			//（有行项目时提交的金额一律忽略，见 ResolveContractAmount）。
			existing, err := s.repo.ListContractProducts(ctx, c.ID)
			if err != nil {
				return err
			}
			lineAmounts = contractLineAmounts(existing)
		}
		signDate, startDate, endDate, err := parseContractDates(req.SignDate, req.StartDate, req.EndDate, s.businessLocation())
		if err != nil {
			return err
		}
		// 已签合同金额锁定（req.Amount 忽略）；其余状态按不变量裁决。
		amount := c.Amount
		if !signed {
			if amount, err = ResolveContractAmount(req.Amount, lineAmounts); err != nil {
				return err
			}
		}

		// 只写请求列（PR #39 审查 P1）：编号/客户/来源商机/状态/驳回原因/owner_*
		// 不在编辑路径写入，并发状态流转或归属变更不会被旧快照回写覆盖。
		fields := map[string]any{
			"name":       req.Name,
			"sign_date":  signDate,
			"start_date": startDate,
			"end_date":   endDate,
			"remark":     req.Remark,
		}
		if !signed {
			fields["amount"] = amount
		}
		if _, err := tx.UpdateContractFields(ctx, c.ID, fields); err != nil {
			return err
		}
		if req.Products != nil {
			return tx.ReplaceContractProducts(ctx, c.ID, products)
		}
		return nil
	})
}

// ChangeContractStatus 状态流转（用户唯一入口）：先做纯函数校验（非法流转 400），
// 再按目标动作分派——提交走 ApprovalService（同事务启动 bpm 实例），作废先过
// VoidContractGuard 守卫（#18 接回款约束）再条件更新；待审作废时同事务终止
// 运行中的审批实例，避免遗留待办与业务状态分叉（issue #47）。
//
// 审批通过与驳回不在本方法：它们由 bpm 待办动作触发 BusinessHandler 回写
// 合同状态（见 contract_approval.go），状态端点不再提供直通审批。
//
// 并发保护（PR #39 审查 P1）：整个「锁定 → 复核最新状态 → 流转/守卫计数 →
// 更新」在同一事务内完成，合同行是最先也唯一被锁的行（锁序：合同 → 计划）。
// 条件更新仍是第二道保险：状态被非本路径并发变更时 0 行报 409。
func (s *Service) ChangeContractStatus(ctx context.Context, req *dto.ContractStatusReq) (*dto.ContractDetail, error) {
	if err := ValidateContractStatus(req.Status); err != nil {
		return nil, err
	}
	var id int64
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		c, err := tx.LockContractByID(ctx, req.ID)
		if err != nil {
			return err
		}
		if c == nil {
			return xerr.NotFound("合同不存在")
		}
		ok, err := s.contractPermitted(ctx, c)
		if err != nil {
			return err
		}
		if !ok {
			return xerr.NotFound("合同不存在")
		}
		// 以锁定后的最新状态复核流转：编辑/审批并发时，后拿到锁的请求
		// 按最新状态裁决，而不是按进入函数时的旧快照。
		if err := CanTransitionContractStatus(c.Status, req.Status); err != nil {
			return err
		}
		switch req.Status {
		case model.ContractStatusPending:
			// 审批引擎必须由 internal/app 装配（SetBPM 同时注入 approvals）；
			// 未装配时显式报错，不静默降级成直通审批（issue #47）。
			if s.approvals == nil {
				return xerr.Internal(errors.New("crm: 审批引擎未装配"))
			}
			err = s.approvals.Submit(ctx, tx, c)
		case model.ContractStatusVoid:
			// 待审合同作废：先在同一事务内终止运行中的审批实例（实例 → 合同
			// 锁序与 bpm 回调一致；回调把合同置草稿），再按最新状态落作废。
			if c.Status == model.ContractStatusPending {
				if err = s.cancelRunningContractApproval(ctx, tx, c.ID); err != nil {
					return err
				}
				latest, err := tx.LockContractByID(ctx, c.ID)
				if err != nil {
					return err
				}
				if latest != nil {
					c.Status = latest.Status
				}
			}
			// 守卫在同一事务、合同行锁内计数回款记录：并发登记回款会在
			// LockContractByID 上排队，作废提交后读到作废态被拒绝，反之
			// 守卫看到已提交的记录而拒绝作废。
			if err = s.canVoidContractTx(ctx, tx, c.ID); err != nil {
				return err
			}
			var rows int64
			rows, err = tx.TransitionContractStatus(ctx, c.ID, c.Status, model.ContractStatusVoid, "")
			if err == nil && rows == 0 {
				err = xerr.Conflict("合同状态已被其他操作变更，请刷新后重试")
			}
		default:
			err = xerr.BadRequest(fmt.Sprintf(
				"合同当前为「%s」状态，不能变更为「%s」",
				ContractStatusNames[c.Status], ContractStatusNames[req.Status],
			))
		}
		id = c.ID
		return err
	})
	if err != nil {
		return nil, err
	}
	// 状态/驳回原因已在库上变更，重读返回最新态（含名称与审批时间线）。
	return s.GetContract(ctx, id)
}

// cancelRunningContractApproval 在调用方事务内终止合同的运行中审批实例；
// 无运行中实例（如历史直通审批或测试替身）时静默跳过。bpm 未装配同样跳过
// （状态机本身不依赖审批实现，非 HTTP 装配的单元测试仍可作废待审合同）。
func (s *Service) cancelRunningContractApproval(ctx context.Context, tx *repository.Repository, contractID int64) error {
	if s.bpm == nil {
		return nil
	}
	inst, err := s.bpm.FindRunningInstanceByBusinessInTx(ctx, tx.DB(), ContractApprovalBusinessType, contractID)
	if err != nil {
		return err
	}
	if inst == nil {
		return nil
	}
	_, err = s.bpm.CancelInstanceInTx(ctx, tx.DB(), inst.ID, "合同作废，审批流程自动终止")
	return err
}

// DeleteContract 软删除合同（数据范围外 404）。删除口径见文件头注释：
// 待审/已签不可删（400）；作废/删除前过 VoidContractGuard 守卫（#18 接
// 「有回款记录禁止删除」，spec §4.5）。行项目随之不可见，其引用的产品恢复可删。
//
// 并发保护（PR #39 审查 P1）：事务内先锁合同行并复核状态，再在锁内计数回款
// 记录，最后软删；与登记回款（先合同、后计划）同一锁序，不会删出「已删合同
// + 新增回款」。
func (s *Service) DeleteContract(ctx context.Context, id int64) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		c, err := tx.LockContractByID(ctx, id)
		if err != nil {
			return err
		}
		if c == nil {
			return xerr.NotFound("合同不存在")
		}
		ok, err := s.contractPermitted(ctx, c)
		if err != nil {
			return err
		}
		if !ok {
			return xerr.NotFound("合同不存在")
		}
		switch c.Status {
		case model.ContractStatusPending:
			return xerr.BadRequest("待审合同不可删除：请先驳回或作废")
		case model.ContractStatusSigned:
			return xerr.BadRequest("已签合同不可删除：生效合同请先作废")
		}
		if err := s.canVoidContractTx(ctx, tx, c.ID); err != nil {
			return err
		}
		return tx.DeleteContract(ctx, c.ID)
	})
}

// findVisibleContract 取合同并做数据范围校验：不存在或范围外都返回 404。
func (s *Service) findVisibleContract(ctx context.Context, id int64) (*model.Contract, error) {
	c, err := s.repo.FindContractByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, xerr.NotFound("合同不存在")
	}
	ok, err := s.contractPermitted(ctx, c)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, xerr.NotFound("合同不存在")
	}
	return c, nil
}

// loadContractExtras 填充详情展示字段：负责人/部门/客户/商机名称、行项目（带产品名）与合计。
func (s *Service) loadContractExtras(ctx context.Context, c *model.Contract) error {
	products, err := s.repo.ListContractProducts(ctx, c.ID)
	if err != nil {
		return err
	}
	s.fillContractProducts(ctx, c, products)
	s.fillContractName(ctx, c)
	return nil
}

// fillContractProducts 补齐行项目的产品名称并计算合计；金额已在落库前算好，
// 这里直接累加（校验过的持久化数据不再重算）。
func (s *Service) fillContractProducts(ctx context.Context, c *model.Contract, products []model.ContractProduct) {
	c.Products = products
	amounts := make([]money.Money, 0, len(products))
	productIDs := make([]int64, 0, len(products))
	for _, p := range products {
		amounts = append(amounts, p.Amount)
		productIDs = append(productIDs, p.ProductID)
	}
	total, err := SumLineAmounts(amounts)
	if err == nil {
		c.TotalAmount = &total
	}
	if len(productIDs) == 0 {
		return
	}
	// 产品名称缺失不阻塞查询（与负责人名称补齐同一降级口径）。
	if items, err := s.repo.FindProductsByIDs(ctx, productIDs); err == nil {
		names := make(map[int64]string, len(items))
		for _, p := range items {
			names[p.ID] = p.Name
		}
		for i := range c.Products {
			c.Products[i].ProductName = names[c.Products[i].ProductID]
		}
	}
}

// buildContractProducts 把 DTO 行项目转成持久化模型：校验产品存在（租户内），
// 金额 = 数量 × 单价服务端计算。contractID 传 0 表示创建中（尚未落库）。
func (s *Service) buildContractProducts(ctx context.Context, contractID int64, items []dto.ContractProductItem) ([]model.ContractProduct, error) {
	if len(items) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ProductID)
	}
	found, err := s.repo.FindProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	exists := make(map[int64]bool, len(found))
	for _, p := range found {
		exists[p.ID] = true
	}
	products := make([]model.ContractProduct, 0, len(items))
	for _, it := range items {
		if !exists[it.ProductID] {
			return nil, xerr.BadRequest(fmt.Sprintf("行项目产品不存在：产品 %d", it.ProductID))
		}
		amount, err := ComputeLineAmount(it.Quantity, it.Price)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("行项目金额计算失败（数量 %d × 单价 %s）：%s", it.Quantity, it.Price.String(), err.Error()))
		}
		products = append(products, model.ContractProduct{
			ContractID: contractID,
			ProductID:  it.ProductID,
			TenantID:   currentTenantID(ctx),
			Quantity:   it.Quantity,
			Price:      it.Price,
			Amount:     amount,
		})
	}
	return products, nil
}

// contractLineItemsFromDTO 把 DTO 行项目转为校验用纯数据（金额在计算阶段单独处理）。
func contractLineItemsFromDTO(items []dto.ContractProductItem) []lineItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]lineItem, 0, len(items))
	for _, it := range items {
		out = append(out, lineItem{ProductID: it.ProductID, Quantity: it.Quantity, Price: it.Price})
	}
	return out
}

// contractLineAmounts 收集行项目金额（金额不变量裁决用）。
func contractLineAmounts(products []model.ContractProduct) []money.Money {
	if len(products) == 0 {
		return nil
	}
	out := make([]money.Money, 0, len(products))
	for _, p := range products {
		out = append(out, p.Amount)
	}
	return out
}

// sumContractProducts 累加已算好的行项目金额（创建响应回显用）。
func sumContractProducts(products []model.ContractProduct) money.Money {
	total, err := SumLineAmounts(contractLineAmounts(products))
	if err != nil {
		return money.Money{}
	}
	return total
}

// parseContractDates 解析并校验签订/起止日期：起 ≤ 止校验对起止生效，
// 签订日期不参与先后约束（补签的合同签订日期可以晚于生效期起点）。
func parseContractDates(signDate, startDate, endDate string, loc *time.Location) (*time.Time, *time.Time, *time.Time, error) {
	sign, err := ParseContractDate(signDate, loc)
	if err != nil {
		return nil, nil, nil, err
	}
	start, err := ParseContractDate(startDate, loc)
	if err != nil {
		return nil, nil, nil, err
	}
	end, err := ParseContractDate(endDate, loc)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := ValidateContractDateRange(start, end); err != nil {
		return nil, nil, nil, err
	}
	return sign, start, end, nil
}

// fillContractNames 批量补齐负责人昵称与部门名称（列表展示用）。
func (s *Service) fillContractNames(ctx context.Context, list []model.Contract) {
	userIDs, deptIDs := collectContractOwnerIDs(list)
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	for i := range list {
		list[i].OwnerUserName = names[list[i].OwnerUserID]
		list[i].OwnerDeptName = deptNames[list[i].OwnerDeptID]
	}
}

// fillContractName 补齐单个合同的负责人/部门/客户/来源商机名称（创建/详情返回值用）。
func (s *Service) fillContractName(ctx context.Context, c *model.Contract) {
	userIDs, deptIDs := collectContractOwnerIDs([]model.Contract{*c})
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	c.OwnerUserName = names[c.OwnerUserID]
	c.OwnerDeptName = deptNames[c.OwnerDeptID]
	if c.CustomerID > 0 {
		customers, err := s.repo.FindCustomersByIDs(ctx, []int64{c.CustomerID})
		if err == nil && len(customers) > 0 {
			c.CustomerName = customers[0].Name
		}
	}
	if c.OpportunityID > 0 {
		opportunities, err := s.repo.FindOpportunitiesByIDs(ctx, []int64{c.OpportunityID})
		if err == nil && len(opportunities) > 0 {
			c.OpportunityName = opportunities[0].Name
		}
	}
}

// collectContractOwnerIDs 收集非零的负责人用户 ID 与部门 ID。
func collectContractOwnerIDs(list []model.Contract) (userIDs, deptIDs []int64) {
	userIDs = make([]int64, 0, len(list))
	deptIDs = make([]int64, 0, len(list))
	for _, c := range list {
		if c.OwnerUserID > 0 {
			userIDs = append(userIDs, c.OwnerUserID)
		}
		if c.OwnerDeptID > 0 {
			deptIDs = append(deptIDs, c.OwnerDeptID)
		}
	}
	return userIDs, deptIDs
}
