package excel_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

type importRow struct {
	Name     string    `excel:"客户名称,required"`
	Age      int       `excel:"年龄"`
	Amount   float64   `excel:"金额"`
	Active   bool      `excel:"启用"`
	JoinedAt time.Time `excel:"加入日期"`
	Note     string    `excel:"备注"`
}

// buildWorkbook 生成内存中的 xlsx，rows 中的每一行写入 A1 起始的位置。
func buildWorkbook(t *testing.T, sheet string, rows [][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if sheet != "" && sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			t.Fatalf("rename sheet: %v", err)
		}
	} else {
		sheet = "Sheet1"
	}
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("set row: %v", err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	return buf.Bytes()
}

func TestImport(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄", "金额", "启用", "加入日期", "备注"},
		{"张三", 30, 1234.56, true, "2026-01-02", "重点"},
		{}, // 空行应被跳过
		{"李四", 28, 99, false, "2026/3/4 10:20:30", ""},
	})

	rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Location: time.UTC})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	first := rows[0]
	if first.Name != "张三" || first.Age != 30 || first.Amount != 1234.56 || !first.Active || first.Note != "重点" {
		t.Errorf("unexpected first row: %+v", first)
	}
	if want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC); !first.JoinedAt.Equal(want) {
		t.Errorf("joined at = %v, want %v", first.JoinedAt, want)
	}
	second := rows[1]
	if second.Name != "李四" || second.Age != 28 || second.Active || second.Note != "" {
		t.Errorf("unexpected second row: %+v", second)
	}
	if want := time.Date(2026, 3, 4, 10, 20, 30, 0, time.UTC); !second.JoinedAt.Equal(want) {
		t.Errorf("joined at = %v, want %v", second.JoinedAt, want)
	}
}

func asImportError(t *testing.T, err error) *excel.ImportError {
	t.Helper()
	var ie *excel.ImportError
	if !errors.As(err, &ie) {
		t.Fatalf("error = %v, want *excel.ImportError", err)
	}
	return ie
}

func TestImportHeaderErrors(t *testing.T) {
	headers := []any{"客户名称", "年龄", "金额", "启用", "加入日期", "备注"}

	t.Run("missing required column", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{{"年龄", "金额"}, {30, 1}})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		ie := asImportError(t, err)
		assertCellError(t, ie, 1, "客户名称", "缺少必填列")
	})

	t.Run("unknown column", func(t *testing.T) {
		withExtra := append(append([]any{}, headers...), "额外列")
		data := buildWorkbook(t, "数据", [][]any{withExtra, {"张三", 30, 1, true, "2026-01-02", "", "x"}})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		ie := asImportError(t, err)
		assertCellError(t, ie, 1, "额外列", "未知列")
	})

	t.Run("unknown column allowed", func(t *testing.T) {
		withExtra := append(append([]any{}, headers...), "额外列")
		data := buildWorkbook(t, "数据", [][]any{withExtra, {"张三", 30, 1, true, "2026-01-02", "", "x"}})
		rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{AllowUnknownHeaders: true})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if len(rows) != 1 || rows[0].Name != "张三" {
			t.Fatalf("unexpected rows: %+v", rows)
		}
	})

	t.Run("optional column missing", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{
			{"客户名称", "年龄"},
			{"张三", 30},
		})
		rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if len(rows) != 1 || rows[0].Note != "" {
			t.Fatalf("unexpected rows: %+v", rows)
		}
	})
}

func TestImportRowErrors(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄", "金额", "启用", "加入日期", "备注"},
		{"", 30, 1, true, "2026-01-02", "缺名称"},
		{"张三", "abc", 1, true, "2026-01-02", "年龄错误"},
		{"李四", 20, 1, true, "not-a-date", "日期错误"},
	})
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	ie := asImportError(t, err)
	if len(ie.Errors) != 3 {
		t.Fatalf("got %d errors, want 3: %+v", len(ie.Errors), ie.Errors)
	}
	if ie.Total != 3 || ie.Truncated {
		t.Errorf("total = %d, truncated = %v; want 3, false", ie.Total, ie.Truncated)
	}
	assertCellError(t, ie, 2, "客户名称", "必填")
	assertCellError(t, ie, 3, "年龄", "不是有效的整数")
	assertCellError(t, ie, 4, "加入日期", "不是有效的日期")
}

