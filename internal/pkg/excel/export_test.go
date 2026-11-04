package excel_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
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
	got, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Location: time.UTC})
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
	if got[1][0] != "甲" || got[1][1] != "高" || got[1][2] != "张三" {
		t.Errorf("row = %v", got[1])
	}
	if len(got[1]) > 3 && got[1][3] != "" {
		t.Errorf("nil score should be blank, got %q", got[1][3])
	}
}

func TestExportAutoFilterRange(t *testing.T) {
	data, err := excel.Export([]sampleRow{{Name: "张三"}, {Name: "李四"}}, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	sheetXML := zipEntry(t, data, "xl/worksheets/sheet1.xml")
	if !strings.Contains(sheetXML, `autoFilter ref="$A$1:$F$3"`) {
		t.Errorf("autoFilter should cover data rows, sheet xml: %s", sheetXML)
	}
}

func TestExportFormatterAliasConflict(t *testing.T) {
	_, err := excel.Export([]sampleRow{{Name: "张三"}}, excel.ExportOptions{
		Formatters: map[string]excel.Formatter{
			"Amount": func(value any) (any, error) { return value, nil },
			"金额":     func(value any) (any, error) { return value, nil },
		},
	})
	if !errors.Is(err, excel.ErrDuplicateColumn) {
		t.Fatalf("error = %v, want ErrDuplicateColumn", err)
	}
}

type unmarshalOnlyLevel int

func (l *unmarshalOnlyLevel) UnmarshalText(text []byte) error {
	*l = 1
	return nil
}

type unmarshalOnlyRow struct {
	Level unmarshalOnlyLevel `excel:"级别"`
}

func TestExportUnmarshalOnlyError(t *testing.T) {
	_, err := excel.Export([]unmarshalOnlyRow{{Level: 1}}, excel.ExportOptions{})
	if err == nil || !strings.Contains(err.Error(), "MarshalText") {
		t.Fatalf("error = %v, want MarshalText guidance", err)
	}
}

func zipEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, file := range zr.File {
		if file.Name != name {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		content, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(content)
	}
	t.Fatalf("zip entry %s not found", name)
	return ""
}
