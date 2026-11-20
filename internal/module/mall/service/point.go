package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// point.go 是积分域的业务层（issue #175，docs/mall-mvp-spec.md §3.1/§3.4/§4）。
//
// ┌─────────────────────────────────────────────────────────────────────────┐
// │ 唯一入账入口（grep 自证，spec §3.4「积分不变量」硬约束）                    │
// │                                                                         │
// │ member_user.point_balance 在整个 mall 包内只有一个可写路径：              │
// │   Service.ApplyPointChange（本文件）→ 事务内：                            │
// │     1. LockMemberPoint          行锁读余额（SELECT ... FOR UPDATE）       │
// │     2. 锁内预判 newBalance < 0  → 409（文案带当前余额/变动量）             │
// │     3. UpdateMemberPointBalance 条件 UPDATE（出向 AND balance+? >= 0，    │
// │        SQL 双保险；0 行 → 409）——非负禁令的最终裁决                       │
// │     4. InsertPointRecord        流水（balance_after 快照结存，append-only）│
// │                                                                         │
// │ 其它任何代码（member.go 的 UpdateMemberFields 列白名单不含 point_balance、│
// │ repository 各查询原语、api/dto 层）都不得写 point_balance；                │
// │ `grep -rn "point_balance" internal/module/mall` 自证：写入仅出现在        │
// │ repository/member_point.go 的 UpdateMemberPointBalance 一条 UPDATE 语句， │
// │ 且只被本函数调用。照 pay service/wallet.go ApplyWalletChange 先例。       │
// └─────────────────────────────────────────────────────────────────────────┘
//
// biz_type 三类（model.MemberPointBiz*，spec §3.1）：
//   - admin_adjust 管理员调整（本票唯一调用方：AdjustPoint）；
//   - order_pay / order_cancel 常量先定义，调用方随 T4（支付联动入账）/
//     T3/T9（取消/关单回扣）接入——入口校验已放行两类，接线时无需返工。
//
// 快照可重放：任一会员按流水 id 正序累计 point，代数和恒等于 point_balance
// （同一事务内写入保证；集成测试 TestIntegrationPointConcurrent 验证）。

// PointChange 是一笔积分变动请求（后续票的业务流组装，本票由 AdjustPoint
// 与集成测试经管理端 HTTP 调用）。
type PointChange struct {
	// UserID 是积分归属会员（member_user.id）。
	UserID int64
	// BizType ∈ model.MemberPointBizTypes（order_pay/order_cancel/admin_adjust）。
	BizType string
	// BizID 是关联业务单据 ID（订单 ID；admin_adjust 恒 0）。
	BizID int64
	// Point 正入负出（入账为正、扣减为负），不得为 0。
	Point int
	// Remark 是流水备注（调整原因/订单号引用），选填 ≤500 字。
	Remark string
}

// validatePointChange 校验入账请求（纯函数）：归属、biz_type、积分、备注。
func validatePointChange(ch PointChange) error {
	if ch.UserID <= 0 {
		return xerr.BadRequest("积分入账缺少归属会员")
	}
	if err := ValidatePointBizType(ch.BizType); err != nil {
		return err
	}
	if ch.BizID < 0 {
		return xerr.BadRequest(fmt.Sprintf("积分流水业务单据 ID %d 不合法：应为订单 ID 或 0", ch.BizID))
	}
	if ch.BizType == model.MemberPointBizAdminAdjust && ch.BizID != 0 {
		// 迁移 00044 注释口径：admin_adjust 无单据语义，biz_id 恒 0。
		return xerr.BadRequest("管理员调整（admin_adjust）不携带业务单据 ID")
	}
	if ch.Point == 0 {
		return xerr.BadRequest("变动积分不能为 0：正数入账、负数扣减")
	}
	if err := ValidatePointRemark(ch.Remark); err != nil {
		return err
	}
	return nil
}