func assertCellError(t *testing.T, ie *excel.ImportError, row int, column, msg string) {
	t.Helper()
	for _, item := range ie.Errors {
		if item.Row == row && item.Column == column && strings.Contains(item.Msg, msg) {
			return
		}
	}
	t.Errorf("missing error row=%d column=%q msg~%q in %+v", row, column, msg, ie.Errors)
}

func TestImportValidate(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄", "金额", "启用", "加入日期", "备注"},
		{"张三", 30, -1, true, "2026-01-02", ""},
		{"李四", 20, 5, true, "2026-01-02", ""},
		{"王五", "abc", 5, true, "2026-01-02", ""},
	})
	validated := map[int]bool{}
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{
		Validate: func(row int, v *importRow) error {
			validated[row] = true
			if v.Amount < 0 {
				return errors.New("金额不能为负")
			}
			return nil
		},
	})
	ie := asImportError(t, err)
	if len(ie.Errors) != 2 {
		t.Fatalf("got %d errors, want 2: %+v", len(ie.Errors), ie.Errors)
	}
	assertCellError(t, ie, 2, "", "金额不能为负")
	assertCellError(t, ie, 4, "年龄", "不是有效的整数")
	if validated[4] {
		t.Error("validator should not run for rows with cell errors")
	}
	if !validated[2] || !validated[3] {
		t.Errorf("validator should run for parsed rows, got %v", validated)
	}
}

type level int

func (l *level) UnmarshalText(text []byte) error {
	switch string(text) {
	case "高":
		*l = 1
	case "中":
		*l = 2
	case "低":
		*l = 3
	default:
		return errors.New("未知级别 " + string(text))
	}
	return nil
}

func (l level) MarshalText() ([]byte, error) {
	switch l {
	case 1:
		return []byte("高"), nil
	case 2:
		return []byte("中"), nil
	case 3:
		return []byte("低"), nil
	}
	return nil, fmt.Errorf("未知级别 %d", l)
}

type typedRow struct {
	Name     string    `excel:"名称"`
	Level    level     `excel:"级别"`
	Owner    *string   `excel:"负责人"`
	Score    *int      `excel:"评分"`
	JoinedAt time.Time `excel:"加入日期"`
}

func TestImportTypes(t *testing.T) {
	// 46024 是 2026-01-02 的 Excel 序列号（1900 日期系统）。
	const serial = 46024
	data := buildWorkbook(t, "数据", [][]any{
		{"名称", "级别", "负责人", "评分", "加入日期"},
		{"甲", "高", "张三", 5, serial},
		{"乙", "中", "", "", "2026-01-02T08:09:10Z"},
	})
	rows, err := excel.Import[typedRow](bytes.NewReader(data), excel.ImportOptions[typedRow]{Location: time.UTC})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Level != 1 || rows[0].Owner == nil || *rows[0].Owner != "张三" || rows[0].Score == nil || *rows[0].Score != 5 {
		t.Errorf("unexpected first row: %+v", rows[0])
	}
	if want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC); !rows[0].JoinedAt.Equal(want) {
		t.Errorf("serial date = %v, want %v", rows[0].JoinedAt, want)
	}
	if rows[1].Level != 2 || rows[1].Owner != nil || rows[1].Score != nil {
		t.Errorf("unexpected second row: %+v", rows[1])
	}
	if want := time.Date(2026, 1, 2, 8, 9, 10, 0, time.UTC); !rows[1].JoinedAt.Equal(want) {
		t.Errorf("rfc3339 date = %v, want %v", rows[1].JoinedAt, want)
	}

	t.Run("invalid custom value", func(t *testing.T) {
		bad := buildWorkbook(t, "数据", [][]any{
			{"名称", "级别"},
			{"甲", "超高"},
		})
		_, err := excel.Import[typedRow](bytes.NewReader(bad), excel.ImportOptions[typedRow]{AllowUnknownHeaders: true})
		ie := asImportError(t, err)
		assertCellError(t, ie, 2, "级别", "未知级别")
	})
}

