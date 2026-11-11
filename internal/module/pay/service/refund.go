package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// refund.go 是退款域（issue #135，docs/pay-mvp-spec.md §4.4/§4.9/§4.10/§8.2
// 退款行）。
//
// 发起 POST /pay/refund/create（消费方服务端端点，登录即可，同 #133 下单契约）：
// 挂订单须 10 成功（订单行锁内重读裁决，非 10 各状态 409 文案可区分）；
// refund_price > 0（409）；order.refund_price + 本次 ≤ price（409 超额，文案含
// 已退/本次/实付）；merchant_refund_id 幂等同下单（撞 UNIQUE 返回原单不报错，
// 跨应用/跨租户同号不冲突）；TK 单号 pay_sequence 事务内发放（DTO 不收 no）；
// pay_price 原单实付快照；notify_url 缺省取应用 refund_notify_url 快照（请求
// 可覆盖，同 #133 订单 notify_url 口径）。渠道一律原路退回：channel_code =
// 订单支付渠道（订单 10 必有渠道回执）。
//
// 渠道 Refund（发起事务内）：
//   - wallet：唯一入账入口回加原下单用户余额（biz=4，正价）→ 同步成功
//    （与支付扣减同锁路径，spec §4.4 回加并发安全）；
//   - mock：按渠道配置模拟成功/失败两分支（失败分支供测试，demo 退款默认
//     走成功分支）。
//
// 成功统一走退款成功入账函数（refundSuccessTx，三条路径共用）：退款单认领
// 0→10（0 行 = 已处理，幂等返回）→ 订单 refund_price 累计（全额置 20、部分
// 保持 10）→ 建商户回调任务（type=2，max_notify_times=9）。函数内不做任何
// 余额变动（wallet 回加在渠道 Refund 内，先回加后认领，认领失败整体回滚）。
//
// 越权防线（spec §4.11）：发起端点只挂登录，订单非本人 403（防代他人退款）；
// 管理端点挂 pay:refund:query（管理页不设 owner 行级数据权限，租户内全量
// 可见）；跨租户业务 ID 一律 404。

// ---------- 应用删除引用检查（spec §4.10，#135 注册退款引用） ----------

// RefundRefChecker 是退款单引用检查（AppRefChecker 实现）：应用存在退款单
// 禁删。与 OrderRefChecker 以 RefCheckerChain 组合注册（internal/app 装配处）。
type RefundRefChecker struct {
	Repo *payrepo.Repository
}

// AppHasReference 实现 AppRefChecker：有退款单（任意状态）即引用。
func (c RefundRefChecker) AppHasReference(ctx context.Context, appID int64) (bool, string, error) {
	total, err := c.Repo.CountRefundsByAppID(ctx, appID)
	if err != nil {
		return false, "", err
	}
	return total > 0, "退款单", nil
}

// ---------- 发起退款（POST /pay/refund/create，登录） ----------

