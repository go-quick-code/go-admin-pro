package service

import (
	"context"
	"strconv"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 跟进记录·客户维度（issue #12）----------
// 跟进自身没有负责人：列表 join 父客户套父谓词，详情/修改/删除先解析所属
// 客户的数据范围（scope.Permits 作用于客户 owner_*），客户范围外一律 404，
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

// ListFollowUps 分页查询跟进，可见性跟随所属客户（spec §4.1）。
func (s *Service) ListFollowUps(ctx context.Context, req *dto.FollowUpPageReq) ([]model.FollowUp, int64, error) {
	if err := ValidateFollowUpType(req.Type); err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()

	filter := repository.FollowUpFilter{
		CustomerID: req.CustomerID,
		Type:       req.Type,
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
	s.fillFollowUpCustomerNames(ctx, list)
	return list, total, nil
}

// GetFollowUp 查询跟进详情；客户范围外 404。
func (s *Service) GetFollowUp(ctx context.Context, id int64) (*model.FollowUp, error) {
	followUp, err := s.findVisibleFollowUp(ctx, id)
	if err != nil {
		return nil, err
	}
	s.fillFollowUpCustomerName(ctx, followUp)
	return followUp, nil
}

// CreateFollowUp 在客户下创建跟进：客户必须存在且对当前用户可见（404 兜底），
// 类型/下次跟进时间先做校验（400）；创建与客户 last_follow_time 刷新同事务
// （spec §4.2，repository.CreateFollowUp）。
func (s *Service) CreateFollowUp(ctx context.Context, req *dto.FollowUpCreateReq) (*model.FollowUp, error) {
	if err := ValidateFollowUpType(req.Type); err != nil {
		return nil, err
	}
	nextTime, err := ValidateFollowUpNextTime(req.NextFollowTime)
	if err != nil {
		return nil, err
	}
	// 客户必须存在且可见才能写跟进；复用客户的可见性解析（范围外 404）。
	if _, err := s.findVisibleCustomer(ctx, req.CustomerID); err != nil {
		return nil, err
	}
	followUp := &model.FollowUp{
		CustomerID: req.CustomerID,
		Content:    req.Content,
		NextTime:   nextTime,
	}
	if req.Type != nil {
		followUp.Type = *req.Type
	} else {
		followUp.Type = model.FollowUpTypeOther
	}
	if err := s.repo.CreateFollowUp(ctx, followUp); err != nil {
		return nil, err
	}
	s.fillFollowUpCustomerName(ctx, followUp)
	return followUp, nil
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
	nextTime, err := ValidateFollowUpNextTime(req.NextFollowTime)
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

// findVisibleFollowUp 取跟进并做可见性校验：先解析所属客户的数据范围，
// 跟进不存在（含已删除）或父客户不存在/范围外都返回 404。
func (s *Service) findVisibleFollowUp(ctx context.Context, id int64) (*model.FollowUp, error) {
	followUp, err := s.repo.FindFollowUpByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if followUp == nil {
		return nil, xerr.NotFound("跟进记录不存在")
	}
	customer, err := s.repo.FindCustomerByID(ctx, followUp.CustomerID)
	if err != nil {
		return nil, err
	}
	if customer == nil {
		// 父客户已删除：子记录不级联硬删，但随之不可见（spec §4.5）。
		return nil, xerr.NotFound("跟进记录不存在")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(customer.OwnerDeptID, customer.OwnerUserID) {
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

// fillFollowUpCustomerNames 批量补齐所属客户名称，供列表展示。
func (s *Service) fillFollowUpCustomerNames(ctx context.Context, list []model.FollowUp) {
	ids := make([]int64, 0, len(list))
	seen := make(map[int64]bool, len(list))
	for _, f := range list {
		if f.CustomerID > 0 && !seen[f.CustomerID] {
			ids = append(ids, f.CustomerID)
			seen[f.CustomerID] = true
		}
	}
	customers, err := s.repo.FindCustomersByIDs(ctx, ids)
	if err != nil {
		return // 名称缺失不阻塞查询
	}
	names := make(map[int64]string, len(customers))
	for _, c := range customers {
		names[c.ID] = c.Name
	}
	for i := range list {
		list[i].CustomerName = names[list[i].CustomerID]
	}
}

// fillFollowUpCustomerName 补齐单个跟进的所属客户名称（创建/详情返回值用）。
func (s *Service) fillFollowUpCustomerName(ctx context.Context, f *model.FollowUp) {
	if f.CustomerID <= 0 {
		return
	}
	customers, err := s.repo.FindCustomersByIDs(ctx, []int64{f.CustomerID})
	if err != nil || len(customers) == 0 {
		return
	}
	f.CustomerName = customers[0].Name
}
