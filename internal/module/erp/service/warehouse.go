// warehouse.go 是仓库的业务方法（issue #89，spec §3.1/§4.10）：
// 分页 CRUD + owner 数据范围过滤；principal 仅展示不做校验；删除跑引用校验
// 扩展点（party_refs.go，erp_warehouse 引用类型，单据/库存域 #90+ 注册后生效）；
// Excel 导出见 party_excel.go。
package service

import (
	"context"
	"math"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListWarehouses 分页查询仓库（筛选 + owner 数据范围）。
func (s *Service) ListWarehouses(ctx context.Context, req *dto.WarehousePageReq) ([]model.Warehouse, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.warehouseFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListWarehouses(ctx, filter, page.Offset(), page.PageSize)
}

// warehouseFilter 构造仓库列表/导出的筛选与数据范围谓词（导出复用同一口径，
// 保证导出内容与当前用户可见范围一致）。
func (s *Service) warehouseFilter(ctx context.Context, req *dto.WarehousePageReq) (repository.WarehouseFilter, error) {
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		return repository.WarehouseFilter{}, xerr.BadRequest("仓库状态只支持 0 启用 / 1 停用")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.WarehouseFilter{}, err
	}
	filter := repository.WarehouseFilter{
		Name:   req.Name,
		Status: req.Status,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetWarehouse 查询仓库详情（不存在 404）。
func (s *Service) GetWarehouse(ctx context.Context, id int64) (*model.Warehouse, error) {
	return s.findWarehouse(ctx, id)
}

// CreateWarehouse 创建仓库：状态枚举校验；owner = 创建人（spec §4.10），
// 部门快照随 owner 写入。
func (s *Service) CreateWarehouse(ctx context.Context, req *dto.WarehouseCreateReq) (*model.Warehouse, error) {
	status, sort, err := statusAndSort(req.Status, req.Sort)
	if err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	row := &model.Warehouse{
		Name:        req.Name,
		Address:     req.Address,
		Principal:   req.Principal,
		Sort:        sort,
		Status:      status,
		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
	}
	if err := s.repo.CreateWarehouse(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateWarehouse 修改仓库：状态枚举校验同创建；字段全量覆盖；owner 不随更新变化。
func (s *Service) UpdateWarehouse(ctx context.Context, req *dto.WarehouseUpdateReq) error {
	row, err := s.findWarehouse(ctx, req.ID)
	if err != nil {
		return err
	}
	status, sort, err := statusAndSort(req.Status, req.Sort)
	if err != nil {
		return err
	}
	row.Name = req.Name
	row.Address = req.Address
	row.Principal = req.Principal
	row.Sort = sort
	row.Status = status
	return s.repo.SaveWarehouse(ctx, row)
}

// DeleteWarehouse 软删除仓库；删除前跑引用校验扩展点（party_refs.go），
// erp_warehouse 引用类型当前无单据/库存引用源（#90+ 注册后自动生效）。
func (s *Service) DeleteWarehouse(ctx context.Context, id int64) error {
	row, err := s.findWarehouse(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkWarehouseRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteWarehouse(ctx, row.ID)
}

// findWarehouse 取仓库并做可见性校验：不存在/数据范围外一律 404（不泄露存在性，spec §4.10）。
func (s *Service) findWarehouse(ctx context.Context, id int64) (*model.Warehouse, error) {
	row, err := s.repo.FindWarehouseByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("仓库不存在")
	}
	if err := s.checkRowVisible(ctx, "仓库", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// statusAndSort 归一主数据的 status/sort 入参：status 缺省启用、枚举非法 400；
// sort 缺省 0，且必须落在 MySQL int（int32）列域内（#98 审查 MINOR：超出会让
// 落库报 1264 → 500）。仓库/客户/供应商/结算账户共用（状态口径见迁移 00029：
// 0 启用 / 1 停用）。
func statusAndSort(status *int8, sort *int) (int8, int, error) {
	st := model.StatusEnable
	if status != nil {
		if !model.ValidStatus(*status) {
			return 0, 0, xerr.BadRequest("状态只支持 0 启用 / 1 停用")
		}
		st = *status
	}
	so := 0
	if sort != nil {
		if err := validateSortRange(*sort); err != nil {
			return 0, 0, err
		}
		so = *sort
	}
	return st, so, nil
}

// validateSortRange 校验排序值落在 MySQL int（int32）列域内（#98 审查 MINOR）。
func validateSortRange(sort int) error {
	if sort < math.MinInt32 || sort > math.MaxInt32 {
		return xerr.BadRequest("排序值超出范围（-2147483648 ~ 2147483647）")
	}
	return nil
}
