package excel_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

func TestTemplate(t *testing.T) {
	data, err := excel.Template[sampleRow](excel.TemplateOptions{
		Notes: map[string]string{"Name": "必填，填写客户全称"},
	})
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open template: %v", err)
	}
	defer f.Close()

	header, err := f.GetRows("数据")
	if err != nil {
		t.Fatalf("get data sheet: %v", err)
	}
	if len(header) != 1 {
		t.Fatalf("template should only contain header row, got %v", header)
	}
	if strings.Join(header[0], "|") != "客户名称|年龄|金额|启用|加入日期|备注" {
		t.Errorf("header = %v", header[0])
	}

	notes, err := f.GetRows("填写说明")
	if err != nil {
		t.Fatalf("get notes sheet: %v", err)
	}
	if len(notes) != 7 {
		t.Fatalf("got %d note rows, want 7: %v", len(notes), notes)
	}
	if strings.Join(notes[0], "|") != "列名|是否必填|填写说明" {
		t.Errorf("notes header = %v", notes[0])
	}
	if notes[1][0] != "客户名称" || notes[1][1] != "是" || notes[1][2] != "必填，填写客户全称" {
		t.Errorf("first note = %v", notes[1])
	}
	if notes[2][0] != "年龄" || notes[2][1] != "否" {
		t.Errorf("second note = %v", notes[2])
	}

	t.Run("round trip empty", func(t *testing.T) {
		rows, err := excel.Import[importRow](bytes.NewReader(data), excel.ImportOptions[importRow]{})
		if err != nil {
			t.Fatalf("Import template: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("expected no rows, got %+v", rows)
		}
	})

	t.Run("unknown note column", func(t *testing.T) {
		_, err := excel.Template[sampleRow](excel.TemplateOptions{
			Notes: map[string]string{"不存在": "x"},
		})
		if !errors.Is(err, excel.ErrColumnNotFound) {
			t.Fatalf("error = %v, want ErrColumnNotFound", err)
		}
	})

	t.Run("alias conflict", func(t *testing.T) {
		_, err := excel.Template[sampleRow](excel.TemplateOptions{
			Notes: map[string]string{"Name": "a", "客户名称": "b"},
		})
		if !errors.Is(err, excel.ErrDuplicateColumn) {
			t.Fatalf("error = %v, want ErrDuplicateColumn", err)
		}
	})
}
