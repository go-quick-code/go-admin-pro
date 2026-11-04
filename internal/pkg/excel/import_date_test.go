package excel_test

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

// buildDateWorkbook 生成包含「客户名称」「加入日期」两列、单条数据行的工作簿，
// 加入日期写入 value（数字按 Excel 序列号、字符串按日期文本解析）。
func buildDateWorkbook(t *testing.T, date1904 bool, value any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if date1904 {
		if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: boolPtr(true)}); err != nil {
			t.Fatalf("set workbook props: %v", err)
		}
	}
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"客户名称", "加入日期"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{"甲", value})
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	return buf.Bytes()
}

func TestImportDate1904SerialZero(t *testing.T) {
	tz := time.FixedZone("CST", 8*3600)
	// 1904 日期系统的序列号 0 是 1904-01-01，必须可导入。
	data := buildDateWorkbook(t, true, 0)
	rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Location: tz})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0].JoinedAt
	want := time.Date(1904, time.January, 1, 0, 0, 0, 0, tz)
	if !got.Equal(want) {
		t.Errorf("joined at = %v, want %v", got, want)
	}
	if got.Location() != tz {
		t.Errorf("location = %v, want %v", got.Location(), tz)
	}
}

func TestImportDateSerialMaxValues(t *testing.T) {
	tz := time.FixedZone("CST", 8*3600)
	want := time.Date(9999, time.December, 31, 0, 0, 0, 0, tz)
	tests := []struct {
		name     string
		date1904 bool
		serial   float64
	}{
		{"1900 serial 2958465", false, 2958465},
		{"1904 serial 2957003", true, 2957003},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildDateWorkbook(t, tt.date1904, tt.serial)
			rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Location: tz})
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("got %d rows, want 1", len(rows))
			}
			got := rows[0].JoinedAt
			if !got.Equal(want) {
				t.Errorf("joined at = %v, want %v", got, want)
			}
			if got.Location() != tz {
				t.Errorf("location = %v, want %v", got.Location(), tz)
			}
		})
	}
}

func TestImportDateSerialOutOfRange(t *testing.T) {
	tests := []struct {
		name     string
		date1904 bool
		serial   any
	}{
		{"1900 just above max", false, 2958465.999999},
		{"1900 max plus one", false, 2958466},
		{"1904 max plus one", true, 2957004},
		{"1900 zero", false, 0},
		{"1900 negative", false, -1},
		{"1904 negative", true, -1},
		{"1900 NaN", false, math.NaN()},
		{"1900 positive infinity", false, math.Inf(1)},
		{"1904 negative infinity", true, math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildDateWorkbook(t, tt.date1904, tt.serial)
			_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
			ie := asImportError(t, err)
			assertCellError(t, ie, 2, "加入日期", "超出 Excel 日期范围")
		})
	}
}
