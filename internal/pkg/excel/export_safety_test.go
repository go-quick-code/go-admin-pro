package excel_test

import (
	"math"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
)

type floatSafetyRow struct {
	Name   string   `excel:"名称"`
	Score  float64  `excel:"评分"`
	Ratio  float32  `excel:"比率"`
	Nested *float64 `excel:"嵌套值"`
}

func TestExportRejectsNonFiniteFloats(t *testing.T) {
	cases := []struct {
		name    string
		row     floatSafetyRow
		wantCol string
	}{
		{"float64 NaN", floatSafetyRow{Score: math.NaN()}, "评分"},
		{"float64 +Inf", floatSafetyRow{Score: math.Inf(1)}, "评分"},
		{"float64 -Inf", floatSafetyRow{Score: math.Inf(-1)}, "评分"},
		{"float32 NaN", floatSafetyRow{Ratio: float32(math.NaN())}, "比率"},
		{"float32 +Inf", floatSafetyRow{Ratio: float32(math.Inf(1))}, "比率"},
		{"float32 -Inf", floatSafetyRow{Ratio: float32(math.Inf(-1))}, "比率"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excel.Export([]floatSafetyRow{tc.row}, excel.ExportOptions{})
			if err == nil {
				t.Fatal("Export succeeded, want non-finite float rejection")
			}
			if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "["+tc.wantCol+"]") {
				t.Fatalf("error = %v, want row 2 and column %s", err, tc.wantCol)
			}
			if !strings.Contains(err.Error(), "需为有限数字") {
				t.Fatalf("error = %v, want expected form hint 需为有限数字", err)
			}
		})
	}
}

func TestExportRejectsNonFinitePointerFloat(t *testing.T) {
	value := math.Inf(1)
	_, err := excel.Export([]floatSafetyRow{{Name: "甲", Nested: &value}}, excel.ExportOptions{})
	if err == nil {
		t.Fatal("Export succeeded, want non-finite pointer float rejection")
	}
	if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[嵌套值]") {
		t.Fatalf("error = %v, want row 2 and column 嵌套值", err)
	}
}

func TestExportRejectsNonFiniteFormatterResult(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"float64 NaN", math.NaN()},
		{"float64 +Inf", math.Inf(1)},
		{"float64 -Inf", math.Inf(-1)},
		{"float32 NaN", float32(math.NaN())},
		{"float32 +Inf", float32(math.Inf(1))},
		{"float32 -Inf", float32(math.Inf(-1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excel.Export([]floatSafetyRow{{Name: "甲"}}, excel.ExportOptions{
				Formatters: map[string]excel.Formatter{
					"评分": func(any) (any, error) { return tc.value, nil },
				},
			})
			if err == nil {
				t.Fatal("Export succeeded, want non-finite formatter rejection")
			}
			if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[评分]") {
				t.Fatalf("error = %v, want row 2 and column 评分", err)
			}
		})
	}
}

type longText struct {
	text string
}

func (t longText) MarshalText() ([]byte, error) {
	return []byte(t.text), nil
}

func (t *longText) UnmarshalText(text []byte) error {
	t.text = string(text)
	return nil
}

type stringSafetyRow struct {
	Name string   `excel:"名称"`
	Note string   `excel:"备注"`
	Text longText `excel:"长文本"`
}

func TestExportStringLengthLimit(t *testing.T) {
	t.Run("32767 ASCII units succeeds", func(t *testing.T) {
		note := strings.Repeat("a", 32767)
		data, err := excel.Export([]stringSafetyRow{{Name: "甲", Note: note}}, excel.ExportOptions{})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		got := openExport(t, data, "数据")
		if len(got[1]) < 2 || got[1][1] != note {
			t.Fatalf("exported note length = %d, want %d", len(got[1][1]), len(note))
		}
	})

	t.Run("32768 ASCII units fails", func(t *testing.T) {
		_, err := excel.Export([]stringSafetyRow{{Name: "甲", Note: strings.Repeat("a", 32768)}}, excel.ExportOptions{})
		if err == nil {
			t.Fatal("Export succeeded, want long string rejection")
		}
		if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[备注]") {
			t.Fatalf("error = %v, want row 2 and column 备注", err)
		}
	})

	t.Run("32767 UTF-16 units with emoji succeeds", func(t *testing.T) {
		note := strings.Repeat("😀", 16383) + "a" // 32767 个 UTF-16 code unit，但 65533 字节
		data, err := excel.Export([]stringSafetyRow{{Name: "甲", Note: note}}, excel.ExportOptions{})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		got := openExport(t, data, "数据")
		if got[1][1] != note {
			t.Fatalf("exported note = %q..., want original emoji string", got[1][1][:16])
		}
	})

	t.Run("32768 UTF-16 units with emoji fails", func(t *testing.T) {
		_, err := excel.Export([]stringSafetyRow{{Name: "甲", Note: strings.Repeat("😀", 16384)}}, excel.ExportOptions{})
		if err == nil {
			t.Fatal("Export succeeded, want long string rejection")
		}
		if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[备注]") {
			t.Fatalf("error = %v, want row 2 and column 备注", err)
		}
	})
}

