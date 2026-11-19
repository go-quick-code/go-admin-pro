package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	paydto "github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// trade_pay.go 是交易域支付联动（issue #181，T8；docs/mall-mvp-spec.md §1
// 第 4 条/§2/§7 裁决 4/§3.4）。mall 是 pay `OrderSuccessHandler` 扩展点的
// 第二消费方（充值是第一，pay service.New 内注册）：pay 侧零改动，mall 只
// 作为消费方接线（mall→pay 单向依赖，spec §2——平台能力域消费，非业务套件
// 打通）。
//
// 三条链路：
//  1. 下单即建支付单（AppCreateOrder 事务提交后 linkPayOrderAfterCreate
//     独立事务挂单）：paySvc.CreateOrder 以 merchant_order_id = mall 订单号
//     （SO）幂等——同号撞键时 pay 快路径返回原单，mall 不重复建单；挂单
//     失败订单留待支付，C 端重试发起支付时补建。
//  2. C 端发起支付（AppPayOrder）：委托主体复用/补建 pay 单后转
//     paySvc.SubmitOrder，wallet 渠道同步扣款成功即支付成功（联动 handler
//     同事务置单），mock 渠道停在待支付等回调（集成测试照 pay demo 回调
//     手法触发）。
//  3. 支付成功联动（HandlePaySuccess，app.go 装配期
//     paySvc.SetOrderSuccessHandler 注册）：与 pay 订单认领同一事务内置
//     mall 订单 10 已支付 + pay 四列 + 积分按实付入账（唯一入账入口的事务
//     内核 applyPointChangeTx），任一失败整体回滚——wallet 渠道扣款随事务
//     回滚自动返还，mock/真实渠道为外部资金由 pay 超时关单 job 收口。
//
// 取消联动口径（spec §3.4/主会话裁决）：C 端取消（AppCancelOrder）不动
// pay 单——在途支付窗由 HandlePaySuccess 的状态守卫（订单非待支付即拒绝
// 联动、整笔回滚）+ pay 超时关单 job 收口：取消后回调到达 → 守卫拒绝 →
// pay 认领回滚、pay 单保持待支付 → 超时关单将其关闭。MVP 不做「取消作废
// pay 单」的反向调用（pay 侧零改动约束下无作废入口）。

// MallPayAppKey 是 mall 挂单应用的 app_key（种子 seedMallPayApp 预置
// app_key=mall，绑定 mock/wallet）。同一常量两处消费：建单定位应用 +
// 种子写入，改值需两处同步（无独立配置项——MVP 固定应用语义，照
// payservice.DefaultRechargeAppKey 的缺省口径）。
const MallPayAppKey = "mall"

// SetPaySvc 注入 pay Service（app.go 装配期；mall→pay 单向，spec §2）。
func (s *Service) SetPaySvc(paySvc *payservice.Service) { s.paySvc = paySvc }

// paySvcReady 防御未装配即调用（装配缺漏立即暴露为 500，不静默降级）。
func (s *Service) paySvcReady() error {
	if s.paySvc == nil {
		return xerr.Internal(errors.New("mall 支付联动未装配 pay Service"))
	}
	return nil
}

// ---------- 下单即建支付单（不同事务，AppCreateOrder 事务提交后调用） ----------

// linkPayOrderAfterCreate 在 mall 订单事务提交后挂 pay 支付单（issue #181，
// 主会话裁决「不同事务」）：paySvc.CreateOrder 以 merchant_order_id = SO 号
// 幂等挂单（appKey=mall、subject=商品名摘要、price=pay_money），成功后把
// payOrderId/No 记回（UpdateOrderPayOrder 首绑补写，WHERE pay_order_id=0）。
//
// 失败口径：mall 订单已提交，pay 挂单任何失败（pay 服务异常/应用缺失/金额
// 非法等）只告警不回滚——订单留待支付、payOrderId 保持 0，C 端重试发起支付
// 时补建（AppPayOrder 走同一幂等建单入口，同号复用/补建）。审计列语义：
// pay_order.creator 记录委托主体的 member_user ID（见 delegateMemberCtx）。
func (s *Service) linkPayOrderAfterCreate(ctx context.Context, tenantID, userID int64, out *dto.AppCreateOrderResp, userIP string) {
	if s.paySvc == nil {
		// 装配缺漏不阻断下单（订单本身有效）；发起支付时 paySvcReady 再拦。
		slog.Warn("mall 支付联动未装配 pay Service，订单留待支付无支付单", "orderNo", out.No)
		return
	}
	member, err := s.repo.FindMemberByID(ctx, userID)
	if err != nil || member == nil {
		slog.Warn("mall 下单挂支付单失败：会员不可见", "orderNo", out.No, "memberID", userID, "err", err)
		return
	}
	payCtx := s.delegateMemberCtx(ctx, userID, tenantID, member.Mobile)
	payOrder, err := s.paySvc.CreateOrder(payCtx, &paydto.OrderCreateReq{
		AppKey:          MallPayAppKey,
		MerchantOrderID: out.No,
		Subject:         s.orderSubject(ctx, out.ID),
		Price:           out.PayMoney,
	}, userIP)
	if err != nil {
		slog.Warn("mall 下单挂支付单失败（订单留待支付，可重试发起支付补建）",
			"orderNo", out.No, "err", err)
		return
	}
	if err := s.repo.UpdateOrderPayOrder(ctx, tenantID, out.ID, payOrder.ID, payOrder.No); err != nil {
		slog.Warn("mall 下单记回 payOrderId 失败（发起支付时按 SO 幂等复用）",
			"orderNo", out.No, "payOrderID", payOrder.ID, "err", err)
	}
}

