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

// recharge.go 是充值与套餐域（issue #136，docs/pay-mvp-spec.md §3.4/§4.7/
// §4.10/§8.2 充值行）。
//
// 套餐 CRUD（管理端，权限 pay:wallet-recharge-package:*）：档位 name/
// pay_price/bonus_price/status；被充值单引用禁删（409，引用计数 Unscoped——
// 充值单不可删、引用永久，spec §4.10）。
//
// 发起充值（POST /pay/wallet-recharge/create，登录 + 本人钱包，spec §4.7）：
// 选套餐（pay_price/bonus_price 快照进单，请求不收金额）或自定义 pay_price
//（bonus_price 恒 0）；同事务挂一笔 pay_order（subject=钱包充值、本人
// user_id/user_type=2、expire_time 服务端设定、ZF 单号 pay_sequence 发放）。
//
// 挂单应用决策（spec 未指定挂单应用）：service 配置 rechargeAppKey（configs
// pay.recharge-app-key，装配期 SetRechargeAppKey 注入），缺省 demo——spec §7
// 演示数据每租户播种 app_key=demo 且绑定 mock/wallet（充值走收银台必须有
// 可用渠道）；应用缺失 409 引导配置。merchant_order_id = RCH-<充值单 ID>
//（同事务先落充值单拿自增 ID 再建订单；充值发起无客户端幂等键——每次提交
// 都是新的充值意图，与收银台重新进入一致；UNIQUE(tenant_id, app_id,
// merchant_order_id) 兜底）。
//
// 支付成功联动（type=1 命中充值单）：经 OrderSuccessHandler 扩展点注册
//（mirror #135 的 payloadEnricher 注册形态），paymentSuccessTx 认领订单后
// 同事务执行——充值单支付认领（pay_status 0→1 条件更新即幂等门槛，重复回调
// 0 行跳过）→ 唯一入账入口入账 pay_price + bonus_price（biz=1，
// total_recharge 累计）。先认领后入账：入账失败整体回滚，不留「已认领未入账」。
//
// 充值退款（ride #135 退款机制，spec §4.7/§8.2）：发起挂充值单对应支付订单
//——仅全额（refund_price = 订单实付，充值单 refund_* 三列是单次全额回写，
// 无部分退款累计）且余额充足（先校验后退款，不足 409 不发起）；退款成功回调
//（type=2）扣回钱包 refund_total_price（pay + bonus 全额，biz=2，出向 SQL
// 双保险）+ refund_* 三列回写 + total_recharge 调减；成功回调时余额不足以
// 扣回（发起后余额被并发消费）→ 退款单认领 0→20 失败、充值单记失败态、
// 不入账不累计不建任务。
//
// 越权防线（spec §4.11）：发起/我的充值只挂登录，服务层强制 wallet 归属
// 本人；管理端分页挂 pay:wallet-recharge:query（不设 owner 行级数据权限）。

// DefaultRechargeAppKey 是充值单挂单应用的缺省 app_key（见包内 recharge.go
// 头注释的挂单应用决策）。
const DefaultRechargeAppKey = "demo"

// RechargeMerchantOrderPrefix 是充值单挂单 merchant_order_id 前缀
// （RCH-<充值单 ID>，见头注释）。
const RechargeMerchantOrderPrefix = "RCH-"

// rechargeOrderSubject 是充值单挂单的商品标题（spec §4.7 固定：subject=钱包充值）。
const rechargeOrderSubject = "钱包充值"

// ---------- 充值套餐 CRUD（管理端，spec §5） ----------

// ListRechargePackages 分页查询充值套餐（管理端）：pay 管理页不设 owner 行级
// 数据权限（spec §4.11），租户内全量可见。
func (s *Service) ListRechargePackages(ctx context.Context, req *dto.RechargePackagePageReq) ([]model.WalletRechargePackage, int64, error) {
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListRechargePackages(ctx, payrepo.RechargePackageFilter{
		Name:   trimString(req.Name),
		Status: req.Status,
	}, page.Offset(), page.PageSize)
}

// GetRechargePackage 充值套餐详情。
func (s *Service) GetRechargePackage(ctx context.Context, id int64) (*model.WalletRechargePackage, error) {
	row, err := s.repo.FindRechargePackageByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("充值套餐不存在")
	}
	return row, nil
}

