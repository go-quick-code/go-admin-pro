package excel_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

func openExport(t *testing.T, data []byte, sheet string) [][]string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("get rows: %v", err)
	}
	return rows
}

func TestExport(t *testing.T) {
	rows := []sampleRow{
		{Name: "张三", Age: 30, Amount: 1234.5, Active: true, JoinedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Note: "重点"},
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
	wantHeader := []string{"客户名称", "年龄", "金额", "启用", "加入日期", "备注"}
	if strings.Join(got[0], "|") != strings.Join(wantHeader, "|") {
		t.Errorf("header = %v, want %v", got[0], wantHeader)
	}
	if got[1][0] != "张三" || got[1][1] != "30" || got[1][2] != "1234.5" || got[1][3] != "TRUE" ||
		got[1][4] != "2026-01-02 03:04:05" || got[1][5] != "重点" {
		t.Errorf("unexpected first row: %v", got[1])
	}
	if got[2][0] != "李四" || got[2][3] != "FALSE" {
		t.Errorf("unexpected second row: %v", got[2])
	}
}

func TestExportColumns(t *testing.T) {
	rows := []sampleRow{{Name: "张三", Amount: 99.5}}

	t.Run("by field name", func(t *testing.T) {
		data, err := excel.Export(rows, excel.ExportOptions{Columns: []string{"Amount", "Name"}})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		got := openExport(t, data, "数据")
		if strings.Join(got[0], "|") != "金额|客户名称" {
			t.Errorf("header = %v, want [金额 客户名称]", got[0])
		}
		if got[1][0] != "99.5" || got[1][1] != "张三" {
			t.Errorf("row = %v", got[1])
		}
	})

	t.Run("by header", func(t *testing.T) {
		data, err := excel.Export(rows, excel.ExportOptions{Columns: []string{"客户名称"}})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		got := openExport(t, data, "数据")
		if strings.Join(got[0], "|") != "客户名称" {
			t.Errorf("header = %v", got[0])
		}
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := excel.Export(rows, excel.ExportOptions{Columns: []string{"不存在"}})
		if !errors.Is(err, excel.ErrColumnNotFound) {
			t.Fatalf("error = %v, want ErrColumnNotFound", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		_, err := excel.Export(rows, excel.ExportOptions{Columns: []string{"Name", "客户名称"}})
		if !errors.Is(err, excel.ErrDuplicateColumn) {
			t.Fatalf("error = %v, want ErrDuplicateColumn", err)
		}
	})
}

func TestExportFormatters(t *testing.T) {
	rows := []sampleRow{{Name: "张三", Active: true, JoinedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}}
	data, err := excel.Export(rows, excel.ExportOptions{
		Formatters: map[string]excel.Formatter{
			"Active": func(value any) (any, error) {
				if value.(bool) {
					return "是", nil
				}
				return "否", nil
			},
			"加入日期": func(value any) (any, error) {
				return value.(time.Time).Format("2006-01-02"), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := openExport(t, data, "数据")
	if got[1][3] != "是" || got[1][4] != "2026-01-02" {
		t.Errorf("unexpected formatted row: %v", got[1])
	}
}

func TestExportFormatterError(t *testing.T) {
	rows := []sampleRow{{Name: "张三"}, {Name: "李四"}}
	_, err := excel.Export(rows, excel.ExportOptions{
		Formatters: map[string]excel.Formatter{
			"Name": func(value any) (any, error) {
				if value == "李四" {
					return nil, errors.New("不能导出")
				}
				return value, nil
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "第 3 行") {
		t.Fatalf("error = %v, want row number in message", err)
	}
}

func TestExportRoundTrip(t *testing.T) {
	want := []sampleRow{
		{Name: "张三", Age: 30, Amount: 1234.56, Active: true, JoinedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Note: "重点"},
		{Name: "李四", Age: 18, Amount: 0, Active: false, JoinedAt: time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)},
	}
	data, err := excel.Export(want, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Age != want[i].Age || got[i].Amount != want[i].Amount ||
			got[i].Active != want[i].Active || !got[i].JoinedAt.Equal(want[i].JoinedAt) || got[i].Note != want[i].Note {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestExportSheetAndPointers(t *testing.T) {
	name := "张三"
	rows := []typedRow{{Name: "甲", Level: 1, Owner: &name, Score: nil}}
	data, err := excel.Export(rows, excel.ExportOptions{Sheet: "负责人"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := openExport(t, data, "负责人")
	if strings.Join(got[0], "|") != "名称|级别|负责人|评分|加入日期" {
		t.Errorf("header = %v", got[0])
	}
	if got[1][0] != "甲" || got[1][1] != "1" || got[1][2] != "张三" {
		t.Errorf("row = %v", got[1])
	}
	if len(got[1]) > 3 && got[1][3] != "" {
		t.Errorf("nil score should be blank, got %q", got[1][3])
	}
}
