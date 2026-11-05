// Package dto 是 bpm 模块的请求/响应 DTO（issue #44）。
package dto

import "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"

// ---------- 流程定义（issue #44，spec #42 §5 bpm 节）----------

// DefinitionCreateReq 新建流程定义草稿。同 key 再建即「发新版本」入口，
// 版本号由服务端按同 key（含软删）max+1 发放，客户端不可指定。
// key/businessType 落库后不可变；节点结构与引用合法性由 service 校验
// （中文错误文案指明节点序号，不用 binding 原文）。
type DefinitionCreateReq struct {
	Key          string       `json:"key" binding:"required"`
	Name         string       `json:"name" binding:"required"`
	BusinessType string       `json:"businessType" binding:"required"`
	Nodes        []model.Node `json:"nodes"`
	Remark       string       `json:"remark"`
}

// DefinitionUpdateReq 编辑流程定义草稿（仅草稿）。key 与 businessType 不可变，
// 请求不携带；发布/停用后的编辑在 service 层以 409 拒绝。
type DefinitionUpdateReq struct {
	ID     int64        `json:"id" binding:"required"`
	Name   string       `json:"name" binding:"required"`
	Nodes  []model.Node `json:"nodes"`
	Remark string       `json:"remark"`
}

// DefinitionPageReq 定义分页筛选：key/name 模糊、businessType/status 精确。
// 按 key 查询（不筛状态）即版本历史。
type DefinitionPageReq struct {
	PageNo       int    `form:"pageNo"`
	PageSize     int    `form:"pageSize"`
	Key          string `form:"key"`
	Name         string `form:"name"`
	BusinessType string `form:"businessType"`
	Status       *int8  `form:"status"`
}
