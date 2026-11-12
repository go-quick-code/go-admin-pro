package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// order.go 是支付订单与收银台（issue #133，docs/pay-mvp-spec.md §4.2/§4.9/§4.10/
// §8.2 下单收银台行）。
//
// 主干 tracer bullet：下单 → 收银台 → 发起支付 → 入账。
//   - 下单 POST /pay/order/create：merchant_order_id 幂等（撞 UNIQUE 返回原单
//     不报错，跨应用/跨租户同号不冲突）；金额服务端定（调用方为消费方业务
//     服务端，price 即业务价，收银台/submit 不收金额）；notify_url 默认取应用
//     order_notify_url 快照；expire_time 服务端设定；ZF 单号 pay_sequence
//     事务内发放。
//   - 收银台 GET /pay/cashier/order：登录 + 本人订单；可用渠道 = 应用绑定 ∧
//     平台启用 ∧ 渠道已实现（注册表有 Client）。
//   - 发起支付 POST /pay/order/submit：四重校验各 409（订单待支付未过期 /
//     应用已绑定 / 平台启用 / 渠道已实现）→ extension（处理中，no=订单号-发起序）
//     → 渠道 Order。wallet 渠道同事务扣减（唯一入账入口 biz=3）→ 支付成功
//     入账；mock 渠道订单停在待支付。
//   - 支付成功入账函数（paymentSuccessTx）三条路径共用：wallet 发起 / mock
//     模拟回调 / 渠道回调——状态认领 UPDATE ... WHERE status=0（0 行 =
//     已处理，幂等成功返回）→ success_time → extension 置成功 → 建回调任务
//     （type=1，max_notify_times=9）。函数内不做任何余额变动（wallet 扣减在
//     渠道 Order 内，mock/真实渠道资金外部到账）。
//   - mock 触发 POST /pay/mock/notify：不公开（登录 + 订单归属本人 + 渠道为
//     mock，防代他人确认支付，spec §4.6）；成功分支走与真实渠道回调完全相同
//     的解析 → 入账 → 建任务链路。
//   - 渠道回调 POST /pay/channel-notify/:channelCode（spec §4.6 公开端点）：
//     渠道码 → Client ∧ 平台启用（否则 404）→ 渠道验签解析（失败 4xx）→
//     成功走支付成功入账函数（幂等）、失败置发起失败；定位键 = 回调回传的
//     outTradeNo（v1 mock 即发起 extension no）。仅承接支付回调，退款/转账
//     渠道回调随真实渠道实现另立工单（§10）。
//
// 越权防线（spec §4.11）：自助端点只挂登录，服务层强制 user_id 归属过滤；
// 管理端点挂 pay:order:query（管理页不设 owner 行级数据权限，租户内全量可见）。

// 订单过期时间的服务端默认值（expire_time 服务端设定，请求不收）。
const orderExpireDuration = 2 * time.Hour

// ---------- 应用删除引用检查（spec §4.10，#133 注册订单引用） ----------

// OrderRefChecker 是订单引用检查（AppRefChecker 实现）：应用存在订单禁删。
// 退款/转账引用检查由 #135/#136 以同形态加入 RefCheckerChain。
type OrderRefChecker struct {
	Repo *payrepo.Repository
}

// AppHasReference 实现 AppRefChecker：有订单（任意状态）即引用。
func (c OrderRefChecker) AppHasReference(ctx context.Context, appID int64) (bool, string, error) {
	total, err := c.Repo.CountOrdersByAppID(ctx, appID)
	if err != nil {
		return false, "", err
	}
	return total > 0, "订单", nil
}

// ---------- 下单（POST /pay/order/create，登录） ----------

