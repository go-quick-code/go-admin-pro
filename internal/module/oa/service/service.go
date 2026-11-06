// Package service 承载 oa 模块的业务能力：共享可见范围判定（issue #43）、
// 工作日历（issue #50）；公告/请假/报销/考勤/日程/会议室/文档等域由后续
// 工单按 docs/oa-mvp-spec.md §2 的模块边界逐个落地。
package service

import (
	"context"

	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// SystemGateway 是 oa 对 system 模块的依赖面（依赖接口而非实现，与 crm 的
// SystemGateway 同构，spec §2：oa 依赖 system 的用户/部门/角色/权限解析）。
// app 装配处直接传入 *systemservice.Service（方法挂在 system/service/gateway.go）。
type SystemGateway interface {
	// UserHasPermission 判断用户是否持有指定权限码（super_admin 以 "*" 通配）。
	UserHasPermission(ctx context.Context, userID int64, permission string) (bool, error)
	// ListDeptIDsByLeader 返回用户担任负责人的部门 ID 集合（租户内）。
	ListDeptIDsByLeader(ctx context.Context, leaderUserID int64) ([]int64, error)
	// DeptWithChildren 返回部门及其子树的部门 ID 集合（含自身）。
	DeptWithChildren(ctx context.Context, deptID int64) ([]int64, error)
	// ListUserIDsByDeptIDs 返回部门集合内（dept_id IN）的用户 ID 集合（租户内）。
	ListUserIDsByDeptIDs(ctx context.Context, deptIDs []int64) ([]int64, error)
	// UserNicknames 返回用户 ID 集合的昵称映射（额度管理页展示员工姓名，issue #51）。
	UserNicknames(ctx context.Context, userIDs []int64) (map[int64]string, error)
	// UserDeptID 返回用户所属部门（公告受众判定：查看者祖先部门链起点，issue #48）。
	UserDeptID(ctx context.Context, userID int64) (int64, bool, error)
	// DeptParentID 返回部门父部门（公告受众判定：向上构造祖先部门链，issue #48）。
	DeptParentID(ctx context.Context, deptID int64) (int64, bool, error)
	// UserRoleIDs 返回用户持有的角色 ID（公告受众判定：角色并集，issue #48）。
	UserRoleIDs(ctx context.Context, userID int64) ([]int64, error)
	// ListActiveUserIDsByRole 返回角色下启用用户 ID（公告发布按角色展开收件人）。
	ListActiveUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error)
	// ListActiveUserIDs 返回租户内启用用户 ID（公告全员发布展开收件人）。
	ListActiveUserIDs(ctx context.Context) ([]int64, error)
	// DeptNames 批量返回部门名称（公告受众回显）。
	DeptNames(ctx context.Context, deptIDs []int64) (map[int64]string, error)
	// RoleNames 批量返回角色名称（公告受众回显）。
	RoleNames(ctx context.Context, roleIDs []int64) (map[int64]string, error)
}

// NotifyGateway 是 oa 对 notify 模块的依赖面（notify 为平台级被依赖方，
// spec §4.13：通知与源操作同一事务，同生共死）。公告发布在事务内批量投递
// announcement 通知，写失败则发布回滚。
type NotifyGateway interface {
	// CreateInTx 在调用方事务内批量投递（与 notify.Service.CreateInTx 同形）。
	CreateInTx(ctx context.Context, tx *gorm.DB, inputs []notifyservice.Input) error
}

// Service 汇聚 oa 模块的业务能力。
type Service struct {
	sys    SystemGateway
	repo   *repository.Repository
	notify NotifyGateway
	files  AnnouncementFileStore
	// apiPrefix 是附件鉴权下载地址前缀（config.App.APIPrefix，issue #48）。
	apiPrefix string
}

// New 创建 oa Service。repo 是 OA 实体表仓储（issue #50 起引入，工作日历
// 首用）；可见范围判定只依赖 SystemGateway，repo 仅在无实体表读写时可为
// nil（既有单测），生产装配必传。notify 是公告发布的站内通知 seam
// （spec §4.13），生产装配必传。
func New(sys SystemGateway, repo *repository.Repository, notify NotifyGateway) *Service {
	return &Service{sys: sys, repo: repo, notify: notify}
}

// VisibilityScopeOf 计算当前用户在指定资源上的可见范围（spec §4.6 取数编排）：
//
//  1. 持有 oa:<resource>:manage（super_admin "*" 通配）→ 全租户，不取部门数据；
//  2. 当前用户是部门负责人 → 逐负责部门解析子树、再取子树内用户集合，多部门取并集；
//  3. 否则仅本人。
//
// 所有取数都发生在当前登录租户内（system_dept/system_user 已登记租户插件，
// 查询自动追加 tenant_id 条件），跨租户的用户/部门不可能进入可见集合，
// 跨租户业务 ID 在消费方按 404 处理，不泄露存在性。
func (s *Service) VisibilityScopeOf(ctx context.Context, resource string) (VisibilityScope, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		// fail-closed：无登录主体（异常上下文）不给任何可见范围。
		return VisibilityScope{UserIDs: []int64{}}, nil
	}
	if !IsVisibilityResource(resource) {
		return VisibilityScope{}, xerr.BadRequest("未知的 OA 资源类型：" + resource)
	}

	hasManage, err := s.sys.UserHasPermission(ctx, userID, ManagePermission(resource))
	if err != nil {
		return VisibilityScope{}, err
	}
	if hasManage {
		return VisibilityScope{All: true}, nil
	}

	ledDepts, err := s.sys.ListDeptIDsByLeader(ctx, userID)
	if err != nil {
		return VisibilityScope{}, err
	}
	subtrees := make([][]int64, 0, len(ledDepts))
	for _, deptID := range ledDepts {
		children, err := s.sys.DeptWithChildren(ctx, deptID)
		if err != nil {
			return VisibilityScope{}, err
		}
		if len(children) == 0 {
			continue
		}
		users, err := s.sys.ListUserIDsByDeptIDs(ctx, children)
		if err != nil {
			return VisibilityScope{}, err
		}
		subtrees = append(subtrees, users)
	}
	return ResolveVisibility(false, subtrees, userID), nil
}

// CurrentUserID 返回 context 中的当前用户 ID（与 crm/service 同源的认证主体读取）。
func CurrentUserID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.UserID
	}
	return 0
}
