// date_parse.go 是 CRM 日期解析的共享入口（ADR 0006）。
//
// 所有无时区日期文本都按调用方传入的业务时区解释：调用方统一传
// Service.businessLocation()（app.timezone 的唯一来源是 config.Location()），
// 各字段解析函数不再直接使用 time.Local，避免解析口径绕过应用时区配置。
package service

import "time"

// crmDateLayouts 是 CRM 日期字段接受的格式：纯日期、前端 value-format
// （YYYY-MM-DD HH:mm:ss）与 RFC3339。
var crmDateLayouts = []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339}

// parseDateIn 按 layouts 依次用 loc 解析 value。loc 为 nil 时退回 time.Local：
// 只用于未装配时区的测试/工具场景，生产路径由 Service.businessLocation 保证非 nil。
func parseDateIn(layouts []string, value string, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.Local
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
