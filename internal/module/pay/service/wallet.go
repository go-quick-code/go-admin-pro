package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// wallet.go 是钱包域（issue #132，docs/pay-mvp-spec.md §3.4/§4.3/§4.11/§4.9）。
//
// ApplyWalletChange 是全 pay 唯一的钱包入账入口——任何余额变动（后续票的
// 支付/充值/退款/转账）都必须经它，任何代码不得直改 pay_wallet.balance
//（spec §4.3 硬约束）。事务内：锁钱包行（SELECT ... FOR UPDATE）→ 校验 →
// 条件 UPDATE（出向 `AND balance + ? >= 0` SQL 双保险，对齐 ERP 负库存禁令）
// → 插流水（快照变动后 balance，LS 单号在调用方事务内发放）。
//
// 自助端点（我的钱包/我的流水）只挂登录：服务层强制 user_id 归属过滤，
// 请求无法注入他人 user_id（spec §4.11）；管理端分页挂 pay:wallet:query。

// WalletChange 是一笔钱包入账请求（后续票的业务流组装，本票由集成测试直调）。
type WalletChange struct {
	// UserID/UserType 是入账钱包的归属（懒创建的唯一定位键）。
	UserID   int64
	UserType int8
	// BizType ∈ model.WalletBizTypes；5（余额调整）预留无入口，入账拒绝。
	BizType int8
	// BizID 是关联业务单据 ID（如 pay_order.id）；暂无业务流时为 0。
	BizID int64
	// Title 是流水标题（流水抽屉展示），必填。
	Title string
	// Price 正入负出（入账为正、扣款为负），不得为 0。
	Price money.Money
}

// validateWalletChange 校验入账请求（纯函数）：归属、biz_type、标题、金额。
func validateWalletChange(ch WalletChange) error {
	if ch.UserID <= 0 {
		return xerr.BadRequest("钱包入账缺少归属用户")
	}
	if ch.UserType != model.WalletUserTypeMember && ch.UserType != model.WalletUserTypeAdmin {
		return xerr.BadRequest(fmt.Sprintf("钱包归属类型不合法：%d（1 会员 / 2 后台用户）", ch.UserType))
	}
	if ch.BizType == model.WalletBizUpdateBalance {
		// spec §4.3/§7 差异清单：UPDATE_BALANCE 预留枚举，v1 无管理端调余额入口。
		return xerr.BadRequest("余额调整为预留类型，v1 无入账入口")
	}
	if !model.IsWalletBizType(ch.BizType) {
		return xerr.BadRequest(fmt.Sprintf("钱包流水业务类型不合法：%d", ch.BizType))
	}
	if len(trimString(ch.Title)) == 0 {
		return xerr.BadRequest("钱包流水标题不能为空")
	}
	if len(ch.Title) > 100 {
		return xerr.BadRequest("钱包流水标题不能超过 100 字")
	}
	if ch.Price.Cents() == 0 {
		return xerr.BadRequest("钱包变动金额不能为 0")
	}
	return nil
}

// walletCounterDeltas 按 biz_type 给出非负累计列增量（纯函数，spec §4.3/§4.7）：
//   - 充值侧 total_recharge：biz=1 累计入账额、biz=2 随退款调减（负增量）；
//   - 支出侧 total_expense：biz=3 累计扣款额、biz=4 随退款回加调减（负增量）；
//   - 其余 biz_type 不动统计列。
func walletCounterDeltas(ch WalletChange) (rechargeDelta, expenseDelta money.Money) {
	switch ch.BizType {
	case model.WalletBizRecharge, model.WalletBizRechargeRefund:
		return ch.Price, money.Money{}
	case model.WalletBizPayment, model.WalletBizPaymentRefund:
		return money.Money{}, money.FromCents(-ch.Price.Cents())
	default:
		return money.Money{}, money.Money{}
	}
}

