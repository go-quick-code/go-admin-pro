package excel_test

import (
	"bytes"
	"strconv"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

// openWorkbook 打开导出的工作簿供逐单元格断言。
func openWorkbook(t *testing.T, data []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// rawCellValue 读取单元格的原始值（不套用数字格式）。
func rawCellValue(t *testing.T, f *excelize.File, sheet, cell string) string {
	t.Helper()
	value, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("raw value of %s: %v", cell, err)
	}
	return value
}

// shanghaiZone 返回 Asia/Shanghai；无 tzdata 的环境退化为等效的固定偏移时区。
func shanghaiZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return loc
}

// excelSerial 把 time.Time 换算成 1900 日期系统的 Excel 序列号。
// 序列号按 time 所在时区的墙上时间写入；1900-02-29 伪闰日之后整体 +1
// （Excel 兼容 Lotus 1-2-3 的历史 bug），因此基准是 1899-12-31 00:00。
func excelSerial(wall time.Time) float64 {
	utcWall := time.Date(wall.Year(), wall.Month(), wall.Day(),
		wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), time.UTC)
	epoch := time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	const secondsPerDay = 24 * 60 * 60
	return utcWall.Sub(epoch).Seconds()/secondsPerDay + 1
}

type nativeDateRow struct {
	Name     string     `excel:"客户名称"`
	JoinedAt time.Time  `excel:"加入日期"`
	Deadline *time.Time `excel:"截止时间"`
}

func TestExportNativeDates(t *testing.T) {
	loc := shanghaiZone(t)
	joined := time.Date(2026, 1, 2, 8, 0, 5, 0, loc)
	deadline := time.Date(2026, 6, 30, 23, 59, 59, 0, loc)
	// 锚定序列号基准：46024 是 2026-01-02 的 Excel 序列号（与 import_test 同源）。
	if got := excelSerial(time.Date(2026, 1, 2, 0, 0, 0, 0, loc)); got != 46024 {
		t.Fatalf("excelSerial(2026-01-02) = %v, want 46024", got)
	}

	rows := []nativeDateRow{
		{Name: "张三", JoinedAt: joined, Deadline: &deadline},
		{Name: "李四"}, // 零值 time.Time 与 nil *time.Time 都应输出空单元格
	}
	data, err := excel.Export(rows, excel.ExportOptions{NativeDates: true})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	f := openWorkbook(t, data)

	// 非日期列不受影响，仍是文本。
	if got := rawCellValue(t, f, excel.DefaultSheetName, "A2"); got != "张三" {
		t.Errorf("raw A2 = %q, want 张三", got)
	}

	const serialEpsilon = 1e-9
	// 加入日期：原始值是序列号（按 Asia/Shanghai 墙上时间，08:00:05 不因时区偏移）。
	if got, want := parseSerial(t, rawCellValue(t, f, excel.DefaultSheetName, "B2")), excelSerial(joined); absDiff(got, want) > serialEpsilon {
		t.Errorf("raw serial B2 = %v, want %v", got, want)
	}
	assertDateCell(t, f, excel.DefaultSheetName, 2, 2, "2026-01-02 08:00:05")
	// 截止时间：指针字段同样写成原生日期。
	if got, want := parseSerial(t, rawCellValue(t, f, excel.DefaultSheetName, "C2")), excelSerial(deadline); absDiff(got, want) > serialEpsilon {
		t.Errorf("raw serial C2 = %v, want %v", got, want)
	}
	assertDateCell(t, f, excel.DefaultSheetName, 3, 2, "2026-06-30 23:59:59")

	// 第二行：零值 time.Time 与 nil 指针仍是空单元格，且不套日期格式。
	for _, cell := range []string{"B3", "C3"} {
		if got := rawCellValue(t, f, excel.DefaultSheetName, cell); got != "" {
			t.Errorf("raw %s = %q, want empty", cell, got)
		}
	}
}