// CreateRechargePackage 创建充值套餐：payPrice > 0、bonusPrice ≥ 0（409，
// spec §8.2 金额类校验冲突语义）；status 缺省启用。
func (s *Service) CreateRechargePackage(ctx context.Context, req *dto.RechargePackageSaveReq) (*model.WalletRechargePackage, error) {
	name, payPrice, bonusPrice, err := validateRechargePackage(req)
	if err != nil {
		return nil, err
	}
	status := int8(model.StatusEnable)
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	row := &model.WalletRechargePackage{
		Name:       name,
		PayPrice:   payPrice,
		BonusPrice: bonusPrice,
		Status:     status,
	}
	if err := s.repo.InsertRechargePackage(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateRechargePackage 修改充值套餐（id 在 body）：改档位只影响后续新建
// 充值单（快照隔离），已建充值单不变（spec 验收）。
func (s *Service) UpdateRechargePackage(ctx context.Context, req *dto.RechargePackageUpdateReq) error {
	name, payPrice, bonusPrice, err := validateRechargePackage(&req.RechargePackageSaveReq)
	if err != nil {
		return err
	}
	row, err := s.repo.FindRechargePackageByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("充值套餐不存在")
	}
	fields := map[string]any{
		"name":        name,
		"pay_price":   payPrice,
		"bonus_price": bonusPrice,
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return err
		}
		fields["status"] = *req.Status
	}
	return s.repo.UpdateRechargePackageFields(ctx, row.ID, fields)
}

// DeleteRechargePackage 删除充值套餐（软删）：被充值单引用禁删（409，spec
// §4.10）——引用计数 Unscoped（充值单不可删，引用永久）。
func (s *Service) DeleteRechargePackage(ctx context.Context, id int64) error {
	row, err := s.repo.FindRechargePackageByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("充值套餐不存在")
	}
	refs, err := s.repo.CountRechargesByPackageID(ctx, row.ID)
	if err != nil {
		return err
	}
	if refs > 0 {
		return xerr.Conflict(fmt.Sprintf("充值套餐已被 %d 笔充值单引用，禁止删除", refs))
	}
	return s.repo.DeleteRechargePackage(ctx, row.ID)
}

// ListRechargePackageOptions 启用套餐清单（自助端点「发起充值」的套餐选择
// 数据源，登录即可，同 /pay/channel/list 的租户侧只读清单先例）。
func (s *Service) ListRechargePackageOptions(ctx context.Context) ([]dto.RechargePackageOption, error) {
	rows, err := s.repo.ListEnabledRechargePackages(ctx)
	if err != nil {
		return nil, err
	}
	options := make([]dto.RechargePackageOption, 0, len(rows))
	for _, row := range rows {
		pay, err := row.PayPrice.MarshalText()
		if err != nil {
			return nil, fmt.Errorf("编码套餐实付金额: %w", err)
		}
		bonus, err := row.BonusPrice.MarshalText()
		if err != nil {
			return nil, fmt.Errorf("编码套餐赠送金额: %w", err)
		}
		options = append(options, dto.RechargePackageOption{
			ID:         row.ID,
			Name:       row.Name,
			PayPrice:   string(pay),
			BonusPrice: string(bonus),
		})
	}
	return options, nil
}

// validateRechargePackage 校验套餐公共字段（纯函数）：name 必填、payPrice > 0、
// bonusPrice ≥ 0（409 文案可区分）。
func validateRechargePackage(req *dto.RechargePackageSaveReq) (name string, payPrice, bonusPrice money.Money, err error) {
	name = trimString(req.Name)
	if name == "" {
		return "", money.Money{}, money.Money{}, xerr.BadRequest("套餐名称不能为空")
	}
	if req.PayPrice.Cents() <= 0 {
		return "", money.Money{}, money.Money{}, xerr.Conflict(fmt.Sprintf("套餐实付档位必须大于 0：%s", req.PayPrice))
	}
	if req.BonusPrice.Cents() < 0 {
		return "", money.Money{}, money.Money{}, xerr.Conflict(fmt.Sprintf("套餐赠送档位不能为负：%s", req.BonusPrice))
	}
	return name, req.PayPrice, req.BonusPrice, nil
}

