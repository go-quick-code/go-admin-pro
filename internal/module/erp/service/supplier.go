// supplier.go 是供应商的业务方法（issue #89，spec §3.1/§4.10）：分页 CRUD +
// 全展示字段（税务/开户信息）+ owner 数据范围过滤；删除跑引用校验扩展点
// （party_refs.go，erp_supplier 引用类型，#91 采购域注册后生效）；Excel 导出
// 见 party_excel.go。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListSuppliers 分页查询供应商（筛选 + owner 数据范围）。
func (s *Service) ListSuppliers(ctx context.Context, req *dto.SupplierPageReq) ([]model.Supplier, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.supplierFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListSuppliers(ctx, filter, page.Offset(), page.PageSize)
}

// supplierFilter 构造供应商列表/导出的筛选与数据范围谓词（导出复用同一口径）。
func (s *Service) supplierFilter(ctx context.Context, req *dto.SupplierPageReq) (repository.SupplierFilter, error) {
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		return repository.SupplierFilter{}, xerr.BadRequest("供应商状态只支持 0 启用 / 1 停用")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.SupplierFilter{}, err
	}
	filter := repository.SupplierFilter{
		Name:   req.Name,
		Mobile: req.Mobile,
		Status: req.Status,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetSupplier 查询供应商详情（不存在 404）。
func (s *Service) GetSupplier(ctx context.Context, id int64) (*model.Supplier, error) {
	return s.findSupplier(ctx, id)
}

// CreateSupplier 创建供应商：状态/税率校验；owner = 创建人（spec §4.10），
// 部门快照随 owner 写入。
func (s *Service) CreateSupplier(ctx context.Context, req *dto.SupplierCreateReq) (*model.Supplier, error) {
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
	row := &model.Supplier{
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
	if err := s.repo.CreateSupplier(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateSupplier 修改供应商：状态/税率校验同创建；字段全量覆盖；owner 不随更新变化。
func (s *Service) UpdateSupplier(ctx context.Context, req *dto.SupplierUpdateReq) error {
	row, err := s.findSupplier(ctx, req.ID)
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
	return s.repo.SaveSupplier(ctx, row)
}

// DeleteSupplier 软删除供应商；删除前跑引用校验扩展点（party_refs.go），
// erp_supplier 引用类型当前无单据引用源（#91 注册后自动生效）。
func (s *Service) DeleteSupplier(ctx context.Context, id int64) error {
	row, err := s.findSupplier(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkSupplierRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteSupplier(ctx, row.ID)
}

// findSupplier 取供应商并做可见性校验：不存在/数据范围外一律 404（不泄露存在性，spec §4.10）。
func (s *Service) findSupplier(ctx context.Context, id int64) (*model.Supplier, error) {
	row, err := s.repo.FindSupplierByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("供应商不存在")
	}
	if err := s.checkRowVisible(ctx, "供应商", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}
