package service

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service/doc"
)

// TestDocStoreSchemaItemsFK 校验每类 DocSchema.ItemsFK 与模型 association 的
// 外键列一致：ItemsFK 是唯一拼进 SQL 的标识符，拼错/留空只能靠集成测试暴露。
func TestDocStoreSchemaItemsFK(t *testing.T) {
	s := &Service{}
	assertItemsFK(t, s.stockInStore(), &model.StockIn{}, "stock_in_id")
	assertItemsFK(t, s.stockOutStore(), &model.StockOut{}, "stock_out_id")
	assertItemsFK(t, s.purchaseOrderStore(), &model.PurchaseOrder{}, "purchase_order_id")
	assertItemsFK(t, s.purchaseInStore(), &model.PurchaseIn{}, "purchase_in_id")
	assertItemsFK(t, s.purchaseReturnStore(), &model.PurchaseReturn{}, "purchase_return_id")
	assertItemsFK(t, s.saleOrderStore(), &model.SaleOrder{}, "sale_order_id")
	assertItemsFK(t, s.saleOutStore(), &model.SaleOut{}, "sale_out_id")
	assertItemsFK(t, s.saleReturnStore(), &model.SaleReturn{}, "sale_return_id")
	assertItemsFK(t, s.stockMoveStore(), &model.StockMove{}, "stock_move_id")
	assertItemsFK(t, s.stockCheckStore(), &model.StockCheck{}, "stock_check_id")
	assertItemsFK(t, s.financeReceiptStore(), &model.FinanceReceipt{}, "receipt_id")
	assertItemsFK(t, s.financePaymentStore(), &model.FinancePayment{}, "payment_id")
}

func assertItemsFK[H doc.Header, I any](t *testing.T, store doc.Store[H, I], header any, want string) {
	t.Helper()
	gs, ok := store.(*genericDocStore[H, I])
	if !ok {
		t.Fatalf("unexpected store type %T", store)
	}
	if gs.schema.ItemsFK != want {
		t.Fatalf("DocSchema.ItemsFK = %q，期望 %q", gs.schema.ItemsFK, want)
	}
	parsed, err := schema.Parse(header, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析模型 schema: %v", err)
	}
	rel, ok := parsed.Relationships.Relations["Items"]
	if !ok {
		t.Fatalf("%T 缺少 Items association", header)
	}
	if len(rel.References) == 0 || rel.References[0].ForeignKey == nil || rel.References[0].ForeignKey.DBName != want {
		t.Fatalf("%T 的 Items 外键列与 ItemsFK 不一致（期望 %q）", header, want)
	}
}
