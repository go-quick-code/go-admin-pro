package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

// demo.go 是示例业务域：示例订单与示例提现（issue #137，
// docs/pay-mvp-spec.md §3.5/§4.8/§4.11/§5/§6）。
//
// 示例订单（spec §4.11「demo 创建」自助端点 + §6 发起支付/发起退款）：
//   - 创建 POST /pay/demo-order/create（登录）：spuId 取硬编码字典（spec §3.5
//     不建表），名称/金额服务端定（字典价，请求不收金额）；同事务挂支付订单
//    （merchant_order_id = DEMO-<id>，挂单应用/口径照 #136 充值挂单——
//     rechargeApp 即 pay.recharge-app-key 配置应用，缺省 demo）；发起支付经
//     收银台（appKey + merchantOrderId 定位，钱包渠道被 #136 反查守卫禁用
//     不适用于示例订单）。
//   - 退款 PUT /pay/demo-order/:id/refund（登录 + 本人订单）：金额服务端定
//    （原单未退余额全额退，累计裁决与防代退在 #135 CreateRefund 链路内收紧，
//     本端点先行 403 本人校验）；退款成功联动走 #134 回调链路（type=2 任务 →
//     job → demo/refund 端点 → demo 订单退款列回写）——task → job → demo 端点
//     是唯一权威联动链路，发起时不直写退款列（决策记录）。
//   - 分页 GET /pay/demo-order/page（管理端 pay:demo-order:query）：不设 owner
//     行级数据权限（spec §4.11），租户内全量可见；无 my-scope（示例页是管理台
//     页面，自助用户经收银台交互，决策记录见 test 报告）。
//
// 示例提现（spec §4.8 申请→审批→转账→回调联动）：
//   - 申请 POST /pay/demo-withdraw/create（登录）：user_account/user_name 服务
//     端取登录主体（请求不收，spec §4.11）；type 仅 1 钱包（2 支付宝预留，
//     创建即拒绝——无渠道可转）；status 0 等待。
//   - 审批 PUT /pay/demo-withdraw/:id/approve（权限 pay:demo-withdraw:approve）：
//     行锁内校验（0 等待且未发起转账，否则 409）→ 创建转账单
//    （merchant_transfer_id = WD-<提现单 ID>，决策记录：确定性派生号使审批
//     幂等可重试；wallet 渠道入账申请人钱包 biz=6）→ 回写 pay_transfer_id
//    （status 保持 0 等待——转账成功回调联动才置 10）。转账成功 → type=3 任务
//     → job → demo/transfer 端点 → 提现单 10 成功（#134 链路 E2E）。
//   - 关闭 PUT /pay/demo-withdraw/:id/close（权限 pay:demo-withdraw:close，
//     决策记录：spec §6 示例提现页有关闭按钮，§5 未列端点——按页面实际授予
//     补端点与独立权限码；转账在途 409 不可关，防资金事实矛盾）→ 20 不建
//     转账单。
//
// demo 商户回调联动（SetDemoNotifyHandler 注册实现，spec §4.5/§4.8）：
//   - 严格 kind↔type 校验（端点种类与报文 type 不符 → 业务失败，任务转 21
//     重试——报文错投属路由缺陷，静默吞掉会掩盖装配错误，决策记录）；
//   - 定位键 = payload.DataID（关联 pay 单据 ID，全局唯一）经 tenant.Skip 反查
//     消费行，租户从消费行派生后携带执行（决策记录：公开端点上下文无租户，
//     报文不带内部租户字段——签名校验先行，DataID 只可能来自我方 job 的签名
//     报文）；消费行不存在 → 幂等应答不联动（demo 端点是商户模拟器，多应用
//     可指向同一端点，非 demo 行的回调应答成功避免任务空转重试）；
//   - kind=order → pay_status 0→1 + pay_time/pay_channel_code（取支付订单
//     回执）；kind=refund → pay_refund_id/refund_price 累加/refund_time（取
//     退款单回执）；kind=transfer → status 0→10 + transfer_time/
//     transfer_channel_code（取转账单回执），失败报文防御性记
//     transfer_error_msg（v1 转账同步成功、任务只在成功时创建）。

