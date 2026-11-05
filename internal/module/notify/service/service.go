// Package service 是 notify 模块的业务层：站内通知的投递（写）与读取。
//
// notify 是平台级基础设施模块：被 bpm/oa/crm 依赖，不反向依赖任何业务模块。
// 生产者经 CreateInTx 把通知写进自己的事务（spec §4.13：与触发事件同一事务，
// 写失败源操作回滚）；读取侧全部以当前登录用户本人收口，租户条件由租户插件
// 按请求上下文自动追加（docs/oa-mvp-spec.md §4.13）。
package service

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/repository"
)

// Service 是 notify 模块的业务服务。
type Service struct {
	repo *repository.Repository
}

// New 创建 notify Service。
func New(repo *repository.Repository) *Service { return &Service{repo: repo} }