// CreateRefund 创建退款单并按订单支付渠道原路发起退款：merchant_refund_id
// 幂等——同租户同应用撞幂等键返回原单（200 不报错，失败原单亦然——消费方
// 换新 merchant_refund_id 重试，幂等语义与下单一致自负其责）；跨应用/跨租户
// 同号不冲突。见包内 refund.go 头注释的校验矩阵与两分支语义。
func (s *Service) CreateRefund(ctx context.Context, req *dto.RefundCreateReq, userIP string) (*model.Refund, error) {
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
	merchantRefundID := trimString(req.MerchantRefundID)
	if appKey == "" || merchantOrderID == "" || merchantRefundID == "" {
		return nil, xerr.BadRequest("应用标识、消费方订单号与消费方退款单号不能为空")
	}
	if len(merchantRefundID) > 64 {
		return nil, xerr.BadRequest("消费方退款单号不能超过 64 字")
	}
	if req.RefundPrice.Cents() <= 0 {
		// spec §4.4/验收：refund_price ≤ 0 一律 409（同超额/状态校验的冲突语义）。
		return nil, xerr.Conflict(fmt.Sprintf("退款金额必须大于 0：%s", req.RefundPrice))
	}

	// 幂等快路径：已有同号退款单直接返回原单（不校验金额一致性——幂等语义
	// 消费方自负其责，与 #133 下单口径一致）。
	app, err := s.findAppByKey(ctx, appKey)
	if err != nil {
		return nil, err
	}
	if existing, err := s.repo.FindRefundByMerchantID(ctx, app.ID, merchantRefundID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	order, err := s.repo.FindOrderByMerchantID(ctx, app.ID, merchantOrderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		// 不存在/跨租户统一 404，不泄露存在性（spec §8.2 越权行）。
		return nil, xerr.NotFound("支付订单不存在")
	}
	if order.UserID != p.UserID {
		// 防代他人退款（spec §4.11 自助端点归属校验，同 submit/mock 口径 403）。
		return nil, xerr.Forbidden("仅本人订单可发起退款")
	}

	var out *model.Refund
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		// 订单行锁内重读：status/refund_price/price 为权威值（同一订单的并发
		// 退款由行锁串行化，累计判定无并发窗，spec §4.4）。渠道校验在状态
		// 校验之后：非 10 订单的状态 409 文案优先（可区分矩阵）。
		locked, err := tx.LockOrder(ctx, tenantID, order.ID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("支付订单不存在")
		}
		if locked.Status != model.OrderStatusSuccess {
			return xerr.Conflict(refundOrderStatusMessage(locked.Status))
		}
		if locked.ChannelCode == "" {
			// 10 成功订单必有渠道回执；DB 造数据绕过时明确拒绝（防御）。
			return xerr.Conflict("订单缺少支付渠道，无法原路退回")
		}
		client := s.channelRegistry().Get(locked.ChannelCode)
		if client == nil {
			return xerr.Conflict("渠道未实现：" + channel.Name(locked.ChannelCode) + "（" + locked.ChannelCode + "）")
		}
		if total, err := locked.RefundPrice.Add(req.RefundPrice); err != nil {
			return xerr.Conflict(fmt.Sprintf("累计退款金额超出范围：已退 %s，本次 %s，实付 %s", locked.RefundPrice, req.RefundPrice, locked.Price))
		} else if total.Cents() > locked.Price.Cents() {
			return xerr.Conflict(fmt.Sprintf("累计退款超过实付金额：已退 %s，本次 %s，实付 %s", locked.RefundPrice, req.RefundPrice, locked.Price))
		}

		// TK 单号：pay_sequence 事务内发放（spec §4.9），DTO 不收 no（客户端
		// 提交的 no 一律不采纳）。
		key := dateKey(s.businessNow())
		seq, err := tx.NextSequenceValue(ctx, tenantID, SeqRefund, key)
		if err != nil {
			return err
		}
		notifyURL := trimString(req.NotifyURL)
		if notifyURL == "" {
			notifyURL = app.RefundNotifyURL
		}
		out = &model.Refund{
			AppID:            app.ID,
			No:               formatNo(NoPrefixRefund, key, seq),
			OrderID:          locked.ID,
			OrderNo:          locked.No,
			MerchantOrderID:  locked.MerchantOrderID,
			MerchantRefundID: merchantRefundID,
			UserID:           locked.UserID,
			UserType:         locked.UserType,
			ChannelCode:      locked.ChannelCode, // 原路退回（spec §4.4）
			NotifyURL:        notifyURL,
			PayPrice:         locked.Price,
			RefundPrice:      req.RefundPrice,
			Reason:           trimString(req.Reason),
			UserIP:           userIP,
			ChannelOrderNo:   locked.ChannelOrderNo,
		}
		if err := tx.InsertRefund(ctx, out); err != nil {
			if !payrepo.IsDuplicateEntry(err) {
				return err
			}
			// 撞幂等键：并发首触的赢家已提交（INSERT 等待其定局），回读原单。
			existing, ferr := tx.FindRefundByMerchantID(ctx, app.ID, merchantRefundID)
			if ferr != nil {
				return ferr
			}
			if existing == nil {
				return xerr.Conflict("退款单创建冲突，请重试：" + merchantRefundID)
			}
			out = existing
			return nil
		}

		// 渠道统一动作：wallet 在 ctx 事务内回加（biz=4），mock 按配置模拟
		// 成功/失败。金额一律退款单快照，渠道请求不含任何客户端输入金额。
		resp, err := client.Refund(payrepo.WithTx(ctx, tx), channel.RefundReq{
			OutRefundNo:    out.No,
			ChannelOrderNo: out.ChannelOrderNo,
			RefundPrice:    out.RefundPrice,
			Reason:         out.Reason,
			BizID:          out.ID,
		})
		if err != nil {
			// 回加失败（如累计列越界）：整体回滚，退款单不落库、余额不变。
			return err
		}
		switch resp.Status {
		case channel.RefundStatusSuccess:
			claimed, err := s.refundSuccessTx(ctx, tx, tenantID, out, resp.ChannelRefundNo, resp.Raw)
			if err != nil {
				return err
			}
			if !claimed {
				// 同退款单并发已被认领：回滚本次回加（409，钱包余额不变）。
				return xerr.Conflict("退款单已处理，请勿重复发起")
			}
		case channel.RefundStatusFail:
			// mock 失败分支（spec §4.4 供测试）：退款单置 20 失败，订单不累计、
			// 无回调任务；错误列与回调原文落库。
			return tx.UpdateRefundResult(ctx, tenantID, out.ID, model.RefundStatusFail, "MOCK_FAIL", "模拟退款失败", resp.Raw)
		default:
			// 处理中（异步渠道预留）：退款单停 0 待处理，等渠道回调后走
			// refundSuccessTx（channel-notify 端点随真实渠道实现承接）。
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 回读终态（同步成功 10 / 失败 20 / 处理中 0）。
	return s.repo.FindRefundByID(ctx, out.ID)
}

// refundOrderStatusMessage 订单非成功状态的 409 文案（退款发起校验矩阵，
// 文案可区分）。
func refundOrderStatusMessage(status int8) string {
	switch status {
	case model.OrderStatusPending:
		return "订单未支付，无法退款"
	case model.OrderStatusRefunded:
		return "订单已全额退款，无法再次退款"
	case model.OrderStatusClosed:
		return "订单已关闭，无法退款"
	default:
		return fmt.Sprintf("订单状态不支持退款：%d", status)
	}
}

// ---------- wallet 渠道退款回加后端（channel.WalletRefunder 实现） ----------

// RefundByWallet 实现 channel.WalletRefunder（wallet 渠道回加后端）：从 ctx 取
// 发起事务，回读本事务内已落库的退款单（req.BizID），经唯一入账入口回加原
// 下单用户钱包（biz=4 支付退款，正价入账，biz_id=退款单 ID，spec §4.3/§4.4）。
// 累计列越界等业务失败由入账函数返回（调用方整体回滚，无部分入账）；不做
// 退款单/订单状态变更（认领/累计/建任务由发起方在回加成功后走退款成功入账
// 函数）。
func (s *Service) RefundByWallet(ctx context.Context, req channel.RefundReq) error {
	tx, ok := payrepo.TxFrom(ctx)
	if !ok {
		return xerr.Internal(errors.New("wallet 渠道退款缺少事务上下文"))
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return xerr.Unauthorized("")
	}
	if req.BizID <= 0 {
		return xerr.BadRequest("wallet 渠道退款缺少退款单据 ID")
	}
	refund, err := tx.FindRefundByID(ctx, req.BizID)
	if err != nil {
		return err
	}
	if refund == nil {
		// 回加对象取退款单的 user_id/user_type 快照（原路退回）：单据缺失属
		// 程序错误（发起方必须先落库再调渠道）。
		return xerr.Internal(errors.New("wallet 渠道退款单不存在或不可见"))
	}
	change := WalletChange{
		UserID:   refund.UserID,
		UserType: refund.UserType,
		BizType:  model.WalletBizPaymentRefund,
		BizID:    refund.ID,
		Title:    clampTitle(refundFlowTitle(refund)),
		Price:    money.FromCents(refund.RefundPrice.Cents()),
	}
	_, err = s.applyWalletChangeTx(ctx, tx, tenantID, change)
	return err
}

// refundFlowTitle 钱包退款流水标题：退款：+ 退款原因（缺省用退款单号）。
func refundFlowTitle(refund *model.Refund) string {
	if trimString(refund.Reason) == "" {
		return "退款：" + refund.No
	}
	return "退款：" + trimString(refund.Reason)
}

// ---------- 退款成功入账（三条路径共用，spec §4.4） ----------

// refundSuccessTx 退款成功入账（wallet 发起 / mock 模拟成功 / 未来真实渠道
// 退款回调共用；spec §4.4）：
//  1. 退款单状态认领 UPDATE ... WHERE id=? AND status=0——0 行 = 已被并发方
//     处理，幂等成功返回（不重复累计/不重复建任务）；
//  2. success_time + 渠道回执（channel_refund_no/末次回调原文）；
//  3. 订单 refund_price 累计（订单行锁内重读裁决：累计 = price 置 20 已退款，
//     部分退款保持 10，spec §4.4）；
//  4. 建商户回调任务（type=2，data_id=退款单 ID，merchant_refund_id，notify_url
//     发起快照，max_notify_times=9，next_notify_time=now；job 消费是 #134）。
//
// 函数内不做任何余额变动：wallet 回加发生在渠道 Refund 内（先回加后认领，
// 认领失败整体回滚），mock/真实渠道资金为外部退回。订单行锁由本函数自取
// （同步路径调用方已持有同一锁——同事务重入无等待；异步回调路径由此获得
// 锁保护，先锁后写同钱包口径）。
func (s *Service) refundSuccessTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, refund *model.Refund, channelRefundNo, notifyData string) (bool, error) {
	successTime := s.businessNow()
	rows, err := tx.ClaimRefundSuccess(ctx, tenantID, refund.ID, channelRefundNo, notifyData, successTime)
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	// 订单行锁内重读：status/refund_price/price 为权威值（同一订单的并发退款
	// 被行锁串行化，部分退款累计判定无并发窗，spec §4.4）。
	order, err := tx.LockOrder(ctx, tenantID, refund.OrderID)
	if err != nil {
		return false, err
	}
	if order == nil || order.Status != model.OrderStatusSuccess {
		return false, xerr.Conflict("订单状态不支持退款累计")
	}
	accumulated, err := order.RefundPrice.Add(refund.RefundPrice)
	if err != nil {
		return false, xerr.Conflict(fmt.Sprintf("累计退款金额超出范围：已退 %s，本次 %s，实付 %s", order.RefundPrice, refund.RefundPrice, order.Price))
	}
	newStatus := model.OrderStatusSuccess
	if accumulated.Cents() >= order.Price.Cents() {
		// 全额退完（累计 = price）订单置 20 已退款；部分退款保持 10。
		newStatus = model.OrderStatusRefunded
	}
	if err := tx.ApplyOrderRefund(ctx, tenantID, order.ID, refund.RefundPrice, newStatus); err != nil {
		return false, err
	}
	task := &model.NotifyTask{
		AppID:            refund.AppID,
		Type:             model.NotifyTypeRefund,
		DataID:           refund.ID,
		MerchantRefundID: refund.MerchantRefundID,
		Status:           model.NotifyTaskStatusWaiting,
		NotifyURL:        refund.NotifyURL,
		NextNotifyTime:   &successTime,
		NotifyTimes:      0,
		MaxNotifyTimes:   model.NotifyTaskMaxTimes,
	}
	return true, tx.InsertNotifyTask(ctx, task)
}

// ---------- 商户回调报文详情（type=2 退款，#134 的 NotifyPayloadEnricher） ----------

// enrichRefundPayload 实现 type=2（退款）回调报文详情扩展（service.New 内注册，
// spec §4.5）：原支付订单号/渠道单号/本次退款金额/退款成功时间；商户以
// merchant_refund_id（任务自带）关联自己的退款单。
func (s *Service) enrichRefundPayload(ctx context.Context, task *model.NotifyTask, payload *NotifyPayload) error {
	refund, err := s.repo.FindRefundByID(ctx, task.DataID)
	if err != nil {
		return err
	}
	if refund == nil {
		return fmt.Errorf("回调任务 %d 对应的退款单 %d 不存在", task.ID, task.DataID)
	}
	payload.OrderNo = refund.OrderNo
	payload.ChannelOrderNo = refund.ChannelOrderNo
	text, err := refund.RefundPrice.MarshalText()
	if err != nil {
		return fmt.Errorf("编码退款金额: %w", err)
	}
	payload.Price = string(text)
	if refund.SuccessTime != nil {
		payload.SuccessTime = refund.SuccessTime.Format(time.RFC3339)
	}
	return nil
}

// ---------- 管理端（权限码 pay:refund:query，spec §4.11/§7） ----------

// ListRefunds 分页查询退款单（管理端）：pay 管理页不设 owner 行级数据权限
// （记录无创建人语义，spec §4.11），租户内全量可见。
func (s *Service) ListRefunds(ctx context.Context, req *dto.RefundPageReq) ([]model.Refund, int64, error) {
	if req.Status != nil {
		if err := validateRefundStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListRefunds(ctx, payrepo.RefundFilter{
		No:               trimString(req.No),
		MerchantRefundID: trimString(req.MerchantRefundID),
		MerchantOrderID:  trimString(req.MerchantOrderID),
		AppID:            req.AppID,
		OrderID:          req.OrderID,
		ChannelCode:      trimString(req.ChannelCode),
		Status:           req.Status,
	}, page.Offset(), page.PageSize)
}

// GetRefund 退款单详情（附关联支付订单，前端详情 drawer 展示关联订单信息）。
func (s *Service) GetRefund(ctx context.Context, id int64) (*dto.RefundDetail, error) {
	refund, err := s.repo.FindRefundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if refund == nil {
		return nil, xerr.NotFound("退款单不存在")
	}
	order, err := s.repo.FindOrderByID(ctx, refund.OrderID)
	if err != nil {
		return nil, err
	}
	return &dto.RefundDetail{Refund: refund, Order: order}, nil
}

// validateRefundStatus 校验退款单状态过滤值枚举。
func validateRefundStatus(status int8) error {
	switch status {
	case model.RefundStatusPending, model.RefundStatusSuccess, model.RefundStatusFail:
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("退款单状态不合法：%d（0 待处理 / 10 成功 / 20 失败）", status))
	}
}
