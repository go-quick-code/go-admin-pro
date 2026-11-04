// Package service 是 crm 模块的业务层：客户 CRUD、编号发放与数据范围过滤。
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/authn"
	"github.com/lautung/go-admin-pro/internal/pkg/datascope"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// SystemGateway 是 crm 对 system 模块的依赖面（依赖接口而非实现，避免反向耦合）：
// 数据范围解析（internal/pkg/datascope 引擎的宿主）与负责人归属快照所需的用户/部门信息。
// 由 internal/app 装配时注入 system 模块的 Service 实现。
type SystemGateway interface {
	// DataScopeFilter 计算当前用户的数据范围谓词（多角色并集，见 ADR 0004）。
	DataScopeFilter(ctx context.Context, userID int64) (datascope.Filter, error)
	// UserDeptID 返回用户所属部门 ID；用户不存在时 ok=false。
	UserDeptID(ctx context.Context, userID int64) (deptID int64, ok bool, err error)
	// UserNicknames 批量返回用户昵称（列表/详情展示补齐）。
	UserNicknames(ctx context.Context, ids []int64) (map[int64]string, error)
	// DeptNames 批量返回部门名称。
	DeptNames(ctx context.Context, ids []int64) (map[int64]string, error)
}

// Service 汇聚 crm 模块的业务能力。
type Service struct {
	repo *repository.Repository
	sys  SystemGateway
}

// New 创建 crm Service。
func New(repo *repository.Repository, sys SystemGateway) *Service {
	return &Service{repo: repo, sys: sys}
}

// CurrentUserID 返回 context 中的当前用户 ID。
func CurrentUserID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.UserID
	}
	return 0
}

// currentTenantID 返回 context 中的当前租户 ID；租户中间件保证登录链路必有值。
func currentTenantID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.TenantID
	}
	return 0
}