func TestImportLimits(t *testing.T) {
	t.Run("file too large", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{{"客户名称"}, {"张三"}})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{MaxBytes: 64})
		if !errors.Is(err, excel.ErrFileTooLarge) {
			t.Fatalf("error = %v, want ErrFileTooLarge", err)
		}
	})

	t.Run("too many rows custom", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{
			{"客户名称"}, {"甲"}, {"乙"},
		})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{MaxRows: 1})
		if !errors.Is(err, excel.ErrTooManyRows) {
			t.Fatalf("error = %v, want ErrTooManyRows", err)
		}
	})

	t.Run("too many rows default", func(t *testing.T) {
		rows := make([][]any, 0, excel.DefaultMaxRows+2)
		rows = append(rows, []any{"客户名称"})
		for i := 0; i <= excel.DefaultMaxRows; i++ {
			rows = append(rows, []any{"客户"})
		}
		data := buildWorkbook(t, "数据", rows)
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		if !errors.Is(err, excel.ErrTooManyRows) {
			t.Fatalf("error = %v, want ErrTooManyRows", err)
		}
	})
}

func TestImportWorkbookErrors(t *testing.T) {
	t.Run("not a workbook", func(t *testing.T) {
		_, err := excel.Import[importRow](strings.NewReader("not an excel file"), excel.ImportOptions[importRow]{})
		if !errors.Is(err, excel.ErrInvalidWorkbook) {
			t.Fatalf("error = %v, want ErrInvalidWorkbook", err)
		}
	})

	t.Run("empty sheet", func(t *testing.T) {
		data := buildWorkbook(t, "数据", nil)
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		if !errors.Is(err, excel.ErrInvalidWorkbook) {
			t.Fatalf("error = %v, want ErrInvalidWorkbook", err)
		}
	})

	t.Run("sheet missing", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{{"客户名称"}, {"张三"}})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Sheet: "不存在"})
		if !errors.Is(err, excel.ErrSheetNotFound) {
			t.Fatalf("error = %v, want ErrSheetNotFound", err)
		}
	})

	t.Run("header not on first row", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{nil, nil, {"客户名称"}, {"张三"}})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		if !errors.Is(err, excel.ErrInvalidWorkbook) {
			t.Fatalf("error = %v, want ErrInvalidWorkbook", err)
		}
	})
}

func TestImportPointerRows(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄"},
		{"张三", 30},
	})
	rows, err := excel.Import[*importRow](bytes.NewReader(data), excel.ImportOptions[*importRow]{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(rows) != 1 || rows[0] == nil || rows[0].Name != "张三" || rows[0].Age != 30 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestImportRowNumbersWithGaps(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄"},
		nil, // 第 2 行为空
		{"张三", "abc"},
	})
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	ie := asImportError(t, err)
	assertCellError(t, ie, 3, "年龄", "不是有效的整数")
}

func TestImportUnknownOnlyRowIsNotDropped(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "额外列"},
		{"", "只在未知列有值"},
	})
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{AllowUnknownHeaders: true})
	ie := asImportError(t, err)
	assertCellError(t, ie, 2, "客户名称", "必填")
}

type formattedRow struct {
	Name     string    `excel:"客户名称"`
	Amount   float64   `excel:"金额"`
	JoinedAt time.Time `excel:"加入日期"`
	Active   bool      `excel:"启用"`
}