// ApplyWalletChange 是全 pay 唯一的钱包入账入口（spec §4.3）。
//
// 流程（单事务）：
//  1. 懒创建钱包（UNIQUE 兜底并发，冲突 FOR UPDATE 重读）；
//  2. SELECT ... FOR UPDATE 锁钱包行，读当前余额/累计；
//  3. 校验：余额上限（decimal(12,2) 范围）400、非负累计列越减 409；
//  4. 条件 UPDATE（出向 `AND balance + ? >= 0` SQL 双保险；0 行 → 409，
//     提示当前余额/变更量）；
//  5. pay_sequence 事务内发放 LS 流水号（同事务，spec §4.9）；
//  6. 插入流水（price 正入负出 + 变动后 balance 快照）。
//
// 任一步失败整体回滚：余额不变、无流水、序号作废（自然空洞）。
func (s *Service) ApplyWalletChange(ctx context.Context, ch WalletChange) (*model.WalletTransaction, error) {
	if err := validateWalletChange(ch); err != nil {
		return nil, err
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		// 入账原语走显式租户参数：缺租户上下文宁可拒绝，绝不落 tenant 0 脏数据。
		return nil, xerr.Unauthorized("")
	}

	var out *model.WalletTransaction
	err := s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		var err error
		out, err = s.applyWalletChangeTx(ctx, tx, tenantID, ch)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// applyWalletChangeTx 是入账入口的事务内核（调用方已持有事务，钱包发起点
// ——wallet 渠道支付扣减——在同一事务内复用；spec §4.2 wallet 渠道同事务
// 扣减）。校验已由 ApplyWalletChange 前置完成（内部调用方自行保证）。
func (s *Service) applyWalletChangeTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, ch WalletChange) (*model.WalletTransaction, error) {
	// 1) 懒创建（幂等；UNIQUE 兜底并发首触）。
	walletID, err := tx.EnsureWallet(ctx, tenantID, ch.UserID, ch.UserType)
	if err != nil {
		return nil, err
	}
	// 2) 行锁 + 当前余额/累计（先锁后写）。
	current, err := tx.LockWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, err
	}
	// 3) 校验。余额上限在锁内预判（decimal(12,2) 范围，同 ERP 累计越界 400）；
	//    余额下限交给第 4 步的 SQL 条件做最终裁决。
	after, err := current.Balance.Add(ch.Price)
	if err != nil {
		return nil, xerr.BadRequest(fmt.Sprintf("钱包余额累计超出范围：当前 %s + 变动 %s", current.Balance, ch.Price))
	}
	rechargeDelta, expenseDelta := walletCounterDeltas(ch)
	if msg := checkWalletCounter(current.TotalRecharge, rechargeDelta, "累计充值"); msg != "" {
		return nil, xerr.Conflict(msg)
	}
	if msg := checkWalletCounter(current.TotalExpense, expenseDelta, "累计支出"); msg != "" {
		return nil, xerr.Conflict(msg)
	}
	// 4) 更新余额。SQL 条件是负余额禁令的最终裁决：出向影响 0 行 → 409。
	rows, err := tx.UpdateWalletBalance(ctx, tenantID, walletID, ch.Price, rechargeDelta, expenseDelta, ch.Price.Cents() < 0)
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, xerr.Conflict(walletShortMessage(ch, current.Balance))
	}
	// 5) LS 流水号：pay_sequence 事务内发放（tx 为绑定事务的 Repository，
	//    发放与流水落库同事务）。日期键取一次口径，避免跨日翻转错位。
	no, err := s.nextNoInTx(ctx, tx, tenantID, SeqWalletTransaction, NoPrefixWalletTransaction)
	if err != nil {
		return nil, err
	}
	// 6) 流水（append-only）：正入负出 + 变动后结存快照。
	out := &model.WalletTransaction{
		TenantID: tenantID,
		No:       no,
		WalletID: walletID,
		BizType:  ch.BizType,
		BizID:    ch.BizID,
		Title:    trimString(ch.Title),
		Price:    ch.Price,
		Balance:  after,
	}
	return out, tx.InsertWalletTransaction(ctx, out)
}

// checkWalletCounter 校验非负累计列锁内越减（纯函数）：delta < 0 且累加后为负
// 时返回 409 文案，否则返回空串。累计列在行锁内读写，无并发窗。
func checkWalletCounter(current, delta money.Money, label string) string {
	if delta.Cents() >= 0 {
		return ""
	}
	after, err := current.Add(delta)
	if err == nil && after.Cents() >= 0 {
		return ""
	}
	return fmt.Sprintf("%s不足以扣减：当前 %s，需调减 %s", label, current, money.FromCents(-delta.Cents()))
}

// walletShortMessage 生成余额不足的 409 文案（spec §4.3：提示当前余额/变更量）。
func walletShortMessage(ch WalletChange, current money.Money) string {
	return fmt.Sprintf("钱包余额不足：当前余额 %s，需要变动 %s", current, ch.Price)
}

