package money_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/xuri/excelize/v2"
)

// excelRow 验证 Money 作为 excel 字段类型开箱即用：
// 导出走 MarshalText，导入走 UnmarshalText，模板无需额外配置。
type excelRow struct {
	Name  string      `excel:"产品名称,required"`
	Price money.Money `excel:"价格"`
}

func TestMoneyExcelRoundTrip(t *testing.T) {
	rows := []excelRow{
		{Name: "键盘", Price: mustParse(t, "1234.56")},
		{Name: "鼠标", Price: mustParse(t, "0.01")},
		{Name: "退款", Price: mustParse(t, "-99.90")},
		{Name: "上限", Price: mustParse(t, "9999999999.99")},
	}
	data, err := excel.Export(rows, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export 返回错误：%v", err)
	}
	got, err := excel.Import(bytes.NewReader(data), excel.ImportOptions[excelRow]{})
	if err != nil {
		t.Fatalf("Import 返回错误：%v", err)
	}
	if len(got) != len(rows) {
		t.Fatalf("导入行数 = %d, 期望 %d", len(got), len(rows))
	}
	for i, want := range rows {
		if got[i] != want {
			t.Fatalf("第 %d 行往返不一致：got %+v, want %+v", i+2, got[i], want)
		}
		if got[i].Price.String() != want.Price.String() {
			t.Fatalf("第 %d 行金额不一致：got %q, want %q", i+2, got[i].Price.String(), want.Price.String())
		}
	}
}

func TestMoneyExcelZeroExportsLiteralZero(t *testing.T) {
	data, err := excel.Export([]excelRow{{Name: "赠品"}}, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export 返回错误：%v", err)
	}
	got, err := excel.Import(bytes.NewReader(data), excel.ImportOptions[excelRow]{})
	if err != nil {
		t.Fatalf("Import 返回错误：%v", err)
	}
	if len(got) != 1 || got[0].Price.String() != "0.00" {
		t.Fatalf("零值金额应往返为 0.00, 得到 %+v", got)
	}
}

func TestMoneyExcelImportRejectsInvalidAmount(t *testing.T) {
	// 直接构造金额列含三位小数的工作簿。
	f := excelize.NewFile()
	defer f.Close()
	f.SetCellValue("Sheet1", "A1", "产品名称")
	f.SetCellValue("Sheet1", "B1", "价格")
	f.SetCellValue("Sheet1", "A2", "键盘")
	f.SetCellValue("Sheet1", "B2", "10.005")
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("生成工作簿失败：%v", err)
	}
	var importErr *excel.ImportError
	_, err = excel.Import(buf, excel.ImportOptions[excelRow]{})
	if !errors.As(err, &importErr) {
		t.Fatalf("非法金额应返回 *ImportError, 得到 %v", err)
	}
	if importErr.Total == 0 {
		t.Fatalf("非法金额应产生明细错误")
	}
	if importErr.Errors[0].Column != "价格" {
		t.Fatalf("明细错误应指向价格列, 得到 %+v", importErr.Errors[0])
	}
}

func TestMoneyExcelTemplateUsable(t *testing.T) {
	data, err := excel.Template[excelRow](excel.TemplateOptions{
		Notes: map[string]string{"价格": "十进制金额，最多两位小数，如 1234.56"},
	})
	if err != nil {
		t.Fatalf("Template 返回错误：%v", err)
	}
	got, err := excel.Import(bytes.NewReader(data), excel.ImportOptions[excelRow]{})
	if err != nil {
		t.Fatalf("导入模板应成功且无数据行, 得到错误：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("模板导入行数 = %d, 期望 0", len(got))
	}
}

func mustParse(t *testing.T, text string) money.Money {
	t.Helper()
	m, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("ParseMoney(%q) 返回错误：%v", text, err)
	}
	return m
}