func TestImportFormattedValues(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"客户名称", "金额", "加入日期", "启用"})
	_ = f.SetCellValue("Sheet1", "A2", "张三")
	_ = f.SetCellValue("Sheet1", "B2", 1234.56)
	_ = f.SetCellValue("Sheet1", "C2", time.Date(2026, 1, 2, 10, 30, 0, 0, time.UTC))
	_ = f.SetCellValue("Sheet1", "D2", true)

	money, err := f.NewStyle(&excelize.Style{CustomNumFmt: stringPtr("#,##0.00")})
	if err != nil {
		t.Fatalf("money style: %v", err)
	}
	if err := f.SetCellStyle("Sheet1", "B2", "B2", money); err != nil {
		t.Fatalf("money cell style: %v", err)
	}
	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 22}) // m/d/yy h:mm
	if err != nil {
		t.Fatalf("date style: %v", err)
	}
	if err := f.SetCellStyle("Sheet1", "C2", "C2", dateStyle); err != nil {
		t.Fatalf("date cell style: %v", err)
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	rows, importErr := excel.Import[formattedRow](bytes.NewReader(buf.Bytes()), excel.ImportOptions[formattedRow]{})
	if importErr != nil {
		t.Fatalf("Import: %v", importErr)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Amount != 1234.56 {
		t.Errorf("amount = %v, want 1234.56", row.Amount)
	}
	if row.JoinedAt.Year() != 2026 || row.JoinedAt.Month() != time.January || row.JoinedAt.Day() != 2 ||
		row.JoinedAt.Hour() != 10 || row.JoinedAt.Minute() != 30 {
		t.Errorf("joined at = %v, want 2026-01-02 10:30", row.JoinedAt)
	}
	if !row.Active {
		t.Error("active should be true")
	}
}

func TestImportDate1904(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: boolPtr(true)}); err != nil {
		t.Fatalf("set workbook props: %v", err)
	}
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"客户名称", "加入日期"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{"张三", 44562}) // 1904 系统中 2026-01-02
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	rows, importErr := excel.Import[formattedRow](bytes.NewReader(buf.Bytes()), excel.ImportOptions[formattedRow]{AllowUnknownHeaders: true})
	if importErr != nil {
		t.Fatalf("Import: %v", importErr)
	}
	if len(rows) != 1 || rows[0].JoinedAt.Year() != 2026 || rows[0].JoinedAt.Month() != time.January || rows[0].JoinedAt.Day() != 2 {
		t.Fatalf("unexpected date: %+v", rows)
	}
}

func stringPtr(v string) *string { return &v }

func boolPtr(v bool) *bool { return &v }

func TestImportTimeLayoutsWithoutSeconds(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "加入日期"},
		{"张三", "2026-01-02 10:30"},
		{"李四", "2026/1/2 10:30"},
	})
	rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for i, row := range rows {
		got := row.JoinedAt
		if got.Year() != 2026 || got.Month() != time.January || got.Day() != 2 || got.Hour() != 10 || got.Minute() != 30 || got.Second() != 0 {
			t.Errorf("row %d joined at = %v, want 2026-01-02 10:30", i, got)
		}
	}
}

func TestImportLocation(t *testing.T) {
	tz := time.FixedZone("CST", 8*3600)
	const serial = 46024 + 10.5/24 // 2026-01-02 10:30
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "加入日期"},
		{"张三", "2026-01-02 10:30"},
		{"李四", serial},
	})
	rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Location: tz})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for i, row := range rows {
		got := row.JoinedAt
		if got.Location() != tz {
			t.Errorf("row %d location = %v, want %v", i, got.Location(), tz)
		}
		if got.Year() != 2026 || got.Month() != time.January || got.Day() != 2 || got.Hour() != 10 || got.Minute() != 30 {
			t.Errorf("row %d joined at = %v, want 2026-01-02 10:30", i, got)
		}
	}
}

func TestImportMaxErrors(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄"},
		{"", 1},
		{"", 2},
		{"", 3},
	})
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{MaxErrors: 2})
	ie := asImportError(t, err)
	if len(ie.Errors) != 2 || ie.Total != 3 || !ie.Truncated {
		t.Fatalf("errors = %d, total = %d, truncated = %v; want 2, 3, true", len(ie.Errors), ie.Total, ie.Truncated)
	}
	if !strings.Contains(err.Error(), "共 3") {
		t.Errorf("error message should mention total: %v", err)
	}
}

func TestImportMaxUnzipBytes(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称"},
		{"张三"},
	})
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{MaxUnzipBytes: 100})
	if !errors.Is(err, excel.ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
}

