package excel_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
)

// maxErrorMessageRunes 是单条单元格错误消息的合理上限。
// 错误消息由固定文案（约 20–30 个 rune）加最多 32 个 rune 的回显值组成，
// 留出余量后 120 个 rune 足以覆盖所有错误类别。
const maxErrorMessageRunes = 120

// echoLimitRunes 是错误消息中回显单元格值的截断长度（与实现约定一致）。
const echoLimitRunes = 32

// TestImportLongValueErrorMessageSize 验证超长单元格内容不会原样进入错误消息。
//
// 100 行、每行 20000 个字符的错误值在未截断时会产生约 2MB 的 JSON；
// 截断到约 32 个 rune 后单条约 100 字节，整体应稳定在 64KB 以内。
func TestImportLongValueErrorMessageSize(t *testing.T) {
	const (
		rowCount = 100
		// Excel 单元格上限是 32767 个字符；20000 已足够暴露体积问题。
		valueLen = 20000
		jsonMax  = 64 << 10
	)
	long := strings.Repeat("x", valueLen)

	rows := make([][]any, 0, rowCount+1)
	rows = append(rows, []any{"客户名称", "年龄"})
	for i := 0; i < rowCount; i++ {
		rows = append(rows, []any{fmt.Sprintf("客户%d", i+1), long})
	}

	_, err := excel.Import[importRow](bytes.NewReader(buildWorkbook(t, "数据", rows)), excel.ImportOptions[importRow]{})
	ie := asImportError(t, err)
	if len(ie.Errors) != rowCount || ie.Total != rowCount || ie.Truncated {
		t.Fatalf("errors = %d, total = %d, truncated = %v; want %d, %d, false",
			len(ie.Errors), ie.Total, ie.Truncated, rowCount, rowCount)
	}

	for i, item := range ie.Errors {
		if wantRow := i + 2; item.Row != wantRow {
			t.Fatalf("errors[%d].Row = %d, want %d", i, item.Row, wantRow)
		}
		if item.Column != "年龄" {
			t.Fatalf("errors[%d].Column = %q, want 年龄", i, item.Column)
		}
		if n := utf8.RuneCountInString(item.Msg); n > maxErrorMessageRunes {
			t.Fatalf("errors[%d].Msg uses %d runes, want <= %d: %.120q", i, n, maxErrorMessageRunes, item.Msg)
		}
		if !strings.Contains(item.Msg, "…") {
			t.Fatalf("errors[%d].Msg = %q, want ellipsis after truncation", i, item.Msg)
		}
		if !strings.Contains(item.Msg, "不是有效的整数") {
			t.Fatalf("errors[%d].Msg = %q, want surrounding text kept", i, item.Msg)
		}
		// 截断后的值最多 32 个 x；连续 64 个 x 说明原始长值泄漏进了消息。
		if strings.Contains(item.Msg, strings.Repeat("x", 64)) {
			t.Fatalf("errors[%d].Msg still contains raw cell text", i)
		}
	}

	raw, err := json.Marshal(ie)
	if err != nil {
		t.Fatalf("marshal ImportError: %v", err)
	}
	if len(raw) >= jsonMax {
		t.Fatalf("ImportError JSON = %d bytes, want < %d", len(raw), jsonMax)
	}
}

// sizeCode 只接受固定前缀，用于覆盖 TextUnmarshaler 的回显路径。
type sizeCode string

func (c *sizeCode) UnmarshalText(text []byte) error {
	if strings.HasPrefix(string(text), "CODE-") {
		*c = sizeCode(text)
		return nil
	}
	return fmt.Errorf("编码必须以 CODE- 开头")
}

type longValueTypesRow struct {
	Count uint      `excel:"数量"`
	Rate  float64   `excel:"比率"`
	Flag  bool      `excel:"开关"`
	Date  time.Time `excel:"日期"`
	Code  sizeCode  `excel:"编码"`
}

