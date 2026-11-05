package excel_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

func TestExportNilPointerRowFirst(t *testing.T) {
	_, err := excel.Export([]*sampleRow{nil, {Name: "李四"}}, excel.ExportOptions{})
	if err == nil {
		t.Fatal("Export with nil pointer row: want error, got nil")
	}
	if !strings.Contains(err.Error(), "第 2 行为 nil") {
		t.Fatalf("error = %v, want 第 2 行为 nil", err)
	}
	if !strings.Contains(err.Error(), "请传入非 nil 行") {
		t.Fatalf("error = %v, want expected form hint 请传入非 nil 行", err)
	}
}

func TestExportNilPointerRowMiddle(t *testing.T) {
	rows := []*sampleRow{{Name: "张三"}, nil, {Name: "王五"}}
	_, err := excel.Export(rows, excel.ExportOptions{})
	if err == nil {
		t.Fatal("Export with nil pointer row: want error, got nil")
	}
	if !strings.Contains(err.Error(), "第 3 行为 nil") {
		t.Fatalf("error = %v, want 第 3 行为 nil", err)
	}
}

func TestExportNilInPointerChain(t *testing.T) {
	row := &sampleRow{Name: "张三"}
	outer := &row
	var nilInner *sampleRow
	withNilInner := &nilInner

	_, err := excel.Export([]**sampleRow{outer, withNilInner}, excel.ExportOptions{})
	if err == nil {
		t.Fatal("Export with nil in pointer chain: want error, got nil")
	}
	if !strings.Contains(err.Error(), "第 3 行为 nil") {
		t.Fatalf("error = %v, want 第 3 行为 nil", err)
	}
}

func TestExportPointerRowsUnaffected(t *testing.T) {
	rows := []*sampleRow{
		{Name: "张三", Age: 30, Amount: 1234.5, Active: true},
		{Name: "李四"},
	}
	data, err := excel.Export(rows, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := openExport(t, data, "数据")
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %v", len(got), got)
	}
	wantHeader := "客户名称|年龄|金额|启用|加入日期|备注"
	if strings.Join(got[0], "|") != wantHeader {
		t.Errorf("header = %v, want %s", got[0], wantHeader)
	}
	if got[1][0] != "张三" || got[1][1] != "30" || got[1][2] != "1234.5" || got[1][3] != "TRUE" {
		t.Errorf("unexpected first row: %v", got[1])
	}
	if got[2][0] != "李四" || got[2][3] != "FALSE" {
		t.Errorf("unexpected second row: %v", got[2])
	}
}

func TestExportValueRowsUnaffected(t *testing.T) {
	data, err := excel.Export([]sampleRow{{Name: "张三", Age: 30}}, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := openExport(t, data, "数据")
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(got), got)
	}
	if got[1][0] != "张三" || got[1][1] != "30" {
		t.Errorf("unexpected row: %v", got[1])
	}
}
