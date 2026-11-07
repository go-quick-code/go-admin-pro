// stock_doc_rules.go 是杂项出入库单据的共享规则（issue #90，docs/erp-mvp-spec.md
// §4.1/§4.2/§4.8）：明细不变量（数量 > 0、行金额 = 单价 × 数量）、审核状态机、
// 编号发放。其它入库（QR）/其它出库（QC）镜像共用；#91–#94 的十二类单据沿此
// 模式复制。
package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 单号前缀（spec §4.8：其它入库 QR / 其它出库 QC，2 字母拼音风格对齐 CRM）。
const (
	StockInNoPrefix  = "QR"
	StockOutNoPrefix = "QC"
)

// erp_sequence 的 biz_type（spec §4.8：与单据表名一致）。
const (
	SeqBizTypeStockIn  = "stock_in"
	SeqBizTypeStockOut = "stock_out"
)

// docItemDraft 是规则层产出的明细草稿：行金额已按不变量计算（服务端权威，
// 客户端不提交行金额，spec §4.2）。
type docItemDraft struct {
	ProductID    int64
	Count        qty.Qty
	ProductPrice money.Money
	TotalPrice   money.Money
}

// buildDocItems 校验并换算单据明细（创建/编辑共用，审核前兜底复用）：
//   - 至少一条明细；
//   - 产品 ID 必填、数量 > 0（spec §4.2 明细不变量）；
//   - 单价非负（记录用，缺省 0.00）；
//   - 行金额 = 单价 × 数量（Money.MulQty，128 位中间量；越界 400）。
func buildDocItems(items []dto.StockDocItemPayload, docLabel string) ([]docItemDraft, error) {
	if len(items) == 0 {
		return nil, xerr.BadRequest(fmt.Sprintf("%s至少一条明细", docLabel))
	}
	drafts := make([]docItemDraft, 0, len(items))
	for i, item := range items {
		if item.ProductId <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("%s第 %d 行缺少产品", docLabel, i+1))
		}
		if item.Count.Milli() <= 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("%s第 %d 行数量必须大于 0", docLabel, i+1))
		}
		if item.ProductPrice.Cents() < 0 {
			return nil, xerr.BadRequest(fmt.Sprintf("%s第 %d 行单价不能为负数", docLabel, i+1))
		}
		total, err := item.ProductPrice.MulQty(item.Count)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("%s第 %d 行金额超出范围（单价 × 数量）", docLabel, i+1))
		}
		drafts = append(drafts, docItemDraft{
			ProductID:    item.ProductId,
			Count:        item.Count,
			ProductPrice: item.ProductPrice,
			TotalPrice:   total,
		})
	}
	return drafts, nil
}

// validateDocType 校验单据类型枚举（字典取值，非法 400）。
func validateDocType(docType *int8, valid func(int8) bool, label string) (int8, error) {
	if docType == nil {
		return model.StockInTypeOther, nil
	}
	if !valid(*docType) {
		return 0, xerr.BadRequest(fmt.Sprintf("%s类型 %d 不合法", label, *docType))
	}
	return *docType, nil
}

// checkDocStatusTransition 校验审核状态机（spec §4.1）：幂等目标态放行；
// 10→20 审核、20→10 反审核合法；其余非法。返回 (幂等?, 错误)。
func checkDocStatusTransition(current, target int8) (bool, error) {
	if !model.ValidDocStatus(target) {
		return false, xerr.BadRequest(fmt.Sprintf("单据审核状态 %d 不合法：期望 10 未审核 / 20 已审核", target))
	}
	if current == target {
		return true, nil
	}
	switch {
	case current == model.DocStatusUnaudited && target == model.DocStatusApproved,
		current == model.DocStatusApproved && target == model.DocStatusUnaudited:
		return false, nil
	}
	return false, xerr.Conflict(fmt.Sprintf("单据当前状态 %d 不允许变更到 %d", current, target))
}

// nextStockDocNo 在事务内发放单号：前缀 + yyyyMMdd（业务时区）+ 至少 4 位序号
// （超 9999 自然扩位，spec §4.8）。序号经 erp_sequence 原子发放并与单据落库
// 同事务；客户端提交的单号不采纳（DTO 无单号字段）。
func (s *Service) nextStockDocNo(ctx context.Context, tx *repository.Repository, seqBizType, prefix string) (string, error) {
	now := s.businessNow()
	dateKey := now.Format("20060102")
	seq, err := tx.NextSequenceValue(ctx, erpTenantID(ctx), seqBizType, dateKey)
	if err != nil {
		return "", err
	}
	return formatStockDocNo(prefix, dateKey, seq), nil
}

// formatStockDocNo 把前缀、日期键与序号格式化为单号（纯函数，spec §4.8）：
// 前缀 + yyyyMMdd + 至少 4 位序号，序号超过 9999 自然扩位（唯一性优先于
// 固定位数，绝不截断）。
func formatStockDocNo(prefix, dateKey string, seq int64) string {
	return prefix + dateKey + fmt.Sprintf("%04d", seq)
}

// sortItemsByID 明细按 ID 升序（同一仓库内入账的确定性顺序；多仓库场景的
// 加锁顺序由调用方按仓库 ID 升序保证，spec §4.2）。
func sortItemsByID[T any](items []T, id func(T) int64) []T {
	sorted := make([]T, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return id(sorted[i]) < id(sorted[j]) })
	return sorted
}

// stockInTypeNames / stockOutTypeNames 是单据类型中文展示（导出/详情用，
// 与 seed 字典 erp_stock_in_type / erp_stock_out_type 一致）。
var stockInTypeNames = map[int8]string{
	model.StockInTypePurchase: "采购",
	model.StockInTypeReturn:   "退货",
	model.StockInTypeBorrow:   "借入",
	model.StockInTypeGiveBack: "归还",
	model.StockInTypeGift:     "赠品",
	model.StockInTypeOther:    "其它",
}

var stockOutTypeNames = map[int8]string{
	model.StockOutTypeLoss:  "报损",
	model.StockOutTypeUse:   "领用",
	model.StockOutTypeLend:  "借出",
	model.StockOutTypeOther: "其它",
}

// docStatusNames 是审核状态中文展示（对齐芋道 erp_audit_status：10/20）。
var docStatusNames = map[int8]string{
	model.DocStatusUnaudited: "未审核",
	model.DocStatusApproved:  "已审核",
}
