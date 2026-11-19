package service

import (
	"context"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// trade_admin.go 是交易域管理端订单业务（issue #180，T7；docs/mall-mvp-spec.md
// §4「订单分页（状态/编号/会员/时间过滤）/详情（含 items）/管理员备注/
// 取消（待支付）/完成（已支付）」）。
//
// 与 C 端（trade_app.go）的分界：
//   - 管理端面向本租户全部会员订单（不加 user_id 谓词，租户隔离由租户插件
//     保证，跨租户订单 ID 一律 404 不泄露存在性）；
//   - 取消走管理员认领（ClaimOrderCancelByAdmin，cancel_reason 固定
//     「管理员取消」），库存回补与状态迁移同事务（spec §3.4「取消/关单回补」）；
//   - 完成走状态机 10→20（ValidOrderTransition + ClaimOrderComplete 条件
//     UPDATE，非法迁移一律 409）；
//   - 备注任意状态可补注（终态也允许，纯字段更新不走状态机）。

// AdminOrderCancelReason 是管理员取消的固定 cancel_reason（票面口径：
// 管理员取消记 cancel_reason="管理员取消"，与 C 端用户自述原因分型）。
const AdminOrderCancelReason = "管理员取消"

// tradeTimeLayouts 是订单分页时间过滤的入参布局（对齐 erp parseDocTimeRange
// 口径的紧凑版：datetime / 纯日期两种）。
var tradeTimeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"}

// AdminPageOrders 管理端订单分页（GET /mall/trade/order/page，权限
// mall:trade:query）：状态（∈ 五态枚举，非法 400）/编号模糊/会员 ID/下单
// 时间范围过滤，全部可选。分页项为订单主行（items 随详情端点，列表不带）。
func (s *Service) AdminPageOrders(ctx context.Context, req *dto.AdminOrderPageReq) ([]model.TradeOrder, int64, error) {
	var status *int8
	if req.Status != nil {
		if !model.IsTradeOrderStatus(*req.Status) {
			return nil, 0, xerr.BadRequest("非法的订单状态过滤值")
		}
		status = req.Status
	}
	begin, end, err := s.parseOrderTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListOrders(ctx, mallrepo.OrderFilter{
		Status:          status,
		NoLike:          trimString(req.No),
		UserID:          req.UserID,
		CreateTimeBegin: begin,
		CreateTimeEnd:   end,
	}, page)
}

// parseOrderTimeRange 解析下单时间范围入参（两端均可空；按业务时区解释；
// end 为纯日期（YYYY-MM-DD）按「含当日」语义外推到次日 0 点——repo 上界
// 谓词 < 排他。非法格式 400，文案点名开始/结束时间）。照 erp
// parseDocTimeRange 先例的紧凑版（mall 无其他消费点，不抽公共包）。
func (s *Service) parseOrderTimeRange(start, end string) (*time.Time, *time.Time, error) {
	loc := s.businessLocation()
	parse := func(v, field string) (*time.Time, error) {
		for _, layout := range tradeTimeLayouts {
			if t, err := time.ParseInLocation(layout, v, loc); err == nil {
				return &t, nil
			}
		}
		return nil, xerr.BadRequest(field + "格式错误：" + v)
	}
	var begin, endTime *time.Time
	if v := strings.TrimSpace(start); v != "" {
		t, err := parse(v, "开始时间")
		if err != nil {
			return nil, nil, err
		}
		begin = t
	}
	if v := strings.TrimSpace(end); v != "" {
		t, err := parse(v, "结束时间")
		if err != nil {
			return nil, nil, err
		}
		if len(v) == 10 {
			// 纯日期：含当日（上界外推到次日 0 点，配合 < 排他谓词）。
			next := t.AddDate(0, 0, 1)
			t = &next
		}
		endTime = t
	}
	return begin, endTime, nil
}

// AdminGetOrder 管理端订单详情（GET /mall/trade/order/:id，权限
// mall:trade:query）：管理面字段全量 + 订单项成交快照。跨租户/不存在统一
// 404（不泄露存在性）。
func (s *Service) AdminGetOrder(ctx context.Context, orderID int64) (*dto.AdminOrderResp, error) {
	order, err := s.repo.FindOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, xerr.NotFound("订单不存在")
	}
	items, err := s.repo.ListOrderItemsByOrder(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	return adminOrderResp(order, items), nil
}

// adminOrderResp 规整管理端订单视图（主行全量字段 + items 成交快照）。
func adminOrderResp(order *model.TradeOrder, items []model.TradeOrderItem) *dto.AdminOrderResp {
	out := &dto.AdminOrderResp{
		ID:             order.ID,
		No:             order.No,
		UserID:         order.UserID,
		Status:         order.Status,
		Type:           order.Type,
		UserRemark:     order.UserRemark,
		AdminRemark:    order.AdminRemark,
		PayOrderID:     order.PayOrderID,
		PayOrderNo:     order.PayOrderNo,
		PayChannelCode: order.PayChannelCode,
		PayTime:        order.PayTime,
		CancelTime:     order.CancelTime,
		CancelReason:   order.CancelReason,
		CloseTime:      order.CloseTime,
		CloseReason:    order.CloseReason,
		TotalQuantity:  order.TotalQuantity,
		ItemMoney:      order.ItemMoney,
		DiscountMoney:  order.DiscountMoney,
		Postage:        order.Postage,
		PayMoney:       order.PayMoney,
		ReceiverName:   order.ReceiverName,
		ReceiverMobile: order.ReceiverMobile,
		ReceiverArea:   order.ReceiverArea,
		ReceiverDetail: order.ReceiverDetail,
		CreateTime:     order.CreatedAt,
		Items:          make([]dto.AdminOrderItemResp, 0, len(items)),
	}
	for _, it := range items {
		out.Items = append(out.Items, dto.AdminOrderItemResp{
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

// AdminUpdateOrderRemark 管理员备注（PUT /mall/trade/order/:id/remark，权限
// mall:trade:update）：任意状态可补注（终态也允许——备注是管理面字段更新，
// 不走状态机），≤500 字由 DTO binding 校验。跨租户/不存在统一 404。
func (s *Service) AdminUpdateOrderRemark(ctx context.Context, orderID int64, remark string) error {
	order, err := s.repo.FindOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return xerr.NotFound("订单不存在")
	}
	rows, err := s.repo.UpdateOrderAdminRemark(ctx, orderID, trimString(remark))
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.NotFound("订单不存在")
	}
	return nil
}

// AdminCancelOrder 管理员取消待支付订单（POST /mall/trade/order/:id/cancel，
// 权限 mall:trade:cancel）：仅待支付（0）可取消，其余状态走 trade_rules
// 快判 409（最终裁决是 ClaimOrderCancelByAdmin 条件 UPDATE）；cancel_reason
// 固定「管理员取消」；库存回补与状态迁移同事务（spec §3.4，与 C 端取消
// 同形状——回补 0 行报错整体回滚，不允许「状态已取消但库存未回补」）。
func (s *Service) AdminCancelOrder(ctx context.Context, orderID int64) error {
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		// 回补/认领走显式租户参数：缺租户上下文宁可拒绝。
		return xerr.Unauthorized("")
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		order, err := tx.FindOrderByID(ctx, orderID)
		if err != nil {
			return err
		}
		if order == nil {
			return xerr.NotFound("订单不存在")
		}
		// 状态机快判（409 文案区分终态与不可取消态；最终裁决是条件 UPDATE）。
		if !CanCancelOrder(order.Status) {
			return xerr.Conflict(orderCancelConflictMessage(order.Status))
		}
		// 条件 UPDATE 认领 0→30（0 行 = 并发改动 → 409）。
		rows, err := tx.ClaimOrderCancelByAdmin(ctx, order.ID, tenantID, AdminOrderCancelReason, s.businessNow())
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("订单状态已变更，请刷新后重试")
		}
		return restoreOrderStock(ctx, tx, tenantID, order.ID)
	})
}

