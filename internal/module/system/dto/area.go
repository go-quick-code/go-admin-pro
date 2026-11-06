package dto

// ---------- 地区（issue #82，spec §3.9/§4.8/§5.7）----------

// AreaImportItem 是 POST /system/area/import 的单个条目（JSON 数组逐项）。
// Status 缺省视为启用（0）；Sort 缺省 0。
type AreaImportItem struct {
	Code       string `json:"code"`
	ParentCode string `json:"parentCode"`
	Name       string `json:"name"`
	Level      int8   `json:"level"`
	Sort       int    `json:"sort"`
	Status     *int8  `json:"status"`
}

// AreaNode 是树接口的节点（嵌套 children，叶子为空数组）。
type AreaNode struct {
	ID         int64       `json:"id"`
	Code       string      `json:"code"`
	ParentCode string      `json:"parentCode"`
	Name       string      `json:"name"`
	Level      int8        `json:"level"`
	Sort       int         `json:"sort"`
	Status     int8        `json:"status"`
	Children   []*AreaNode `json:"children"`
}
