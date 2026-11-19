package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// trade_app.go 是交易域 C 端订单业务（issue #179，docs/mall-mvp-spec.md
// §3.3/§3.4/§4）：结算试算（选中项 + 默认地址 + 金额，不下单）、创建订单
//（收货地址 id + 备注，按选中项结算，下单即扣库存）、我的订单分页（状态
// 过滤）/详情（含 items）/取消（回补库存）、我的积分流水分页（复用 T2
// 的 repo.ListPointRecords）。
//
// 越权防线（spec §4）：订单/购物车/地址查询全部带 user_id 谓词——会员 A
// 拿不到会员 B 的行，一律 404（不泄露存在性）。
// 金额不变量（spec §3.4）：金额三列全部服务端定（ComputeOrderAmount），
// 客户端金额不采纳；item_money = Σ subtotal、pay_money = item − discount +
// postage（discount/postage 恒 0 落列，spec §7 裁决 8）。
// 库存不变量（spec §3.4）：下单即扣减（行锁条件 UPDATE，0 行 → 409），
// 取消回补（同形状 + ?）；扣减/回补与订单状态迁移同事务。

// AppSettlementPreview 结算试算（GET /mall/app/trade/settlement，不下单）：
// 选中购物车项 + 默认地址 + 金额三列。无默认地址时 address=null（前端引导
// 选地址）；无选中项 400。
func (s *Service) AppSettlementPreview(ctx context.Context, userID int64) (*dto.AppSettlementResp, error) {
	lines, err := s.loadSettlementLines(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.settlementResp(ctx, userID, lines), nil
}

// settlementResp 组装试算响应（试算与下单前核对共用）：金额三列服务端定
// （discount/postage 恒 0，spec §7 裁决 8）+ 默认地址（is_default=1，缺省 null）。
func (s *Service) settlementResp(ctx context.Context, userID int64, lines []settlementLine) *dto.AppSettlementResp {
	amountLines := make([]OrderItemLine, 0, len(lines))
	items := make([]dto.AppSettlementItem, 0, len(lines))
	for _, l := range lines {
		amountLines = append(amountLines, OrderItemLine{Price: l.Price, Count: l.Count})
		items = append(items, dto.AppSettlementItem{
			SPUID:      l.SPUID,
			SKUID:      l.SKUID,
			SPUName:    l.SPUName,
			SKUPicURL:  l.SKUPic,
			Properties: l.Props,
			Price:      l.Price,
			Count:      l.Count,
			Subtotal:   l.Subtotal,
		})
	}
	amounts := ComputeOrderAmount(amountLines, money.FromCents(0), money.FromCents(0))
	return &dto.AppSettlementResp{
		Items:         items,
		Address:       s.defaultAddress(ctx, userID),
		TotalQuantity: amounts.TotalQuantity,
		ItemMoney:     amounts.ItemMoney,
		DiscountMoney: amounts.DiscountMoney,
		Postage:       amounts.Postage,
		PayMoney:      amounts.PayMoney,
	}
}

// defaultAddress 读默认收货地址（is_default=1，无则 null——试算不因缺地址
// 失败，创建订单才强制）。
func (s *Service) defaultAddress(ctx context.Context, userID int64) *dto.AppAddress {
	addresses, err := s.repo.ListAllAddressesByUser(ctx, userID)
	if err != nil || len(addresses) == 0 {
		return nil
	}
	for i := range addresses {
		if addresses[i].IsDefault == 1 {
			return appAddressFrom(&addresses[i])
		}
	}
	// 无显式默认：取清单首位（ListAllAddressesByUser 默认在前、id ASC）。
	return appAddressFrom(&addresses[0])
}

// appAddressFrom 规整 C 端地址视图。
func appAddressFrom(row *model.MemberAddress) *dto.AppAddress {
	return &dto.AppAddress{
		ID:             row.ID,
		ReceiverName:   row.ReceiverName,
		ReceiverMobile: row.ReceiverMobile,
		Province:       row.Province,
		City:           row.City,
		District:       row.District,
		Detail:         row.Detail,
		IsDefault:      row.IsDefault,
	}
}

// AppCreateOrder 创建订单（POST /mall/app/trade/order）：收货地址 id（必须
// 属于当前会员，否则 404）+ 备注，按选中购物车项结算。单事务内完成：
//
//  1. 校验收货地址归属（404）；
//  2. 读选中购物车项 + 商品现值对账（空 400 / 信息变更或下架 409）；
//  3. mall_sequence 事务内发放 SO 单号（先取序列行锁、再写业务行——
//     internal/pkg/sequence 锁序约定）；
//  4. 逐 SKU 行锁扣减库存（条件 UPDATE，0 行 → 409 库存不足）；
//  5. 订单 + 订单项落库（成交快照：spu_name/sku_pic/properties/price 现值；
//     金额三列服务端定；收货快照取自地址）；
//  6. 清掉已结算的购物车选中项。
//
// 幂等口径（spec §3.4）：重复提交同购物车结算产生新订单（不做幂等键）。
func (s *Service) AppCreateOrder(ctx context.Context, userID int64, req *dto.AppCreateOrderReq) (*dto.AppCreateOrderResp, error) {
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		// 库存扣减/序列发放走显式租户参数：缺租户上下文宁可拒绝。
		return nil, xerr.Unauthorized("")
	}
	var out *dto.AppCreateOrderResp
	err := s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		var err error
		out, err = s.createOrderTx(ctx, tx, tenantID, userID, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// createOrderTx 是创建订单的事务内核（步骤见 AppCreateOrder 注释）。
func (s *Service) createOrderTx(ctx context.Context, tx *mallrepo.Repository, tenantID, userID int64, req *dto.AppCreateOrderReq) (*dto.AppCreateOrderResp, error) {
	// 1) 收货地址归属校验（404：不存在/跨租户/跨会员不可区分）。
	address, err := tx.FindAddressByID(ctx, req.AddressID)
	if err != nil {
		return nil, err
	}
	if address == nil || address.UserID != userID {
		return nil, xerr.NotFound("收货地址不存在")
	}
	// 2) 选中项 + 商品现值对账（空 400 / 变更 409；tx 句柄保证同事务快照）。
	lines, err := s.loadSettlementLinesIn(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	// 3) SO 单号（事务内发放：先取序列行锁、再写业务行）。
	no, err := s.nextOrderNoInTx(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	// 4) 逐 SKU 行锁扣减库存（0 行 → 409；SKU 现值已在对账时读得）。
	amountLines := make([]OrderItemLine, 0, len(lines))
	for _, l := range lines {
		amountLines = append(amountLines, OrderItemLine{Price: l.Price, Count: l.Count})
		rows, err := tx.DeductSKUStock(ctx, tenantID, l.SKUID, l.Count)
		if err != nil {
			return nil, err
		}
		if rows == 0 {
			return nil, xerr.Conflict("库存不足：" + l.SPUName)
		}
	}
	// 5) 金额三列（服务端定）+ 订单/订单项落库（成交快照）。
	amounts := ComputeOrderAmount(amountLines, money.FromCents(0), money.FromCents(0))
	order := &model.TradeOrder{
		TenantID:       tenantID,
		No:             no,
		UserID:         userID,
		Status:         model.TradeOrderStatusPending,
		Type:           0,
		UserRemark:     trimString(req.UserRemark),
		TotalQuantity:  amounts.TotalQuantity,
		ItemMoney:      amounts.ItemMoney,
		DiscountMoney:  amounts.DiscountMoney,
		Postage:        amounts.Postage,
		PayMoney:       amounts.PayMoney,
		ReceiverName:   address.ReceiverName,
		ReceiverMobile: address.ReceiverMobile,
		ReceiverArea:   receiverArea(address),
		ReceiverDetail: address.Detail,
	}
	if err := tx.CreateOrder(ctx, order); err != nil {
		return nil, err
	}
	items := make([]model.TradeOrderItem, 0, len(lines))
	for _, l := range lines {
		items = append(items, model.TradeOrderItem{
			TenantID:      tenantID,
			OrderID:       order.ID,
			UserID:        userID,
			SPUID:         l.SPUID,
			SKUID:         l.SKUID,
			SPUName:       l.SPUName,
			SKUPic:        l.SKUPic,
			SKUProperties: ensureRawJSON(l.Props),
			Price:         l.Price,
			Count:         l.Count,
			Subtotal:      l.Subtotal,
		})
	}
	if err := tx.CreateOrderItems(ctx, items); err != nil {
		return nil, err
	}
	// 6) 清掉已结算的购物车选中项（与订单落库同事务）。
	if err := tx.DeleteSelectedCarts(ctx, userID); err != nil {
		return nil, err
	}
	return &dto.AppCreateOrderResp{ID: order.ID, No: order.No, PayMoney: order.PayMoney}, nil
}

// receiverArea 收货地区拼接（province/city/district 空格拼接，缺项跳过——
// spec §3.3「receiver_area（province city district 拼接）」）。
func receiverArea(a *model.MemberAddress) string {
	parts := make([]string, 0, 3)
	for _, v := range []string{a.Province, a.City, a.District} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// ensureRawJSON 保证 JSON 列非 nil（空值落 []，满足 NOT NULL，照 SPU 落库口径）。
func ensureRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}

// AppPageOrders 我的订单分页（GET /mall/app/trade/order/page）：user_id 恒取
// 当前会员（越权谓词）；status 过滤 ∈ 五态枚举（非法 400）。分页项含订单
// 主行（items 随详情端点，列表不带）。
func (s *Service) AppPageOrders(ctx context.Context, userID int64, req *dto.AppOrderPageReq) ([]model.TradeOrder, int64, error) {
	var status *int8
	if req.Status != nil {
		if !model.IsTradeOrderStatus(*req.Status) {
			return nil, 0, xerr.BadRequest("非法的订单状态过滤值")
		}
		status = req.Status
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListOrders(ctx, mallrepo.OrderFilter{UserID: userID, Status: status}, page)
}

// AppGetOrder 我的订单详情（GET /mall/app/trade/order/:id）：user_id 越权
// 谓词——不存在/跨租户/跨会员统一 404（不泄露存在性）；含 items。
func (s *Service) AppGetOrder(ctx context.Context, userID, orderID int64) (*dto.AppOrderResp, error) {
	order, err := s.repo.FindOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil || order.UserID != userID {
		return nil, xerr.NotFound("订单不存在")
	}
	items, err := s.repo.ListOrderItemsByOrder(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	return appOrderResp(order, items), nil
}

// appOrderResp 规整 C 端订单视图（主行 + items）。
func appOrderResp(order *model.TradeOrder, items []model.TradeOrderItem) *dto.AppOrderResp {
	out := &dto.AppOrderResp{
		ID:             order.ID,
		No:             order.No,
		Status:         order.Status,
		UserRemark:     order.UserRemark,
		TotalQuantity:  order.TotalQuantity,
		ItemMoney:      order.ItemMoney,
		DiscountMoney:  order.DiscountMoney,
		Postage:        order.Postage,
		PayMoney:       order.PayMoney,
		ReceiverName:   order.ReceiverName,
		ReceiverMobile: order.ReceiverMobile,
		ReceiverArea:   order.ReceiverArea,
		ReceiverDetail: order.ReceiverDetail,
		PayTime:        order.PayTime,
		CancelTime:     order.CancelTime,
		CancelReason:   order.CancelReason,
		CloseTime:      order.CloseTime,
		CloseReason:    order.CloseReason,
		CreatedAt:      order.CreatedAt,
		Items:          make([]dto.AppOrderItemResp, 0, len(items)),
	}
	for _, it := range items {
		out.Items = append(out.Items, dto.AppOrderItemResp{
			ID:         it.ID,
			SPUID:      it.SPUID,
			SKUID:      it.SKUID,
			SPUName:    it.SPUName,
			SKUPicURL:  it.SKUPic,
			Properties: ensureRawJSON(it.SKUProperties),
			Price:      it.Price,
			Count:      it.Count,
			Subtotal:   it.Subtotal,
		})
	}
	return out
}

// AppCancelOrder 取消待支付订单（POST /mall/app/trade/order/:id/cancel）：
// 状态机校验（非待支付一律 409——已支付/已完成/已取消/已关闭）+ 库存回补
// 与状态迁移同事务（spec §3.4「取消/关单回补」）。归属防线：user_id 谓词，
// 跨会员 404。
func (s *Service) AppCancelOrder(ctx context.Context, userID, orderID int64, req *dto.AppCancelOrderReq) error {
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		return xerr.Unauthorized("")
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		order, err := tx.FindOrderByID(ctx, orderID)
		if err != nil {
			return err
		}
		if order == nil || order.UserID != userID {
			return xerr.NotFound("订单不存在")
		}
		// 状态机快判（409 文案区分终态与不可取消态；最终裁决是条件 UPDATE）。
		if !CanCancelOrder(order.Status) {
			return xerr.Conflict(orderCancelConflictMessage(order.Status))
		}
		// 条件 UPDATE 认领 0→30（0 行 = 并发改动 → 409）。
		rows, err := tx.ClaimOrderCancel(ctx, order.ID, userID, tenantID, trimString(req.Reason), s.businessNow())
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("订单状态已变更，请刷新后重试")
		}
		// 库存回补（同形状 stock + ?，与状态迁移同事务；T7 抽出与管理员取消
		// 共用 restoreOrderStock）；0 行 = SKU 已不可见（软删/异常），报错整体
		// 回滚——不允许「状态已取消但库存未回补」。
		return restoreOrderStock(ctx, tx, tenantID, order.ID)
	})
}

// orderCancelConflictMessage 按当前状态给不可取消的 409 文案（对齐状态机
// 注释：已支付/已完成不可取消；已取消/已关闭终态幂等拒绝）。
func orderCancelConflictMessage(status int8) string {
	switch status {
	case model.TradeOrderStatusCanceled:
		return "订单已取消，请勿重复操作"
	case model.TradeOrderStatusClosed:
		return "订单已关闭，无法取消"
	case model.TradeOrderStatusPaid, model.TradeOrderStatusCompleted:
		return "订单当前状态不可取消"
	default:
		return "订单当前状态不可取消"
	}
}

// AppPageMyPointRecords 我的积分流水分页（GET /mall/app/trade/point-records）：
// 复用 T2 的 repo.ListPointRecords（user_id 恒取当前会员——越权谓词；
// biz_type 过滤 ∈ 三类枚举，非法 400）。排序固定 id ASC（重放序，见
// repository.ListPointRecords 注释）。
func (s *Service) AppPageMyPointRecords(ctx context.Context, userID int64, req *dto.AppPointRecordPageReq) ([]model.MemberPointRecord, int64, error) {
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