// restoreOrderStock 按订单项逐 SKU 回补库存（取消/关单共用：C 端取消与
// T7 管理员取消同形状，stock + ?；0 行 = SKU 已不可见（软删/异常）→ 报错
// 整体回滚）。必须在持有订单事务内调用。
func restoreOrderStock(ctx context.Context, tx *mallrepo.Repository, tenantID, orderID int64) error {
	items, err := tx.ListOrderItemsByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	for _, it := range items {
		rows, err := tx.RestoreSKUStock(ctx, tenantID, it.SKUID, it.Count)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("商品库存回补失败，请稍后重试")
		}
	}
	return nil
}

// AdminCompleteOrder 管理员完成已支付订单（POST /mall/trade/order/:id/
// complete，权限 mall:trade:complete）：仅已支付（10）→ 已完成（20），
// 走状态机（ValidOrderTransition 快判 409 + ClaimOrderComplete 条件 UPDATE
// 最终裁决）；其余状态（含待支付/终态）一律 409。跨租户/不存在统一 404。
func (s *Service) AdminCompleteOrder(ctx context.Context, orderID int64) error {
	tenantID := mallTenantID(ctx)
	if tenantID == 0 {
		return xerr.Unauthorized("")
	}
	order, err := s.repo.FindOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return xerr.NotFound("订单不存在")
	}
	if !ValidOrderTransition(order.Status, model.TradeOrderStatusCompleted) {
		return xerr.Conflict(orderCompleteConflictMessage(order.Status))
	}
	rows, err := s.repo.ClaimOrderComplete(ctx, order.ID, tenantID, s.businessNow())
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.Conflict("订单状态已变更，请刷新后重试")
	}
	return nil
}

// orderCompleteConflictMessage 按当前状态给不可完成的 409 文案（对齐状态机
// 注释：仅已支付可完成；已取消/已关闭终态幂等拒绝）。
func orderCompleteConflictMessage(status int8) string {
	switch status {
	case model.TradeOrderStatusCompleted:
		return "订单已完成，请勿重复操作"
	case model.TradeOrderStatusCanceled:
		return "订单已取消，无法完成"
	case model.TradeOrderStatusClosed:
		return "订单已关闭，无法完成"
	default:
		return "订单当前状态不可完成"
	}
}