// ---------- 自助端点（只挂登录，服务层强制本人归属，spec §4.11） ----------

// myWalletIdentity 取当前登录主体的自助钱包归属：后台用户 user_type=2。
// 无登录主体时 401（路由已挂 Auth，此处兜底 fail-closed）。
func (s *Service) myWalletIdentity(ctx context.Context) (int64, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return 0, xerr.Unauthorized("")
	}
	return p.UserID, nil
}

// GetMyWallet 返回本人钱包（懒创建：首次查询无钱包则插入，spec §4.3）。
// user_id 取登录主体，请求不可指定他人。
func (s *Service) GetMyWallet(ctx context.Context) (*model.Wallet, error) {
	userID, err := s.myWalletIdentity(ctx)
	if err != nil {
		return nil, err
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	var wallet *model.Wallet
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		walletID, err := tx.EnsureWallet(ctx, tenantID, userID, model.WalletUserTypeAdmin)
		if err != nil {
			return err
		}
		wallet, err = tx.FindWalletByID(ctx, walletID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return wallet, nil
}

// ListMyWalletTransactions 返回本人钱包流水分页：服务层强制 walletId 为本人
// 钱包，请求里的 walletId 不采纳（spec §4.11）。无钱包（未发生过任何入账）
// 返回空页，不做懒创建（只读清单不产生数据）。
func (s *Service) ListMyWalletTransactions(ctx context.Context, req *dto.WalletTransactionPageReq) ([]model.WalletTransaction, int64, error) {
	userID, err := s.myWalletIdentity(ctx)
	if err != nil {
		return nil, 0, err
	}
	wallet, err := s.repo.FindWalletByUser(ctx, userID, model.WalletUserTypeAdmin)
	if err != nil {
		return nil, 0, err
	}
	if wallet == nil {
		return []model.WalletTransaction{}, 0, nil
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.listWalletTransactions(ctx, payrepo.WalletTransactionFilter{
		WalletID: wallet.ID,
		BizType:  req.BizType,
		No:       req.No,
	}, page)
}

// ---------- 管理端（权限码 pay:wallet:query，spec §4.11/§7） ----------

// ListWallets 分页查询钱包（管理端）：pay 管理页不设 owner 行级数据权限
// （记录无创建人语义，spec §4.11），租户内全量可见。
func (s *Service) ListWallets(ctx context.Context, req *dto.WalletPageReq) ([]model.Wallet, int64, error) {
	if req.UserType != nil {
		if *req.UserType != model.WalletUserTypeMember && *req.UserType != model.WalletUserTypeAdmin {
			return nil, 0, xerr.BadRequest(fmt.Sprintf("钱包归属类型不合法：%d（1 会员 / 2 后台用户）", *req.UserType))
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListWallets(ctx, payrepo.WalletFilter{
		UserID:   req.UserID,
		UserType: req.UserType,
	}, page.Offset(), page.PageSize)
}

// ListWalletTransactions 分页查询钱包流水（管理端）：钱包列表行点开流水抽屉
// 的数据源；biz_type 过滤值校验合法性。
func (s *Service) ListWalletTransactions(ctx context.Context, req *dto.WalletTransactionPageReq) ([]model.WalletTransaction, int64, error) {
	if err := validateBizTypeFilter(req.BizType); err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.listWalletTransactions(ctx, payrepo.WalletTransactionFilter{
		WalletID: req.WalletID,
		BizType:  req.BizType,
		No:       req.No,
	}, page)
}

// listWalletTransactions 是管理端/自助端共用的流水分页实现（校验+归一化后）：
// 两个入口各自 Normalize 一次后调用（重复 Normalize 幂等无害，但此前管理端
// 归一化两次——PR #140 F4 收敛为各入口一次）。
func (s *Service) listWalletTransactions(ctx context.Context, filter payrepo.WalletTransactionFilter, page web.PageQuery) ([]model.WalletTransaction, int64, error) {
	return s.repo.ListWalletTransactions(ctx, filter, page.Offset(), page.PageSize)
}

// validateBizTypeFilter 校验分页请求的 biz_type 过滤值 ∈ 六类枚举。
func validateBizTypeFilter(bizType *int8) error {
	if bizType == nil {
		return nil
	}
	if !model.IsWalletBizType(*bizType) {
		return xerr.BadRequest(fmt.Sprintf("钱包流水业务类型不合法：%d", *bizType))
	}
	return nil
}
