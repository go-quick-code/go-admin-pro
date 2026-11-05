// Package repository 是 oa 模块的 DAL 层。
//
// 本工单（issue #43）没有 OA 业务表，本包暂无查询；跨模块取数
// （用户/部门/权限解析）按仓库约定走 system 模块网关
// （见 oa/service.SystemGateway 与 system/service/gateway.go），
// 不在 oa 侧直查 system 表。OA 实体表的查询随建表工单进入本包。
package repository