// DemoMerchantOrderPrefix 是示例订单挂单 merchant_order_id 前缀（DEMO-<订单
// ID>，同 #136 充值挂单形态）。
const DemoMerchantOrderPrefix = "DEMO-"

// DemoWithdrawMerchantPrefix 是示例提现审批派生的消费方转账单号前缀
// （WD-<提现单 ID>，决策记录见头注释——确定性派生号使重复审批幂等返回
// 同一转账单）。
const DemoWithdrawMerchantPrefix = "WD-"

// DemoRefundMerchantPrefix 是示例订单退款派生的消费方退款单号前缀
// （DEMO-RFD-<订单 ID>-<UnixNano>）：决策记录——与转账的确定性派生号不同，
// 退款采用「每次发起新号」（照 #136 我的充值前端 RFR-RCH<id>-<时间戳> 先例），
// 因为退款失败（20）后重试必须能换新号重新发起，而撞旧幂等键只会返回失败
// 原单；并发双击由 #135 的订单行锁 + 累计 ≤ 实付裁决兜底（第二次 409 超额）。
const DemoRefundMerchantPrefix = "DEMO-RFD-"

// demoSPU 是演示商品（硬编码字典，spec §3.5 不建表）：ID 稳定不可变，
// 价格即服务端定价（请求不收金额）。
type demoSPU struct {
	ID    int64
	Name  string
	Price money.Money
}

// demoSPUs 是演示商品字典（3-4 款，spec §5/§6 示例订单页商品选择数据源）。
var demoSPUs = []demoSPU{
	{ID: 1, Name: "无线蓝牙耳机", Price: money.FromCents(1990)},
	{ID: 2, Name: "机械键盘 87 键", Price: money.FromCents(9900)},
	{ID: 3, Name: "便携咖啡杯", Price: money.FromCents(2990)},
	{ID: 4, Name: "精装笔记本 A5", Price: money.FromCents(1250)},
}

// demoSPUByID 按 ID 取演示商品。
func demoSPUByID(id int64) (demoSPU, bool) {
	for _, spu := range demoSPUs {
		if spu.ID == id {
			return spu, true
		}
	}
	return demoSPU{}, false
}

// ListDemoSPUs 演示商品字典清单（GET /pay/demo-order/spus，登录即可）：创建
// 表单的商品选择数据源（spec §5 未列字典端点——照 #136 套餐自助清单先例补充，
// 避免前端复刻字典漂移）。
func (s *Service) ListDemoSPUs(ctx context.Context) ([]dto.DemoSPUOption, error) {
	options := make([]dto.DemoSPUOption, 0, len(demoSPUs))
	for _, spu := range demoSPUs {
		text, err := spu.Price.MarshalText()
		if err != nil {
			return nil, fmt.Errorf("编码演示商品金额: %w", err)
		}
		options = append(options, dto.DemoSPUOption{ID: spu.ID, Name: spu.Name, Price: string(text)})
	}
	return options, nil
}

// ---------- 示例订单（创建/退款自助，分页管理端） ----------

