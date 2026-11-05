package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/money"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 商机（issue #15）----------
// owner-scoped 正体（与 #8 客户同构）：owner_user_id/owner_dept_id 承载数据范围谓词，
// 列表按谓词过滤、范围外详情/修改/删除一律 404（看不见的即不存在，不泄露存在性）。
//
// 阶段口径（spec §4.4）：阶段变更唯一入口是 PUT /opportunity/stage（条件更新防并发覆盖）；
// 编辑接口不携带阶段字段。商机进入赢单/输单终态后不可再变更阶段，其余字段
//（名称/预计金额/预计成交日期/备注/行项目）仍可编辑——终态锁定的是成交结果
// 的归属，不冻结事后订正（口径决策，见提交说明）。
//
// 行项目口径：金额 = 数量 × 单价由服务端计算，客户端不提交金额；行项目随商机
// 整体保存（先删后插同事务）。#16 合同行项目复用同一规则（money.Mul + 校验函数）。

// BizTypeOpportunity 是商机编号在 crm_sequence 中的业务类型。
const BizTypeOpportunity = "opportunity"

// OpportunityNoPrefix 是商机编号前缀。
const OpportunityNoPrefix = "SJ"

// FormatOpportunityNo 把日期键与序号格式化为商机编号：SJ + yyyyMMdd + 至少 4 位序号。
// 序号超过 9999 时自然扩位：唯一性优先于固定位数，绝不截断（与 FormatCustomerNo 同款）。
func FormatOpportunityNo(dateKey string, seq int64) string {
	return OpportunityNoPrefix + dateKey + fmt.Sprintf("%04d", seq)
}

