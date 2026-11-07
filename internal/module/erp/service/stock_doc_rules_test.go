package service

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// stock_doc_rules_test.go 是单据规则的纯函数表驱动测试（issue #90，spec §8.1
// 次缝：明细不变量/审核状态机/编号格式）。HTTP 契约层的行为在
// *_http_integration_test.go。

// TestBuildDocItemsRowAmount 明细行金额 = 单价 × 数量（Money.MulQty，128 位
// 中间量，绝对值四舍五入到分）。
func TestBuildDocItemsRowAmount(t *testing.T) {
	cases := []struct {
		name      string
		productID int64
		price     string
		count     string
		wantTotal string
		wantErr   bool
	}{
		{name: "整数数量", productID: 1, price: "2.50", count: "3", wantTotal: "7.50"},
		{name: "小数数量精确整除", productID: 1, price: "0.20", count: "0.500", wantTotal: "0.10"},
		{name: "小数数量四舍五入", productID: 1, price: "0.33", count: "3", wantTotal: "0.99"},
		{name: "余数进位", productID: 1, price: "0.33", count: "1.515", wantTotal: "0.50"},
		{name: "零单价缺省", productID: 1, price: "0.00", count: "2", wantTotal: "0.00"},
		{name: "数量为 0 拒绝", productID: 1, price: "1.00", count: "0", wantErr: true},
		{name: "数量为负拒绝", productID: 1, price: "1.00", count: "-1", wantErr: true},
		{name: "单价为负拒绝", productID: 1, price: "-0.01", count: "1", wantErr: true},
		{name: "缺少产品拒绝", productID: 0, price: "1.00", count: "1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := []dto.StockDocItemPayload{{
				ProductId:    tc.productID,
				Count:        mustQty(t, tc.count),
				ProductPrice: mustMoney(t, tc.price),
			}}
			drafts, err := buildDocItems(items, "入库明细")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望校验拒绝，实际放行")
				}
				var xerrPtr *xerr.Error
				if !asXerrBadRequest(err, &xerrPtr) {
					t.Fatalf("期望 400 业务错误，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("校验意外失败: %v", err)
			}
			if got := drafts[0].TotalPrice.String(); got != tc.wantTotal {
				t.Fatalf("行金额 = %s，期望 %s（单价 %s × 数量 %s）", got, tc.wantTotal, tc.price, tc.count)
			}
		})
	}
}

// TestBuildDocItemsEmpty 明细为空拒绝（spec §4.2：明细不变量的前置条件）。
func TestBuildDocItemsEmpty(t *testing.T) {
	if _, err := buildDocItems(nil, "入库明细"); err == nil {
		t.Fatal("空明细应拒绝")
	}
}

// TestCheckDocStatusTransition 审核状态机（spec §4.1：10/20，幂等目标态）。
func TestCheckDocStatusTransition(t *testing.T) {
	cases := []struct {
		name       string
		current    int8
		target     int8
		idempotent bool
		wantErr    bool
	}{
		{name: "审核 10→20", current: 10, target: 20},
		{name: "反审核 20→10", current: 20, target: 10},
		{name: "重复审核幂等", current: 20, target: 20, idempotent: true},
		{name: "重复反审核幂等", current: 10, target: 10, idempotent: true},
		{name: "非法状态值", current: 10, target: 30, wantErr: true},
		{name: "非法状态值 0", current: 10, target: 0, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idempotent, err := checkDocStatusTransition(tc.current, tc.target)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望非法状态拒绝")
				}
				return
			}
			if err != nil {
				t.Fatalf("状态机意外拒绝: %v", err)
			}
			if idempotent != tc.idempotent {
				t.Fatalf("幂等标记 = %v，期望 %v", idempotent, tc.idempotent)
			}
		})
	}
}

// TestFormatStockDocNo 编号格式（spec §4.8：前缀 + yyyyMMdd + 至少 4 位序号，
// 超 9999 扩位）。
func TestFormatStockDocNo(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		dateKey string
		seq     int64
		want    string
	}{
		{name: "入库首号", prefix: "QR", dateKey: "20261107", seq: 1, want: "QR202611070001"},
		{name: "出库续号", prefix: "QC", dateKey: "20261107", seq: 123, want: "QC202611070123"},
		{name: "序号扩位", prefix: "QR", dateKey: "20261231", seq: 12345, want: "QR2026123112345"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatStockDocNo(tc.prefix, tc.dateKey, tc.seq); got != tc.want {
				t.Fatalf("单号 = %s，期望 %s", got, tc.want)
			}
		})
	}
	// 全集前缀与 spec §4.8 一致（QR/QC 本票；其余 #91+）。
	if StockInNoPrefix != "QR" || StockOutNoPrefix != "QC" {
		t.Fatalf("编号前缀不符 spec §4.8：%s/%s", StockInNoPrefix, StockOutNoPrefix)
	}
}

