// Package service 承载 oa 模块的业务能力：共享可见范围判定（issue #43）、
// 工作日历（issue #50）；公告/请假/报销/考勤/日程/会议室/文档等域由后续
// 工单按 docs/oa-mvp-spec.md §2 的模块边界逐个落地。
package service

import (
	"context"
	"time"

	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	systemmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
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
	// ListSimpleUsers 返回本租户启用用户的精简列表（考勤组员物化的合法性
	// 校验用，issue #60）。
	ListSimpleUsers(ctx context.Context) ([]systemmodel.SimpleUser, error)
	// UserNicknames 批量返回用户昵称（额度管理页展示员工姓名，issue #51；
	// 考勤组员列表同样使用，缺失的 ID 不在返回集）。
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
	// ListEnabledTenantIDs 返回全部启用租户 ID（系统级，无租户过滤）：
	// 考勤日结果任务按租户迭代时使用（issue #61，spec §4.10）。
	ListEnabledTenantIDs(ctx context.Context) ([]int64, error)
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
	sys  SystemGateway
	repo *repository.Repository
	// notify 是公告发布的站内通知 seam（issue #48，spec §4.13）。
	notify NotifyGateway
	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入；
	// 考勤打卡日期按它取当天（issue #60）。nil 时退回 time.Local。
	location *time.Location
	// bpm 是审批引擎（issue #52 请假提交/回调的接点）：OA → bpm 单向依赖，
	// bpm 不反向依赖业务模块（ADR 0008/0009）。装配前为 nil，相关入口显式报错。
	bpm *bpmservice.Service
	// files 是请假附件的存储依赖面（由 internal/app 装配 infra 适配器，issue #52）。
	files LeaveFileStore
	// announcementFiles 是公告附件的存储依赖面（由 internal/app 装配 infra
	// 适配器，issue #48）；与请假附件字段分开，避免同一工单合并时互相覆盖。
	announcementFiles AnnouncementFileStore
	// apiPrefix 是附件鉴权下载地址前缀（config.App.APIPrefix）。
	apiPrefix string
	// leaveDays 是请假天数取数接缝（issue #61 预留，#62 装配请假联动）；
	// nil 表示无请假联动，派生时 leave_days 恒 0。
	leaveDays LeaveDaysProvider
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