// ListOpportunities 分页查询商机，按数据范围谓词过滤（owner-scoped 正体）。
func (s *Service) ListOpportunities(ctx context.Context, req *dto.OpportunityPageReq) ([]model.Opportunity, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	if req.Stage != nil {
		if err := ValidateOpportunityStage(*req.Stage); err != nil {
			return nil, 0, err
		}
	}

	filter := repository.OpportunityFilter{
		Name:       req.Name,
		Stage:      req.Stage,
		CustomerID: req.CustomerID,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	// 复用 applyCustomerScope（与联系人 join 路径同一翻译，ADR 0004 同构）。
	applyCustomerScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)

	list, total, err := s.repo.ListOpportunities(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillOpportunityNames(ctx, list)
	return list, total, nil
}

// GetOpportunity 查询商机详情（含行项目与合计）。数据范围外一律 404。
func (s *Service) GetOpportunity(ctx context.Context, id int64) (*model.Opportunity, error) {
	o, err := s.findVisibleOpportunity(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.loadOpportunityExtras(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

// CreateOpportunity 创建商机：编号、商机行与行项目同事务（失败回滚、不消耗序号）。
// 负责人缺省当前登录用户；阶段缺省 1 初步，允许显式传 1–4（进行中），
// 赢单/输单只能通过阶段推进进入（状态机入口唯一）。
func (s *Service) CreateOpportunity(ctx context.Context, req *dto.OpportunityCreateReq) (*model.Opportunity, error) {
	stage := model.OpportunityStageInitial
	if req.Stage != nil {
		if err := ValidateOpportunityStage(*req.Stage); err != nil {
			return nil, err
		}
		if model.IsTerminalOpportunityStage(*req.Stage) {
			return nil, xerr.BadRequest("商机创建时阶段必须为进行中（1–4），赢单/输单只能通过阶段推进进入")
		}
		stage = *req.Stage
	}
	if err := ValidateOpportunityLineItems(lineItemsFromDTO(req.Products)); err != nil {
		return nil, err
	}
	products, err := s.buildOpportunityProducts(ctx, 0, req.Products)
	if err != nil {
		return nil, err
	}
	// 客户必须存在且可见才能挂商机（复用客户可见性解析，范围外 404）。
	if _, err := s.findVisibleCustomer(ctx, req.CustomerID); err != nil {
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
	closeDate, err := ParseExpectedCloseDate(req.ExpectedCloseDate)
	if err != nil {
		return nil, err
	}

	o := &model.Opportunity{
		CustomerID:        req.CustomerID,
		Name:              req.Name,
		Stage:             stage,
		ExpectedAmount:    req.ExpectedAmount,
		ExpectedCloseDate: closeDate,
		OwnerUserID:       ownerID,
		OwnerDeptID:       deptID,
		Remark:            req.Remark,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		now := time.Now()
		dateKey := DateKey(now)
		seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeOpportunity, dateKey)
		if err != nil {
			return err
		}
		o.OpportunityNo = FormatOpportunityNo(dateKey, seq)
		if err := tx.CreateOpportunity(ctx, o); err != nil {
			return err
		}
		return tx.ReplaceOpportunityProducts(ctx, o.ID, products)
	})
	if err != nil {
		return nil, err
	}
	o.Products = products
	total := sumOfProducts(products)
	o.TotalAmount = &total
	s.fillOpportunityName(ctx, o)
	return o, nil
}

// UpdateOpportunity 修改商机：数据范围外一律 404；编号/客户/阶段不可改
// （阶段走专门端点）；终态后其余字段仍可编辑（口径见文件头）。
// products 传 nil 保持行项目不变，传空数组清空。
func (s *Service) UpdateOpportunity(ctx context.Context, req *dto.OpportunityUpdateReq) error {
	o, err := s.findVisibleOpportunity(ctx, req.ID)
	if err != nil {
		return err
	}
	var products []model.OpportunityProduct
	if req.Products != nil {
		if err := ValidateOpportunityLineItems(lineItemsFromDTO(req.Products)); err != nil {
			return err
		}
		if products, err = s.buildOpportunityProducts(ctx, o.ID, req.Products); err != nil {
			return err
		}
	}
	closeDate, err := ParseExpectedCloseDate(req.ExpectedCloseDate)
	if err != nil {
		return err
	}

	o.Name = req.Name
	o.ExpectedAmount = req.ExpectedAmount
	o.ExpectedCloseDate = closeDate
	o.Remark = req.Remark
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.SaveOpportunity(ctx, o); err != nil {
			return err
		}
		if req.Products != nil {
			return tx.ReplaceOpportunityProducts(ctx, o.ID, products)
		}
		return nil
	})
}

// AdvanceOpportunityStage 阶段推进（唯一阶段入口）：终态拒绝、非法拒绝（400），
// 条件更新防并发推进覆盖（行数为 0 报冲突）。
func (s *Service) AdvanceOpportunityStage(ctx context.Context, req *dto.OpportunityStageReq) (*model.Opportunity, error) {
	o, err := s.findVisibleOpportunity(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := CanAdvanceStage(o.Stage, req.Stage); err != nil {
		return nil, err
	}
	if o.Stage != req.Stage {
		rows, err := s.repo.AdvanceOpportunityStage(ctx, o.ID, o.Stage, req.Stage)
		if err != nil {
			return nil, err
		}
		if rows == 0 {
			return nil, xerr.Conflict("商机阶段已被其他操作变更，请刷新后重试")
		}
		o.Stage = req.Stage
	}
	return o, nil
}

// DeleteOpportunity 软删除商机（数据范围外 404）。行项目随之不可见；
// 其引用的产品恢复可删（引用校验只统计未删除商机的行项目）。
func (s *Service) DeleteOpportunity(ctx context.Context, id int64) error {
	o, err := s.findVisibleOpportunity(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteOpportunity(ctx, o.ID)
}

// findVisibleOpportunity 取商机并做数据范围校验：不存在或范围外都返回 404。
func (s *Service) findVisibleOpportunity(ctx context.Context, id int64) (*model.Opportunity, error) {
	o, err := s.repo.FindOpportunityByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, xerr.NotFound("商机不存在")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(o.OwnerDeptID, o.OwnerUserID) {
		return nil, xerr.NotFound("商机不存在")
	}
	return o, nil
}

// loadOpportunityExtras 填充详情展示字段：负责人/部门/客户名称、行项目（带产品名）与合计。
func (s *Service) loadOpportunityExtras(ctx context.Context, o *model.Opportunity) error {
	products, err := s.repo.ListOpportunityProducts(ctx, o.ID)
	if err != nil {
		return err
	}
	s.fillOpportunityProducts(ctx, o, products)
	s.fillOpportunityName(ctx, o)
	return nil
}

// fillOpportunityProducts 补齐行项目的产品名称并计算合计；金额已在落库前算好，
// 这里直接累加（校验过的持久化数据不再重算）。
func (s *Service) fillOpportunityProducts(ctx context.Context, o *model.Opportunity, products []model.OpportunityProduct) {
	o.Products = products
	amounts := make([]money.Money, 0, len(products))
	productIDs := make([]int64, 0, len(products))
	for _, p := range products {
		amounts = append(amounts, p.Amount)
		productIDs = append(productIDs, p.ProductID)
	}
	total, err := SumOpportunityLineAmounts(amounts)
	if err == nil {
		o.TotalAmount = &total
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
		for i := range o.Products {
			o.Products[i].ProductName = names[o.Products[i].ProductID]
		}
	}
}

// buildOpportunityProducts 把 DTO 行项目转成持久化模型：校验产品存在（租户内），
// 金额 = 数量 × 单价服务端计算。opportunityID 传 0 表示创建中（尚未落库）。
func (s *Service) buildOpportunityProducts(ctx context.Context, opportunityID int64, items []dto.OpportunityProductItem) ([]model.OpportunityProduct, error) {
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
	products := make([]model.OpportunityProduct, 0, len(items))
	for _, it := range items {
		if !exists[it.ProductID] {
			return nil, xerr.BadRequest(fmt.Sprintf("行项目产品不存在：产品 %d", it.ProductID))
		}
		amount, err := ComputeOpportunityLineAmount(it.Quantity, it.Price)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("行项目金额计算失败（数量 %d × 单价 %s）：%s", it.Quantity, it.Price.String(), err.Error()))
		}
		products = append(products, model.OpportunityProduct{
			OpportunityID: opportunityID,
			ProductID:     it.ProductID,
			TenantID:      currentTenantID(ctx),
			Quantity:      it.Quantity,
			Price:         it.Price,
			Amount:        amount,
		})
	}
	return products, nil
}

// lineItemsFromDTO 把 DTO 行项目转为校验用纯数据（金额在计算阶段单独处理）。
func lineItemsFromDTO(items []dto.OpportunityProductItem) []lineItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]lineItem, 0, len(items))
	for _, it := range items {
		out = append(out, lineItem{ProductID: it.ProductID, Quantity: it.Quantity, Price: it.Price})
	}
	return out
}