// ---------- C 端发起支付（POST /mall/app/trade/order/:id/pay） ----------

// AppPayOrder 会员对待支付订单发起支付（issue #181）：校验订单归属与状态 →
// 委托主体建单/复用 pay 单 → paySvc.SubmitOrder 转发起。pay 侧错误原样
// 透传（wallet 余额不足 409、订单过期 409 等文案可区分，C 端收银台据此
// 提示）。
//
// 委托主体（主会话裁决）：pay 的 CreateOrder/SubmitOrder 都要求 authn.From
// 的 system 主体（UserID>0），而 mall C 端是会员——以 memberID 构造
// Principal 委托调用（CRM contract_approval 存量补建先例同形状）。语义
// 注记：pay_order.creator 审计列经 AuditPlugin 记录该主体——对 mall 发起的
// 支付单，creator = 触发会员的 member_user ID（system_user 空间可能撞号，
// 用 Username 字段区分域：mall:member:<mobile>）；pay_order.user_id 同值，
// 即 pay「本人订单」校验天然对会员成立。
func (s *Service) AppPayOrder(ctx context.Context, memberID, orderID int64, req *dto.AppOrderPayReq, userIP string) (*dto.AppOrderPayResp, error) {
	if err := s.paySvcReady(); err != nil {
		return nil, err
	}
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		// pay 委托主体需要租户；缺租户上下文宁可拒绝（照 AppCreateOrder 口径）。
		return nil, xerr.Unauthorized("")
	}
	channelCode := trimString(req.ChannelCode)
	// 订单归属 + 状态守卫（404/409 在 mall 侧裁决，pay 侧不感知 mall 订单）。
	order, err := s.repo.FindOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil || order.UserID != memberID {
		return nil, xerr.NotFound("订单不存在")
	}
	if order.Status != model.TradeOrderStatusPending {
		return nil, xerr.Conflict(orderPayConflictMessage(order.Status))
	}
	// 会员现值（委托主体 Username 的 mobile 来源；memberAuth 已验状态，
	// 这里只兜存在性——会员行被并发软删时 404）。
	member, err := s.repo.FindMemberByID(ctx, memberID)
	if err != nil {
		return nil, err
	}
	if member == nil {
		return nil, xerr.NotFound("会员不存在")
	}
	payCtx := s.delegateMemberCtx(ctx, memberID, tenantID, member.Mobile)

	// 建单/复用（merchant_order_id = SO 号幂等）：pay CreateOrder 幂等快
	// 路径对同租户同应用同号返回原单——重复发起支付复用既有待支付 pay 单，
	// 不产生第二笔挂单（spec §3.4「复用待支付的既有 pay 单」）。
	payOrder, err := s.paySvc.CreateOrder(payCtx, &paydto.OrderCreateReq{
		AppKey:          MallPayAppKey,
		MerchantOrderID: order.No,
		Subject:         s.orderSubject(ctx, order.ID),
		Price:           order.PayMoney,
	}, userIP)
	if err != nil {
		return nil, err
	}
	// payOrderId 记回（首建链路）：mall 订单与 pay 单不同事务（两段式裁决
	// ——pay 建单失败订单留待支付无支付单，C 端重试发起支付时补建；此处
	// UPDATE 只补 pay_order_id=0 的首次绑定，不覆盖）。已记录但 pay 单号
	// 变更不存在（幂等复用返回原单，单号不变）。
	if order.PayOrderID == 0 {
		if err := s.repo.UpdateOrderPayOrder(ctx, tenantID, order.ID, payOrder.ID, payOrder.No); err != nil {
			return nil, err
		}
	}
	// 发起支付：wallet 渠道同事务扣款成功即支付成功（联动 handler 在同一
	// pay 事务内置 mall 订单已支付——SubmitOrder 返回即已联动完成）；mock
	// 渠道停在待支付等回调。
	submit, err := s.paySvc.SubmitOrder(payCtx, &paydto.OrderSubmitReq{
		OrderID:     payOrder.ID,
		ChannelCode: channelCode,
	}, userIP)
	if err != nil {
		return nil, err
	}
	return &dto.AppOrderPayResp{
		PayOrderID:  payOrder.ID,
		PayOrderNo:  payOrder.No,
		ExtensionID: submit.ExtensionID,
		ExtensionNo: submit.ExtensionNo,
		Status:      submit.Status,
		DisplayMode: submit.DisplayMode,
		Content:     submit.Content,
	}, nil
}