// ---------- 挂单应用（configs pay.recharge-app-key，缺省 demo） ----------

// SetRechargeAppKey 注入充值挂单应用标识（装配期，configs pay.recharge-app-key）。
// 空 = 缺省 demo（见头注释的挂单应用决策）。
func (s *Service) SetRechargeAppKey(appKey string) {
	s.rechargeAppKey = trimString(appKey)
}

// rechargeApp 解析充值单挂单应用（租户内按 app_key 查找）：缺失 409 引导配置
// （挂单应用是配置问题而非资源路径参数，冲突语义 + 指引文案）。
func (s *Service) rechargeApp(ctx context.Context) (*model.App, error) {
	key := s.rechargeAppKey
	if key == "" {
		key = DefaultRechargeAppKey
	}
	app, err := s.repo.FindAppByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, xerr.Conflict(fmt.Sprintf(
			"充值支付应用不存在（app_key=%s）：请先创建支付应用，或将 pay.recharge-app-key 配置为已有应用", key))
	}
	return app, nil
}

// ---------- 发起充值（POST /pay/wallet-recharge/create，登录 + 本人钱包） ----------

// CreateRecharge 发起充值（spec §4.7）：选套餐（快照）或自定义 pay_price
// （bonus_price 恒 0），同事务挂一笔支付订单（subject=钱包充值、本人
// user_id/user_type=2、expire_time 服务端设定、ZF 单号事务内发放）。
// 见包内头注释的挂单应用决策与幂等口径。返回充值单 + 收银台定位参数。
func (s *Service) CreateRecharge(ctx context.Context, req *dto.RechargeCreateReq) (*dto.RechargeCreateResp, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	if req.PackageID <= 0 && req.PayPrice.Cents() <= 0 {
		return nil, xerr.BadRequest("请选择充值套餐，或填写自定义充值金额")
	}
	if req.PackageID > 0 && req.PayPrice.Cents() > 0 {
		return nil, xerr.BadRequest("套餐充值金额由套餐档位决定，请勿提交 payPrice")
	}
	app, err := s.rechargeApp(ctx)
	if err != nil {
		return nil, err
	}

	var (
		recharge *model.WalletRecharge
		order    *model.Order
	)
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		// 套餐快照（packageID > 0）：档位服务端取——请求不收金额（金额服务端
		// 定，spec §4.2/§4.7），停用套餐 409（选择器只出启用档位，直调兜底）。
		var payPrice, bonusPrice money.Money
		var packageID *int64
		body := "自定义充值金额"
		if req.PackageID > 0 {
			pkg, err := tx.FindRechargePackageByID(ctx, req.PackageID)
			if err != nil {
				return err
			}
			if pkg == nil {
				return xerr.NotFound("充值套餐不存在")
			}
			if pkg.Status != model.StatusEnable {
				return xerr.Conflict("充值套餐已停用：" + pkg.Name)
			}
			payPrice, bonusPrice = pkg.PayPrice, pkg.BonusPrice
			pid := pkg.ID
			packageID = &pid
			body = "充值套餐：" + pkg.Name
		} else {
			payPrice = req.PayPrice
			bonusPrice = money.Money{}
		}
		totalPrice, err := payPrice.Add(bonusPrice)
		if err != nil {
			return xerr.Conflict(fmt.Sprintf("充值金额超出范围：实付 %s + 赠送 %s", payPrice, bonusPrice))
		}

		// 本人钱包（懒创建；自助端点归属即登录主体，请求不可指定他人钱包）。
		walletID, err := tx.EnsureWallet(ctx, tenantID, p.UserID, model.WalletUserTypeAdmin)
		if err != nil {
			return err
		}

		// 充值单先落库拿自增 ID：merchant_order_id = RCH-<充值单 ID>（头注释）。
		recharge = &model.WalletRecharge{
			TenantID:   tenantID,
			WalletID:   walletID,
			TotalPrice: totalPrice,
			PayPrice:   payPrice,
			BonusPrice: bonusPrice,
			PackageID:  packageID,
			PayStatus:  model.RechargePayStatusPending,
		}
		if err := tx.InsertRecharge(ctx, recharge); err != nil {
			return err
		}

		// 同事务挂支付订单（spec §4.7）：ZF 单号事务内发放；merchant_order_id
		// 幂等键取充值单派生号；notify_url 取应用 order_notify_url 快照；
		// expire_time 服务端设定（同 #133 下单口径）。
		key := dateKey(s.businessNow())
		seq, err := tx.NextSequenceValue(ctx, tenantID, SeqOrder, key)
		if err != nil {
			return err
		}
		expireTime := s.businessNow().Add(orderExpireDuration)
		order = &model.Order{
			AppID:           app.ID,
			No:              formatNo(NoPrefixOrder, key, seq),
			MerchantOrderID: fmt.Sprintf("%s%d", RechargeMerchantOrderPrefix, recharge.ID),
			UserID:          p.UserID,
			UserType:        model.WalletUserTypeAdmin,
			Subject:         rechargeOrderSubject,
			Body:            body,
			NotifyURL:       app.OrderNotifyURL,
			Price:           payPrice,
			UserIP:          "",
			ExpireTime:      &expireTime,
		}
		if err := tx.InsertOrder(ctx, order); err != nil {
			if payrepo.IsDuplicateEntry(err) {
				// 充值单 ID 派生号撞幂等键只在充值行被并发复用时出现（自增
				// ID 全局唯一），直调兜底 409。
				return xerr.Conflict("充值订单创建冲突，请重试：" + order.MerchantOrderID)
			}
			return err
		}
		return tx.UpdateRechargePayOrder(ctx, recharge.ID, order.ID)
	})
	if err != nil {
		return nil, err
	}
	return &dto.RechargeCreateResp{
		Recharge:        recharge,
		AppKey:          app.AppKey,
		OrderID:         order.ID,
		OrderNo:         order.No,
		MerchantOrderID: order.MerchantOrderID,
	}, nil
}