func TestImportDateRange(t *testing.T) {
	t.Run("historical and max dates", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{
			{"客户名称", "加入日期"},
			{"甲", 18264},   // 1950-01-01
			{"乙", 2958465}, // 9999-12-31
		})
		rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{Location: time.UTC})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if got := rows[0].JoinedAt; got.Year() != 1950 || got.Month() != time.January || got.Day() != 1 {
			t.Errorf("historical date = %v, want 1950-01-01", got)
		}
		if got := rows[1].JoinedAt; got.Year() != 9999 || got.Month() != time.December || got.Day() != 31 {
			t.Errorf("max date = %v, want 9999-12-31", got)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		data := buildWorkbook(t, "数据", [][]any{
			{"客户名称", "加入日期"},
			{"甲", 0},
			{"乙", 2958466},
			{"丙", 20260102},
			{"丁", "Inf"},
			{"戊", "1e308"},
			{"己", "NaN"},
		})
		_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		ie := asImportError(t, err)
		if len(ie.Errors) != 6 {
			t.Fatalf("got %d errors, want 6: %+v", len(ie.Errors), ie.Errors)
		}
		for row := 2; row <= 7; row++ {
			assertCellError(t, ie, row, "加入日期", "超出 Excel 日期范围")
		}
	})

	t.Run("1904 upper bound", func(t *testing.T) {
		valid := buildDate1904Workbook(t, 2957003) // 9999-12-31
		rows, err := excel.Import[importRow](bytes.NewReader(valid), excel.ImportOptions[importRow]{Location: time.UTC})
		if err != nil {
			t.Fatalf("Import valid: %v", err)
		}
		if got := rows[0].JoinedAt; got.Year() != 9999 {
			t.Errorf("1904 max date = %v, want year 9999", got)
		}

		invalid := buildDate1904Workbook(t, 2957004)
		_, err = excel.Import[importRow](bytes.NewReader(invalid), excel.ImportOptions[importRow]{})
		ie := asImportError(t, err)
		assertCellError(t, ie, 2, "加入日期", "超出 Excel 日期范围")
	})
}

func buildDate1904Workbook(t *testing.T, serial int) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: boolPtr(true)}); err != nil {
		t.Fatalf("set workbook props: %v", err)
	}
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"客户名称", "加入日期"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{"甲", serial})
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	return buf.Bytes()
}

type optionalRow struct {
	Name  string `excel:"名称"`
	Phone string `excel:"电话"`
}

func TestImportAllUnknownHeaders(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"Name", "Phone"},
		{"a", "b"},
	})
	_, err := excel.Import[optionalRow](bytes.NewReader(data), excel.ImportOptions[optionalRow]{AllowUnknownHeaders: true})
	if !errors.Is(err, excel.ErrInvalidWorkbook) {
		t.Fatalf("error = %v, want ErrInvalidWorkbook", err)
	}
}

type floatRow struct {
	Value float64 `excel:"数值"`
}

func TestImportFloatSpecialValues(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"数值"},
		{"NaN"},
		{"Inf"},
		{"-Inf"},
	})
	_, err := excel.Import[floatRow](bytes.NewReader(data), excel.ImportOptions[floatRow]{})
	ie := asImportError(t, err)
	if len(ie.Errors) != 3 {
		t.Fatalf("got %d errors, want 3: %+v", len(ie.Errors), ie.Errors)
	}
	for row := 2; row <= 4; row++ {
		assertCellError(t, ie, row, "数值", "不是有效的数字")
	}
}

func TestImportDuplicateHeader(t *testing.T) {
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "客户名称"},
		{"张三", "李四"},
	})
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	ie := asImportError(t, err)
	assertCellError(t, ie, 1, "客户名称", "表头重复")
}

func TestImportDefaultMaxErrors(t *testing.T) {
	rows := make([][]any, 0, excel.DefaultMaxErrors+2)
	rows = append(rows, []any{"客户名称", "年龄"})
	for i := 0; i <= excel.DefaultMaxErrors; i++ {
		rows = append(rows, []any{"", i})
	}
	data := buildWorkbook(t, "数据", rows)
	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	ie := asImportError(t, err)
	if len(ie.Errors) != excel.DefaultMaxErrors || ie.Total != excel.DefaultMaxErrors+1 || !ie.Truncated {
		t.Fatalf("errors = %d, total = %d, truncated = %v", len(ie.Errors), ie.Total, ie.Truncated)
	}
}

func TestImportExactlyMaxRows(t *testing.T) {
	rows := make([][]any, 0, excel.DefaultMaxRows+1)
	rows = append(rows, []any{"客户名称"})
	for i := 0; i < excel.DefaultMaxRows; i++ {
		rows = append(rows, []any{"客户"})
	}
	data := buildWorkbook(t, "数据", rows)
	got, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(got) != excel.DefaultMaxRows {
		t.Fatalf("got %d rows, want %d", len(got), excel.DefaultMaxRows)
	}
}
