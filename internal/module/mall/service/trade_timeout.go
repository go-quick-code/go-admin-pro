package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// trade_timeout.go 是订单超时关单 job（issue #182，docs/mall-mvp-spec.md §1
// 第 7 条「超时关单 job，JobSpecs 三处声明，照 payOrderTimeoutClose 先例」）。
//
// 三处声明（ADR 0024 补遗）的模块侧在本文件：JobHandlerName 常量 +
// RunOrderTimeoutClose 入口；规格在 internal/seed/job.go，装配绑定在
// internal/app（jobBindings 闭包调本入口，缺绑定拒启）。跨租户逐租户扫描
// 与每租户结果聚合照 pay RunOrderTimeoutClose 形状（listEnabledTenants →
// 逐租户执行 → 聚合 Closed，单租户失败记入 errs 不中断他租户）；每单关单
// 与库存回补同事务（照取消回补形状：ClaimOrderTimeout 条件 UPDATE +
// restoreOrderStock）。时间判定一律走 businessNow()（mall 时钟缝，issue
// #179 T6 已落——本票只消费，拨钟测试的唯一拨点）。

// OrderTimeoutCloseJobHandlerName 是商城订单超时关闭任务的处理器名。
const OrderTimeoutCloseJobHandlerName = "mallOrderTimeoutClose"

// orderTimeoutDuration 是待支付订单的超时阈值（spec §1 第 7 条「待支付超
// 15 分钟的订单自动关闭」）：created_at 早于 businessNow−本值 即视为过期。
const orderTimeoutDuration = 15 * time.Minute

// OrderTimeoutCloseReason 是超时关单的 close_reason 固定文案（与用户取消
// 的自填 cancel_reason 区分：关单原因是系统判定，不由调用方传入）。
const OrderTimeoutCloseReason = "超时未支付"

// OrderTimeoutCloseResult 汇总一轮超时关单的执行结果。
type OrderTimeoutCloseResult struct {
	Tenants int   // 扫过的租户数
	Closed  int64 // 关闭的超时待支付订单数
}

// TenantLister 是启用租户清单的窄接口（job 跨租户迭代的数据源）：由
// internal/app 装配处适配 system Service（照 pay TenantLister 同型）。
type TenantLister interface {
	ListEnabledTenantIDs(ctx context.Context) ([]int64, error)
}

// SetTenantLister 注入启用租户清单来源（装配期）。未注入时 job 报错拒跑
// （fail-closed：静默空转会掩盖装配缺陷）。
func (s *Service) SetTenantLister(l TenantLister) {
	s.tenants = l
}

// RunOrderTimeoutClose 是商城订单超时关闭任务入口（系统级动作，逐租户执行，
// 照 pay RunOrderTimeoutClose 形状）：扫 status=0 且 created_at <
// businessNow−15min 的待支付订单 → 每单同事务「条件 UPDATE 认领 0→40 已
// 关闭 + 按订单项回补库存」。认领 0 行 = 列出后已被并发改动（支付成功/
// 用户取消先行），跳过不回补不计数；已取消(30)/已支付(10)/已完成(20)/
// 已关闭(40) 不在候选集内（WHERE status=0，终态无出边）。
func (s *Service) RunOrderTimeoutClose(ctx context.Context) (OrderTimeoutCloseResult, error) {
	tenantIDs, err := s.listEnabledTenants(ctx)
	if err != nil {
		return OrderTimeoutCloseResult{}, err
	}
	result := OrderTimeoutCloseResult{Tenants: len(tenantIDs)}
	var errs []error
	for _, id := range tenantIDs {
		closed, err := s.closeTimeoutOrders(tenant.WithTenantID(ctx, id), id)
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 超时关单失败: %w", id, err))
			continue
		}
		result.Closed += closed
	}
	if len(errs) > 0 {
		return result, errs[0]
	}
	return result, nil
}

// listEnabledTenants 列启用租户：lister 未注入时报错拒跑（fail-closed，
// 照 pay 同名先例）。
func (s *Service) listEnabledTenants(ctx context.Context) ([]int64, error) {
	if s.tenants == nil {
		return nil, errors.New("mall 超时关单任务未配置租户清单来源（SetTenantLister）")
	}
	return s.tenants.ListEnabledTenantIDs(ctx)
}

// closeTimeoutOrders 对单租户执行一轮超时关单：列出候选（待支付且
// created_at 早于阈值）→ 逐单事务认领关单 + 回补库存。单单失败中断本轮并
// 上抛（聚合层记该租户失败，其余租户不受影响）。
func (s *Service) closeTimeoutOrders(ctx context.Context, tenantID int64) (int64, error) {
	now := s.businessNow()
	cutoff := now.Add(-orderTimeoutDuration)
	orders, err := s.repo.ListTimeoutOrders(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	var closed int64
	for i := range orders {
		n, err := s.closeTimeoutOrder(ctx, tenantID, orders[i].ID, now)
		if err != nil {
			return closed, err
		}
		closed += n
	}
	return closed, nil
}

// closeTimeoutOrder 认领关单一单（同事务：条件 UPDATE 0→40 落 close_time/
// close_reason + 按订单项回补库存，照取消回补「状态迁移与回补同事务、失败
// 整体回滚」的收口）。认领 0 行（列出后已被并发改动）返回 0 且不回补。
func (s *Service) closeTimeoutOrder(ctx context.Context, tenantID, orderID int64, now time.Time) (int64, error) {
	var closed int64
	err := s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		rows, err := tx.ClaimOrderTimeout(ctx, orderID, tenantID, OrderTimeoutCloseReason, now)
		if err != nil {
			return err
		}
		if rows == 0 {
			return nil
		}
		closed = rows
		return restoreOrderStock(ctx, tx, tenantID, orderID)
	})
	if err != nil {
		return 0, err
	}
	return closed, nil
}