// delegateMemberCtx 以会员身份构造 pay 委托主体（AppPayOrder 委托语义的
// 单一构造点，注释见其函数头）。
func (s *Service) delegateMemberCtx(ctx context.Context, memberID, tenantID int64, mobile string) context.Context {
	return authn.With(ctx, authn.Principal{
		UserID:   memberID,
		TenantID: tenantID,
		Username: "mall:member:" + mobile,
	})
}

// orderPayConflictMessage 按当前状态给不可发起支付的 409 文案（对齐取消侧
// orderCancelConflictMessage 的可区分口径）。
func orderPayConflictMessage(status int8) string {
	switch status {
	case model.TradeOrderStatusPaid, model.TradeOrderStatusCompleted:
		return "订单已支付，请勿重复支付"
	case model.TradeOrderStatusCanceled:
		return "订单已取消，无法支付"
	case model.TradeOrderStatusClosed:
		return "订单已关闭，无法支付"
	default:
		return "订单当前状态不可支付"
	}
}

// orderSubject 组装 pay 单商品标题（商品名摘要，≤100 字——pay Subject 列
// 上限）：订单项 SPU 名按顺序「、」拼接后截断。读失败降级为「商城订单」
// （展示字段，不为它阻断支付）。
func (s *Service) orderSubject(ctx context.Context, orderID int64) string {
	subject := "商城订单"
	items, err := s.repo.ListOrderItemsByOrder(ctx, orderID)
	if err == nil && len(items) > 0 {
		names := make([]string, 0, len(items))
		for _, it := range items {
			names = append(names, it.SPUName)
		}
		subject = strings.Join(names, "、")
	}
	runes := []rune(subject)
	if len(runes) > 100 {
		subject = string(runes[:100])
	}
	return subject
}

// ---------- 支付成功联动（pay OrderSuccessHandler 第二消费方） ----------

