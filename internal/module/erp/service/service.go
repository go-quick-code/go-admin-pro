// Package service 是 erp 模块的业务层：主数据 CRUD、引用约束与数据范围过滤。
// 依赖只指向平台层（system 数据范围、Money/qty/pkg、租户插件），不依赖
// crm/oa/bpm（docs/erp-mvp-spec.md §2）；crm/oa 也不依赖 erp（ADR 0015）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/datascope"
)

// SystemGateway 是 erp 对 system 模块的依赖面（依赖接口而非实现，避免反向耦合）：
// 数据范围解析（internal/pkg/datascope 引擎的宿主）与 owner 快照所需的部门归属。
// 由 internal/app 装配时注入 system 模块的 Service 实现（与 crm 同构）。
type SystemGateway interface {
	// DataScopeFilter 计算当前用户的数据范围谓词（多角色并集，见 ADR 0004）。
	DataScopeFilter(ctx context.Context, userID int64) (datascope.Filter, error)
	// UserDeptID 返回用户所属部门 ID；用户不存在时 ok=false。
	UserDeptID(ctx context.Context, userID int64) (deptID int64, ok bool, err error)
}

// Service 汇聚 erp 模块的业务能力。
type Service struct {
	repo *repository.Repository
	sys  SystemGateway
	// productRefs 是产品删除引用校验扩展点（issue #88，spec §4.9；本票为
	// 骨架，单据域引用类型由 #90–#94 注册），见 product_refs.go。
	productRefs ProductRefChecker
}

// New 创建 erp Service。
func New(repo *repository.Repository, sys SystemGateway) *Service {
	return &Service{repo: repo, sys: sys}
}

// CurrentUserID 返回 context 中的当前用户 ID（认证主体来自 authn）。
func CurrentUserID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.UserID
	}
	return 0
}

// ownerDeptSnapshot 取 owner 的部门快照（owner = 创建人，spec §4.10；与 crm
// ownerDeptSnapshot 同构）。用户不存在时报 400（owner 恒有值，不落 0）。
func (s *Service) ownerDeptSnapshot(ctx context.Context, ownerUserID int64) (int64, error) {
	deptID, ok, err := s.sys.UserDeptID(ctx, ownerUserID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, BadRequestOwnerMissing(ownerUserID)
	}
	return deptID, nil
}

// applyScope 把 datascope.Filter 翻译成 owner_* 谓词入参（与 crm scope.go 同构）：
//   - All：不加范围条件；
//   - IncludeSelf：谓词 OR owner_user_id = 本人；
//   - 部门类范围：owner_dept_id IN 可见部门；无可见部门且不含本人时
//     传空切片表达谓词恒假（fail-closed，绝不回退全部）。
func applyScope(scope datascope.Filter, ownerDeptIDs *[]int64, selfOwnerUserID *int64) {
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