// TestImportLongValueTruncatedAcrossFieldTypes 验证所有字段类型的错误回显都会截断，
// 而不只是整数路径。
func TestImportLongValueTruncatedAcrossFieldTypes(t *testing.T) {
	long := strings.Repeat("z", 20000)
	data := buildWorkbook(t, "数据", [][]any{
		{"数量", "比率", "开关", "日期", "编码"},
		{long, long, long, long, long},
	})

	_, err := excel.Import[longValueTypesRow](bytes.NewReader(data), excel.ImportOptions[longValueTypesRow]{})
	ie := asImportError(t, err)
	wantColumns := []string{"数量", "比率", "开关", "日期", "编码"}
	if len(ie.Errors) != len(wantColumns) || ie.Total != len(wantColumns) || ie.Truncated {
		t.Fatalf("errors = %d, total = %d, truncated = %v; want %d, %d, false",
			len(ie.Errors), ie.Total, ie.Truncated, len(wantColumns), len(wantColumns))
	}

	for i, item := range ie.Errors {
		if item.Row != 2 || item.Column != wantColumns[i] {
			t.Fatalf("errors[%d] = row %d column %q, want row 2 column %q", i, item.Row, item.Column, wantColumns[i])
		}
		if n := utf8.RuneCountInString(item.Msg); n > maxErrorMessageRunes {
			t.Fatalf("errors[%d].Msg uses %d runes, want <= %d: %.120q", i, n, maxErrorMessageRunes, item.Msg)
		}
		if !strings.Contains(item.Msg, "…") {
			t.Fatalf("errors[%d].Msg = %q, want ellipsis after truncation", i, item.Msg)
		}
		if strings.Contains(item.Msg, strings.Repeat("z", 64)) {
			t.Fatalf("errors[%d].Msg still contains raw cell text", i)
		}
	}
}

// TestImportShortValueErrorMessageNotTruncated 验证短值原样回显，不追加省略号。
func TestImportShortValueErrorMessageNotTruncated(t *testing.T) {
	const short = "abc"
	data := buildWorkbook(t, "数据", [][]any{
		{"客户名称", "年龄"},
		{"张三", short},
	})

	_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
	ie := asImportError(t, err)
	if len(ie.Errors) != 1 || ie.Total != 1 || ie.Truncated {
		t.Fatalf("errors = %d, total = %d, truncated = %v; want 1, 1, false",
			len(ie.Errors), ie.Total, ie.Truncated)
	}
	msg := ie.Errors[0].Msg
	if !strings.Contains(msg, `"`+short+`"`) {
		t.Fatalf("Msg = %q, want short value echoed unchanged", msg)
	}
	if strings.Contains(msg, "…") {
		t.Fatalf("Msg = %q, want no ellipsis for short value", msg)
	}
}

// TestImportErrorMessageTruncationBoundary 验证 32 个 rune 以内不截断，
// 超出时按 rune（而非字节）截断，避免把多字节字符切成乱码。
func TestImportErrorMessageTruncationBoundary(t *testing.T) {
	asciiAtLimit := strings.Repeat("a", echoLimitRunes)
	chinese := strings.Repeat("错", 40)
	chinesePrefix := string([]rune(chinese)[:echoLimitRunes])

	tests := []struct {
		name         string
		value        string
		wantEllipsis bool
		wantEcho     string
	}{
		{name: "exactly 32 runes", value: asciiAtLimit, wantEcho: `"` + asciiAtLimit + `"`},
		{name: "33 runes", value: asciiAtLimit + "a", wantEllipsis: true, wantEcho: `"` + asciiAtLimit + `…"`},
		{name: "multibyte runes", value: chinese, wantEllipsis: true, wantEcho: `"` + chinesePrefix + `…"`},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := buildWorkbook(t, "数据", [][]any{
				{"客户名称", "年龄"},
				{"张三", tc.value},
			})
			_, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
			ie := asImportError(t, err)
			if len(ie.Errors) != 1 {
				t.Fatalf("errors = %d, want 1", len(ie.Errors))
			}
			item := ie.Errors[0]
			if item.Row != 2 || item.Column != "年龄" {
				t.Fatalf("row = %d, column = %q; want row 2, column 年龄", item.Row, item.Column)
			}
			if got := strings.Contains(item.Msg, "…"); got != tc.wantEllipsis {
				t.Fatalf("Msg = %q, ellipsis = %v; want %v", item.Msg, got, tc.wantEllipsis)
			}
			if !strings.Contains(item.Msg, tc.wantEcho) {
				t.Fatalf("Msg = %q, want echo %q", item.Msg, tc.wantEcho)
			}
			if !utf8.ValidString(item.Msg) {
				t.Fatalf("Msg = %q is not valid UTF-8 (truncated mid-rune?)", item.Msg)
			}
			if n := utf8.RuneCountInString(item.Msg); n > maxErrorMessageRunes {
				t.Fatalf("Msg uses %d runes, want <= %d", n, maxErrorMessageRunes)
			}
			if i == 0 && strings.Contains(item.Msg, strings.Repeat("a", echoLimitRunes+1)) {
				t.Fatalf("32-rune value should not be shortened: %q", item.Msg)
			}
		})
	}
}
