package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// trade_cart.go 是购物车 C 端业务（issue #179，docs/mall-mvp-spec.md §3.3/§4）：
// 加购（同 SKU 合并数量）/数量修改/选中切换/删除/我的购物车（SKU 现价与
// 库存展示）。归属防线：全部读写恒带 user_id 谓词（越权一律 404，不泄露
// 存在性）；加购边界：SKU 必须属于上架 SPU（下架 409）。

// AppAddCart 加购（POST /mall/app/cart）：skuId + count 正整数；同 SKU 合并
// 数量（upsert 原子递增，UNIQUE 兜底并发）；SKU 必须存在且归属上架 SPU
// （下架 409「商品已下架」，不存在 404——不泄露存在性）。
func (s *Service) AppAddCart(ctx context.Context, userID int64, req *dto.AppCartAddReq) error {
	if err := ValidateOrderItemCount(req.Count); err != nil {
		return err
	}
	sku, _, err := s.findSaleableSKU(ctx, req.SKUID)
	if err != nil {
		return err
	}
	return s.repo.UpsertCartMerge(ctx, &model.TradeCart{
		TenantID: mallTenantID(ctx),
		UserID:   userID,
		SPUID:    sku.SPUID,
		SKUID:    sku.ID,
		Count:    req.Count,
		Selected: 1,
	})
}

// findSaleableSKU 加购/结算共用的售卖边界校验：SKU 存在（404）且其 SPU 存在
// 且上架（409「商品已下架」）。返回 SKU 与 SPU（下单快照取现值）。
func (s *Service) findSaleableSKU(ctx context.Context, skuID int64) (*model.ProductSKU, *model.ProductSPU, error) {
	sku, err := s.findSKU(ctx, skuID)
	if err != nil {
		return nil, nil, err
	}
	spu, err := s.repo.FindSPUByID(ctx, sku.SPUID)
	if err != nil {
		return nil, nil, err
	}
	if spu == nil || spu.Status != model.ProductSPUStatusOn {
		return nil, nil, xerr.Conflict("商品已下架")
	}
	return sku, spu, nil
}

// findSKU 按 ID 查 SKU（404 不泄露存在性）。
func (s *Service) findSKU(ctx context.Context, skuID int64) (*model.ProductSKU, error) {
	rows, err := s.repo.ListSKUsByIDs(ctx, []int64{skuID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, xerr.NotFound("商品不存在")
	}
	return &rows[0], nil
}

// AppUpdateCartCount 修改加购数量（PUT /mall/app/cart/:id/count）：绝对值
// 覆盖，正整数校验；行不存在/跨会员 404。
func (s *Service) AppUpdateCartCount(ctx context.Context, userID, cartID int64, count int) error {
	if err := ValidateOrderItemCount(count); err != nil {
		return err
	}
	rows, err := s.repo.UpdateCartCount(ctx, cartID, userID, count)
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.NotFound("购物车项不存在")
	}
	return nil
}

// AppSelectCart 结算选中切换（PUT /mall/app/cart/:id/selected）：selected ∈
// {0,1}（400）；行不存在/跨会员 404。
func (s *Service) AppSelectCart(ctx context.Context, userID, cartID int64, selected int8) error {
	if selected != 0 && selected != 1 {
		return xerr.BadRequest("selected 取值只能为 0 或 1")
	}
	rows, err := s.repo.UpdateCartSelected(ctx, cartID, userID, selected)
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.NotFound("购物车项不存在")
	}
	return nil
}

// AppDeleteCart 删除购物车行（POST /mall/app/cart/delete，批量）：IDs 非空；
// 行不存在/跨会员 404（全或无：任一不可见即 404，不部分删除）。
func (s *Service) AppDeleteCart(ctx context.Context, userID int64, ids []int64) error {
	if len(ids) == 0 {
		return xerr.BadRequest("请选择要删除的购物车项")
	}
	rows, err := s.repo.DeleteCartRows(ctx, userID, ids)
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.NotFound("购物车项不存在")
	}
	return nil
}

// AppListCart 我的购物车（GET /mall/app/cart）：全量行 + SKU 现价/现库存/
// 规格图与 SPU 名称/主图/上架态（spec §4「我的购物车（含 SKU 现价与库存
// 展示）」）；汇总为选中项口径（与结算试算同源）。SKU/SPU 已被软删的行
// 照旧展示快照（count/selected），SKU 现值置空、OnSale=false。
func (s *Service) AppListCart(ctx context.Context, userID int64) (*dto.AppCartListResp, error) {
	rows, err := s.repo.ListCartsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]dto.AppCartItem, 0, len(rows))
	var selectedLines []OrderItemLine
	var selectedCount int
	for i := range rows {
		row := &rows[i]
		item := dto.AppCartItem{
			ID:       row.ID,
			SPUID:    row.SPUID,
			SKUID:    row.SKUID,
			Count:    row.Count,
			Selected: row.Selected,
		}
		sku, spu := s.loadCartSkuSpu(ctx, row)
		if sku != nil {
			item.Properties = sku.Properties
			item.SKUPicURL = sku.PicURL
			item.Price = sku.Price
			item.Stock = sku.Stock
			item.Subtotal = OrderItemSubtotal(sku.Price, row.Count)
		}
		if spu != nil {
			item.SPUName = spu.Name
			item.SPUPicURL = spu.PicURL
			item.OnSale = spu.Status == model.ProductSPUStatusOn
			if sku == nil {
				// SKU 软删：图片回落 SPU 主图（展示口径）。
				item.SKUPicURL = spu.PicURL
			}
		}
		if row.Selected == 1 && sku != nil && item.OnSale {
			selectedCount++
			selectedLines = append(selectedLines, OrderItemLine{Price: sku.Price, Count: row.Count})
		}
		items = append(items, item)
	}
	amounts := ComputeOrderAmount(selectedLines, money.FromCents(0), money.FromCents(0))
	return &dto.AppCartListResp{
		Items: items,
		Summary: dto.AppCartSummary{
			SelectedCount: selectedCount,
			TotalQuantity: amounts.TotalQuantity,
			ItemMoney:     amounts.ItemMoney,
		},
	}, nil
}

