package excel_test

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

type exampleCustomer struct {
	Name  string `excel:"客户名称,required"`
	Phone string `excel:"电话"`
	Level int    `excel:"级别"`
}

func ExampleImport() {
	f := excelize.NewFile()
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"客户名称", "电话", "级别"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{"张三", "13800000000", 1})
	buf, err := f.WriteToBuffer()
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = f.Close()

	rows, err := excel.Import[exampleCustomer](bytes.NewReader(buf.Bytes()), excel.ImportOptions[exampleCustomer]{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, row := range rows {
		fmt.Println(row.Name, row.Phone, row.Level)
	}
	// Output: 张三 13800000000 1
}

func ExampleExport() {
	data, err := excel.Export([]exampleCustomer{
		{Name: "张三", Phone: "13800000000", Level: 1},
	}, excel.ExportOptions{
		Columns: []string{"Name", "Phone"},
		Formatters: map[string]excel.Formatter{
			"Phone": func(value any) (any, error) { return "已脱敏", nil },
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	rows, err := f.GetRows("数据")
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, row := range rows {
		fmt.Println(strings.Join(row, " | "))
	}
	// Output:
	// 客户名称 | 电话
	// 张三 | 已脱敏
}

func ExampleTemplate() {
	data, err := excel.Template[exampleCustomer](excel.TemplateOptions{
		Notes: map[string]string{"Name": "填写客户全称"},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	rows, err := f.GetRows("填写说明")
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, row := range rows[:2] {
		fmt.Println(strings.Join(row, " | "))
	}
	// Output:
	// 列名 | 是否必填 | 填写说明
	// 客户名称 | 是 | 填写客户全称
}
