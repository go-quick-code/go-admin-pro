package codegen

// 本文件是生成包内的 INTEGRATION.md（装配说明）模板。
// app.go / router.go / tenant_tables.go / seed.go 不自动改写，
// 全部接线动作以 patch 说明形式写在这里，由实施人执行。

const tmplEntityIntegration = `# [[ .Comment ]]（[[ .ClassName ]]）脚手架装配说明

[[ .GenMark ]]，元信息：模块 [[ .Module ]]、实体 [[ .Entity ]]、表 [[ .TableName ]]、
权限码前缀 [[ .PermPrefix ]]、路由组 /[[ .RouteGroup ]]。

能力开关（生成时配置，改配置后重新生成即可）：
- owner-scoped 数据范围：[[ if .OwnerScoped ]]开（owner_dept_id / owner_user_id 谓词 + scope.Permits 404）[[ else ]]关[[end]]
- 业务编号（crm_sequence）：[[ if .HasSequence ]]开（前缀 [[ .SeqPrefix ]]，biz_type [[ .BizType ]]）[[ else ]]关（owner-scoped 实体默认不带编号）[[end]]
- Excel 导入导出：[[ if .ImportExport ]]预留接线位（生成器不产出实现，见文末）[[ else ]]关[[end]]

## 产物清单

| 产物 | 说明 |
| --- | --- |
| go/internal/module/[[ .Module ]]/model/[[ .Entity ]].go | 模型（BaseEntity + 租户列[[ if .OwnerScoped ]] + owner 列[[end]][[ if .HasSequence ]] + [[ .NoGoName ]][[end]]） |
| go/internal/module/[[ .Module ]]/dto/[[ .Entity ]].go | Create / Update / Page 三个请求结构 |
| go/internal/module/[[ .Module ]]/repository/[[ .Entity ]].go | Filter + Find / List / Create / Save / Delete |
| go/internal/module/[[ .Module ]]/service/[[ .Entity ]].go | 业务方法（数据范围、[[ if .OwnerScoped ]]负责人快照与名称补齐、[[end]][[ if .HasSequence ]]编号发放、[[end]]可见性 404） |
| go/internal/module/[[ .Module ]]/api/[[ .Entity ]].go | 5 个 HTTP handler + Register[[ .ClassName ]]Routes |
| go/internal/module/[[ .Module ]]/service/[[ .Entity ]]_rules*.go / *_test.go | 枚举校验纯函数 + 外部测试包骨架（有枚举字段时生成） |
| go/internal/module/[[ .Module ]]/[[ .Entity ]]_scope_integration_test.go | 集成测试骨架（//go:build integration） |
| migrations/0000X_[[ .TableName ]].tmpl | 迁移模板（编号由实施工单分配） |
| web/src/api/[[ .Module ]].append.ts | web/src/api/[[ .Module ]].ts 的追加片段 |
| web/src/views/[[ .Module ]]/[[ .Entity ]]/index.vue | ProTable 列表页（列/搜索/表单全套） |
| seed/menus.append.go | 菜单/权限码种子片段（提供 menu 配置时生成） |

## 前置条件

- [[ if .ModuleScaffold ]]本包已生成模块骨架（service/service.go、api/router.go 等），无需手写。
  装配 app.go 时按下文「新模块装配」注入。[[ else ]]目标模块（internal/module/[[ .Module ]]）已有骨架：
  service.Service（含 SystemGateway、CurrentUserID[[ if .HasSequence ]]、currentTenantID、DateKey[[end]]）与
  repository.Repository（含 notFound）已存在——本包 per-entity 文件的方法挂在这些类型上。
  缺哪项就先从 crm 样板（internal/module/crm）或本包 skeleton 说明补齐。[[end]]
- 迁移编号已由实施工单确定（不得使用模板名中的 0000X 占位）。

## 接线步骤（按顺序）

1. **迁移**：把 migrations/0000X_[[ .TableName ]].tmpl 复制为
   migrations/<实际编号>_[[ .TableName ]].sql，按需调整列/索引后纳入版本。
2. **租户插件登记**：internal/pkg/database/tenant_tables.go 的 TenantTables 追加一行：

   ~~~go
   "[[ .TableName ]]",
   ~~~

3. **放置后端文件**：把 go/ 目录下的文件拷入 internal/module/[[ .Module ]]/ 对应子目录。
4. **路由注册**：在 internal/module/[[ .Module ]]/api/router.go 的 RegisterProtected 末尾追加一行：

   ~~~go
   h.Register[[ .ClassName ]]Routes(rg)
   ~~~

   （ModuleScaffold 产物已含这行，无需再改。）[[ if not .ModuleScaffold ]]
5. **app.go**：[[ .Module ]] 模块已在 app.go 装配过（repo → service → api.New → router.New），
   per-entity 文件不引入新组件，**无需改动 app.go**。[[ else ]]
5. **app.go（新模块装配）**：在 internal/app/app.go 中按 crm 同款方式装配：

   ~~~go
   [[ .Module ]]Repo := [[ .Module ]]repo.New(db)
   [[ .Module ]]Svc := [[ .Module ]]service.New([[ .Module ]]Repo, sysSvc)
   [[ .Module ]]API := [[ .Module ]]api.New([[ .Module ]]Svc, sysAPI.RequirePermission)
   ~~~

   并把 [[ .Module ]]API 传入 router.New、在 router.New 中注册 RegisterProtected。[[end]][[ if .Menu ]]
6. **菜单/权限码种子**：把 seed/menus.append.go 中的 menu/btn 行追加进
   internal/seed/seed.go 的 seedMenus（父目录 [[ .Menu.ParentID ]] 须已存在）；
   权限码：[[ .PermPrefix ]]:query / :create / :update / :delete。[[ else ]]
6. **菜单/权限码**：本包未带菜单配置，请手工在「系统管理 → 菜单管理」或 seed 中补：
   权限码 [[ .PermPrefix ]]:query / :create / :update / :delete。[[end]]
7. **前端**：
   - 把 web/src/api/[[ .Module ]].append.ts 的内容追加到 web/src/api/[[ .Module ]].ts 末尾；
   - 把 web/src/views/[[ .Module ]]/[[ .Entity ]]/ 整目录拷入 web/src/views/[[ .Module ]]/。
8. **验证**：
   ~~~bash
   go build ./... && go vet ./...
   go test ./internal/module/[[ .Module ]]/...
   GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test_tN?charset=utf8mb4&parseTime=True' \
     make test-integration   # [[ .Entity ]]_scope_integration_test.go 生效
   cd web && pnpm type-check
   ~~~

## Excel 导入导出（预留接线位，本包不产出实现）

[[ if .ImportExport ]]生成物已留位：
- service/[[ .Entity ]].go 末尾的「导入导出接线位」注释（校验策略、行数/体积上限、导出套数据范围）；
- api/[[ .Entity ]].go Register[[ .ClassName ]]Routes 内注释掉的三条路由
  （/import、/export、/import-template，权限码 :import / :export）。

实现时参考 internal/module/crm 的手写实现，按 internal/pkg/excel 的
schema/template/errors 能力接线，导出必须复用 List[[ .ClassName ]] 的筛选与数据范围谓词
（导出内容与权限一致）。[[ else ]]本实体生成时未开启导入导出；如需，补配置后重新生成。[[end]]

## 限制与调整建议

- 生成物是起点：唯一约束、业务联动（如归属校验、状态机）按需补在 service 层；
- 枚举 Valid 按连续区间判断，非连续枚举改成查表；
- 字典字段前端选项走 useDictOptions（web/src/composables/useDict.ts）：字典类型与数据
  需在系统字典中维护，缺失时下拉为空、列表回退显示原值；
- 重新生成会覆盖同名产物文件，本地手工修改请先迁移到 git 管理或自行合并。
`