// loadCartSkuSpu 购物车行的 SKU/SPU 现值（批量端点外单行查询——购物车行数
// 量级小；查不到（软删/异常）返回 nil，不阻断列表）。
func (s *Service) loadCartSkuSpu(ctx context.Context, row *model.TradeCart) (*model.ProductSKU, *model.ProductSPU) {
	skus, err := s.repo.ListSKUsByIDs(ctx, []int64{row.SKUID})
	if err != nil || len(skus) == 0 {
		return nil, nil
	}
	spus, err := s.repo.ListSPUsByIDs(ctx, []int64{row.SPUID})
	if err != nil || len(spus) == 0 {
		return &skus[0], nil
	}
	return &skus[0], &spus[0]
}

// settlementLine 是试算/下单共用的结算行（购物车选中项 × SKU/SPU 现值）。
type settlementLine struct {
	CartID   int64
	SKUID    int64
	SPUID    int64
	SPUName  string
	SKUPic   string
	Props    json.RawMessage
	Price    money.Money
	Count    int
	Subtotal money.Money
}

// loadSettlementLines 读选中购物车项并对账商品现值（试算入口：走模块主句柄）。
func (s *Service) loadSettlementLines(ctx context.Context, userID int64) ([]settlementLine, error) {
	return s.loadSettlementLinesIn(ctx, s.repo, userID)
}

// loadSettlementLinesIn 读选中购物车项并对账商品现值（试算与下单共用；repo
// 句柄由调用方传入——下单事务内核传绑定事务的 tx，保证「读选中项 → 扣库存 →
// 落单」在同一事务快照上）：选中项为空 400；SKU 缺失/与购物车行 SPU 归属不符
// 409（商品信息已变更）；SPU 下架/缺失 409。返回结算行（现价快照输入）。
func (s *Service) loadSettlementLinesIn(ctx context.Context, r *mallrepo.Repository, userID int64) ([]settlementLine, error) {
	carts, err := r.ListSelectedCarts(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(carts) == 0 {
		return nil, xerr.BadRequest("请先选择要结算的商品")
	}
	skuIDs := make([]int64, 0, len(carts))
	spuIDs := make([]int64, 0, len(carts))
	for _, c := range carts {
		skuIDs = append(skuIDs, c.SKUID)
		spuIDs = append(spuIDs, c.SPUID)
	}
	skus, err := r.ListSKUsByIDs(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	skuByID := make(map[int64]*model.ProductSKU, len(skus))
	for i := range skus {
		skuByID[skus[i].ID] = &skus[i]
	}
	spus, err := r.ListSPUsByIDs(ctx, spuIDs)
	if err != nil {
		return nil, err
	}
	spuByID := make(map[int64]*model.ProductSPU, len(spus))
	for i := range spus {
		spuByID[spus[i].ID] = &spus[i]
	}
	lines := make([]settlementLine, 0, len(carts))
	for _, c := range carts {
		sku, ok := skuByID[c.SKUID]
		if !ok || sku.SPUID != c.SPUID {
			// SKU 已软删/跨 SPU：商品信息已变更，让用户重新加购（409）。
			return nil, xerr.Conflict("商品信息已变更，请重新加购")
		}
		spu := spuByID[c.SPUID]
		if spu == nil || spu.Status != model.ProductSPUStatusOn {
			return nil, xerr.Conflict("商品已下架：" + spuNameOr(spu, c.SPUID))
		}
		if err := ValidateOrderItemCount(c.Count); err != nil {
			return nil, xerr.Conflict("购物车数量异常，请修改数量后重试")
		}
		pic := sku.PicURL
		if pic == "" {
			pic = spu.PicURL
		}
		lines = append(lines, settlementLine{
			CartID:   c.ID,
			SKUID:    sku.ID,
			SPUID:    spu.ID,
			SPUName:  spu.Name,
			SKUPic:   pic,
			Props:    sku.Properties,
			Price:    sku.Price,
			Count:    c.Count,
			Subtotal: OrderItemSubtotal(sku.Price, c.Count),
		})
	}
	return lines, nil
}

// spuNameOr 兜底文案（SPU 行缺失时退化为 ID，避免空文案）。
func spuNameOr(spu *model.ProductSPU, id int64) string {
	if spu != nil {
		return spu.Name
	}
	return fmt.Sprintf("#%d", id)
}
