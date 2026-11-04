package excel

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// newFile 创建只含目标工作表的空工作簿。
func newFile(sheet string) (*excelize.File, error) {
	f := excelize.NewFile()
	if sheet != "" && sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			f.Close()
			return nil, fmt.Errorf("工作表名 %q 不合法：%w", sheet, err)
		}
	}
	return f, nil
}

// writeHeader 写入表头行，并设置列宽、样式与冻结首行；
// 自动筛选由 Export 在写入数据行后设置。
func writeHeader(f *excelize.File, sheet string, cols []Column) error {
	if len(cols) == 0 {
		return nil
	}
	style, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F3F5"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if err != nil {
		return err
	}
	lastCol, _ := excelize.ColumnNumberToName(len(cols))
	for i, col := range cols {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		cell := colName + "1"
		if err := f.SetCellValue(sheet, cell, col.Header); err != nil {
			return err
		}
		width := col.Width
		if width <= 0 {
			width = 18
		}
		if err := f.SetColWidth(sheet, colName, colName, width); err != nil {
			return err
		}
	}
	if err := f.SetCellStyle(sheet, "A1", lastCol+"1", style); err != nil {
		return err
	}
	if err := f.SetRowHeight(sheet, 1, 24); err != nil {
		return err
	}
	if err := f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
	}); err != nil {
		return err
	}
	return nil
}