// TestStockRecordBizTypes 流水 biz_type 与 _CANCEL 对成对定义（spec §4.3），
// 中文展示映射齐全（导出/前端标签）：全集 8 对 16 个值单点注册在
// stockRecordBizNames（终审 P1：跨文件 init 合并已移除，测试覆盖全部对）。
func TestStockRecordBizTypes(t *testing.T) {
	pairs := [][2]string{
		{model.StockRecordBizStockIn, model.StockRecordBizStockInCancel},
		{model.StockRecordBizStockOut, model.StockRecordBizStockOutCancel},
		{model.StockRecordBizPurchaseIn, model.StockRecordBizPurchaseInCancel},
		{model.StockRecordBizPurchaseReturn, model.StockRecordBizPurchaseReturnCancel},
		{model.StockRecordBizSaleOut, model.StockRecordBizSaleOutCancel},
		{model.StockRecordBizSaleReturn, model.StockRecordBizSaleReturnCancel},
		{model.StockRecordBizStockMove, model.StockRecordBizStockMoveCancel},
		{model.StockRecordBizStockCheck, model.StockRecordBizStockCheckCancel},
	}
	if len(stockRecordBizNames) != len(pairs)*2 {
		t.Fatalf("biz_type 中文名映射应有 %d 项（%d 对），实际 %d（多注册/漏注册）",
			len(pairs)*2, len(pairs), len(stockRecordBizNames))
	}
	for _, pair := range pairs {
		if !strings.HasSuffix(pair[1], "_CANCEL") {
			t.Fatalf("反向流水 %s 必须以 _CANCEL 结尾（成对定义，spec §4.3）", pair[1])
		}
		if pair[0]+"_CANCEL" != pair[1] {
			t.Fatalf("反向流水 %s 与正向 %s 不成对（应为 %s_CANCEL）", pair[1], pair[0], pair[0])
		}
		for _, biz := range pair {
			if stockRecordBizNames[biz] == "" {
				t.Fatalf("biz_type %s 缺少中文展示", biz)
			}
		}
	}
}

// TestDocTypeValidation 单据类型枚举（字典取值，非法 400）。
func TestDocTypeValidation(t *testing.T) {
	for _, v := range []int8{10, 20, 30, 40, 50, 99} {
		if _, err := validateDocType(&v, model.ValidStockInType, "入库单"); err != nil {
			t.Fatalf("入库类型 %d 应合法: %v", v, err)
		}
	}
	for _, v := range []int8{10, 20, 30, 99} {
		if _, err := validateDocType(&v, model.ValidStockOutType, "出库单"); err != nil {
			t.Fatalf("出库类型 %d 应合法: %v", v, err)
		}
	}
	invalid := int8(1)
	if _, err := validateDocType(&invalid, model.ValidStockInType, "入库单"); err == nil {
		t.Fatal("非法入库类型应拒绝")
	}
	if _, err := validateDocType(&invalid, model.ValidStockOutType, "出库单"); err == nil {
		t.Fatal("非法出库类型应拒绝")
	}
	// 缺省归 99 其它。
	if got, err := validateDocType(nil, model.ValidStockOutType, "出库单"); err != nil || got != model.StockOutTypeOther {
		t.Fatalf("缺省类型应为 99 其它，实际 %d/%v", got, err)
	}
}

func mustQty(t *testing.T, s string) qty.Qty {
	t.Helper()
	q, err := qty.ParseQty(s)
	if err != nil {
		t.Fatalf("解析数量 %s: %v", s, err)
	}
	return q
}

func mustMoney(t *testing.T, s string) money.Money {
	t.Helper()
	m, err := money.ParseMoney(s)
	if err != nil {
		t.Fatalf("解析金额 %s: %v", s, err)
	}
	return m
}

// asXerrBadRequest 判定 err 是 400 业务错误（xerr.Error.Status == 400）。
func asXerrBadRequest(err error, target **xerr.Error) bool {
	if e, ok := xerr.As(err); ok && e.Status == 400 {
		*target = e
		return true
	}
	return false
}