// CreateDemoOrder 创建示例订单（spec §4.11 demo 创建自助端点）：spuId 取字典
// （名称/金额服务端定），同事务挂支付订单（merchant_order_id = DEMO-<id>，
// 挂单应用与口径照 #136 充值挂单）。返回订单视图（挂单定位供发起支付跳收银台）。
func (s *Service) CreateDemoOrder(ctx context.Context, req *dto.DemoOrderCreateReq) (*dto.DemoOrderView, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	spu, ok := demoSPUByID(req.SpuID)
	if !ok {
		return nil, xerr.NotFound(fmt.Sprintf("演示商品不存在：%d（可选 ID 1-%d）", req.SpuID, len(demoSPUs)))
	}
	app, err := s.rechargeApp(ctx)
	if err != nil {
		return nil, err
	}

	var (
		order    *model.DemoOrder
		payOrder *model.Order
	)
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		// 示例订单先落库拿自增 ID：merchant_order_id = DEMO-<订单 ID>。
		order = &model.DemoOrder{
			UserID:    p.UserID,
			SpuID:     spu.ID,
			SpuName:   spu.Name,
			Price:     spu.Price,
			PayStatus: model.DemoOrderPayStatusPending,
		}
		if err := tx.InsertDemoOrder(ctx, order); err != nil {
			return err
		}
		// 同事务挂支付订单（spec §4.2 金额服务端定：字典价）：ZF 单号事务内
		// 发放；notify_url 取应用 order_notify_url 快照；expire_time 服务端
		// 设定（同 #133/#136 挂单口径）。
		key := dateKey(s.businessNow())
		seq, err := tx.NextSequenceValue(ctx, tenantID, SeqOrder, key)
		if err != nil {
			return err
		}
		expireTime := s.businessNow().Add(orderExpireDuration)
		payOrder = &model.Order{
			AppID:           app.ID,
			No:              formatNo(NoPrefixOrder, key, seq),
			MerchantOrderID: fmt.Sprintf("%s%d", DemoMerchantOrderPrefix, order.ID),
			UserID:          p.UserID,
			UserType:        model.WalletUserTypeAdmin,
			Subject:         spu.Name,
			Body:            "示例订单",
			NotifyURL:       app.OrderNotifyURL,
			Price:           spu.Price,
			UserIP:          "",
			ExpireTime:      &expireTime,
		}
		if err := tx.InsertOrder(ctx, payOrder); err != nil {
			if payrepo.IsDuplicateEntry(err) {
				// 示例订单 ID 派生号撞幂等键只在订单行被并发复用时出现（自增
				// ID 全局唯一），直调兜底 409。
				return xerr.Conflict("示例订单挂单冲突，请重试：" + payOrder.MerchantOrderID)
			}
			return err
		}
		return tx.UpdateDemoOrderPayOrder(ctx, order.ID, payOrder.ID)
	})
	if err != nil {
		return nil, err
	}
	return &dto.DemoOrderView{
		DemoOrder:       order,
		AppKey:          app.AppKey,
		PayOrderNo:      payOrder.No,
		MerchantOrderID: payOrder.MerchantOrderID,
	}, nil
}

// RefundDemoOrder 示例订单发起退款（登录 + 本人订单）：金额服务端定——原单
// 未退余额全额退（累计 ≤ 实付与防代退在 #135 CreateRefund 链路内收紧，本端点
// 先行本人校验与未支付拦截）。退款成功联动走 #134 回调链路（type=2 任务 →
// job → demo/refund 端点），发起时不直写退款列（决策见头注释）。
func (s *Service) RefundDemoOrder(ctx context.Context, id int64, req *dto.DemoOrderRefundReq, userIP string) (*model.Refund, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	row, err := s.repo.FindDemoOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("示例订单不存在")
	}
	if row.UserID != p.UserID {
		// 防代他人退款（spec §4.11 自助端点归属校验，同 #135 退款 403 口径）。
		return nil, xerr.Forbidden("仅本人示例订单可发起退款")
	}
	if row.PayStatus != model.DemoOrderPayStatusSuccess {
		return nil, xerr.Conflict("示例订单未支付，无法退款")
	}
	payOrder, err := s.repo.FindOrderByID(ctx, row.PayOrderID)
	if err != nil {
		return nil, err
	}
	if payOrder == nil {
		// 挂单与创建同事务，行缺失属数据异常（直调造数），明确拒绝。
		return nil, xerr.Conflict("示例订单缺少关联支付订单，无法退款")
	}
	app, err := s.repo.FindAppByID(ctx, payOrder.AppID)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, xerr.Conflict("示例订单支付应用不存在，无法退款")
	}
	remaining, err := payOrder.Price.Add(money.FromCents(-payOrder.RefundPrice.Cents()))
	if err != nil || remaining.Cents() <= 0 {
		return nil, xerr.Conflict(fmt.Sprintf("示例订单已全额退款：实付 %s，已退 %s", payOrder.Price, payOrder.RefundPrice))
	}
	// 委托 #135 退款链路（原路退回、累计裁决、mock/wallet 分支、成功建 type=2
	// 任务）；merchant_refund_id 每次发起新号（决策见 DemoRefundMerchantPrefix）。
	return s.CreateRefund(ctx, &dto.RefundCreateReq{
		AppKey:           app.AppKey,
		MerchantOrderID:  payOrder.MerchantOrderID,
		MerchantRefundID: fmt.Sprintf("%s%d-%d", DemoRefundMerchantPrefix, id, time.Now().UnixNano()),
		RefundPrice:      remaining,
		Reason:           trimString(req.Reason),
	}, userIP)
}