// ListCustomers 分页查询客户，按数据范围谓词过滤（行级权限引擎，见 internal/pkg/datascope）：
// CRM 主表的谓词列是 owner_dept_id（部门类范围）与 owner_user_id（仅本人）。
func (s *Service) ListCustomers(ctx context.Context, req *dto.CustomerPageReq) ([]model.Customer, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	if req.Status != nil {
		if err := ValidateCustomerStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}

	filter := repository.CustomerFilter{
		Name:        req.Name,
		Status:      req.Status,
		OwnerUserID: req.OwnerUserID,
		OwnerDeptID: req.OwnerDeptID,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	if !scope.All {
		if scope.IncludeSelf {
			filter.SelfOwnerUserID = scope.UserID
		}
		switch {
		case len(scope.DeptIDs) > 0:
			filter.OwnerDeptIDs = scope.DeptIDs
		case !scope.IncludeSelf:
			// 无可见部门且不含本人：谓词恒假（fail-closed，绝不回退全部）。
			filter.OwnerDeptIDs = []int64{}
		}
	}

	list, total, err := s.repo.ListCustomers(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillOwnerNames(ctx, list)
	return list, total, nil
}

// GetCustomer 查询客户详情。数据范围外的客户一律 404：
// 与列表可见性保持一致——看不见的即不存在，不泄露存在性。
func (s *Service) GetCustomer(ctx context.Context, id int64) (*model.Customer, error) {
	c, err := s.findVisibleCustomer(ctx, id)
	if err != nil {
		return nil, err
	}
	s.fillOwnerName(ctx, c)
	return c, nil
}

// CreateCustomer 创建客户：编号与落库同事务（失败回滚、不消耗序号），
// 负责人缺省当前登录用户，部门快照随负责人写入（ADR 0004）。
func (s *Service) CreateCustomer(ctx context.Context, req *dto.CustomerCreateReq) (*model.Customer, error) {
	status := model.CustomerStatusPotential
	if req.Status != nil {
		if err := ValidateCustomerStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	ownerID := req.OwnerUserID
	if ownerID == 0 {
		// 无负责人即公海客户；公海页面与认领在后续工单（#9）落地，
		// 本切片创建时缺省归属创建人，避免「建完即不可见」的空窗。
		ownerID = CurrentUserID(ctx)
	}
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	dateKey := DateKey(now)
	c := &model.Customer{
		Name:        req.Name,
		Industry:    req.Industry,
		Level:       req.Level,
		Source:      req.Source,
		Phone:       req.Phone,
		Website:     req.Website,
		Address:     req.Address,
		Status:      status,
		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
		Remark:      req.Remark,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeCustomer, dateKey)
		if err != nil {
			return err
		}
		c.CustomerNo = FormatCustomerNo(dateKey, seq)
		return tx.CreateCustomer(ctx, c)
	})
	if err != nil {
		return nil, err
	}
	s.fillOwnerName(ctx, c)
	return c, nil
}

// UpdateCustomer 修改客户：数据范围外一律 404；编号不可改；ownerUserId
// 传 0 保持现负责人，传非 0 值重落部门快照。
func (s *Service) UpdateCustomer(ctx context.Context, req *dto.CustomerUpdateReq) error {
	c, err := s.findVisibleCustomer(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if err := ValidateCustomerStatus(*req.Status); err != nil {
			return err
		}
		c.Status = *req.Status
	}
	if req.OwnerUserID > 0 && req.OwnerUserID != c.OwnerUserID {
		deptID, err := s.ownerDeptSnapshot(ctx, req.OwnerUserID)
		if err != nil {
			return err
		}
		c.OwnerUserID = req.OwnerUserID
		c.OwnerDeptID = deptID
	}
	c.Name = req.Name
	c.Industry = req.Industry
	c.Level = req.Level
	c.Source = req.Source
	c.Phone = req.Phone
	c.Website = req.Website
	c.Address = req.Address
	c.Remark = req.Remark
	return s.repo.SaveCustomer(ctx, c)
}

// DeleteCustomer 软删除客户。删除约束（未作废合同禁止删除）在合同工单落地后补充。
func (s *Service) DeleteCustomer(ctx context.Context, id int64) error {
	c, err := s.findVisibleCustomer(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteCustomer(ctx, c.ID)
}

// findVisibleCustomer 取客户并做数据范围校验：不存在或范围外都返回 404。
func (s *Service) findVisibleCustomer(ctx context.Context, id int64) (*model.Customer, error) {
	c, err := s.repo.FindCustomerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, xerr.NotFound("客户不存在")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(c.OwnerDeptID, c.OwnerUserID) {
		return nil, xerr.NotFound("客户不存在")
	}
	return c, nil
}

// ownerDeptSnapshot 解析负责人的部门快照；负责人必须是当前租户内存在的用户。
func (s *Service) ownerDeptSnapshot(ctx context.Context, ownerUserID int64) (int64, error) {
	deptID, ok, err := s.sys.UserDeptID(ctx, ownerUserID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, xerr.BadRequest(fmt.Sprintf("负责人不存在：用户 %d", ownerUserID))
	}
	return deptID, nil
}

// fillOwnerNames 批量补齐负责人昵称与部门名称，供列表展示。
// 必须按下标回写：for range 的元素是副本，改副本不影响调用方的切片。
func (s *Service) fillOwnerNames(ctx context.Context, list []model.Customer) {
	userIDs, deptIDs := collectOwnerIDs(list)
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	for i := range list {
		list[i].OwnerUserName = names[list[i].OwnerUserID]
		list[i].OwnerDeptName = deptNames[list[i].OwnerDeptID]
	}
}

// fillOwnerName 补齐单个客户的负责人昵称与部门名称（创建/详情返回值用）。
func (s *Service) fillOwnerName(ctx context.Context, c *model.Customer) {
	userIDs, deptIDs := collectOwnerIDs([]model.Customer{*c})
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	c.OwnerUserName = names[c.OwnerUserID]
	c.OwnerDeptName = deptNames[c.OwnerDeptID]
}

// collectOwnerIDs 收集非零的负责人用户 ID 与部门 ID（快照列为 0 表示无归属）。
func collectOwnerIDs(list []model.Customer) (userIDs, deptIDs []int64) {
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

// fetchOwnerNames 批量拉取名称映射；网关查询失败时降级为空映射（缺名称不阻塞查询）。
func (s *Service) fetchOwnerNames(ctx context.Context, userIDs, deptIDs []int64) (names, deptNames map[int64]string) {
	names = map[int64]string{}
	if len(userIDs) > 0 {
		if m, err := s.sys.UserNicknames(ctx, userIDs); err == nil {
			names = m
		}
	}
	deptNames = map[int64]string{}
	if len(deptIDs) > 0 {
		if m, err := s.sys.DeptNames(ctx, deptIDs); err == nil {
			deptNames = m
		}
	}
	return names, deptNames
}