// CreateOrder 创建支付订单：merchant_order_id 幂等——同租户同应用撞幂等键
// 返回原单（200 不报错），跨应用/跨租户同号不冲突（唯一键含 app_id/tenant_id）。
// 金额服务端定：price 即业务价（调用方为消费方业务服务端，自负其责，
// spec §4.2）；收银台与 submit 没有金额入口。notify_url 缺省取应用
// order_notify_url 快照；expire_time 服务端设定；ZF 单号事务内发放。
func (s *Service) CreateOrder(ctx context.Context, req *dto.OrderCreateReq, userIP string) (*model.Order, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	appKey := trimString(req.AppKey)
	merchantOrderID := trimString(req.MerchantOrderID)
	subject := trimString(req.Subject)
	if appKey == "" || merchantOrderID == "" || subject == "" {
		return nil, xerr.BadRequest("应用标识、消费方单号与商品标题不能为空")
	}
	if len(merchantOrderID) > 64 {
		return nil, xerr.BadRequest("消费方单号不能超过 64 字")
	}
	if req.Price.Cents() <= 0 {
		return nil, xerr.BadRequest(fmt.Sprintf("订单金额必须大于 0：%s", req.Price))
	}

	// 幂等快路径：已有同号订单直接返回原单（不校验金额/主题一致性——
	// 消费方服务端对幂等语义自负其责，与芋道对齐）。
	app, err := s.findAppByKey(ctx, appKey)
	if err != nil {
		return nil, err
	}
	if existing, err := s.repo.FindOrderByMerchantID(ctx, app.ID, merchantOrderID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	var out *model.Order
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		// ZF 单号：pay_sequence 事务内发放（spec §4.9），客户端提交的 no
		// 不存在入口（DTO 不收）。
		key := dateKey(s.businessNow())
		seq, err := tx.NextSequenceValue(ctx, tenantID, SeqOrder, key)
		if err != nil {
			return err
		}
		notifyURL := trimString(req.NotifyURL)
		if notifyURL == "" {
			notifyURL = app.OrderNotifyURL
		}
		expireTime := s.businessNow().Add(orderExpireDuration)
		out = &model.Order{
			AppID:           app.ID,
			No:              formatNo(NoPrefixOrder, key, seq),
			MerchantOrderID: merchantOrderID,
			UserID:          p.UserID,
			UserType:        model.WalletUserTypeAdmin,
			Subject:         subject,
			Body:            req.Body,
			NotifyURL:       notifyURL,
			Price:           req.Price,
			UserIP:          userIP,
			ExpireTime:      &expireTime,
		}
		if err := tx.InsertOrder(ctx, out); err != nil {
			if !payrepo.IsDuplicateEntry(err) {
				return err
			}
			// 撞幂等键：并发首触的赢家已提交（INSERT 等待其定局），回读原单。
			existing, ferr := tx.FindOrderByMerchantID(ctx, app.ID, merchantOrderID)
			if ferr != nil {
				return ferr
			}
			if existing == nil {
				return xerr.Conflict("订单创建冲突，请重试：" + merchantOrderID)
			}
			out = existing
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- 收银台（GET /pay/cashier/order，登录 + 本人订单） ----------

// GetCashierOrder 收银台数据：订单（本人校验，非本人 403，跨租户 404）+
// 可用渠道 = 应用绑定 ∧ 平台启用 ∧ 渠道已实现（spec §4.2）。
func (s *Service) GetCashierOrder(ctx context.Context, appKey, merchantOrderID string) (*dto.CashierOrderResp, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	appKey = trimString(appKey)
	merchantOrderID = trimString(merchantOrderID)
	if appKey == "" || merchantOrderID == "" {
		return nil, xerr.BadRequest("应用标识与消费方单号不能为空")
	}
	app, err := s.findAppByKey(ctx, appKey)
	if err != nil {
		return nil, err
	}
	order, err := s.repo.FindOrderByMerchantID(ctx, app.ID, merchantOrderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, xerr.NotFound("支付订单不存在")
	}
	if order.UserID != p.UserID {
		// 存在但非本人：403（不泄露内容，但明确拒绝，spec §4.2 本人订单校验）。
		return nil, xerr.Forbidden("仅本人订单可发起支付")
	}
	codes, err := s.repo.ListAppChannels(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	channels, err := s.availableChannels(ctx, codes)
	if err != nil {
		return nil, err
	}
	return &dto.CashierOrderResp{Order: order, Channels: channels}, nil
}

// availableChannels 过滤可用渠道：绑定码 ∩ 平台启用 ∩ 注册表已实现。
// 返回码保序（绑定顺序即展示顺序）。
func (s *Service) availableChannels(ctx context.Context, boundCodes []string) ([]dto.CashierChannel, error) {
	if len(boundCodes) == 0 {
		return []dto.CashierChannel{}, nil
	}
	registry := s.channelRegistry()
	rows, err := s.repo.ListChannelsByCodes(ctx, boundCodes)
	if err != nil {
		return nil, err
	}
	statusByCode := make(map[string]int8, len(rows))
	for _, row := range rows {
		statusByCode[row.Code] = row.Status
	}
	channels := make([]dto.CashierChannel, 0, len(boundCodes))
	for _, code := range boundCodes {
		if statusByCode[code] != model.StatusEnable || !registry.Known(code) {
			continue
		}
		channels = append(channels, dto.CashierChannel{Code: code, Name: channel.Name(code)})
	}
	return channels, nil
}

// ---------- 发起支付（POST /pay/order/submit，登录 + 本人订单） ----------

// SubmitOrder 发起支付：四重校验（订单待支付未过期 / 应用已绑定 / 平台启用 /
// 已实现）各 409 文案可区分 → 订单行锁串行化 → extension（处理中，no=订单号
// -发起序）→ 渠道 Order。wallet 渠道同事务扣减成功即支付成功（走入账函数）；
// mock 渠道订单停在待支付等模拟回调。待支付订单可重复 submit（每次新
// extension）；任何失败整体回滚（extension 不落库、无部分扣减）。
func (s *Service) SubmitOrder(ctx context.Context, req *dto.OrderSubmitReq, userIP string) (*dto.OrderSubmitResp, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	code := trimString(req.ChannelCode)
	if !channel.IsKnownCode(code) {
		// 渠道码常量是唯一合法码源（spec §4.1）：非 14 常量码 409 未知渠道。
		return nil, xerr.Conflict("未知支付渠道码：" + code)
	}
	registry := s.channelRegistry()
	client := registry.Get(code)
	if client == nil {
		// 已注册才可实现：14 常量中无 Client 的渠道（wx_*/alipay_*）409。
		return nil, xerr.Conflict("渠道未实现：" + channel.Name(code) + "（" + code + "）")
	}

	order, err := s.repo.FindOrderByID(ctx, req.OrderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		// 不存在/跨租户统一 404，不泄露存在性（spec §4.6 越权矩阵）。
		return nil, xerr.NotFound("支付订单不存在")
	}
	if order.UserID != p.UserID {
		return nil, xerr.Forbidden("仅本人订单可发起支付")
	}

	var out *dto.OrderSubmitResp
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		// 订单行锁：串行化同一订单的并发 submit（extension 发起序与 wallet
		// 双扣都在锁后裁决）。锁定后的 status 是权威值（先锁后写，同钱包口径）。
		locked, err := tx.LockOrder(ctx, tenantID, order.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("支付订单不存在")
		}
		if locked.Status != model.OrderStatusPending {
			return xerr.Conflict(orderStatusMessage(locked.Status))
		}
		if order.ExpireTime != nil && s.businessNow().After(*order.ExpireTime) {
			return xerr.Conflict("订单已过期，请重新下单")
		}
		// 充值禁用钱包渠道（#136，spec §4.7）：反查 pay_wallet_recharge.pay_order_id
		// ——充值订单走钱包渠道支付属「扣款 → 充值入账」自引用套利（净零绕圈
		// 刷赠送），一律 409。
		if code == channel.CodeWallet {
			recharge, err := tx.FindRechargeByOrderID(ctx, locked.ID)
			if err != nil {
				return err
			}
			if recharge != nil {
				return xerr.Conflict("充值订单禁用钱包渠道支付：钱包余额支付充值属自引用套利")
			}
		}
		// 校验二：应用已绑定该渠道。
		bound, err := tx.ListAppChannels(ctx, order.AppID)
		if err != nil {
			return err
		}
		if !containsCode(bound, code) {
			return xerr.Conflict("应用未绑定该渠道：" + channel.Name(code) + "（" + code + "）")
		}
		// 校验三：平台渠道启用。
		rows, err := tx.ListChannelsByCodes(ctx, []string{code})
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].Status != model.StatusEnable {
			return xerr.Conflict("支付渠道已停用：" + channel.Name(code) + "（" + code + "）")
		}

		// extension：no = 订单号-发起序（行锁内计数，spec §4.9）。
		count, err := tx.CountOrderExtensions(ctx, order.ID)
		if err != nil {
			return err
		}
		ext := &model.OrderExtension{
			No:          fmt.Sprintf("%s-%d", order.No, count+1),
			OrderID:     order.ID,
			ChannelCode: code,
			UserIP:      userIP,
			Status:      model.ExtensionStatusPending,
		}
		if err := tx.InsertOrderExtension(ctx, ext); err != nil {
			return err
		}

		// 渠道统一动作：wallet 在 ctx 事务内扣减（PayByWallet），mock 返回
		// pending 等回调。金额一律订单快照，渠道请求不含任何客户端输入金额。
		creq := channel.OrderReq{
			OutTradeNo:  order.No,
			Subject:     order.Subject,
			Body:        order.Body,
			Price:       order.Price,
			BizID:       order.ID,
			NotifyURL:   order.NotifyURL,
			ExpireTime:  formatExpireTime(order.ExpireTime),
			ChannelUser: strconv.FormatInt(p.UserID, 10),
		}
		resp, err := client.Order(payrepo.WithTx(ctx, tx), creq)
		if err != nil {
			// 余额不足等业务失败：整体回滚（extension 不落库、无部分扣减）。
			return err
		}
		// 回写最近发起（channel_code/extension_id）；wallet 同步成功由认领
		// 语句一并回写，mock pending 分支单独回写。
		if resp.Status == channel.OrderStatusSuccess {
			claimed, err := s.paymentSuccessTx(ctx, tx, tenantID, order, ext, "", creq.ChannelUser, "")
			if err != nil {
				return err
			}
			if !claimed {
				// 同单并发已被认领：回滚本次扣减（409，钱包余额不变）。
				return xerr.Conflict("订单已支付，请勿重复发起")
			}
		} else if err := tx.UpdateOrderChannelAttempt(ctx, tenantID, order.ID, code, ext.ID); err != nil {
			return err
		}
		out = &dto.OrderSubmitResp{
			OrderID:     order.ID,
			ExtensionID: ext.ID,
			ExtensionNo: ext.No,
			Status:      orderStatusAfter(resp.Status),
			DisplayMode: resp.DisplayMode,
			Content:     resp.Content,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PayByWallet 实现 channel.WalletPayer（wallet 渠道扣减后端）：从 ctx 取发起
// 事务，经唯一入账入口扣减下单用户钱包（biz=3 支付，负价入账，biz_id=订单 ID，
// spec §4.2/§4.3）。余额不足 409（调用方整体回滚，无部分扣减）；不做订单
// 状态变更（认领/extension/建任务由发起方在扣减成功后走支付成功入账函数）。
func (s *Service) PayByWallet(ctx context.Context, req channel.OrderReq) error {
	tx, ok := payrepo.TxFrom(ctx)
	if !ok {
		return xerr.Internal(errors.New("wallet 渠道扣减缺少事务上下文"))
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return xerr.Unauthorized("")
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(req.ChannelUser), 10, 64)
	if err != nil || userID <= 0 {
		return xerr.BadRequest("wallet 渠道缺少合法的下单用户标识")
	}
	change := WalletChange{
		UserID:   userID,
		UserType: model.WalletUserTypeAdmin,
		BizType:  model.WalletBizPayment,
		BizID:    req.BizID,
		Title:    clampTitle("支付：" + req.Subject),
		Price:    money.FromCents(-req.Price.Cents()),
	}
	_, err = s.applyWalletChangeTx(ctx, tx, tenantID, change)
	return err
}

// ---------- 支付成功入账（三条路径共用，spec §4.2） ----------

// paymentSuccessTx 支付成功入账（wallet 发起 / mock 模拟回调 / 未来真实渠道
// 回调共用；spec §4.2）：
//  1. 订单状态认领 UPDATE ... WHERE id=? AND status=0——0 行 = 已被并发方
//     处理，幂等成功返回（不重复入账/不重复建任务）；
//  2. success_time + 渠道回执（channel_code/order_no/user_id/extension_id）；
//  3. extension 置成功（末次回调原文落 channel_notify_data）；
//  4. 建商户回调任务（type=1，data_id=订单 ID，notify_url 下单快照，
//     max_notify_times=9，next_notify_time=now；job 消费是 #134）；
//  5. 业务联动扩展点（#136 充值入账注册于 New；后续票经
//     SetOrderSuccessHandler 追加）——认领后同事务执行，任一失败整体回滚。
//
// 函数内不做任何余额变动：wallet 扣减发生在渠道 Order 内（先扣后认领，认领
// 失败整体回滚），mock/真实渠道资金为外部到账；业务联动里的余额变动（充值
// 入账 biz=1）在扩展点内经唯一入账入口完成。
func (s *Service) paymentSuccessTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, order *model.Order, ext *model.OrderExtension, channelOrderNo, channelUserID, notifyData string) (bool, error) {
	successTime := s.businessNow()
	rows, err := tx.ClaimOrderSuccess(ctx, tenantID, order.ID, ext.ID, ext.ChannelCode, channelOrderNo, channelUserID, successTime)
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	if err := tx.UpdateExtensionResult(ctx, tenantID, ext.ID, model.ExtensionStatusSuccess, "", "", notifyData); err != nil {
		return false, err
	}
	task := &model.NotifyTask{
		AppID:           order.AppID,
		Type:            model.NotifyTypePayment,
		DataID:          order.ID,
		MerchantOrderID: order.MerchantOrderID,
		Status:          model.NotifyTaskStatusWaiting,
		NotifyURL:       order.NotifyURL,
		NextNotifyTime:  &successTime,
		NotifyTimes:     0,
		MaxNotifyTimes:  model.NotifyTaskMaxTimes,
	}
	if err := tx.InsertNotifyTask(ctx, task); err != nil {
		return false, err
	}
	// 业务联动扩展点（#136 充值入账等）：认领成功后同事务执行，任一失败
	// 整体回滚（订单认领/extension/任务一并回滚，回调可安全重试）。
	for _, hook := range s.orderSuccessHandlers {
		if err := hook(ctx, tx, tenantID, order, ext); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ---------- mock 模拟回调（POST /pay/mock/notify，登录 + 本人 + mock 渠道） ----------

// MockNotifyOrder 触发 mock 回调（spec §4.2/§4.6：不公开端点的服务端入口）：
// 订单归属本人（代他人 403）且当前渠道为 mock；成功分支经 mock 渠道
// ParseOrderNotify 解析（与真实渠道回调同一解析链路）后走支付成功入账函数
// （认领 0 行 = 并发已处理，幂等成功返回）；失败分支置 extension 失败，订单
// 保持待支付。
func (s *Service) MockNotifyOrder(ctx context.Context, req *dto.MockNotifyReq) (*model.Order, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	order, err := s.repo.FindOrderByID(ctx, req.OrderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, xerr.NotFound("支付订单不存在")
	}
	if order.UserID != p.UserID {
		// 防代他人确认支付（spec §4.6）：存在但非本人 → 403。
		return nil, xerr.Forbidden("仅本人订单可触发模拟回调")
	}
	if order.ChannelCode != channel.CodeMock {
		return nil, xerr.Conflict("订单当前渠道不支持模拟回调：" + order.ChannelCode)
	}
	mockClient := s.channelRegistry().Get(channel.CodeMock)
	if mockClient == nil {
		return nil, xerr.Conflict("渠道未实现：" + channel.Name(channel.CodeMock))
	}

	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		ext, err := tx.FindPendingExtensionByID(ctx, order.ID)
		if err != nil {
			return err
		}
		if ext == nil {
			return xerr.Conflict("订单无处理中的支付发起，无法模拟回调")
		}
		// 构造回调原文并走渠道解析（与真实渠道回调同一入口，spec §4.2）。
		raw, err := json.Marshal(map[string]string{"outTradeNo": ext.No, "result": req.Result})
		if err != nil {
			return err
		}
		notify, err := mockClient.ParseOrderNotify(ctx, channel.NotifyReq{Body: raw})
		if err != nil {
			return err
		}
		if notify.Success {
			_, err = s.paymentSuccessTx(ctx, tx, tenantID, order, ext, notify.ChannelOrderNo, "", notify.Raw)
			return err
		}
		return tx.UpdateExtensionResult(ctx, tenantID, ext.ID, model.ExtensionStatusFail, "MOCK_FAIL", "模拟回调失败", notify.Raw)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindOrderByID(ctx, req.OrderID)
}

// ---------- 渠道支付回调（POST /pay/channel-notify/:channelCode，公开，spec §4.6） ----------

// ErrChannelNotifyUnknownChannel 是渠道回调端点的 404 哨兵（spec §4.6
// 「未实现/停用渠道 404」）：注册表无 Client 与平台停用同答不区分（不泄露
// 渠道实现/启停差异）。
var ErrChannelNotifyUnknownChannel = errors.New("渠道回调端点不存在")

// ErrChannelNotifyPayloadInvalid 是回调验签/解析失败的哨兵（spec §4.6
// 「验签失败 4xx」）：验签在渠道 Client.ParseOrderNotify 内完成，解析拒绝
// 一律经本哨兵翻译为 4xx。
var ErrChannelNotifyPayloadInvalid = errors.New("回调验签或报文无效")

// ChannelNotifyResult 渠道支付回调的处理结果（HTTP 应答体构造用）。
type ChannelNotifyResult struct {
	OutTradeNo     string // 回调定位的支付发起单号（ZF 单号-发起序）
	ChannelOrderNo string // 渠道回执单号
	Success        bool   // 回调声明的支付结果
	Claimed        bool   // true = 本次认领入账；false = 幂等重放跳过 / 失败结论
}

// ChannelNotifyOrder 渠道支付回调服务端入口（spec §4.6 公开端点，PR #140 F1）：
// 渠道码 → 注册表 Client ∧ 平台 pay_channel 启用（否则 404）→ 渠道验签解析
// （验签在 Client 内；拒绝 → 4xx）→ 按 outTradeNo 定位支付发起 → 成功走支付
// 成功入账函数（wallet 发起 / mock 模拟回调 / 渠道回调三路共用的
// paymentSuccessTx，状态认领幂等——重复回调不双入账/不双建任务）→ 失败结论
// 置发起失败（同 mock 模拟回调失败分支口径）。应答语义同 demo 商户回调端点：
// 成功 code 0；定位/联动失败回非 0 业务码信封（回调方可重试）。
//
// 本端点只承接支付（order）回调：退款/转账渠道回调随真实渠道实现另立工单
//（SPI 的 ParseRefundNotify / 异步转账回调成对承接，spec §4.6/§10）。
//
// 定位语义（v1 即 mock 语义，演练与真实渠道同一解析链路）：回调 outTradeNo =
// 支付发起 extension no（ZF 单号-发起序，/pay/mock/notify 构造的回调原文同
// 源）；报文不带租户字段，全局反查后从行派生租户执行（同 demo 联动先例）。
// 单号 (tenant_id,no) 唯一键跨租户可重号：命中多行拒绝猜测按联动失败处理，
// 真实渠道接入时以渠道携带的应用定位字段收紧。
func (s *Service) ChannelNotifyOrder(ctx context.Context, code string, body []byte) (*ChannelNotifyResult, error) {
	client := s.channelRegistry().Get(code)
	if client == nil {
		// 注册表无 Client（未实现渠道或未知码）：404（spec §4.6）。
		return nil, ErrChannelNotifyUnknownChannel
	}
	rows, err := s.repo.ListChannelsByCodes(ctx, []string{code})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0].Status != model.StatusEnable {
		// 平台停用：404（spec §4.6——注册表命中也以平台启用状态为准）。
		return nil, ErrChannelNotifyUnknownChannel
	}
	notify, err := client.ParseOrderNotify(ctx, channel.NotifyReq{Body: body})
	if err != nil {
		// 验签/解析失败 4xx（spec §4.6）：渠道解析错误文案即面向回调方，
		// 哨兵包装供 HTTP 层分支（%w 保 errors.Is 链）。
		return nil, fmt.Errorf("%w：%w", ErrChannelNotifyPayloadInvalid, err)
	}
	outTradeNo := strings.TrimSpace(notify.OutTradeNo)
	exts, err := s.repo.FindExtensionsByNoGlobal(ctx, code, outTradeNo)
	if err != nil {
		return nil, err
	}
	if len(exts) != 1 {
		// 定位失败（发起不存在 / 单号跨租户撞号拒绝猜测）：联动失败——HTTP
		// 层回业务失败信封（同 demo 端点语义，回调方可重试）。
		return nil, fmt.Errorf("回调定位支付发起失败：out_trade_no=%s 命中 %d 条", outTradeNo, len(exts))
	}
	ext := &exts[0]
	ctx, tenantID, err := guardTenantContext(tenant.WithTenantID(ctx, ext.TenantID))
	if err != nil {
		return nil, err
	}
	order, err := s.repo.FindOrderByID(ctx, ext.OrderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		// 发起与订单同事务落库，行缺失属数据异常：联动失败交回调重试排查。
		return nil, fmt.Errorf("支付发起 %s 关联的订单 %d 不存在", ext.No, ext.OrderID)
	}
	out := &ChannelNotifyResult{OutTradeNo: ext.No, ChannelOrderNo: notify.ChannelOrderNo, Success: notify.Success}
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		if notify.Success {
			claimed, err := s.paymentSuccessTx(ctx, tx, tenantID, order, ext, notify.ChannelOrderNo, notify.ChannelUserID, notify.Raw)
			if err != nil {
				return err
			}
			out.Claimed = claimed
			return nil
		}
		// 失败结论：置发起失败（同 mock 模拟回调失败分支口径），订单不动、
		// 无任务；失败是合法回调结论，应答 code 0（渠道无需重试）。
		return tx.UpdateExtensionResult(ctx, tenantID, ext.ID, model.ExtensionStatusFail, "CHANNEL_FAIL", "渠道回调支付失败", notify.Raw)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- 管理端（权限码 pay:order:query，spec §4.11/§7） ----------

// ListOrders 分页查询订单（管理端）：pay 管理页不设 owner 行级数据权限
// （记录无创建人语义，spec §4.11），租户内全量可见。
func (s *Service) ListOrders(ctx context.Context, req *dto.OrderPageReq) ([]model.Order, int64, error) {
	if req.Status != nil {
		if err := validateOrderStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListOrders(ctx, payrepo.OrderFilter{
		No:              trimString(req.No),
		MerchantOrderID: trimString(req.MerchantOrderID),
		AppID:           req.AppID,
		ChannelCode:     trimString(req.ChannelCode),
		Status:          req.Status,
	}, page.Offset(), page.PageSize)
}

// GetOrder 订单详情（附最近一次发起 extension）。
func (s *Service) GetOrder(ctx context.Context, id int64) (*dto.OrderDetail, error) {
	order, err := s.repo.FindOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, xerr.NotFound("支付订单不存在")
	}
	ext, err := s.repo.FindLatestExtensionByID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	return &dto.OrderDetail{Order: order, Extension: ext}, nil
}

// ---------- 纯函数与助手 ----------

// orderStatusMessage 订单非待支付状态的 409 文案（submit 校验矩阵，文案可区分）。
func orderStatusMessage(status int8) string {
	switch status {
	case model.OrderStatusSuccess:
		return "订单已支付，请勿重复发起"
	case model.OrderStatusRefunded:
		return "订单已退款，无法发起支付"
	case model.OrderStatusClosed:
		return "订单已关闭，无法发起支付"
	default:
		return fmt.Sprintf("订单状态不支持支付：%d", status)
	}
}

// orderStatusAfter 把渠道 OrderResp.Status 翻译为订单支付状态（wallet 同步
// 成功 → 10；pending → 0）。
func orderStatusAfter(respStatus string) int8 {
	if respStatus == channel.OrderStatusSuccess {
		return model.OrderStatusSuccess
	}
	return model.OrderStatusPending
}

// validateOrderStatus 校验订单状态过滤值枚举。
func validateOrderStatus(status int8) error {
	switch status {
	case model.OrderStatusPending, model.OrderStatusSuccess, model.OrderStatusRefunded, model.OrderStatusClosed:
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("订单状态不合法：%d（0 待支付 / 10 成功 / 20 已退款 / 30 已关闭）", status))
	}
}

// containsCode 报告 codes 是否包含 code。
func containsCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// formatExpireTime 订单过期时间转 RFC3339（渠道请求预留字段；nil 返回空串）。
func formatExpireTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// clampTitle 把钱包流水标题截断到 100 字（订单 subject 最长 100，拼接后可能超）。
func clampTitle(title string) string {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) > 100 {
		return string(runes[:100])
	}
	return string(runes)
}
