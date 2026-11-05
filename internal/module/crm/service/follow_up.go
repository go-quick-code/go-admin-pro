package service

import (
	"context"
	"strconv"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 跟进记录·客户维度（issue #12，#15 扩展商机维度，#16 扩展合同维度）----------
// 跟进自身没有负责人：列表 join 父记录套父谓词，详情/修改/删除先解析所属
// 对象的数据范围（scope.Permits 作用于父记录 owner_*），范围外一律 404，
// 与列表可见性保持一致（看不见的即不存在，不泄露存在性）。
//
// 编辑/删除的权限口径（与联系人差异，见提交说明）：联系人只认按钮权限码；
// 跟进在按钮码之外额外放行「创建人本人」——销售对自己的跟进记录随时可改可删，
// 不依赖管理员配按钮。组合校验在 service 层做（同 #13 释放/转移的 canOperateOwner），
// 因此 PUT/DELETE 路由不挂权限中间件。

// 修改/删除跟进的权限码：与 api 路由种子（internal/seed）保持一致。
const (
	PermFollowUpUpdate = "crm:follow-up:update"
	PermFollowUpDelete = "crm:follow-up:delete"
)

// ListFollowUps 分页查询跟进，可见性跟随所属对象（客户/商机/合同，spec §4.1）。
func (s *Service) ListFollowUps(ctx context.Context, req *dto.FollowUpPageReq) ([]model.FollowUp, int64, error) {
	if err := ValidateFollowUpType(req.Type); err != nil {
		return nil, 0, err
	}
	if countParents(req.CustomerID, req.OpportunityID, req.ContractID) > 1 {
		return nil, 0, xerr.BadRequest("customerId、opportunityId、contractId 只能选一个筛选")
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()

	filter := repository.FollowUpFilter{
		CustomerID:    req.CustomerID,
		OpportunityID: req.OpportunityID,
		ContractID:    req.ContractID,
		Type:          req.Type,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	applyCustomerScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)

	list, total, err := s.repo.ListFollowUps(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillFollowUpParentNames(ctx, list)
	return list, total, nil
}

// GetFollowUp 查询跟进详情；客户范围外 404。
func (s *Service) GetFollowUp(ctx context.Context, id int64) (*model.FollowUp, error) {
	followUp, err := s.findVisibleFollowUp(ctx, id)
	if err != nil {
		return nil, err
	}
	s.fillFollowUpParentName(ctx, followUp)
	return followUp, nil
}

// CreateFollowUp 创建跟进：挂客户/商机/合同三选一（多传/都不传 400），
// 所属对象必须存在且对当前用户可见（404 兜底），类型/下次跟进时间先做校验（400）；
// 创建与客户 last_follow_time 刷新同事务（spec §4.2，repository.CreateFollowUp）。
func (s *Service) CreateFollowUp(ctx context.Context, req *dto.FollowUpCreateReq) (*model.FollowUp, error) {
	if err := ValidateFollowUpType(req.Type); err != nil {
		return nil, err
	}
	nextTime, err := ValidateFollowUpNextTime(req.NextFollowTime, s.businessLocation())
	if err != nil {
		return nil, err
	}
	if countParents(req.CustomerID, req.OpportunityID, req.ContractID) != 1 {
		return nil, xerr.BadRequest("请选择跟进对象：customerId、opportunityId、contractId 三选一")
	}
	followUp := &model.FollowUp{
		CustomerID:    req.CustomerID,
		OpportunityID: req.OpportunityID,
		ContractID:    req.ContractID,
		Content:       req.Content,
		NextTime:      nextTime,
	}
	if req.Type != nil {
		followUp.Type = *req.Type
	} else {
		followUp.Type = model.FollowUpTypeOther
	}
	// 所属对象必须存在且可见才能写跟进；复用各自的可见性解析（范围外 404）。
	// 挂商机/合同的跟进也刷新其所属客户的 last_follow_time（spec §4.2：跟进
	// 任一子记录都刷新客户的 last_follow_time，商机/合同都是客户的子记录）。
	refreshCustomerID := req.CustomerID
	if req.OpportunityID > 0 {
		opportunity, err := s.findVisibleOpportunity(ctx, req.OpportunityID)
		if err != nil {
			return nil, err
		}
		refreshCustomerID = opportunity.CustomerID
	} else if req.ContractID > 0 {
		contract, err := s.findVisibleContract(ctx, req.ContractID)
		if err != nil {
			return nil, err
		}
		refreshCustomerID = contract.CustomerID
	} else if _, err := s.findVisibleCustomer(ctx, req.CustomerID); err != nil {
		return nil, err
	}
	if err := s.repo.CreateFollowUp(ctx, followUp, refreshCustomerID); err != nil {
		return nil, err
	}
	s.fillFollowUpParentName(ctx, followUp)
	return followUp, nil
}

// countParents 统计已指定的所属对象个数（客户/商机/合同三选一校验用）。
func countParents(customerID, opportunityID, contractID int64) int {
	n := 0
	for _, id := range []int64{customerID, opportunityID, contractID} {
		if id > 0 {
			n++
		}
	}
	return n
}

// UpdateFollowUp 修改跟进：客户范围外 404；创建人本人或持 crm:follow-up:update；
// 所属客户不可漂移；编辑不刷新 last_follow_time（取舍见 repository.CreateFollowUp）。
func (s *Service) UpdateFollowUp(ctx context.Context, req *dto.FollowUpUpdateReq) error {
	followUp, err := s.findVisibleFollowUp(ctx, req.ID)
	if err != nil {
		return err
	}
	if err := s.canOperateFollowUp(ctx, followUp, PermFollowUpUpdate); err != nil {
		return err
	}
	if err := ValidateFollowUpType(req.Type); err != nil {
		return err
	}
	nextTime, err := ValidateFollowUpNextTime(req.NextFollowTime, s.businessLocation())
	if err != nil {
		return err
	}
	followUp.Content = req.Content
	if req.Type != nil {
		followUp.Type = *req.Type
	} else {
		followUp.Type = model.FollowUpTypeOther
	}
	followUp.NextTime = nextTime
	return s.repo.SaveFollowUp(ctx, followUp)
}

// DeleteFollowUp 软删除跟进。客户范围外 404；创建人本人或持 crm:follow-up:delete；
// 删除不回退 last_follow_time（取舍见 repository.CreateFollowUp）。
func (s *Service) DeleteFollowUp(ctx context.Context, id int64) error {
	followUp, err := s.findVisibleFollowUp(ctx, id)
	if err != nil {
		return err
	}
	if err := s.canOperateFollowUp(ctx, followUp, PermFollowUpDelete); err != nil {
		return err
	}
	return s.repo.DeleteFollowUp(ctx, followUp.ID)
}

// findVisibleFollowUp 取跟进并做可见性校验：按所属对象解析数据范围
// （挂客户套客户谓词，挂商机/合同套各自父记录谓词，#15/#16），跟进不存在
// （含已删除）或父记录不存在/范围外都返回 404。
func (s *Service) findVisibleFollowUp(ctx context.Context, id int64) (*model.FollowUp, error) {
	followUp, err := s.repo.FindFollowUpByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if followUp == nil {
		return nil, xerr.NotFound("跟进记录不存在")
	}
	var deptID, userID int64
	if followUp.CustomerID > 0 {
		customer, err := s.repo.FindCustomerByID(ctx, followUp.CustomerID)
		if err != nil {
			return nil, err
		}
		if customer == nil {
			// 父客户已删除：子记录不级联硬删，但随之不可见（spec §4.5）。
			return nil, xerr.NotFound("跟进记录不存在")
		}
		deptID, userID = customer.OwnerDeptID, customer.OwnerUserID
	} else if followUp.OpportunityID > 0 {
		opportunity, err := s.repo.FindOpportunityByID(ctx, followUp.OpportunityID)
		if err != nil {
			return nil, err
		}
		if opportunity == nil {
			// 父商机已删除：跟进随之不可见（spec §4.5，与客户路径同一口径）。
			return nil, xerr.NotFound("跟进记录不存在")
		}
		deptID, userID = opportunity.OwnerDeptID, opportunity.OwnerUserID
	} else if followUp.ContractID > 0 {
		contract, err := s.repo.FindContractByID(ctx, followUp.ContractID)
		if err != nil {
			return nil, err
		}
		if contract == nil {
			// 父合同已删除：跟进随之不可见（spec §4.5，与客户路径同一口径）。
			return nil, xerr.NotFound("跟进记录不存在")
		}
		deptID, userID = contract.OwnerDeptID, contract.OwnerUserID
	} else {
		return nil, xerr.NotFound("跟进记录不存在")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(deptID, userID) {
		return nil, xerr.NotFound("跟进记录不存在")
	}
	return followUp, nil
}

// canOperateFollowUp 校验当前用户可否编辑/删除该跟进：创建人本人放行；
// 否则须持有对应权限码（super_admin 以 "*" 通配）。口径见文件头注释。
func (s *Service) canOperateFollowUp(ctx context.Context, followUp *model.FollowUp, permission string) error {
	if uid := CurrentUserID(ctx); uid > 0 && strconv.FormatInt(uid, 10) == followUp.Creator {
		return nil
	}
	ok, err := s.sys.UserHasPermission(ctx, CurrentUserID(ctx), permission)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.Forbidden("没有操作权限：" + permission)
	}
	return nil
}

// fillFollowUpParentNames 批量补齐所属客户/商机/合同名称，供列表展示。
func (s *Service) fillFollowUpParentNames(ctx context.Context, list []model.FollowUp) {
	customerIDs := make([]int64, 0, len(list))
	opportunityIDs := make([]int64, 0, len(list))
	contractIDs := make([]int64, 0, len(list))
	seenC, seenO, seenK := make(map[int64]bool, len(list)), make(map[int64]bool, len(list)), make(map[int64]bool, len(list))
	for _, f := range list {
		if f.CustomerID > 0 && !seenC[f.CustomerID] {
			customerIDs = append(customerIDs, f.CustomerID)
			seenC[f.CustomerID] = true
		}
		if f.OpportunityID > 0 && !seenO[f.OpportunityID] {
			opportunityIDs = append(opportunityIDs, f.OpportunityID)
			seenO[f.OpportunityID] = true
		}
		if f.ContractID > 0 && !seenK[f.ContractID] {
			contractIDs = append(contractIDs, f.ContractID)
			seenK[f.ContractID] = true
		}
	}
	customers, err := s.repo.FindCustomersByIDs(ctx, customerIDs)
	if err != nil {
		return // 名称缺失不阻塞查询
	}
	customerNames := make(map[int64]string, len(customers))
	for _, c := range customers {
		customerNames[c.ID] = c.Name
	}
	opportunityNames := map[int64]string{}
	if len(opportunityIDs) > 0 {
		if opportunities, err := s.repo.FindOpportunitiesByIDs(ctx, opportunityIDs); err == nil {
			opportunityNames = make(map[int64]string, len(opportunities))
			for _, o := range opportunities {
				opportunityNames[o.ID] = o.Name
			}
		}
	}
	contractNames := map[int64]string{}
	if len(contractIDs) > 0 {
		if contracts, err := s.repo.FindContractsByIDs(ctx, contractIDs); err == nil {
			contractNames = make(map[int64]string, len(contracts))
			for _, c := range contracts {
				contractNames[c.ID] = c.Name
			}
		}
	}
	for i := range list {
		list[i].CustomerName = customerNames[list[i].CustomerID]
		list[i].OpportunityName = opportunityNames[list[i].OpportunityID]
		list[i].ContractName = contractNames[list[i].ContractID]
	}
}

// fillFollowUpParentName 补齐单个跟进的所属客户/商机/合同名称（创建/详情返回值用）。
func (s *Service) fillFollowUpParentName(ctx context.Context, f *model.FollowUp) {
	if f.CustomerID > 0 {
		customers, err := s.repo.FindCustomersByIDs(ctx, []int64{f.CustomerID})
		if err != nil || len(customers) == 0 {
			return
		}
		f.CustomerName = customers[0].Name
	}
	if f.OpportunityID > 0 {
		opportunities, err := s.repo.FindOpportunitiesByIDs(ctx, []int64{f.OpportunityID})
		if err != nil || len(opportunities) == 0 {
			return
		}
		f.OpportunityName = opportunities[0].Name
	}
	if f.ContractID > 0 {
		contracts, err := s.repo.FindContractsByIDs(ctx, []int64{f.ContractID})
		if err != nil || len(contracts) == 0 {
			return
		}
		f.ContractName = contracts[0].Name
	}
}
