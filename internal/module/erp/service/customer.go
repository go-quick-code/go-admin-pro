// customer.go 是 ERP 客户的业务方法（issue #89，spec §3.1/§4.10）：分页 CRUD +
// 全展示字段（税务/开户信息）+ owner 数据范围过滤；删除跑引用校验扩展点
// （party_refs.go，erp_customer 引用类型，#92 销售域注册后生效）；Excel 导出
// 见 party_excel.go。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// taxPercentMaxCents 是税率列域上限：decimal(5,2) 最大 999.99（99 999 分）。
// money.ParseMoney 只校验 decimal(12,2) 域，落库前须收紧到列域（超限会让 MySQL
// 严格模式直接报错，提前为 400）。
const taxPercentMaxCents int64 = 99_999

// ListCustomers 分页查询 ERP 客户（筛选 + owner 数据范围）。
func (s *Service) ListCustomers(ctx context.Context, req *dto.CustomerPageReq) ([]model.Customer, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.customerFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListCustomers(ctx, filter, page.Offset(), page.PageSize)
}

// customerFilter 构造客户列表/导出的筛选与数据范围谓词（导出复用同一口径）。
func (s *Service) customerFilter(ctx context.Context, req *dto.CustomerPageReq) (repository.CustomerFilter, error) {
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		return repository.CustomerFilter{}, xerr.BadRequest("客户状态只支持 0 启用 / 1 停用")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.CustomerFilter{}, err
	}
	filter := repository.CustomerFilter{
		Name:   req.Name,
		Mobile: req.Mobile,
		Status: req.Status,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetCustomer 查询客户详情（不存在 404）。
func (s *Service) GetCustomer(ctx context.Context, id int64) (*model.Customer, error) {
	return s.findCustomer(ctx, id)
}

// CreateCustomer 创建客户：状态/税率校验；owner = 创建人（spec §4.10），
// 部门快照随 owner 写入。
func (s *Service) CreateCustomer(ctx context.Context, req *dto.CustomerCreateReq) (*model.Customer, error) {
	status, sort, err := statusAndSort(req.Status, req.Sort)
	if err != nil {
		return nil, err
	}
	if err := validateTaxPercent(req.TaxPercent); err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	row := &model.Customer{
		Name:        req.Name,
		Contact:     req.Contact,
		Mobile:      req.Mobile,
		Telephone:   req.Telephone,
		Email:       req.Email,
		Fax:         req.Fax,
		Remark:      req.Remark,
		Status:      status,
		Sort:        sort,
		TaxNo:       req.TaxNo,
		TaxPercent:  req.TaxPercent,
		BankName:    req.BankName,
		BankAccount: req.BankAccount,
		BankAddress: req.BankAddress,
		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
	}
	if err := s.repo.CreateCustomer(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateCustomer 修改客户：状态/税率校验同创建；字段全量覆盖；owner 不随更新变化。
func (s *Service) UpdateCustomer(ctx context.Context, req *dto.CustomerUpdateReq) error {
	row, err := s.findCustomer(ctx, req.ID)
	if err != nil {
		return err
	}
	status, sort, err := statusAndSort(req.Status, req.Sort)
	if err != nil {
		return err
	}
	if err := validateTaxPercent(req.TaxPercent); err != nil {
		return err
	}
	row.Name = req.Name
	row.Contact = req.Contact
	row.Mobile = req.Mobile
	row.Telephone = req.Telephone
	row.Email = req.Email
	row.Fax = req.Fax
	row.Remark = req.Remark
	row.Status = status
	row.Sort = sort
	row.TaxNo = req.TaxNo
	row.TaxPercent = req.TaxPercent
	row.BankName = req.BankName
	row.BankAccount = req.BankAccount
	row.BankAddress = req.BankAddress
	return s.repo.SaveCustomer(ctx, row)
}

// DeleteCustomer 软删除客户；删除前跑引用校验扩展点（party_refs.go），
// erp_customer 引用类型当前无单据引用源（#92 注册后自动生效）。
func (s *Service) DeleteCustomer(ctx context.Context, id int64) error {
	row, err := s.findCustomer(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkCustomerRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteCustomer(ctx, row.ID)
}

// findCustomer 取客户并做可见性校验：不存在/数据范围外一律 404（不泄露存在性，spec §4.10）。
func (s *Service) findCustomer(ctx context.Context, id int64) (*model.Customer, error) {
	row, err := s.repo.FindCustomerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("客户不存在")
	}
	if err := s.checkRowVisible(ctx, "客户", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// validateTaxPercent 校验税率落在 decimal(5,2) 列域且非负（纯展示字段，
// 不做税额计算，spec §1）；nil（未提交）放行落 NULL。
func validateTaxPercent(v *money.Money) error {
	if v == nil {
		return nil
	}
	if cents := v.Cents(); cents < 0 || cents > taxPercentMaxCents {
		return xerr.BadRequest("税率须在 0–999.99 之间（%）")
	}
	return nil
}