// sumOfProducts 累加已算好的行项目金额（创建响应回显用）。
func sumOfProducts(products []model.OpportunityProduct) money.Money {
	amounts := make([]money.Money, 0, len(products))
	for _, p := range products {
		amounts = append(amounts, p.Amount)
	}
	total, err := SumOpportunityLineAmounts(amounts)
	if err != nil {
		return money.Money{}
	}
	return total
}

// fillOpportunityNames 批量补齐负责人昵称与部门名称（列表展示用）。
func (s *Service) fillOpportunityNames(ctx context.Context, list []model.Opportunity) {
	userIDs, deptIDs := collectOpportunityOwnerIDs(list)
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	for i := range list {
		list[i].OwnerUserName = names[list[i].OwnerUserID]
		list[i].OwnerDeptName = deptNames[list[i].OwnerDeptID]
	}
}

// fillOpportunityName 补齐单个商机的负责人/部门/客户名称（创建/详情返回值用）。
func (s *Service) fillOpportunityName(ctx context.Context, o *model.Opportunity) {
	userIDs, deptIDs := collectOpportunityOwnerIDs([]model.Opportunity{*o})
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	o.OwnerUserName = names[o.OwnerUserID]
	o.OwnerDeptName = deptNames[o.OwnerDeptID]
	if o.CustomerID > 0 {
		customers, err := s.repo.FindCustomersByIDs(ctx, []int64{o.CustomerID})
		if err == nil && len(customers) > 0 {
			o.CustomerName = customers[0].Name
		}
	}
}

// collectOpportunityOwnerIDs 收集非零的负责人用户 ID 与部门 ID。
func collectOpportunityOwnerIDs(list []model.Opportunity) (userIDs, deptIDs []int64) {
	userIDs = make([]int64, 0, len(list))
	deptIDs = make([]int64, 0, len(list))
	for _, o := range list {
		if o.OwnerUserID > 0 {
			userIDs = append(userIDs, o.OwnerUserID)
		}
		if o.OwnerDeptID > 0 {
			deptIDs = append(deptIDs, o.OwnerDeptID)
		}
	}
	return userIDs, deptIDs
}
