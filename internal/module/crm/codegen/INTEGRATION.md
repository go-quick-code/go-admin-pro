# 产品（Product）脚手架装配说明

本文件由 codegen 生成（issue #10 样板），元信息：模块 crm、实体 product、表 crm_product、
权限码前缀 crm:product、路由组 /crm/product。

能力开关（生成时配置，改配置后重新生成即可）：
- owner-scoped 数据范围：关
- 业务编号（crm_sequence）：关（owner-scoped 实体默认不带编号）
- Excel 导入导出：预留接线位（生成器不产出实现，见文末）

## 产物清单

| 产物 | 说明 |
| --- | --- |
| go/internal/module/crm/model/product.go | 模型（BaseEntity + 租户列） |
| go/internal/module/crm/dto/product.go | Create / Update / Page 三个请求结构 |
| go/internal/module/crm/repository/product.go | Filter + Find / List / Create / Save / Delete |
| go/internal/module/crm/service/product.go | 业务方法（数据范围、可见性 404） |
| go/internal/module/crm/api/product.go | 5 个 HTTP handler + RegisterProductRoutes |
| go/internal/module/crm/service/product_rules*.go / *_test.go | 枚举校验纯函数 + 外部测试包骨架（有枚举字段时生成） |
| go/internal/module/crm/product_scope_integration_test.go | 集成测试骨架（//go:build integration） |
| migrations/0000X_crm_product.tmpl | 迁移模板（编号由实施工单分配） |
| web/src/api/crm.append.ts | web/src/api/crm.ts 的追加片段 |
| web/src/views/crm/product/index.vue | ProTable 列表页（列/搜索/表单全套） |
| seed/menus.append.go | 菜单/权限码种子片段（提供 menu 配置时生成） |

## 前置条件

- 目标模块（internal/module/crm）已有骨架：
  service.Service（含 SystemGateway、CurrentUserID）与
  repository.Repository（含 notFound）已存在——本包 per-entity 文件的方法挂在这些类型上。
  缺哪项就先从 crm 样板（internal/module/crm）或本包 skeleton 说明补齐。
- 迁移编号已由实施工单确定（不得使用模板名中的 0000X 占位）。

## 接线步骤（按顺序）

1. **迁移**：把 migrations/0000X_crm_product.tmpl 复制为
   migrations/<实际编号>_crm_product.sql，按需调整列/索引后纳入版本。
2. **租户插件登记**：internal/pkg/database/tenant_tables.go 的 TenantTables 追加一行：

   ```go
   "crm_product",
   ```

3. **放置后端文件**：把 go/ 目录下的文件拷入 internal/module/crm/ 对应子目录。
4. **路由注册**：在 internal/module/crm/api/router.go 的 RegisterProtected 末尾追加一行：

   ```go
   h.RegisterProductRoutes(rg)
   ```

   （ModuleScaffold 产物已含这行，无需再改。）
5. **app.go**：crm 模块已在 app.go 装配过（repo → service → api.New → router.New），
   per-entity 文件不引入新组件，**无需改动 app.go**。
6. **菜单/权限码种子**：把 seed/menus.append.go 中的 menu/btn 行追加进
   internal/seed/seed.go 的 seedMenus（父目录 4060 须已存在）；
   权限码：crm:product:query / :create / :update / :delete。
7. **前端**：
   - 把 web/src/api/crm.append.ts 的内容追加到 web/src/api/crm.ts 末尾；
   - 把 web/src/views/crm/product/ 整目录拷入 web/src/views/crm/。
8. **验证**：
   ```bash
   go build ./... && go vet ./...
   go test ./internal/module/crm/...
   GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test_tN?charset=utf8mb4&parseTime=True' \
     make test-integration   # product_scope_integration_test.go 生效
   cd web && pnpm type-check
   ```

## Excel 导入导出（预留接线位，本包不产出实现）

生成物已留位：
- service/product.go 末尾的「导入导出接线位」注释（校验策略、行数/体积上限、导出套数据范围）；
- api/product.go RegisterProductRoutes 内注释掉的三条路由
  （/import、/export、/import-template，权限码 :import / :export）。

实现时参考 internal/module/crm 的手写实现，按 internal/pkg/excel 的
schema/template/errors 能力接线，导出必须复用 ListProduct 的筛选与数据范围谓词
（导出内容与权限一致）。

## 限制与调整建议

- 生成物是起点：唯一约束、业务联动（如归属校验、状态机）按需补在 service 层；
- 枚举 Valid 按连续区间判断，非连续枚举改成查表；
- 字典字段前端选项走 `useDictOptions`（web/src/composables/useDict.ts）：字典类型与数据
  需在系统字典中维护，缺失时下拉为空、列表回退显示原值；
- 重新生成会覆盖同名产物文件，本地手工修改请先迁移到 git 管理或自行合并。