// ---------- 支付成功联动（type=1 命中充值单，spec §4.7） ----------

// creditRechargeOnOrderSuccess 是 OrderSuccessHandler 的充值实现（service.New
// 注册，mirror #135 enrichRefundPayload 的注册形态）：支付订单认领成功后同
// 事务联动充值单——支付认领（pay_status 0→1 条件更新即幂等门槛，重复回调
// 0 行跳过）→ 唯一入账入口入账 total_price（biz=1，total_recharge 累计）。
// 先认领后入账：入账失败整体回滚（认领一并回滚，回调可安全重试）。
func (s *Service) creditRechargeOnOrderSuccess(ctx context.Context, tx *payrepo.Repository, tenantID int64, order *model.Order, ext *model.OrderExtension) error {
	recharge, err := tx.FindRechargeByOrderID(ctx, order.ID)
	if err != nil {
		return err
	}
	if recharge == nil {
		// 非充值订单：无联动（示例订单等消费方走各自链路）。
		return nil
	}
	payTime := s.businessNow()
	rows, err := tx.ClaimRechargePaid(ctx, tenantID, recharge.ID, ext.ChannelCode, payTime)
	if err != nil {
		return err
	}
	if rows == 0 {
		// 已被并发方入账（重复回调）：幂等跳过（spec §8.2 重复回调不重复入账）。
		return nil
	}
	_, err = s.applyWalletChangeTx(ctx, tx, tenantID, WalletChange{
		UserID:   order.UserID,
		UserType: order.UserType,
		BizType:  model.WalletBizRecharge,
		BizID:    recharge.ID,
		Title:    "充值",
		Price:    recharge.TotalPrice,
	})
	return err
}

// ---------- 充值单查询（管理端 + 自助端，spec §5/§4.11） ----------

