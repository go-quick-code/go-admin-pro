// Package model 承载 oa 模块的持久化模型。
//
// 本工单（issue #43）不新增业务表、不加迁移（docs/oa-mvp-spec.md §3 的
// OA 表由 #48/#50/#51/#54/#56/#58/#60 各自的迁移落地）；实体模型随建表
// 工单进入本包，并同步登记 internal/pkg/database.TenantTables（spec §4.6：
// 所有 OA 表纳入租户插件）。
package model