// pointShortMessage 生成积分不足的 409 文案（对齐 pay walletShortMessage：
// 提示当前余额/变动量）。
func pointShortMessage(ch PointChange, current int) string {
	return fmt.Sprintf("积分余额不足：当前余额 %d，需要变动 %d", current, ch.Point)
}

// ApplyPointChange 是全 mall 唯一的积分入账入口（spec §3.4「余额只经唯一
// 入账入口变动」；文件头注释声明完整约束与 grep 自证口径）。
//
// 流程（单事务，照 pay ApplyWalletChange 同构）：
//  1. SELECT ... FOR UPDATE 锁会员行，读当前余额；
//  2. 锁内预判：变动后余额 < 0 → 409（SQL 条件是最终裁决，预判只为文案）；
//  3. 条件 UPDATE point_balance（出向 `AND point_balance + ? >= 0` SQL 双保险；
//     0 行 → 409）；
//  4. 插入流水（point 正入负出 + balance_after 快照）。
//
// 任一步失败整体回滚：余额不变、无流水。
func (s *Service) ApplyPointChange(ctx context.Context, ch PointChange) (*model.MemberPointRecord, error) {
	if err := validatePointChange(ch); err != nil {
		return nil, err
	}
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		// 入账原语走显式租户参数：缺租户上下文宁可拒绝，绝不落 tenant 0 脏数据
		//（照 pay ApplyWalletChange 同款 fail-closed）。
		return nil, xerr.Unauthorized("")
	}

	var out *model.MemberPointRecord
	err := s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		var err error
		out, err = s.applyPointChangeTx(ctx, tx, tenantID, ch)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// applyPointChangeTx 是入账入口的事务内核（T4 支付联动在支付事务内复用同一
// 形状：先锁后写、SQL 双保险、快照落流水）。校验已由 ApplyPointChange 前置
// 完成（内部调用方自行保证）。
func (s *Service) applyPointChangeTx(ctx context.Context, tx *mallrepo.Repository, tenantID int64, ch PointChange) (*model.MemberPointRecord, error) {
	// 1) 行锁 + 当前余额（先锁后写）。
	current, err := tx.LockMemberPoint(ctx, tenantID, ch.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, xerr.NotFound("会员不存在")
		}
		return nil, err
	}
	// 2) 锁内预判（行锁已持有，无并发窗；SQL 条件是负余额禁令的最终裁决）。
	after := current.PointBalance + ch.Point
	if after < 0 {
		return nil, xerr.Conflict(pointShortMessage(ch, current.PointBalance))
	}
	// 3) 更新余额。SQL 条件是双保险：出向影响 0 行 → 409。
	rows, err := tx.UpdateMemberPointBalance(ctx, tenantID, ch.UserID, int64(ch.Point), ch.Point < 0)
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, xerr.Conflict(pointShortMessage(ch, current.PointBalance))
	}
	// 4) 流水（append-only）：正入负出 + 变动后结存快照。
	out := &model.MemberPointRecord{
		TenantID:     tenantID,
		UserID:       ch.UserID,
		BizType:      ch.BizType,
		BizID:        ch.BizID,
		Point:        ch.Point,
		BalanceAfter: after,
		Remark:       trimString(ch.Remark),
	}
	return out, tx.InsertPointRecord(ctx, out)
}

// ---------- 管理端：调整与流水（spec §4 会员行） ----------

// AdjustPoint 管理员调整会员积分（POST /mall/member/adjust-point，
// 权限码 mall:member:update）：point 可正可负，0 拒绝（400）；会员不存在
// 404；余额扣成负数 409。经 ApplyPointChange 落账（唯一入账入口）。
func (s *Service) AdjustPoint(ctx context.Context, req *dto.PointAdjustReq) (*model.MemberPointRecord, error) {
	if err := ValidateAdjustPoint(req.Point); err != nil {
		return nil, err
	}
	if err := ValidatePointRemark(req.Remark); err != nil {
		return nil, err
	}
	// 存在性前置（404 先于锁）：ApplyPointChange 的行锁读也会兜底
	//（NotFound 同语义），这里提前给出无锁的确定性 404。
	if _, err := s.findMember(ctx, req.UserID); err != nil {
		return nil, err
	}
	return s.ApplyPointChange(ctx, PointChange{
		UserID:  req.UserID,
		BizType: model.MemberPointBizAdminAdjust,
		BizID:   0,
		Point:   req.Point,
		Remark:  trimString(req.Remark),
	})
}

