// Package service 是 bpm 模块的业务层：issue #44 交付流程定义管理与发布
// 状态机；issue #46 交付实例/任务运行逻辑（发起、审批动作、BusinessHandler
// 回调、通知联动与通用待办查询）。
package service

import (
	"context"
	"sync"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/repository"
	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"gorm.io/gorm"
)

// SystemGateway 是 bpm 对 system 模块的依赖面（依赖接口而非实现，同 CRM
// 的 SystemGateway 装配方式）：审批人规则的引用存在性校验（发布期）与
// 运行期审批人解析（issue #46，spec §4.2）。查询经租户插件自动限定当前
// 租户，跨租户引用一律视为不存在。
type SystemGateway interface {
	// ActiveUserExists 用户存在且启用（停用用户不能作为审批人，spec §4.2）。
	ActiveUserExists(ctx context.Context, userID int64) (bool, error)
	// RoleExists 角色存在（启用性在运行期解析候选池时再过滤）。
	RoleExists(ctx context.Context, roleID int64) (bool, error)
	// DeptExists 部门存在（指定部门负责人规则的目标部门）。
	DeptExists(ctx context.Context, deptID int64) (bool, error)
	// UserDeptID 用户所属部门 ID；用户不存在 ok=false（发起人部门负责人规则的起点）。
	UserDeptID(ctx context.Context, userID int64) (int64, bool, error)
	// DeptLeaderUserID 部门负责人（leader_user_id > 0）；空缺 ok=false。
	DeptLeaderUserID(ctx context.Context, deptID int64) (int64, bool, error)
	// DeptParentID 部门父 ID；根部门/不存在 ok=false（向上查找的终止条件）。
	DeptParentID(ctx context.Context, deptID int64) (int64, bool, error)
	// ActiveUserIDsByRole 持有角色且启用的用户 ID 集合（角色规则候选池）。
	ActiveUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error)
	// ActiveUserIDsByPermission 持有权限码且启用的用户 ID 集合（权限码规则候选池）。
	ActiveUserIDsByPermission(ctx context.Context, permission string) ([]int64, error)
	// UserHasPermission 用户是否持有权限码（实例详情访问判定：发起人/办理人/查询码）。
	UserHasPermission(ctx context.Context, userID int64, permission string) (bool, error)
	// UserNicknames 批量解析用户昵称（时间线操作人与任务办理人展示名）。
	UserNicknames(ctx context.Context, userIDs []int64) (map[int64]string, error)
}

// NotifyGateway 是 bpm 对 notify 模块的依赖面（notify 为平台级被依赖方，
// spec §4.13：通知与源操作同一事务，同生共死）。bpm 只在事务内投递：
// 新待办发 bpm_todo、结果发 bpm_result、任务处理后把对应待办通知置已读。
type NotifyGateway interface {
	// CreateInTx 在调用方事务内批量投递（与 notify.Service.CreateInTx 同形）。
	CreateInTx(ctx context.Context, tx *gorm.DB, inputs []notifyservice.Input) error
	// MarkBizReadInTx 在调用方事务内把用户名下指定业务实体的未读通知置已读
	//（按 biz_type + biz_id 命中，索引 idx_notify_message_tenant_biz 已预留）。
	MarkBizReadInTx(ctx context.Context, tx *gorm.DB, userID int64, bizType string, bizIDs []int64) error
}

// BusinessHandler 是业务回调接口（spec §4.4，ADR 0008）：实例通过/驳回/撤回
// 时 bpm 在推进状态的同一事务内调用对应 handler，handler 失败整笔回滚。
// 入参包含事务、实例与驳回原因；业务模块实现并在 internal/app 装配时注册。
type BusinessHandler interface {
	// OnApproved 实例全部节点通过。
	OnApproved(ctx context.Context, instance *model.ProcessInstance, tx *gorm.DB) error
	// OnRejected 实例被驳回，reason 为必填的驳回原因（1–500 字）。
	OnRejected(ctx context.Context, instance *model.ProcessInstance, tx *gorm.DB, reason string) error
	// OnWithdrawn 发起人撤回实例。
	OnWithdrawn(ctx context.Context, instance *model.ProcessInstance, tx *gorm.DB) error
}

// ApprovalCommitHook 是 BusinessHandler 的**可选**扩展：实例最终通过、业务回调
// 所在事务**提交成功后**由 bpm 调用一次（同一 goroutine，错误由实现自行记录，
// 不影响已提交的审批）。用于必须读到已提交业务状态的后续动作——例如 issue
// #62 请假通过后的考勤结果重算：重算经 LeaveDaysProvider 读「已通过」的请假
// 记录，事务内读不到未提交状态。
type ApprovalCommitHook interface {
	AfterApproved(ctx context.Context, instance *model.ProcessInstance)
}

// Service 汇聚 bpm 模块的业务能力。
type Service struct {
	repo   *repository.Repository
	sys    SystemGateway
	notify NotifyGateway

	// handlers 是 business_type → BusinessHandler 注册表（spec §4.4：
	// 未注册 handler 的流程定义不允许发布/发起）。并发注册与读取互斥；
	// 生产装配在启动期注册，测试可在用例内注册替身。
	mu       sync.RWMutex
	handlers map[string]BusinessHandler
}

// New 创建 bpm Service。notify 必须提供：通知与审批状态同事务落库是
// spec §4.13 的验收项，装配缺失视为配置错误。
func New(repo *repository.Repository, sys SystemGateway, notify NotifyGateway) *Service {
	if notify == nil {
		panic("bpm: New 需要非 nil 的 NotifyGateway（通知与审批同事务，spec §4.13）")
	}
	return &Service{
		repo:     repo,
		sys:      sys,
		notify:   notify,
		handlers: make(map[string]BusinessHandler),
	}
}

// RegisterBusinessHandler 注册业务回调（internal/app 装配时调用；重复注册
// 覆盖旧实现，便于测试替身替换）。
func (s *Service) RegisterBusinessHandler(businessType string, handler BusinessHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[businessType] = handler
}

// handlerFor 查询业务回调；未注册 ok=false（发布/发起入口据此拒绝）。
func (s *Service) handlerFor(businessType string) (BusinessHandler, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	handler, ok := s.handlers[businessType]
	return handler, ok
}
