// Package codegen 提供代码生成器的两个入口：
//
//  1. 元信息驱动（GenerateEntity / cmd/codegen CLI）：给定实体元信息
//     （EntitySpec），生成与 crm 手写样板同形态的「owner-scoped 实体全栈脚手架」
//     ——model/dto/repository/service/api 五层、路由注册点、菜单/权限码种子、
//     纯函数与集成测试骨架、ProTable 页面、迁移模板与装配说明（INTEGRATION.md）。
//     纯函数：不读数据库与文件系统，同 spec 同产物，产物可编译由测试保证。
//
//  2. 表结构驱动（Generator，读 information_schema）：按已有表结构生成通用
//     CRUD 模板 ZIP。产出是起点代码，不含数据范围与租户登记。
//
// 两个入口都不做工程内装配：app.go / router.go / tenant_tables.go / seed.go
// 的接线动作以产物内说明（patch 说明）给出，由实施人执行。
package codegen
