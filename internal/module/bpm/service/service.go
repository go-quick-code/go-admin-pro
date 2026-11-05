// Package service 是 bpm 模块的业务层（issue #44）：流程定义管理与
// 发布状态机；实例/任务运行逻辑属 #46。
package service

import (
	"context"
	"sync"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/repository"
	"gorm.io/gorm"
)

// SystemGateway 是 bpm 对 system 模块的依赖面（依赖接口而非实现，同 CRM
// 的 SystemGateway 装配方式）：审批人规则的引用存在性校验（发布期）。
// 查询经租户插件自动限定当前租户，跨租户引用一律视为不存在。
type SystemGateway interface {
	// ActiveUserExists 用户存在且启用（停用用户不能作为审批人，spec §4.2）。
	ActiveUserExists(ctx context.Context, userID int64) (bool, error)
	// RoleExists 角色存在（启用性在运行期解析候选池时再过滤，#46）。
	RoleExists(ctx context.Context, roleID int64) (bool, error)
	// DeptExists 部门存在（指定部门负责人规则的目标部门）。
	DeptExists(ctx context.Context, deptID int64) (bool, error)
}

// BusinessHandler 是业务回调接口（spec §4.4，ADR 0008）：实例通过/驳回/撤回
// 时 bpm 在推进状态的同一事务内调用对应 handler，handler 失败整笔回滚。
// 入参包含事务、实例与驳回原因；业务模块实现并在 internal/app 装配时注册。
// #44 只交付注册表与发布 guard，真实调用点在 #46。
type BusinessHandler interface {
	// OnApproved 实例全部节点通过。
	OnApproved(ctx context.Context, instance *model.ProcessInstance, tx *gorm.DB) error
	// OnRejected 实例被驳回，reason 为必填的驳回原因（1–500 字）。
	OnRejected(ctx context.Context, instance *model.ProcessInstance, tx *gorm.DB, reason string) error
	// OnWithdrawn 发起人撤回实例。
	OnWithdrawn(ctx context.Context, instance *model.ProcessInstance, tx *gorm.DB) error
}

// Service 汇聚 bpm 模块的业务能力。
type Service struct {
	repo *repository.Repository
	sys  SystemGateway

	// handlers 是 business_type → BusinessHandler 注册表（spec §4.4：
	// 未注册 handler 的流程定义不允许发布/发起）。并发注册与读取互斥；
	// 生产装配在启动期注册，测试可在用例内注册替身。
	mu       sync.RWMutex
	handlers map[string]BusinessHandler
}

// New 创建 bpm Service。
func New(repo *repository.Repository, sys SystemGateway) *Service {
	return &Service{
		repo:     repo,
		sys:      sys,
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