// ListPointRecords 按会员分页查积分流水（GET /mall/member/:id/point-records，
// 权限码 mall:member:query）：会员不存在 404；biz_type 过滤值校验 400。
// 排序固定 id ASC（重放序，见 repository.ListPointRecords 注释）。
func (s *Service) ListPointRecords(ctx context.Context, userID int64, req *dto.PointRecordPageReq) ([]model.MemberPointRecord, int64, error) {
	if _, err := s.findMember(ctx, userID); err != nil {
		return nil, 0, err
	}
	bizType := trimString(req.BizType)
	if bizType != "" {
		if err := ValidatePointBizType(bizType); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListPointRecords(ctx, mallrepo.PointRecordFilter{
		UserID:  userID,
		BizType: bizType,
	}, page)
}

// ---------- 积分配置（租户级单行，惰性 upsert，spec §3.1） ----------

// GetPointConfig 读租户积分配置（GET /mall/point-config，权限码
// mall:point:query）：行缺失按默认 point_per_yuan=1 惰性建行（单行 upsert，
// UNIQUE(tenant_id) 兜底并发首触）。
func (s *Service) GetPointConfig(ctx context.Context) (*model.MemberPointConfig, error) {
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	var out *model.MemberPointConfig
	err := s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		cur, err := tx.EnsurePointConfig(ctx, tenantID)
		if err != nil {
			return err
		}
		out = pointConfigFrom(cur, tenantID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdatePointConfig 更新租户积分配置（PUT /mall/point-config，权限码
// mall:point:update）：point_per_yuan ≥ 0（400）；行缺失先惰性建行再更新
// （单行 upsert）。
func (s *Service) UpdatePointConfig(ctx context.Context, req *dto.PointConfigSaveReq) (*model.MemberPointConfig, error) {
	if err := ValidatePointPerYuan(req.PointPerYuan); err != nil {
		return nil, err
	}
	if err := ValidatePointRemark(req.Remark); err != nil {
		return nil, err
	}
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	remark := trimString(req.Remark)
	var out *model.MemberPointConfig
	err := s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if _, err := tx.EnsurePointConfig(ctx, tenantID); err != nil {
			return err
		}
		if err := tx.UpdatePointConfigFields(ctx, tenantID, req.PointPerYuan, remark); err != nil {
			return err
		}
		cur, err := tx.FindPointConfig(ctx, tenantID)
		if err != nil {
			return err
		}
		if cur == nil {
			return gorm.ErrRecordNotFound
		}
		out = pointConfigFrom(cur, tenantID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PointPerYuan 读租户积分兑换比例（只读，不建行）：T4 支付联动按实付入账
// 消费（1 元 = N 积分）。行缺失返回默认 1（spec §3.1「行缺失时业务层按
// 默认值 1 执行」）——热路径只读，避免每次支付多一个写事务。
func (s *Service) PointPerYuan(ctx context.Context) (int, error) {
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		return 0, xerr.Unauthorized("")
	}
	cur, err := s.repo.FindPointConfig(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if cur == nil {
		return 1, nil
	}
	return cur.PointPerYuan, nil
}

// pointConfigFrom 把裸读行规整为响应模型（缺失行不该出现在响应路径：
// EnsurePointConfig 已物化，此处兜底默认值）。
func pointConfigFrom(cur *mallrepo.PointConfigCurrent, tenantID int64) *model.MemberPointConfig {
	return &model.MemberPointConfig{
		BaseEntity:   model.BaseEntity{ID: cur.ID},
		TenantID:     tenantID,
		PointPerYuan: cur.PointPerYuan,
		Remark:       cur.Remark,
	}
}