func TestExportRejectsLongFormatterString(t *testing.T) {
	_, err := excel.Export([]stringSafetyRow{{Name: "甲"}}, excel.ExportOptions{
		Formatters: map[string]excel.Formatter{
			"名称": func(any) (any, error) { return strings.Repeat("x", 32768), nil },
		},
	})
	if err == nil {
		t.Fatal("Export succeeded, want long formatter string rejection")
	}
	if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[名称]") {
		t.Fatalf("error = %v, want row 2 and column 名称", err)
	}
}

func TestExportRejectsLongTextMarshaler(t *testing.T) {
	_, err := excel.Export([]stringSafetyRow{{Text: longText{text: strings.Repeat("x", 32768)}}}, excel.ExportOptions{})
	if err == nil {
		t.Fatal("Export succeeded, want long TextMarshaler rejection")
	}
	if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[长文本]") {
		t.Fatalf("error = %v, want row 2 and column 长文本", err)
	}
}

// namedNote 与 namedRate 模拟 Formatter 返回的命名类型。excelize 对未知类型走
// fmt.Sprint 分支，导出层必须在写入前完成长度与有限性校验。
type namedNote string

type namedRate float64

func TestExportRejectsLongNamedString(t *testing.T) {
	_, err := excel.Export([]stringSafetyRow{{Name: "甲"}}, excel.ExportOptions{
		Formatters: map[string]excel.Formatter{
			"名称": func(any) (any, error) { return namedNote(strings.Repeat("x", 40000)), nil },
		},
	})
	if err == nil {
		t.Fatal("Export succeeded, want long named string rejection")
	}
	if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[名称]") {
		t.Fatalf("error = %v, want row 2 and column 名称", err)
	}
	if !strings.Contains(err.Error(), "超过 Excel 单元格上限") {
		t.Fatalf("error = %v, want cell length limit message", err)
	}
}

func TestExportAcceptsShortNamedString(t *testing.T) {
	data, err := excel.Export([]stringSafetyRow{{Name: "甲"}}, excel.ExportOptions{
		Formatters: map[string]excel.Formatter{
			"名称": func(any) (any, error) { return namedNote("短文本"), nil },
		},
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := openExport(t, data, "数据")
	if got[1][0] != "短文本" {
		t.Fatalf("cell = %q, want 短文本", got[1][0])
	}
}

func TestExportRejectsNonFiniteNamedFloat(t *testing.T) {
	cases := []struct {
		name  string
		value namedRate
	}{
		{"NaN", namedRate(math.NaN())},
		{"+Inf", namedRate(math.Inf(1))},
		{"-Inf", namedRate(math.Inf(-1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excel.Export([]floatSafetyRow{{Name: "甲"}}, excel.ExportOptions{
				Formatters: map[string]excel.Formatter{
					"评分": func(any) (any, error) { return tc.value, nil },
				},
			})
			if err == nil {
				t.Fatal("Export succeeded, want non-finite named float rejection")
			}
			if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[评分]") {
				t.Fatalf("error = %v, want row 2 and column 评分", err)
			}
			if !strings.Contains(err.Error(), "非有限浮点数") {
				t.Fatalf("error = %v, want non-finite float message", err)
			}
		})
	}
}

func TestExportByteSliceLengthLimit(t *testing.T) {
	t.Run("long []byte fails", func(t *testing.T) {
		_, err := excel.Export([]stringSafetyRow{{Name: "甲"}}, excel.ExportOptions{
			Formatters: map[string]excel.Formatter{
				"名称": func(any) (any, error) { return []byte(strings.Repeat("b", 40000)), nil },
			},
		})
		if err == nil {
			t.Fatal("Export succeeded, want long []byte rejection")
		}
		if !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[名称]") {
			t.Fatalf("error = %v, want row 2 and column 名称", err)
		}
	})

	t.Run("short []byte succeeds", func(t *testing.T) {
		data, err := excel.Export([]stringSafetyRow{{Name: "甲"}}, excel.ExportOptions{
			Formatters: map[string]excel.Formatter{
				"名称": func(any) (any, error) { return []byte("字节"), nil },
			},
		})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		got := openExport(t, data, "数据")
		if got[1][0] != "字节" {
			t.Fatalf("cell = %q, want 字节", got[1][0])
		}
	})
}
