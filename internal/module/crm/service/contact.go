package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/datascope"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 联系人（issue #9）----------
// 联系人自身没有负责人：列表 join 父客户套父谓词，详情/修改/删除先解析所属
// 客户的数据范围（scope.Permits 作用于客户 owner_*），客户范围外一律 404，
// 与列表可见性保持一致（看不见的即不存在，不泄露存在性）。

// ListContacts 分页查询联系人，可见性跟随所属客户（spec §4.1）。
func (s *Service) ListContacts(ctx context.Context, req *dto.ContactPageReq) ([]model.Contact, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()

	filter := repository.ContactFilter{
		CustomerID: req.CustomerID,
		Name:       req.Name,
		Mobile:     req.Mobile,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	applyCustomerScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)

	list, total, err := s.repo.ListContacts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillCustomerNames(ctx, list)
	return list, total, nil
}

// GetContact 查询联系人详情；客户范围外 404。
func (s *Service) GetContact(ctx context.Context, id int64) (*model.Contact, error) {
	contact, err := s.findVisibleContact(ctx, id)
	if err != nil {
		return nil, err
	}
	s.fillCustomerName(ctx, contact)
	return contact, nil
}

// CreateContact 在客户下创建联系人：客户必须存在且对当前用户可见（404 兜底），
// 手机/邮箱/性别先做格式校验（400）。
func (s *Service) CreateContact(ctx context.Context, req *dto.ContactCreateReq) (*model.Contact, error) {
	if err := validateContactFields(req.Mobile, req.Email, req.Gender); err != nil {
		return nil, err
	}
	// 客户必须存在且可见才能建联系人；复用客户的可见性解析（范围外 404）。
	if _, err := s.findVisibleCustomer(ctx, req.CustomerID); err != nil {
		return nil, err
	}
	contact := &model.Contact{
		CustomerID:   req.CustomerID,
		Name:         req.Name,
		Position:     req.Position,
		Mobile:       req.Mobile,
		Email:        req.Email,
		DecisionRole: req.DecisionRole,
		Remark:       req.Remark,
	}
	if req.Gender != nil {
		contact.Gender = *req.Gender
	}
	if err := s.repo.CreateContact(ctx, contact); err != nil {
		return nil, err
	}
	s.fillCustomerName(ctx, contact)
	return contact, nil
}

// UpdateContact 修改联系人：客户范围外 404；所属客户不可漂移。
func (s *Service) UpdateContact(ctx context.Context, req *dto.ContactUpdateReq) error {
	contact, err := s.findVisibleContact(ctx, req.ID)
	if err != nil {
		return err
	}
	if err := validateContactFields(req.Mobile, req.Email, req.Gender); err != nil {
		return err
	}
	contact.Name = req.Name
	contact.Position = req.Position
	contact.Mobile = req.Mobile
	contact.Email = req.Email
	contact.DecisionRole = req.DecisionRole
	contact.Remark = req.Remark
	if req.Gender != nil {
		contact.Gender = *req.Gender
	}
	return s.repo.SaveContact(ctx, contact)
}

// DeleteContact 软删除联系人。客户范围外 404。
func (s *Service) DeleteContact(ctx context.Context, id int64) error {
	contact, err := s.findVisibleContact(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteContact(ctx, contact.ID)
}

// findVisibleContact 取联系人并做可见性校验：先解析所属客户的数据范围，
// 客户不存在（含已删除）或范围外都返回 404。
func (s *Service) findVisibleContact(ctx context.Context, id int64) (*model.Contact, error) {
	contact, err := s.repo.FindContactByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if contact == nil {
		return nil, xerr.NotFound("联系人不存在")
	}
	customer, err := s.repo.FindCustomerByID(ctx, contact.CustomerID)
	if err != nil {
		return nil, err
	}
	if customer == nil {
		// 父客户已删除：子记录不级联硬删，但随之不可见（spec §4.5）。
		return nil, xerr.NotFound("联系人不存在")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(customer.OwnerDeptID, customer.OwnerUserID) {
		return nil, xerr.NotFound("联系人不存在")
	}
	return contact, nil
}

// applyCustomerScope 把 datascope.Filter 翻译成父客户 owner_* 谓词
// （与 ListCustomers 的翻译一致，供联系人 join 路径复用）。
func applyCustomerScope(scope datascope.Filter, ownerDeptIDs *[]int64, selfOwnerUserID *int64) {
	if scope.All {
		return
	}
	if scope.IncludeSelf {
		*selfOwnerUserID = scope.UserID
	}
	switch {
	case len(scope.DeptIDs) > 0:
		*ownerDeptIDs = scope.DeptIDs
	case !scope.IncludeSelf:
		// 无可见部门且不含本人：谓词恒假（fail-closed，绝不回退全部）。
		*ownerDeptIDs = []int64{}
	}
}

// validateContactFields 校验手机/邮箱/性别，非法值返回 400。
func validateContactFields(mobile, email string, gender *int8) error {
	if err := ValidateContactMobile(mobile); err != nil {
		return err
	}
	if err := ValidateContactEmail(email); err != nil {
		return err
	}
	if gender != nil {
		if err := ValidateContactGender(*gender); err != nil {
			return err
		}
	}
	return nil
}

// fillCustomerNames 批量补齐所属客户名称，供列表展示。
func (s *Service) fillCustomerNames(ctx context.Context, list []model.Contact) {
	ids := make([]int64, 0, len(list))
	seen := make(map[int64]bool, len(list))
	for _, c := range list {
		if c.CustomerID > 0 && !seen[c.CustomerID] {
			ids = append(ids, c.CustomerID)
			seen[c.CustomerID] = true
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

// fillCustomerName 补齐单个联系人的所属客户名称（创建/详情返回值用）。
// 名称缺失（客户不可达或查询失败）时置空，不阻塞主流程。
func (s *Service) fillCustomerName(ctx context.Context, c *model.Contact) {
	if c.CustomerID <= 0 {
		return
	}
	customers, err := s.repo.FindCustomersByIDs(ctx, []int64{c.CustomerID})
	if err != nil || len(customers) == 0 {
		return
	}
	c.CustomerName = customers[0].Name
}
