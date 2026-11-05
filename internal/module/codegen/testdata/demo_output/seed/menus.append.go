package seed

// 本文件由 codegen 生成（issue #10 样板）：菜单/权限码种子片段（非独立文件——把以下注释行解开追加到
// internal/seed/seed.go 的 seedMenus → menus 切片，父目录须已存在于种子；
// 带 package 声明是为了本片段自身始终是合法 Go 文件）。
// ID 来自实体元信息的 menu 配置，与生成的集成测试骨架中的菜单常量一致；
// 与既有 ID 冲突时先调整，再同步集成测试常量与角色绑定。
//
// menu(402, 400, "产品管理", "product", "crm/product/index", "crm:product:query", "Goods", 2),
// btn(4021, 402, "产品查询", "crm:product:query", 1),
// btn(4022, 402, "产品新增", "crm:product:create", 2),
// btn(4023, 402, "产品修改", "crm:product:update", 3),
// btn(4024, 402, "产品删除", "crm:product:delete", 4),
