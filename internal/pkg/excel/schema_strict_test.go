package excel_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// 带 excel 标签的未导出字段会让映射失效，必须在构建 schema 时立即报错，
// 而不是静默跳过整列。三类类型分别验证 Columns、Import、Export 入口。

type columnsUnexportedTaggedRow struct {
	Name string `excel:"名称"`
	note string `excel:"备注"`
}

type importUnexportedTaggedRow struct {
	Name string `excel:"名称"`
	note string `excel:"备注"`
}

type exportUnexportedTaggedRow struct {
	Name string `excel:"名称"`
	note string `excel:"备注"`
}

func TestSchemaUnexportedTaggedFieldFails(t *testing.T) {
	cases := []struct {
		entry string
		call  func() error
	}{
		{"Columns", func() error {
			_, err := excel.Columns[columnsUnexportedTaggedRow]()
			return err
		}},
		{"Import", func() error {
			_, err := excel.Import[importUnexportedTaggedRow](nil, excel.ImportOptions[importUnexportedTaggedRow]{})
			return err
		}},
		{"Export", func() error {
			_, err := excel.Export[exportUnexportedTaggedRow](nil, excel.ExportOptions{})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.entry, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("expected error for unexported field with excel tag")
			}
			if !errors.Is(err, excel.ErrInvalidSchema) {
				t.Fatalf("error = %v, want errors.Is ErrInvalidSchema", err)
			}
			if !strings.Contains(err.Error(), "note") {
				t.Errorf("error = %q, want it to name field note", err)
			}
			if !strings.Contains(err.Error(), "请改为导出字段或移除 excel 标签") {
				t.Errorf("error = %q, want expected form hint", err)
			}
		})
	}
}

// 没有标签的未导出字段与 excel:"-" 字段继续被忽略，不影响其余映射。

type untaggedUnexportedRow struct {
	Name     string `excel:"名称"`
	internal string
	secret   string `excel:"-"`
}

func TestSchemaUnexportedFieldsIgnored(t *testing.T) {
	cols, err := excel.Columns[untaggedUnexportedRow]()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(cols) != 1 || cols[0].Field != "Name" || cols[0].Header != "名称" {
		t.Fatalf("columns = %+v, want only exported Name", cols)
	}
	if _, err := excel.Export([]untaggedUnexportedRow{{Name: "张三"}}, excel.ExportOptions{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
}

// 未导出匿名嵌入结构体的导出字段仍被提升并参与映射。

type strictEmbeddedBase struct {
	Owner string `excel:"负责人"`
}

type strictEmbeddedRow struct {
	strictEmbeddedBase
	Name     string `excel:"名称"`
	internal string
}

func TestSchemaEmbeddedExpansionUnchanged(t *testing.T) {
	cols, err := excel.Columns[strictEmbeddedRow]()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(cols) != 2 || cols[0].Field != "Owner" || cols[1].Field != "Name" {
		t.Fatalf("columns = %+v, want promoted Owner then Name", cols)
	}
}

// 匿名字段带非 "-" 标签时按普通带标签字段处理：未导出即报错。

type strictTaggedEmbeddedRow struct {
	strictEmbeddedBase `excel:"负责人"`
	Name               string `excel:"名称"`
}

func TestSchemaTaggedAnonymousUnexportedFieldFails(t *testing.T) {
	_, err := excel.Columns[strictTaggedEmbeddedRow]()
	if err == nil {
		t.Fatal("expected error for tagged unexported anonymous field")
	}
	if !errors.Is(err, excel.ErrInvalidSchema) {
		t.Fatalf("error = %v, want errors.Is ErrInvalidSchema", err)
	}
	if !strings.Contains(err.Error(), "strictEmbeddedBase") {
		t.Errorf("error = %q, want it to name field strictEmbeddedBase", err)
	}
}
