package excel

import (
	"github.com/xuri/excelize/v2"
)

// nativeDateNumFmt 是原生日期单元格统一使用的数字格式代码，
// 与 DefaultTimeLayout 的显示效果一致。
const nativeDateNumFmt = "yyyy-mm-dd hh:mm:ss"

// newNativeDateStyle 创建原生日期单元格共用的数字格式样式，一次导出只创建一个。
func newNativeDateStyle(f *excelize.File) (int, error) {
	numFmt := nativeDateNumFmt
	return f.NewStyle(&excelize.Style{CustomNumFmt: &numFmt})
}