// HandlePaySuccess 支付成功业务联动（app.go 装配期
// paySvc.SetOrderSuccessHandler 注册；签名即 payservice.OrderSuccessHandler）。
// 在 paymentSuccessTx 的 pay 订单认领之后、同一事务内执行（tx 是发起事务；
// order 已置 10，ext 为本次成功的支付发起）：
//
//  1. 定位 mall 订单：pay_order.merchant_order_id = SO 单号（建单时写入）。
//     查不到 = 非 mall 消费方的 pay 单（充值/示例订单等）→ 无联动返回 nil
//     （照充值 handler 对非充值单的跳过口径）。
//  2. 守卫：mall 订单必须仍待支付（status=0）。已取消/已关闭/已支付 →
//     返回 error 使整笔支付认领事务回滚——pay 订单认领/extension/回调任务
//     一并回滚，pay 单回到待支付；wallet 渠道扣款与认领同事务（pay 渠道
//     Order 内先扣后认领），回滚即资金返还；mock/真实渠道为模拟/外部资金，
//     残留待支付 pay 单由 pay 超时关单 job 按过期时间收口（取消不动 pay 单
//     的窗口分析，见文件头「取消联动口径」）。重复回调幂等由 pay 认领前置
//     保证（ClaimOrderSuccess 0 行即跳过联动，不会走到这里）。
//  3. 同事务置 mall 订单 10 已支付 + pay_order_id/pay_order_no/
//     pay_channel_code/pay_time（条件 UPDATE 终审守卫：0 行 = 并发状态
//     变更，同守卫语义回滚整笔）。
//  4. 积分按实付入账（唯一入账入口的事务内核 applyPointChangeTx）：
//     point_per_yuan 配置（行缺失默认 1）× 实付整数元（分位舍去——积分是
//     int，¥19.90 按 19 元入账；实付不足 1 元不入账，0 变动被入口拒绝，
//     跳过即幂等口径）。biz=order_pay、biz_id=订单 ID。
//
// 事务归属（本票最关键的正确性点）：mall 不可在 pay 事务外另开事务（那会
// 失去原子性），积分入账不走 ApplyPointChange（它用 mall repo 自己的 db 开
// 新事务）而走同形状的事务内核 applyPointChangeTx——mallrepo.New(tx.DB())
// 把 pay 事务的底层 gorm 句柄包装成 mall Repository（payrepo.Repository.DB
// 的既有跨模块传递口径，与充值 handler 经 tx 参数写钱包同构），锁会员行 →
// 条件 UPDATE 余额 → 插流水全部落在同一 gorm 事务上。
func (s *Service) HandlePaySuccess(ctx context.Context, tx *payrepo.Repository, tenantID int64, order *paymodel.Order, ext *paymodel.OrderExtension) error {
	// 渠道回调链路的 ctx 可能只有 pay 侧租户写入（真实渠道回调入口以
	// ext.TenantID 显式补齐）或完全无租户主体（wallet 发起/mock 模拟回调
	// 是会员 ctx 已携带）——以 tenantID 显式派生，保证 mall 侧插件查询的
	// 租户口径与本租户数据一致（照 pay 渠道回调 guardTenantContext 先例）。
	ctx = tenant.WithTenantID(ctx, tenantID)
	mallTx := mallrepo.New(tx.DB())
	mallOrder, err := mallTx.FindOrderByNo(ctx, order.MerchantOrderID)
	if err != nil {
		return err
	}
	if mallOrder == nil {
		// 非 mall 订单（充值/示例等消费方）：无联动。
		return nil
	}
	// 守卫（步骤 2）：非待支付一律拒绝联动、整笔回滚（语义见函数头）。
	if mallOrder.Status != model.TradeOrderStatusPending {
		return xerr.Conflict("订单状态已变更，支付联动中止：" + mallOrder.No)
	}
	payTime := s.businessNow()
	rows, err := mallTx.ClaimOrderPaid(ctx, tenantID, mallOrder.ID, order.ID, order.No, ext.ChannelCode, payTime)
	if err != nil {
		return err
	}
	if rows == 0 {
		// 读守卫与条件 UPDATE 之间的并发状态变更：同守卫语义回滚。
		return xerr.Conflict("订单状态已变更，支付联动中止：" + mallOrder.No)
	}
	// 积分按实付入账（步骤 4）：同一事务、唯一入账入口内核。
	perYuan, err := s.pointPerYuanInTx(ctx, mallTx, tenantID)
	if err != nil {
		return err
	}
	points := orderPayPoints(mallOrder.PayMoney, perYuan)
	if points == 0 {
		// 实付不足 1 元（或比例为 0）：无正变动可入账，跳过（入口拒绝 0
		// 变动；订单联动已置已支付，不受影响）。
		return nil
	}
	_, err = s.applyPointChangeTx(ctx, mallTx, tenantID, PointChange{
		UserID:  mallOrder.UserID,
		BizType: model.MemberPointBizOrderPay,
		BizID:   mallOrder.ID,
		Point:   points,
		Remark:  "订单支付：" + mallOrder.No,
	})
	return err
}

// pointPerYuanInTx 读租户积分兑换比例（事务内裸读，显式租户参数）：行缺失
// 默认 1（spec §3.1 口径，与 PointPerYuan 只读函数同语义；这里是事务内核
// 版——不能用 PointPerYuan：它取 ctx 主体的租户，渠道回调链路的主体不是
// 发起会员，口径会漂）。
func (s *Service) pointPerYuanInTx(ctx context.Context, tx *mallrepo.Repository, tenantID int64) (int, error) {
	cur, err := tx.FindPointConfig(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if cur == nil {
		return 1, nil
	}
	return cur.PointPerYuan, nil
}

// orderPayPoints 计算支付入账积分：实付金额整数元 × point_per_yuan（分位
// 舍去，向零取整——积分是 int，负实付不可能出现：pay_money 服务端定为
// item − discount + postage 的非负结果）。perYuan ≤ 0 或实付不足 1 元 → 0。
func orderPayPoints(payMoney money.Money, perYuan int) int {
	if perYuan <= 0 {
		return 0
	}
	yuan := payMoney.Cents() / 100
	if yuan <= 0 {
		return 0
	}
	return int(yuan) * perYuan
}