// ListDemoOrders 分页查询示例订单（管理端，权限 pay:demo-order:query）：不设
// owner 行级数据权限（spec §4.11），租户内全量可见。
func (s *Service) ListDemoOrders(ctx context.Context, req *dto.DemoOrderPageReq) ([]dto.DemoOrderView, int64, error) {
	if req.PayStatus != nil {
		if *req.PayStatus != model.DemoOrderPayStatusPending && *req.PayStatus != model.DemoOrderPayStatusSuccess {
			return nil, 0, xerr.BadRequest(fmt.Sprintf("示例订单支付状态不合法：%d（0 未支付 / 1 已支付）", *req.PayStatus))
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.ListDemoOrders(ctx, payrepo.DemoOrderFilter{
		UserID:    req.UserID,
		PayStatus: req.PayStatus,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	views, err := s.enrichDemoOrders(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// enrichDemoOrders 回填示例订单的挂单定位（appKey/payOrderNo/merchantOrderId）：
// 发起支付按钮跳收银台的数据源（照 #136 enrichRecharges 形态）。批量两跳
// （订单 → 应用），缺失引用（直调造数）回填空串不阻塞列表。
func (s *Service) enrichDemoOrders(ctx context.Context, rows []model.DemoOrder) ([]dto.DemoOrderView, error) {
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
	views := make([]dto.DemoOrderView, 0, len(rows))
	for i := range rows {
		view := dto.DemoOrderView{DemoOrder: &rows[i]}
		if order := orderByID[rows[i].PayOrderID]; order != nil {
			view.AppKey = appKeyByAppID[order.AppID]
			view.PayOrderNo = order.No
			view.MerchantOrderID = order.MerchantOrderID
		}
		views = append(views, view)
	}
	return views, nil
}

// ---------- 示例提现（申请自助，审批/关闭管理端） ----------

// CreateDemoWithdraw 申请提现（spec §4.8，登录）：subject/price 服务端校验；
// user_account/user_name 服务端取登录主体（请求不收，spec §4.11）；type 仅
// 1 钱包（2 支付宝预留，创建即拒绝）；status 0 等待。
func (s *Service) CreateDemoWithdraw(ctx context.Context, req *dto.DemoWithdrawCreateReq) (*model.DemoWithdraw, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	subject := trimString(req.Subject)
	if subject == "" {
		return nil, xerr.BadRequest("提现标题不能为空")
	}
	if req.Price.Cents() <= 0 {
		return nil, xerr.BadRequest(fmt.Sprintf("提现金额必须大于 0：%s", req.Price))
	}
	withdrawType := int8(model.DemoWithdrawTypeWallet)
	if req.Type != nil {
		if *req.Type != model.DemoWithdrawTypeWallet {
			// 2 支付宝预留（spec §3.5）：无渠道可转，创建即拒绝（409 语义——
			// 校验矩阵里「渠道未实现」类拦截的冲突语义）。
			return nil, xerr.Conflict("提现类型暂不支持：2 支付宝为预留类型，v1 仅支持钱包提现")
		}
		withdrawType = *req.Type
	}
	row := &model.DemoWithdraw{
		UserID:      p.UserID,
		Subject:     subject,
		Price:       req.Price,
		UserAccount: strconv.FormatInt(p.UserID, 10),
		UserName:    p.Username,
		Type:        withdrawType,
		Status:      model.DemoWithdrawStatusWaiting,
	}
	if err := s.repo.InsertDemoWithdraw(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// ApproveDemoWithdraw 审批提现（管理端，权限码在路由层 pay:demo-withdraw:
// approve）：行锁内校验（0 等待且未发起转账，否则 409 文案可区分）→ 创建
// 转账单（merchant_transfer_id = WD-<提现单 ID>，wallet 渠道入账申请人钱包
// biz=6，挂单应用 = rechargeApp——决策：复用 #136 的 pay.recharge-app-key
// 配置应用，缺省 demo，示例提现是同一演示消费方）→ 回写 pay_transfer_id
// （status 保持 0 等待——转账成功回调联动才置 10，spec §4.8；重复审批的幂等
// 由 WD-<id> 撞键返回原单兜底，但锁内 pay_transfer_id != 0 先行 409 提示）。
func (s *Service) ApproveDemoWithdraw(ctx context.Context, id int64, userIP string) (*model.DemoWithdraw, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	app, err := s.rechargeApp(ctx)
	if err != nil {
		return nil, err
	}
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		// 行锁：审批与关闭互斥（先锁后写，防「转账在途被关闭」的资金事实矛盾）。
		locked, err := tx.LockDemoWithdraw(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("示例提现单不存在")
		}
		if locked.Status != model.DemoWithdrawStatusWaiting {
			return xerr.Conflict(demoWithdrawStatusMessage(locked.Status))
		}
		if locked.PayTransferID != 0 {
			return xerr.Conflict("提现单已发起转账，请勿重复审批")
		}
		if locked.Type != model.DemoWithdrawTypeWallet {
			return xerr.Conflict("提现类型暂不支持转账：2 支付宝为预留类型")
		}
		transfer, err := s.createTransferTx(ctx, tx, tenantID, app, transferParams{
			MerchantTransferID: fmt.Sprintf("%s%d", DemoWithdrawMerchantPrefix, locked.ID),
			ChannelCode:        channel.CodeWallet,
			Subject:            locked.Subject,
			Price:              locked.Price,
			UserAccount:        locked.UserAccount,
			UserName:           locked.UserName,
			UserIP:             userIP,
		})
		if err != nil {
			return err
		}
		// 转账单 ID 回写（v1 同步成功；异步渠道预留时回写在途单 ID，页面可见，
		// 终态由回调联动推进）。
		return tx.ApplyDemoWithdrawApproved(ctx, locked.ID, transfer.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindDemoWithdrawByID(ctx, id)
}

// CloseDemoWithdraw 关闭提现（管理端，权限码在路由层 pay:demo-withdraw:close）：
// 仅 0 等待且未发起转账可关（转账在途 409——资金事实不允许「已转账单被关闭」），
// 置 20 不建转账单（spec §4.8 拒绝/关闭置 20）。
func (s *Service) CloseDemoWithdraw(ctx context.Context, id int64) error {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return xerr.Unauthorized("")
	}
	return s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		locked, err := tx.LockDemoWithdraw(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if locked == nil {
			return xerr.NotFound("示例提现单不存在")
		}
		if locked.Status != model.DemoWithdrawStatusWaiting {
			return xerr.Conflict(demoWithdrawStatusMessage(locked.Status))
		}
		if locked.PayTransferID != 0 {
			return xerr.Conflict("提现单已发起转账，无法关闭")
		}
		rows, err := tx.ClaimDemoWithdrawClosed(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if rows == 0 {
			// 行锁内条件仍 0 行属状态机异常（并发写穿透锁），明确拒绝。
			return xerr.Conflict("提现单状态已变更，请刷新后重试")
		}
		return nil
	})
}

// ListDemoWithdraws 分页查询示例提现单（管理端，权限 pay:demo-withdraw:query）。
func (s *Service) ListDemoWithdraws(ctx context.Context, req *dto.DemoWithdrawPageReq) ([]model.DemoWithdraw, int64, error) {
	if req.Status != nil {
		if err := validateDemoWithdrawStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListDemoWithdraws(ctx, payrepo.DemoWithdrawFilter{
		UserID: req.UserID,
		Status: req.Status,
	}, page.Offset(), page.PageSize)
}

// demoWithdrawStatusMessage 提现单非等待状态的 409 文案（审批/关闭校验矩阵，
// 文案可区分）。
func demoWithdrawStatusMessage(status int8) string {
	switch status {
	case model.DemoWithdrawStatusSuccess:
		return "提现单已成功，无需重复处理"
	case model.DemoWithdrawStatusClosed:
		return "提现单已关闭"
	default:
		return fmt.Sprintf("提现单状态不支持该操作：%d", status)
	}
}

// validateDemoWithdrawStatus 校验提现单状态过滤值枚举。
func validateDemoWithdrawStatus(status int8) error {
	switch status {
	case model.DemoWithdrawStatusWaiting, model.DemoWithdrawStatusSuccess, model.DemoWithdrawStatusClosed:
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("提现单状态不合法：%d（0 等待 / 10 成功 / 20 关闭）", status))
	}
}

// ---------- demo 商户回调业务联动（SetDemoNotifyHandler 注册实现） ----------

// demoNotifyTypeByKind 是端点种类 → 回调报文类型的映射（严格 kind↔type 校验）。
var demoNotifyTypeByKind = map[string]int8{
	DemoNotifyKindOrder:    model.NotifyTypePayment,
	DemoNotifyKindRefund:   model.NotifyTypeRefund,
	DemoNotifyKindTransfer: model.NotifyTypeTransfer,
}

// HandleDemoNotify 是 demo 商户回调业务联动的注册实现（internal/app 装配处
// SetDemoNotifyHandler 注册）：严格 kind↔type 校验（不符 → 业务失败，任务转
// 21 重试——报文错投属路由缺陷，静默吞掉会掩盖装配错误）→ 按 payload.DataID
// 定位消费行（tenant.Skip）→ 派生租户后联动（见包内 demo.go 头注释的定位键
// 与幂等应答决策）。联动失败返回 error（HTTP 层回非 0 业务码，任务 21 重试）。
func (s *Service) HandleDemoNotify(ctx context.Context, kind string, payload *NotifyPayload) error {
	wantType, ok := demoNotifyTypeByKind[kind]
	if !ok {
		return fmt.Errorf("未知 demo 回调端点：%s", kind)
	}
	if payload.Type != wantType {
		slog.Warn("pay demo notify kind/type mismatch",
			"kind", kind, "payloadType", payload.Type, "dataId", payload.DataID)
		return fmt.Errorf("回调报文类型与端点不符：端点 %s 期望 type=%d，报文 type=%d", kind, wantType, payload.Type)
	}
	switch kind {
	case DemoNotifyKindOrder:
		return s.linkDemoOrderPaid(ctx, payload)
	case DemoNotifyKindRefund:
		return s.linkDemoOrderRefund(ctx, payload)
	default:
		return s.linkDemoWithdrawTransfer(ctx, payload)
	}
}

// linkDemoOrderPaid 是 kind=order 的联动：pay_status 0→1 + pay_time/
// pay_channel_code（取支付订单回执）。非示例订单的回调（其他应用指向 demo
// 端点）幂等应答不联动；重复回调由认领条件（pay_status=0）幂等跳过。
func (s *Service) linkDemoOrderPaid(ctx context.Context, payload *NotifyPayload) error {
	row, err := s.repo.FindDemoOrderByPayOrderIDGlobal(ctx, payload.DataID)
	if err != nil {
		return err
	}
	if row == nil {
		// 非示例订单的支付回调（demo 端点是商户模拟器）：幂等应答不联动。
		return nil
	}
	if payload.Status != "success" {
		// v1 只在成功时建任务（spec §4.5）；非成功报文无联动语义。
		return nil
	}
	ctx, tenantID, err := guardTenantContext(tenant.WithTenantID(ctx, row.TenantID))
	if err != nil {
		return err
	}
	payOrder, err := s.repo.FindOrderByID(ctx, payload.DataID)
	if err != nil {
		return err
	}
	if payOrder == nil {
		// 挂单与创建同事务，行缺失属数据异常：业务失败 → 任务 21 重试排查。
		return fmt.Errorf("示例订单 %d 关联的支付订单 %d 不存在", row.ID, payload.DataID)
	}
	payTime := s.businessNow()
	if payOrder.SuccessTime != nil {
		payTime = *payOrder.SuccessTime
	}
	_, err = s.repo.ClaimDemoOrderPaid(ctx, tenantID, row.ID, payOrder.ChannelCode, payTime)
	return err
}

// linkDemoOrderRefund 是 kind=refund 的联动：pay_refund_id/refund_price 累加/
// refund_time（取退款单回执）。定位链（决策记录）：payload.DataID = 退款单 ID
// ——退款单先经 tenant.Skip 全局定位派生租户（demo 行的 pay_refund_id 在联动
// 前尚未回写，不能作定位键），再按退款单的 order_id（= 示例订单挂的支付订单
// ID）查消费行；非示例订单的退款回调幂等应答不联动。同一退款单的重复回调由
// 回写条件（pay_refund_id <> 本次退款 ID）幂等跳过。
func (s *Service) linkDemoOrderRefund(ctx context.Context, payload *NotifyPayload) error {
	refund, err := s.repo.FindRefundByIDGlobal(ctx, payload.DataID)
	if err != nil {
		return err
	}
	if refund == nil {
		// 非本系统退款单的回调（demo 端点是商户模拟器）：幂等应答不联动。
		return nil
	}
	if payload.Status != "success" {
		return nil
	}
	ctx, tenantID, err := guardTenantContext(tenant.WithTenantID(ctx, refund.TenantID))
	if err != nil {
		return err
	}
	row, err := s.repo.FindDemoOrderByPayOrderIDGlobal(ctx, refund.OrderID)
	if err != nil {
		return err
	}
	if row == nil {
		// 非示例订单的退款回调：幂等应答不联动。
		return nil
	}
	refundTime := s.businessNow()
	if refund.SuccessTime != nil {
		refundTime = *refund.SuccessTime
	}
	_, err = s.repo.ApplyDemoOrderRefund(ctx, tenantID, row.ID, refund.ID, refund.RefundPrice, refundTime)
	return err
}

// linkDemoWithdrawTransfer 是 kind=transfer 的联动：status 0→10 +
// transfer_time/transfer_channel_code（取转账单回执）；失败报文防御性记
// transfer_error_msg（v1 转账同步成功、任务只在成功时创建——分支为未来异步
// 渠道保留）。重复回调由认领条件（status=0）幂等跳过。
func (s *Service) linkDemoWithdrawTransfer(ctx context.Context, payload *NotifyPayload) error {
	row, err := s.repo.FindDemoWithdrawByTransferIDGlobal(ctx, payload.DataID)
	if err != nil {
		return err
	}
	if row == nil {
		// 非示例提现的转账回调：幂等应答不联动。
		return nil
	}
	ctx, tenantID, err := guardTenantContext(tenant.WithTenantID(ctx, row.TenantID))
	if err != nil {
		return err
	}
	transfer, err := s.repo.FindTransferByID(ctx, payload.DataID)
	if err != nil {
		return err
	}
	if transfer == nil {
		return fmt.Errorf("示例提现单 %d 关联的转账单 %d 不存在", row.ID, payload.DataID)
	}
	if payload.Status != "success" {
		return s.repo.MarkDemoWithdrawTransferError(ctx, tenantID, row.ID, "转账失败（渠道回调报文非成功）")
	}
	transferTime := s.businessNow()
	if transfer.SuccessTime != nil {
		transferTime = *transfer.SuccessTime
	}
	_, err = s.repo.ClaimDemoWithdrawSuccess(ctx, tenantID, row.ID, transfer.ChannelCode, transferTime)
	return err
}