// assertDateCell 断言单元格是数字类型的原生日期，数字格式为
// yyyy-mm-dd hh:mm:ss，且显示文本与期望一致。
func assertDateCell(t *testing.T, f *excelize.File, sheet string, col, row int, display string) {
	t.Helper()
	cell, _ := excelize.CoordinatesToCellName(col, row)
	// 数值单元格在 xlsx 中没有 t 属性，excelize 返回 CellTypeUnset；
	// 这里断言不是文本等类型，即按数字存储而非文本。
	cellType, err := f.GetCellType(sheet, cell)
	if err != nil {
		t.Fatalf("cell type of %s: %v", cell, err)
	}
	switch cellType {
	case excelize.CellTypeSharedString, excelize.CellTypeInlineString, excelize.CellTypeFormula,
		excelize.CellTypeBool, excelize.CellTypeDate, excelize.CellTypeError:
		t.Errorf("cell type of %s = %v, want numeric (unset)", cell, cellType)
	}
	styleID, err := f.GetCellStyle(sheet, cell)
	if err != nil {
		t.Fatalf("cell style of %s: %v", cell, err)
	}
	style, err := f.GetStyle(styleID)
	if err != nil {
		t.Fatalf("style %d of %s: %v", styleID, cell, err)
	}
	const wantNumFmt = "yyyy-mm-dd hh:mm:ss"
	if style.CustomNumFmt == nil || *style.CustomNumFmt != wantNumFmt {
		t.Errorf("num fmt of %s = %v, want %q", cell, style.CustomNumFmt, wantNumFmt)
	}
	// 显示文本：Excel 用户看到的值。
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if got := rows[row-1][col-1]; got != display {
		t.Errorf("display %s = %q, want %q", cell, got, display)
	}
}

func TestExportNativeDatesWallClockRoundTrip(t *testing.T) {
	loc := shanghaiZone(t)
	rows := []nativeDateRow{
		{Name: "张三", JoinedAt: time.Date(2026, 1, 2, 8, 0, 5, 0, loc), Deadline: nil},
	}
	data, err := excel.Export(rows, excel.ExportOptions{NativeDates: true})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	// 按同时区回读：墙上时间仍是 08:00:05，不因 UTC/服务器时区偏移。
	got, err := excel.Import[nativeDateRow](bytes.NewReader(data), excel.ImportOptions[nativeDateRow]{Location: loc})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	back := got[0].JoinedAt
	if !back.Equal(rows[0].JoinedAt) {
		t.Errorf("round trip = %v, want %v", back, rows[0].JoinedAt)
	}
	if hour, min, sec := back.In(loc).Clock(); hour != 8 || min != 0 || sec != 5 {
		t.Errorf("wall clock in Asia/Shanghai = %02d:%02d:%02d, want 08:00:05", hour, min, sec)
	}
}

// 决定：Formatter 返回 time.Time 与 time.Time 字段一致处理——开启 NativeDates
// 时写成原生日期单元格（墙上时间按 Formatter 返回值所在时区保持），默认模式
// 仍按 DefaultTimeLayout 输出文本。
func TestExportFormatterReturnsTime(t *testing.T) {
	loc := shanghaiZone(t)
	rows := []sampleRow{{Name: "张三", JoinedAt: time.Date(2026, 1, 2, 8, 0, 5, 0, loc)}}
	// Formatter 返回一个与字段值不同的 time.Time，证明写入的是它的返回值。
	formatted := time.Date(2026, 3, 4, 9, 30, 0, 0, loc)
	formatters := map[string]excel.Formatter{
		"加入日期": func(any) (any, error) { return formatted, nil },
	}

	t.Run("native dates", func(t *testing.T) {
		data, err := excel.Export(rows, excel.ExportOptions{NativeDates: true, Formatters: formatters})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		f := openWorkbook(t, data)
		if got, want := parseSerial(t, rawCellValue(t, f, excel.DefaultSheetName, "E2")), excelSerial(formatted); absDiff(got, want) > 1e-9 {
			t.Errorf("raw serial E2 = %v, want %v", got, want)
		}
		assertDateCell(t, f, excel.DefaultSheetName, 5, 2, "2026-03-04 09:30:00")
	})

	t.Run("default text", func(t *testing.T) {
		data, err := excel.Export(rows, excel.ExportOptions{Formatters: formatters})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		f := openWorkbook(t, data)
		const want = "2026-03-04 09:30:00"
		if got := rawCellValue(t, f, excel.DefaultSheetName, "E2"); got != want {
			t.Errorf("raw E2 = %q, want text %q", got, want)
		}
	})
}

func parseSerial(t *testing.T, raw string) float64 {
	t.Helper()
	serial, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("serial %q is not a number", raw)
	}
	return serial
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestExportDefaultKeepsTimeText(t *testing.T) {
	rows := []sampleRow{{Name: "张三", JoinedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}}
	data, err := excel.Export(rows, excel.ExportOptions{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	f := openWorkbook(t, data)
	// 默认导出 time.Time 仍是文本：原始值即格式化文本，不是 Excel 序列号。
	const want = "2026-01-02 03:04:05"
	if got := rawCellValue(t, f, excel.DefaultSheetName, "E2"); got != want {
		t.Errorf("raw E2 = %q, want text %q", got, want)
	}
	cellType, err := f.GetCellType(excel.DefaultSheetName, "E2")
	if err != nil {
		t.Fatalf("cell type of E2: %v", err)
	}
	if cellType == excelize.CellTypeNumber || cellType == excelize.CellTypeDate {
		t.Errorf("E2 should stay text in default mode, got %v", cellType)
	}
}
