package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/datascope"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------

// owner-scoped：列表套数据范围谓词（owner_dept_id / owner_user_id，ADR 0004），
// 详情/改/删 scope.Permits，范围外一律 404——与列表可见性一致（看不见的即不存在）。
// ListProducts 分页查询产品，按数据范围谓词过滤。
func (s *Service) ListProducts(ctx context.Context, req *dto.ProductPageReq) ([]model.Product, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}

	filter := repository.ProductFilter{
		Name:   req.Name,
		Unit:   req.Unit,
		Status: req.Status,
	}

	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	applyProductScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)

	list, total, err := s.repo.ListProducts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillProductOwnerNames(ctx, list)
	return list, total, nil
}

// GetProduct 查询产品详情。数据范围外的产品一律 404：
// 与列表可见性保持一致——看不见的即不存在，不泄露存在性。
func (s *Service) GetProduct(ctx context.Context, id int64) (*model.Product, error) {
	row, err := s.findVisibleProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	s.fillProductOwnerName(ctx, row)
	return row, nil
}

// CreateProduct 创建产品。负责人缺省当前登录用户，部门快照随负责人写入（ADR 0004）。
func (s *Service) CreateProduct(ctx context.Context, req *dto.ProductCreateReq) (*model.Product, error) {
	Status := int8(1)
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return nil, err
		}
		Status = *req.Status
	}

	ownerID := req.OwnerUserID
	if ownerID == 0 {
		// 无负责人缺省归属创建人，避免「建完即不可见」的空窗。
		ownerID = CurrentUserID(ctx)
	}
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	row := &model.Product{
		Name:        req.Name,
		Category:    req.Category,
		Unit:        req.Unit,
		Price:       req.Price,
		Status:      Status,
		Remark:      req.Remark,
		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
	}
	if err := s.repo.CreateProduct(ctx, row); err != nil {
		return nil, err
	}
	s.fillProductOwnerName(ctx, row)
	return row, nil
}

// UpdateProduct 修改产品：数据范围外一律 404；ownerUserId 传 0 保持现负责人，传非 0 值重落部门快照。
func (s *Service) UpdateProduct(ctx context.Context, req *dto.ProductUpdateReq) error {
	row, err := s.findVisibleProduct(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return err
		}
		row.Status = *req.Status
	}
	if req.OwnerUserID > 0 && req.OwnerUserID != row.OwnerUserID {
		deptID, err := s.ownerDeptSnapshot(ctx, req.OwnerUserID)
		if err != nil {
			return err
		}
		row.OwnerUserID = req.OwnerUserID
		row.OwnerDeptID = deptID
	}
	row.Name = req.Name
	row.Category = req.Category
	row.Unit = req.Unit
	row.Price = req.Price
	row.Remark = req.Remark
	return s.repo.SaveProduct(ctx, row)
}

// DeleteProduct 软删除产品。数据范围外一律 404。
func (s *Service) DeleteProduct(ctx context.Context, id int64) error {
	row, err := s.findVisibleProduct(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteProduct(ctx, row.ID)
}

// findVisibleProduct 取产品并做可见性校验：不存在或范围外都返回 404。
func (s *Service) findVisibleProduct(ctx context.Context, id int64) (*model.Product, error) {
	row, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("产品不存在")
	}

	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(row.OwnerDeptID, row.OwnerUserID) {
		return nil, xerr.NotFound("产品不存在")
	}
	return row, nil
}

// applyProductScope 把 datascope.Filter 翻译成 product 的 owner_* 谓词：
// 空切片 + 无 self = 恒假（fail-closed，绝不回退全部）。
func applyProductScope(scope datascope.Filter, ownerDeptIDs *[]int64, selfOwnerUserID *int64) {
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
		*ownerDeptIDs = []int64{}
	}
}

// fillProductOwnerNames 批量补齐负责人昵称与部门名称，供列表展示。
// 必须按下标回写：for range 的元素是副本，改副本不影响调用方的切片。
func (s *Service) fillProductOwnerNames(ctx context.Context, list []model.Product) {
	userIDs, deptIDs := collectProductOwnerIDs(list)
	names, deptNames := s.fetchProductOwnerNames(ctx, userIDs, deptIDs)
	for i := range list {
		list[i].OwnerUserName = names[list[i].OwnerUserID]
		list[i].OwnerDeptName = deptNames[list[i].OwnerDeptID]
	}
}

// fillProductOwnerName 补齐单个产品的负责人昵称与部门名称（创建/详情返回值用）。
func (s *Service) fillProductOwnerName(ctx context.Context, row *model.Product) {
	userIDs, deptIDs := collectProductOwnerIDs([]model.Product{*row})
	names, deptNames := s.fetchProductOwnerNames(ctx, userIDs, deptIDs)
	row.OwnerUserName = names[row.OwnerUserID]
	row.OwnerDeptName = deptNames[row.OwnerDeptID]
}

// collectProductOwnerIDs 收集非零的负责人用户 ID 与部门 ID（快照列为 0 表示无归属）。
func collectProductOwnerIDs(list []model.Product) (userIDs, deptIDs []int64) {
	userIDs = make([]int64, 0, len(list))
	deptIDs = make([]int64, 0, len(list))
	for _, row := range list {
		if row.OwnerUserID > 0 {
			userIDs = append(userIDs, row.OwnerUserID)
		}
		if row.OwnerDeptID > 0 {
			deptIDs = append(deptIDs, row.OwnerDeptID)
		}
	}
	return userIDs, deptIDs
}

// fetchProductOwnerNames 批量拉取名称映射；网关查询失败时降级为空映射（缺名称不阻塞查询）。
func (s *Service) fetchProductOwnerNames(ctx context.Context, userIDs, deptIDs []int64) (names, deptNames map[int64]string) {
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

// TODO(#20): Excel 导入导出接线（internal/pkg/excel，docs/crm-mvp-spec.md §7）——
// 本文件生成时仅预留配置位与注释，不产出可用实现：
//   - 导入：模板下载 + 先全量校验（行号+原因可读），有错整批拒绝；上限 5000 行/10MB；
//   - 导出：复用 ListProducts 的筛选与数据范围谓词，导出内容与权限一致。
