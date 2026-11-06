// Package model 承载 oa 模块的持久化模型。
//
// OA 实体表随建表工单进入本包，并同步登记 internal/pkg/database.TenantTables
// （spec §4.6：所有 OA 表纳入租户插件）：工作日历（issue #50，00017）已落地，
// 其余 OA 表由 #48/#51/#54/#56/#58/#60 各自的迁移落地。
package model
