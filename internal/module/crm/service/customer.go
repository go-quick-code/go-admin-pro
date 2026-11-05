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
	"github.com/lautung/go-admin-pro/internal/pkg/excel"
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
	// UserHasPermission 判断用户是否持有指定权限码（super_admin 以 "*" 通配）。
	// 公海释放/转移的「负责人本人 或 权限码」组合校验使用（issue #13）。
	UserHasPermission(ctx context.Context, userID int64, permission string) (bool, error)
	// ListEnabledTenantIDs 返回全部启用租户的 ID（系统级，无租户过滤）。
	// 公海自动回收 job 遍历租户用（issue #14）；租户注册表不属于租户插件
	// 管辖，实现侧对本次查询显式 tenant.Skip。
	ListEnabledTenantIDs(ctx context.Context) ([]int64, error)
}

// Service 汇聚 crm 模块的业务能力。
type Service struct {
	repo *repository.Repository
	sys  SystemGateway
	// productRefs 是产品删除引用校验扩展点（issue #11，#15/#16 落地行项目后，
	// app 装配处以 RefCheckerChain 注册商机 + 合同两侧实现），见 product_refs.go。
	productRefs ProductRefCountChecker
	// contracts 是公海回收的合同排除依赖面（#14 接点，#16 落地后由 app 装配处
	// SetContractChecker 注入真实实现，缺省 NoopContractChecker 恒「无合同」）。
	contracts ContractChecker
	// approvals 是合同审批依赖面（#16，v1 直通实现，bpm 上线后 SetApprovalService
	// 替换，见 approval.go 文件头注释）。
	approvals ApprovalService
	// voidGuard 是作废/删除合同的守卫扩展点（#16 预留，#18 接
	// 「有回款记录禁止作废/删除」，见 contract_guard.go）。
	voidGuard VoidContractGuard
	// files 是合同附件存储依赖面（issue #17，由 app 装配 infra 适配器，
	// 见 contract_file.go；未装配时附件接口显式报错）。
	files ContractFileStore
	// Excel 导入 guard（issue #20）：应用级组件，固定 10MB/5000 行/100 条明细
	// 与 80MB 解压上限，按实体限制并发解析数（excel.GuardDefaultConcurrent）。
	// 见 excel.go 与 internal/pkg/excel/import_guard.go。
	customerImports *excel.ImportGuard[dto.CustomerImportRow]
	contactImports  *excel.ImportGuard[dto.ContactImportRow]
	productImports  *excel.ImportGuard[dto.ProductImportRow]
}

// New 创建 crm Service。合同回收排除缺省空实现（真实实现由 app 装配处注入）；
// 审批缺省 v1 直通实现；作废守卫缺省空实现；导入 guard 使用固定限额与默认并发。
func New(repo *repository.Repository, sys SystemGateway) *Service {
	return &Service{
		repo:            repo,
		sys:             sys,
		contracts:       NoopContractChecker{},
		approvals:       NewPassthroughApprovalService(repo, sys),
		customerImports: excel.NewImportGuard[dto.CustomerImportRow](excel.GuardDefaultConcurrent),
		contactImports:  excel.NewImportGuard[dto.ContactImportRow](excel.GuardDefaultConcurrent),
		productImports:  excel.NewImportGuard[dto.ProductImportRow](excel.GuardDefaultConcurrent),
	}
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
	filter, err := s.customerFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}

	list, total, err := s.repo.ListCustomers(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillOwnerNames(ctx, list)
	return list, total, nil
}

// customerFilter 构造客户列表/导出的筛选与数据范围谓词（导出复用同一口径，
// 保证导出内容与当前用户可见范围一致）。
func (s *Service) customerFilter(ctx context.Context, req *dto.CustomerPageReq) (repository.CustomerFilter, error) {
	if req.Status != nil {
		if err := ValidateCustomerStatus(*req.Status); err != nil {
			return repository.CustomerFilter{}, err
		}
	}

	filter := repository.CustomerFilter{
		Name:        req.Name,
		Status:      req.Status,
		OwnerUserID: req.OwnerUserID,
		OwnerDeptID: req.OwnerDeptID,
		// 普通客户列表排除公海客户（owner IS NULL）：公海在独立页面按
		// crm:customer:pool:view 展示（issue #13）。
		ExcludePool: true,
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.CustomerFilter{}, err
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
	return filter, nil
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
		if err := tx.CreateCustomer(ctx, c); err != nil {
			return err
		}
		// 规格 §4.1：负责人变更写历史；创建即首任归属，落 create 行。
		return tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
			TenantID:       currentTenantID(ctx),
			CustomerID:     c.ID,
			Action:         model.OwnerHistoryCreate,
			ToOwnerUserID:  ownerID,
			ToOwnerDeptID:  deptID,
			OperatorUserID: CurrentUserID(ctx),
		})
	})
	if err != nil {
		return nil, err
	}
	s.fillOwnerName(ctx, c)
	return c, nil
}

// UpdateCustomer 修改客户：数据范围外一律 404；编号不可改；ownerUserId
// 传 0 保持现负责人，传非 0 值视为一次负责人转移（规格 §4.1：负责人变更写
// 历史，issue #13）——走条件更新 + 历史同事务，防并发覆盖；公海客户不能在
// 编辑里直接指定负责人，获取负责人必须走认领接口（受认领上限与互斥约束）。
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
	if c.OwnerUserID == 0 && req.OwnerUserID > 0 {
		return xerr.BadRequest("公海客户请先认领，再修改负责人")
	}
	if req.OwnerUserID > 0 && req.OwnerUserID != c.OwnerUserID {
		newDept, err := s.ownerDeptSnapshot(ctx, req.OwnerUserID)
		if err != nil {
			return err
		}
		fromOwner, fromDept := c.OwnerUserID, c.OwnerDeptID
		err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
			rows, err := tx.TransferCustomerOwner(ctx, c.ID, fromOwner, req.OwnerUserID, newDept)
			if err != nil {
				return err
			}
			if rows == 0 {
				return xerr.Conflict("客户负责人已变更，请刷新后重试")
			}
			return tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
				TenantID:        currentTenantID(ctx),
				CustomerID:      c.ID,
				Action:          model.OwnerHistoryTransfer,
				FromOwnerUserID: fromOwner,
				FromOwnerDeptID: fromDept,
				ToOwnerUserID:   req.OwnerUserID,
				ToOwnerDeptID:   newDept,
				OperatorUserID:  CurrentUserID(ctx),
			})
		})
		if err != nil {
			return err
		}
		c.OwnerUserID = req.OwnerUserID
		c.OwnerDeptID = newDept
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
