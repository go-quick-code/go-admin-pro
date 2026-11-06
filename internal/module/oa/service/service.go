// Package service 承载 oa 模块的业务能力：共享可见范围判定（issue #43）、
// 工作日历（issue #50）；公告/请假/报销/考勤/日程/会议室/文档等域由后续
// 工单按 docs/oa-mvp-spec.md §2 的模块边界逐个落地。
package service

import (
	"context"
	"time"

	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	systemmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
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
}

// Service 汇聚 oa 模块的业务能力。
type Service struct {
	sys  SystemGateway
	repo *repository.Repository
	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入：
	// 考勤打卡日期（issue #60）与请假取消窗口的「今天」（issue #53）都按它
	// 计算。nil 时退回 time.Local。
	location *time.Location
	// bpm 是审批引擎（issue #52 请假提交/回调的接点）：OA → bpm 单向依赖，
	// bpm 不反向依赖业务模块（ADR 0008/0009）。装配前为 nil，相关入口显式报错。
	bpm *bpmservice.Service
	// files 是请假附件的存储依赖面（由 internal/app 装配 infra 适配器）。
	files LeaveFileStore
	// apiPrefix 是附件鉴权下载地址的 HTTP 前缀。
	apiPrefix string
	// clock 是「现在」的来源（未注入时 time.Now），单元测试可替换。
	clock func() time.Time
}

// New 创建 oa Service。repo 是 OA 实体表仓储（issue #50 起引入，工作日历
// 首用）；可见范围判定只依赖 SystemGateway，repo 仅在无实体表读写时可为
// nil（既有单测），生产装配必传。
func New(sys SystemGateway, repo *repository.Repository) *Service {
	return &Service{sys: sys, repo: repo}
}

// SetLocation 注入应用业务时区（ADR 0006：唯一来源是 config.Location()，
// 由 internal/app 装配时调用）。nil 忽略，保留当前值。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区：app 装配后是配置的 app.timezone；
// 未注入（如直接构造 Service 的测试）时退回 time.Local。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// businessNow 返回业务时区下的「现在」：请假取消窗口的「今天」走这里，
// 不直接用 time.Now()（其 Location 是服务器本地时区，与 app.timezone
// 可能不同，跨日边界会判错窗口——ADR 0006）。
func (s *Service) businessNow() time.Time {
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	return clock().In(s.businessLocation())
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