// ListRecharges 分页查询充值单（管理端，权限 pay:wallet-recharge:query）：
// pay 管理页不设 owner 行级数据权限（spec §4.11），租户内全量可见。
func (s *Service) ListRecharges(ctx context.Context, req *dto.RechargePageReq) ([]dto.RechargeView, int64, error) {
	if req.PayStatus != nil {
		if err := validateRechargePayStatus(*req.PayStatus); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.ListRecharges(ctx, payrepo.RechargeFilter{
		WalletID:  req.WalletID,
		PackageID: req.PackageID,
		PayStatus: req.PayStatus,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	views, err := s.enrichRecharges(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// ListMyRecharges 本人充值单分页：服务层强制 walletId 为本人钱包，请求里的
// walletId 不采纳（spec §4.11）。无钱包（未发生过任何入账）返回空页。
func (s *Service) ListMyRecharges(ctx context.Context, req *dto.RechargeMyPageReq) ([]dto.RechargeView, int64, error) {
	userID, err := s.myWalletIdentity(ctx)
	if err != nil {
		return nil, 0, err
	}
	if req.PayStatus != nil {
		if err := validateRechargePayStatus(*req.PayStatus); err != nil {
			return nil, 0, err
		}
	}
	wallet, err := s.repo.FindWalletByUser(ctx, userID, model.WalletUserTypeAdmin)
	if err != nil {
		return nil, 0, err
	}
	if wallet == nil {
		return []dto.RechargeView{}, 0, nil
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.ListRecharges(ctx, payrepo.RechargeFilter{
		WalletID:  wallet.ID,
		PayStatus: req.PayStatus,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	views, err := s.enrichRecharges(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// enrichRecharges 回填充值单的挂单定位（appKey/orderNo/merchantOrderId）：
// 我的充值页跳收银台与发起退款（POST /pay/refund/create 挂充值单订单）的
// 数据源。批量两跳（订单 → 应用），缺失引用（直调造数）回填空串不阻塞列表。
func (s *Service) enrichRecharges(ctx context.Context, rows []model.WalletRecharge) ([]dto.RechargeView, error) {
	orderIDs := make([]int64, 0, len(rows))
	for i := range rows {
		if rows[i].PayOrderID > 0 {
			orderIDs = append(orderIDs, rows[i].PayOrderID)
		}
	}
	appKeyByAppID := make(map[int64]string)
	orderByID := make(map[int64]*model.Order)
	if len(orderIDs) > 0 {
		orders, err := s.repo.FindOrdersByIDs(ctx, orderIDs)
		if err != nil {
			return nil, err
		}
		appIDs := make([]int64, 0, len(orders))
		for i := range orders {
			orderByID[orders[i].ID] = &orders[i]
			appIDs = append(appIDs, orders[i].AppID)
		}
		if len(appIDs) > 0 {
			apps, err := s.repo.FindAppsByIDs(ctx, appIDs)
			if err != nil {
				return nil, err
			}
			for i := range apps {
				appKeyByAppID[apps[i].ID] = apps[i].AppKey
			}
		}
	}
	views := make([]dto.RechargeView, 0, len(rows))
	for i := range rows {
		view := dto.RechargeView{WalletRecharge: &rows[i]}
		if order := orderByID[rows[i].PayOrderID]; order != nil {
			view.AppKey = appKeyByAppID[order.AppID]
			view.OrderNo = order.No
			view.MerchantOrderID = order.MerchantOrderID
		}
		views = append(views, view)
	}
	return views, nil
}

// validateRechargePayStatus 校验充值单支付状态过滤值枚举。
func validateRechargePayStatus(status int8) error {
	switch status {
	case model.RechargePayStatusPending, model.RechargePayStatusSuccess:
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("充值单支付状态不合法：%d（0 未支付 / 1 已支付）", status))
	}
}

// ---------- 充值退款（ride #135 退款机制，spec §4.7/§8.2） ----------

// guardRechargeRefundCreateTx 是 CreateRefund 事务内的充值守卫（发起挂充值单
// 对应支付订单，spec §4.7/§8.2）：
//  1. 仅全额——充值单 refund_* 三列是单次全额回写（无部分退款累计），本次
//     refund_price 必须等于订单实付（否则 409）；
//  2. 余额充足才可发起（先校验后退款，spec §4.7：扣回总额 = pay + bonus，
//     余额不足 409 不发起、不落退款单）。
func (s *Service) guardRechargeRefundCreateTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, order *model.Order, refundPrice money.Money) error {
	recharge, err := tx.FindRechargeByOrderID(ctx, order.ID)
	if err != nil {
		return err
	}
	if recharge == nil {
		return nil
	}
	if refundPrice.Cents() != order.Price.Cents() {
		return xerr.Conflict(fmt.Sprintf("充值退款必须全额退回：实付 %s，本次 %s", order.Price, refundPrice))
	}
	current, err := tx.LockWallet(ctx, tenantID, recharge.WalletID)
	if err != nil {
		return err
	}
	if current.Balance.Cents() < recharge.TotalPrice.Cents() {
		return xerr.Conflict(fmt.Sprintf("钱包余额不足以退回充值：当前余额 %s，需扣回 %s（实付 %s + 赠送 %s）",
			current.Balance, recharge.TotalPrice, recharge.PayPrice, recharge.BonusPrice))
	}
	return nil
}

// applyRechargeRefundSuccessTx 是退款成功入账的充值联动（refundSuccessTx 订单
// 累计后同事务调用）：扣回钱包 refund_total_price（pay + bonus 全额，biz=2，
// 出向 SQL 双保险——余额不足 409 整体回滚，「先校验后退款」在发起与成功回调
// 两处先行裁决，此处兜底）+ refund_* 三列回写（refund_status <> 10 条件更新，
// 失败重试可覆盖）。
func (s *Service) applyRechargeRefundSuccessTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, recharge *model.WalletRecharge, refund *model.Refund) error {
	_, err := s.applyWalletChangeTx(ctx, tx, tenantID, WalletChange{
		UserID:   refund.UserID,
		UserType: refund.UserType,
		BizType:  model.WalletBizRechargeRefund,
		BizID:    refund.ID,
		Title:    "充值退款",
		Price:    money.FromCents(-recharge.TotalPrice.Cents()),
	})
	if err != nil {
		return err
	}
	rows, err := tx.ApplyRechargeRefundSuccess(ctx, tenantID, recharge.ID, refund.ID,
		recharge.TotalPrice, recharge.PayPrice, recharge.BonusPrice, s.businessNow())
	if err != nil {
		return err
	}
	if rows == 0 {
		// 充值单已退款成功（10）：重复回调被退款单认领先行挡住，此处 0 行属
		// 数据异常（同一充值单两个成功退款），明确拒绝整体回滚。
		return xerr.Conflict(fmt.Sprintf("充值单 %d 已退款成功，重复回写冲突", recharge.ID))
	}
	return nil
}

// failRechargeRefundTx 是退款成功回调发现余额不足以扣回的失败联动（spec §4.7
// 先校验后退款）：退款单认领 0→20 失败（幂等门槛同认领——重复回调 0 行跳过）
// + 充值单记失败态（refund_status 0→20，金额列不动、可重新发起）。
// 返回 claimed=false 表示退款单已被并发方处理（幂等返回）。
func (s *Service) failRechargeRefundTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, recharge *model.WalletRecharge, refund *model.Refund, notifyData string) (bool, error) {
	rows, err := tx.ClaimRefundFail(ctx, tenantID, refund.ID,
		"RECHARGE_BALANCE_SHORT",
		fmt.Sprintf("钱包余额不足以扣回充值（需 %s），退款失败", recharge.TotalPrice),
		notifyData)
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	if _, err := tx.FailRechargeRefund(ctx, tenantID, recharge.ID); err != nil {
		return true, err
	}
	return true, nil
}

// ProcessChannelRefundSuccess 是渠道退款成功回调的处理入口（真实渠道的
// channel-notify 端点随渠道实现接入时复用；mock 同步路径经 CreateRefund 内联
// 调用 refundSuccessTx）。开事务 → 订单行锁（先锁订单后锁钱包，与发起路径
// 加锁序一致防死锁）→ 退款成功入账函数（认领 0 行 = 已处理，幂等返回）。
func (s *Service) ProcessChannelRefundSuccess(ctx context.Context, refundID int64, channelRefundNo, notifyData string) (bool, error) {
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return false, xerr.Unauthorized("")
	}
	refund, err := s.repo.FindRefundByID(ctx, refundID)
	if err != nil {
		return false, err
	}
	if refund == nil {
		return false, xerr.NotFound("退款单不存在")
	}
	claimed := false
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		locked, err := tx.LockOrder(ctx, tenantID, refund.OrderID)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("支付订单不存在")
		}
		claimed, err = s.refundSuccessTx(ctx, tx, tenantID, refund, channelRefundNo, notifyData)
		return err
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}
