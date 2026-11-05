# go-admin-pro

[![CI](https://github.com/lautung/go-admin-pro/actions/workflows/ci.yml/badge.svg)](https://github.com/lautung/go-admin-pro/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.5-42b883?logo=vue.js)](https://vuejs.org/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

基于 **Go + Gin + GORM + MySQL + Vue3** 的企业级后台管理系统。

设计思路对标 [芋道源码 ruoyi-vue-pro](https://github.com/YunaiV/ruoyi-vue-pro)：模块化单体分层、RBAC + 数据权限、多租户、代码生成器、基础设施模块，用 Go 重新实现。目标不是逐行移植，而是把芋道里最有价值的那套「后台框架骨架」用 Go 生态落地成可运行、可扩展的工程。

## 界面预览

| 登录 | 仪表盘 |
| --- | --- |
| ![登录](docs/images/login.png) | ![仪表盘](docs/images/dashboard.png) |

![用户管理](docs/images/user.png)

## 功能特性

- **模块化单体**：`framework` 基础能力 + `system` / `infra` / `codegen` 业务模块，依赖单向、边界清晰。
- **认证与 RBAC**：JWT 登录、bcrypt 密码、角色绑定菜单、接口级权限码校验、超级管理员自动放行。
- **多租户**：GORM 插件自动追加 `tenant_id` 条件、插入自动回填；新建租户自动初始化部门 / 超级管理员角色 / `admin` 账号。
- **数据权限**：角色维度支持「全部 / 自定义部门 / 本部门 / 本部门及以下 / 仅本人」，用户、CRM 客户/商机/合同/回款与仪表盘已接入。
- **系统管理**：用户、角色、菜单（目录/菜单/按钮三级）、部门树、岗位、字典、租户、登录日志、操作日志。
- **基础设施**：本地文件存储、参数配置、Cron 定时任务（6 段表达式，支持立即执行、重试、执行日志）。
- **代码生成器**：两种入口——元信息驱动的「owner-scoped 实体全栈脚手架」（model/dto/repository/service/api + 路由注册点 + 菜单/权限码种子 + 测试骨架 + ProTable 页面，配套装配说明），以及读取 `information_schema` 按表结构生成通用 CRUD 模板 ZIP；两者生成物都是起点，接入工程需按产物内说明手工放置并注册。
- **前端动态路由**：菜单由后端下发，前端按组件路径动态注册，按钮权限通过 `v-if` 控制。
- **开箱即用**：`make migrate-up` 按版本建表，启动时校验迁移版本并写入种子数据（菜单、角色、管理员、字典、演示任务）。

## 相关文档

- 术语表：[GLOSSARY.md](GLOSSARY.md)
- 架构决策：[docs/adr/](docs/adr/)
- CRM 详细规格：[docs/crm-mvp-spec.md](docs/crm-mvp-spec.md)

## 技术栈

| 层次 | 选型 |
| --- | --- |
| 语言 | Go 1.26+ |
| Web 框架 | Gin |
| ORM | GORM + MySQL 8 |
| 迁移 | goose（版本化 SQL，可回滚） |
| 认证 | JWT（golang-jwt/jwt/v5）+ bcrypt |
| 配置 | Viper（YAML + 环境变量覆盖） |
| 定时任务 | robfig/cron/v3 |
| 验证码 | base64Captcha（默认关闭，可在配置中开启） |
| 前端 | Vue 3 + TypeScript + Vite + Element Plus + Pinia + Vue Router |
| 包管理 | pnpm 11 |

## 架构

```text
HTTP 请求
  → CORS / Recovery / AccessLog
  → JWT Auth（解析 token，写入 tenant / principal 上下文）
  → OperateLog（记录写操作）
  → RequirePermission（校验权限码）
  → Handler → Service → Repository → GORM（多租户插件）→ MySQL
```

```text
go-admin-pro
├── cmd/server/                 # 启动入口
├── cmd/migrate/                # 迁移命令（up / down / status / create）
├── migrations/                 # goose 版本化 SQL，内嵌进二进制
├── configs/config.yaml         # 配置文件
├── internal/
│   ├── app/                    # 依赖装配
│   ├── config/                 # 配置加载
│   ├── router/                 # 路由、静态资源、SPA 回退
│   ├── seed/                   # 初始化数据（依赖已执行的迁移）
│   ├── testutil/               # 集成测试基座（真实 router + MySQL，integration 标签）
│   ├── module/
│   │   ├── system/             # 认证、用户、角色、菜单、部门、岗位、字典、租户、日志
│   │   ├── infra/              # 文件、参数配置、定时任务
│   │   ├── crm/                # 客户管理（CRM 第一条纵切，手写样板：model/dto/repository/service/api）
│   │   └── codegen/            # 代码生成器
│   └── pkg/                    # 框架能力
│       ├── database/           # GORM 连接、迁移校验 + 多租户/审计插件
│       ├── model/              # 实体底座 BaseEntity（时间戳、软删除、creator/updater）
│       ├── tenant/             # 租户上下文与 Skip
│       ├── jwt/ authn/         # 令牌与登录主体
│       ├── middleware/         # CORS、日志、Recovery
│       ├── excel/              # Excel 导入导出与模板
│       ├── money/              # 金额值类型（int64 分，decimal(12,2)）
│       ├── datascope/          # 行级数据权限谓词引擎（角色范围 1–5、多角色并集）
│       ├── response/ xerr/     # 统一响应与业务错误
│       └── web/ utils/         # 分页、通用工具
├── web/                        # Vue3 前端
│   └── src/{api,components,layout,router,stores,views}
├── docs/images/
├── Dockerfile
├── docker-compose.yml
└── Makefile
```

## 快速开始

### 环境要求

- Go 1.26+
- Node.js 22+、pnpm 11+
- MySQL 8（或 Docker）

### 1. 启动 MySQL

```bash
docker compose up -d mysql
```

默认创建数据库 `go_admin_pro`，root 密码 `123456`。也可以使用已有 MySQL，修改 `configs/config.yaml` 的 `database.dsn`。

### 2. 执行迁移并启动后端

```bash
make migrate-up            # 按版本执行 migrations/ 下的 SQL，可重复执行
go run ./cmd/server -c configs/config.yaml
```

服务启动时只校验迁移版本，不会自动建表：数据库未迁移、落后或超前都会直接报错退出。首次启动会写入种子数据（可用 `GAP_SEED=false` 关闭）。

服务地址：`http://127.0.0.1:8080`，健康检查：`GET /healthz`。

关键配置支持环境变量覆盖：

```bash
export GAP_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True&loc=Local'
export GAP_JWT_SECRET='please-change-me'   # 仅本地开发；生产（app.env 非 dev/test）必须换成唯一强密钥
export GAP_APP_PORT=8080
export GAP_SEED=false   # 生产环境可关闭种子写入
```

### 3. 启动前端（开发模式）

```bash
cd web
pnpm install
pnpm dev
```

访问 `http://localhost:5173`，Vite 会把 `/admin-api` 与 `/files` 代理到 `http://127.0.0.1:8080`。

### 4. 生产构建

```bash
pnpm --dir web build          # 产物：web/dist
go build -trimpath -o bin/server ./cmd/server
go build -trimpath -o bin/migrate ./cmd/migrate
./bin/migrate -c configs/config.yaml up   # 首次部署 / 升级前执行
./bin/server -c configs/config.yaml
```

Go 服务会自动托管 `web/dist`，单进程即可提供前端与 API（`app.web-root` 可配置）。

### 默认账号

| 租户 | 账号 | 密码 | 说明 |
| --- | --- | --- | --- |
| 1 | `admin` | `123456` | 超级管理员，全部权限 |
| 1 | `demo` | `123456` | 普通角色，仅本人数据权限 |
| 1 | `sales1` / `sales2` | `123456` | CRM 演示销售，「仅本人」数据范围 |
| 1 | `manager` | `123456` | CRM 演示销售主管，「本部门及以下」数据范围 |
| 新建租户 | `admin` | `123456` | 创建租户时自动初始化 |

> 生产环境请务必修改默认密码与 `jwt.secret`。

### CRM 快速开始

一条命令即可得到完整的 CRM 演示环境：

```bash
make migrate-up            # 建表（幂等，可重复执行）
make run-demo              # 启动后端并写入 CRM 演示数据（等价于 GAP_SEED_DEMO=true）
```

`database.seed` 默认 `true`（环境变量 `GAP_SEED` 可覆盖）；`database.seed-demo`
默认 `false`，演示数据只在显式开启时写入，避免生产库被演示账号与示例业务数据
污染。演示环境用 `make run-demo`，或手工
`GAP_SEED_DEMO=true go run ./cmd/server -c configs/config.yaml`。首次启动在基础
种子之后写入 CRM 演示组织与业务数据；只有 `crm_customer` 为空时才写入，
重复启动不会重复插入。只想要基础种子（无演示数据）时正常 `make run` 即可。

演示账号（租户 1，密码均为 `123456`）：

| 账号 | 角色 | 数据范围 | 演示视角 |
| --- | --- | --- | --- |
| `admin` | 超级管理员 | 全部 | 全部客户、合同与仪表盘 |
| `demo` | 普通角色 | 仅本人 | 系统功能演示；无 CRM 菜单 |
| `sales1` / `sales2` | 销售 | 仅本人 | 只能看到自己负责的客户/商机/合同/回款 |
| `manager` | 销售主管 | 本部门及以下 | 看到市场部（102）及下级销售的 CRM 数据 |

演示数据覆盖：

- 产品：2 个分类、4 个产品（含价格与单位），供商机/合同行项目引用。
- 客户：8 个，覆盖潜在/活跃/流失三态，其中 2 个在公海（无负责人，带释放原因）；
  跟进时间有近有远，用于演示公海回收。
- 联系人：每客户 1–2 个；跟进：覆盖客户、商机、合同三个维度。
- 商机：6 个，阶段 1–6 各一个；合同：6 个，覆盖草稿/待审/已签（含行项目）/作废。
- 回款：6 期计划覆盖待回款/部分回款/已回款/逾期，5 条回款记录；已签合同与
  回款记录分布在近 12 个月，撑起仪表盘趋势与逾期卡片。
- 编号：客户 `KH`、商机 `SJ`、合同 `HT`、回款记录 `HK` 均同步写入
  `crm_sequence`，启动后通过页面新建不会与演示编号冲突。

手工验收路径（用 `sales1` 或 `manager` 登录前端）：

1. 客户管理 → 新建客户 → 详情内添加联系人、写跟进。
2. 产品管理 → 新建分类/产品。
3. 商机管理 → 新建商机、挂行项目 → 阶段推进到赢单/输单。
4. 合同管理 → 新建合同（行项目）→ 提交审批 → 审批通过（或驳回/作废）。
5. 回款管理 → 按合同拆回款计划 → 在计划行登记回款记录；逾期行标红。
6. CRM 仪表盘 → 4 张卡片、阶段漏斗、近 12 个月合同/回款趋势。
7. 客户管理释放客户 → 公海页面认领；在「定时任务」手动执行「公海自动回收」，
   验证有未作废合同的客户不回收、无合同且超期未跟进的客户回收。

常用命令与关键端点：

```bash
make migrate-up                 # 建表
make run                        # 启动后端（基础种子，无演示数据）
make run-demo                   # 启动后端并写入 CRM 演示数据
make test-integration           # 全量集成测试（真实 MySQL）
GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test?charset=utf8mb4&parseTime=True' \
  go test -tags=integration -p 1 ./internal/module/crm/ ./internal/seed/
```

| 能力 | 端点 |
| --- | --- |
| 登录 | `POST /admin-api/system/auth/login` |
| 客户列表 / 公海 | `GET /admin-api/crm/customer/page`、`GET /admin-api/crm/customer/pool/page` |
| 释放 / 认领 | `PUT /admin-api/crm/customer/release`、`PUT /admin-api/crm/customer/claim` |
| 商机阶段 | `PUT /admin-api/crm/opportunity/stage` |
| 合同状态 | `PUT /admin-api/crm/contract/status` |
| 回款计划 / 记录 | `POST /admin-api/crm/receivable/plan`、`POST /admin-api/crm/receivable/record` |
| 仪表盘 | `GET /admin-api/crm/dashboard/summary` |

端到端验收用例见 `internal/module/crm/e2e_http_integration_test.go`（`//go:build integration`），
演示数据完整性/幂等用例见 `internal/seed/crm_demo_integration_test.go`。

## 核心设计

### 版本化迁移

表结构由 `migrations/` 下的 goose SQL 管理，迁移文件会内嵌进二进制：

- `make migrate-up`：按版本执行未应用的迁移，可重复执行。
- `make migrate-down` / `make migrate-reset`：回滚一个 / 全部版本，必须显式传 `CONFIRM=yes`。**回滚会执行 Down 段的 DROP，请先备份；当前基线回滚等于删掉全部 17 张表。**
- `make migrate-status`：查看每个版本的应用状态。
- `make migrate-stamp version=1`：把结构已存在但没有迁移记录的库标记为基线版本，不执行 DDL；后续版本必须走 `migrate up`。
- `make migrate-create name=add_xxx`：生成下一个版本的 SQL 模板。

迁移默认超时 30 分钟，可用 `go run ./cmd/migrate -timeout 0 up` 关闭超时（数据迁移或 backfill 建议显式放宽）。

应用启动时只校验迁移版本，不建表也不执行迁移；数据库未迁移、落后或超前都会报错退出。已发布的迁移不要修改，表结构变更一律新增文件。

旧版 AutoMigrate 建的库没有 goose 版本记录：先备份，人工核对 17 张表结构与基线一致后执行 `make migrate-stamp version=1`；不一致或开发环境无数据时，删库后重新 `make migrate-up` 更干净。

MySQL 的 DDL 不在事务里：基线在一个文件中建 17 张表，中途失败会留下半套表，需要手工清理后重跑。后续迁移保持单个 DDL，或写成可重入语句。

改动迁移后运行 `make verify-migration`：需要一个空的 disposable 库（DSN 通过 `GAP_DATABASE_DSN` 指定），脚本会做 `up → reset → up`、启动服务、seed、登录与用户分页冒烟。详细步骤见 `docs/agents/verification.md`。


### 多租户隔离

租户来自 JWT 的 `tenantId`，由 Auth 中间件写入 `context.Context`：

```go
ctx = tenant.WithTenantID(ctx, claims.TenantID)
```

`tenant.TenantPlugin` 注册 GORM 回调：

- **Query / Update / Delete**：对注册过的表自动追加 `tenant_id = ?`，业务代码无需重复拼装。
- **Create**：自动回填 `tenant_id`，避免遗漏。
- **fail-closed**：无租户上下文访问租户表会直接报错（哨兵错误 `tenant.ErrNoTenantContext`），不会静默放行；定时任务、seed 等系统级路径必须显式 `tenant.Skip(db)` 跳过过滤。
- **表清单**：`internal/pkg/database/tenant_tables.go`，新增业务表时按需登记。

创建租户时会自动生成：默认部门「总公司」、内置角色 `super_admin`、管理员 `admin/123456`。

### 数据权限

权限范围配置在角色上：

| 值 | 含义 |
| --- | --- |
| 1 | 全部数据 |
| 2 | 自定义部门 |
| 3 | 本部门 |
| 4 | 本部门及以下 |
| 5 | 仅本人 |

数据权限由 `internal/pkg/datascope` 的行级引擎计算：`system.Service.DataScopeFilter` 汇总用户所有角色，按角色范围产出可见部门（自定义部门读 `system_role_dept`，本部门及以下展开子树，仅本人产出 `IncludeSelf`），多角色取可见部门并集，任一角色为「全部」则全可见；用户列表据此追加条件。其他业务模块通过 `SystemGateway.DataScopeFilter` 拿到同一个 `datascope.Filter`，再翻译成模块自己的谓词（CRM 为 `owner_dept_id` / `owner_user_id`，见 ADR 0004）。

### 权限模型

- 菜单分三类：`1 目录`、`2 菜单`、`3 按钮`。
- 接口权限码形如 `system:user:create`，由 `RequirePermission` 中间件校验。
- `super_admin` 角色直接获得 `*`，绕过权限码校验。
- 前端登录后调用 `/system/auth/user-info` 获取菜单树与权限码，动态注册路由并控制按钮显示。

### 代码生成器

生成器有两个入口（`internal/module/codegen`），能力与定位不同，生成物都需要人工接入：

#### 1. 元信息驱动：owner-scoped 实体全栈脚手架（推荐，CRM 后续实体用它）

给定实体元信息（表名、字段列表、权限码前缀、菜单项与能力开关），生成与 `internal/module/crm` 手写样板同形态的全栈脚手架：

- 后端五层 `model / dto / repository / service / api`（方法挂在模块既有 `Service` / `Repository` / `Handler` 上），owner-scoped 语义齐备：列表套数据范围谓词（`owner_dept_id` / `owner_user_id`，空切片 + 无 self = 恒假）、详情/改/删 `scope.Permits` 范围外 404、负责人部门快照与名称补齐；
- 路由注册点（`Register<Entity>Routes`，一行接进模块 router）、菜单/权限码种子片段（权限码 `:query/:create/:update/:delete`）；
- 测试骨架：枚举校验/编号格式的纯函数测试（外部测试包）+ 五种数据范围矩阵与「范围外 404」的集成测试（`//go:build integration`，接线后随 `make test-integration` 生效）；
- 前端：`web/src/api/<module>.ts` 追加片段 + `web/src/views/<module>/<entity>/index.vue`（ProTable 列/搜索/表单全套）；
- 迁移模板与 `INTEGRATION.md` 装配说明（`tenant_tables.go`、router、app.go、seed 的接线步骤；`app.go` 等文件**不自动改写**）。

配置项：字段映射（`string/text/int8/int32/int64/money/datetime/bool` → Go 类型与前端组件，登记在 `entity_mapping.go`）、字典字段（前端 select 占位，选项接系统字典）、枚举字段（模型常量 + service 400 校验）、owner 字段开关（`ownerScoped`，关闭则生成不带数据范围的简单 CRUD）、编号开关（`sequence`，owner-scoped 实体默认不带编号，开启走 `crm_sequence` 事务发放）、导入导出开关（`importExport`，仅生成 #20 接线位与 TODO 注释，不产出可用实现）。

```bash
# 用产品（product）元信息生成一次演示输出到任意目录
go run ./cmd/codegen -spec internal/module/codegen/testdata/entity/product.json -out /tmp/gen
```

`testdata/entity/product.json` 是元信息参考样例；`testdata/demo_output/` 是同一份元信息的产物快照（测试逐字节校验，供人工 review）。生成的代码可信通过 `go build/vet/test`：`TestGeneratedProductCompiles` 会把产物叠加进仓库可编译子集跑真实 `go build/vet` 与纯函数测试。新模块（骨架尚不存在）传 `"moduleScaffold": true`，生成器会一并产出 Service/Repository/Handler 骨架与 api helpers/router。

限制（如实说）：面向 `internal/module/*` 的模块化单体约定，不生成数据库迁移执行（只有模板）；不自动改 `app.go` / `router.go` / `tenant_tables.go` / `seed.go`；多实体差异大的业务逻辑（状态机、唯一性校验）仍需手写。

#### 2. 表结构驱动：通用 CRUD 模板（旧入口，保留）

1. 在数据库中创建业务表（`system_` / `infra_` 前缀的表会被排除）。
2. 打开「代码生成」页面，选择表 → 自动带出字段 → 填写模块名、业务名、类名。
3. 生成并下载 ZIP，包含 model / dto / service / api / Vue 页面与 README；按 ZIP 内 README 手工放置、注册路由并配置权限码。

生成器会校验表名、模块名、业务名、类名，避免路径穿越；生成的 Go 代码经 `go/format` 格式化。这条路径产出的是通用模板代码，不含数据范围、租户登记与测试骨架。

### 定时任务

- 表达式为 **6 段（含秒）**，例如 `0 */5 * * * *` 表示每 5 分钟。
- 内置处理器：`cleanLogs`（在 `internal/app/app.go` 注册，参数为保留天数，默认 30）。
- 演示处理器 `demoJob` 内置在 `internal/module/infra/service/job.go` 的 `NewJobScheduler` 里，配套种子任务「演示任务」默认暂停，仅用于验证调度链路。
- 新增处理器：在 `internal/app/app.go` 中调用 `scheduler.Register("name", fn)`。
- 支持开启 / 暂停、立即执行、失败重试与执行日志。

## API 约定

统一响应：

```json
{ "code": 0, "data": {}, "msg": "" }
```

- `code = 0` 成功；非 0 为业务错误；HTTP 状态码同时表达语义（400/401/403/404/409/500）。
- 分页参数：`pageNo`、`pageSize`；分页响应：`{ "list": [], "total": 0 }`。
- 认证：`Authorization: Bearer <accessToken>`。

示例：

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/admin-api/system/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"123456","tenantId":1}' | jq -r .data.accessToken)

curl -s "http://127.0.0.1:8080/admin-api/system/user/page?pageNo=1&pageSize=10" \
  -H "Authorization: Bearer $TOKEN"
```

主要接口前缀：

| 模块 | 前缀 |
| --- | --- |
| 认证 | `/admin-api/system/auth/*` |
| 用户 / 角色 / 菜单 / 部门 / 岗位 / 字典 / 租户 / 日志 | `/admin-api/system/*` |
| 文件 / 参数 / 定时任务 | `/admin-api/infra/*` |
| CRM（客户/联系人/跟进/商机/合同/回款/仪表盘） | `/admin-api/crm/*` |
| 代码生成 | `/admin-api/infra/codegen/*` |

## 配置说明

| 配置项 | 说明 |
| --- | --- |
| `app.port` | HTTP 端口 |
| `app.api-prefix` | API 前缀，默认 `/admin-api` |
| `app.web-root` | 前端产物目录，默认 `./web/dist` |
| `app.cors.allowed-origins` | 跨域白名单：精确匹配 Origin，非白名单来源不返回 CORS 头；为空表示不放行任何跨域（可用 `GAP_CORS_ALLOWED_ORIGINS` 逗号分隔覆盖） |
| `database.dsn` | MySQL DSN |
| `database.seed` | 启动时写入种子数据 |
| `database.seed-demo` | 是否写入 CRM 演示数据，默认 `false`（`GAP_SEED_DEMO=true` 或 `make run-demo` 开启） |
| `jwt.secret` / `jwt.expire` | JWT 密钥与有效期 |
| `security.captcha.enabled` | 是否启用图形验证码 |
| `security.login.max-fail` / `lock-duration` | 登录失败锁定策略 |
| `storage.local.base-path` | 本地文件存储根目录 |
| `storage.local.public-prefix` | 文件访问前缀，默认 `/files` |
| `tenant.enabled` | 是否启用多租户插件 |
| `logger.level` / `format` | 日志级别与格式（text / json） |

## 部署

```bash
# 1. 构建前端
pnpm --dir web install --frozen-lockfile
pnpm --dir web build

# 2. 构建后端（server + migrate）
go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
go build -trimpath -ldflags="-s -w" -o bin/migrate ./cmd/migrate

# 3. 执行数据库迁移（可重复执行；回滚用 down / reset）
GAP_DATABASE_DSN='...' ./bin/migrate -c configs/config.yaml up

# 4. 运行
GAP_DATABASE_DSN='...' GAP_JWT_SECRET='...' ./bin/server -c configs/config.yaml
```

使用 Docker（compose 不提供默认 JWT 密钥，必须显式传入）：

```bash
GAP_JWT_SECRET="$(openssl rand -hex 32)" docker compose up -d --build
```

compose 会先运行一次性的 `migrate` 服务，迁移成功后 `app` 才启动。镜像内已包含前端产物，访问 `http://127.0.0.1:8080`。

## 开发命令

```bash
make help         # 查看命令
make run          # 启动后端
make migrate-up    # 执行全部待执行迁移
make migrate-down CONFIRM=yes  # 回滚一个迁移版本
make migrate-reset CONFIRM=yes # 回滚全部迁移版本
make migrate-status # 查看迁移状态
make migrate-stamp version=1 # 已有库标记基线版本
make migrate-create name=add_xxx # 新建迁移文件
make verify-migration # 空库迁移往返 + 启动/登录冒烟
make test         # 运行测试
make test-integration # 运行集成测试（真实 router + MySQL 8；DSN 走 GAP_TEST_DATABASE_DSN）
make check        # fmt-check + build + vet + test
make vet          # 静态检查
make fmt          # gofmt
make web-dev      # 启动前端开发服务器
make web-build    # 构建前端
make build        # 构建后端二进制
make docker-up    # 启动 MySQL
make docker-down  # 停止容器
```

## 已知边界

这是一套可运行的框架骨架，而非完整商业产品，当前刻意保持以下边界：

- 登录态为无状态 JWT，未实现 refresh token / 服务端吊销列表。
- 文件存储仅实现本地磁盘，OSS / S3 可通过扩展 `infra` 模块接入。
- 权限快照（角色码 + 权限码）使用进程内缓存（1 分钟 TTL），多实例部署时需替换为 Redis 或统一失效；字典数据未做缓存，每次直接读库。
- 未内置 Swagger，接口约定见上文与各 handler。
- 工作流、消息中心、支付、商城等芋道业务模块不在本次范围内，可按同样分层继续扩展。

## 后续计划

- [ ] Redis 缓存与登录态管理
- [ ] Excel 导入导出
- [ ] 对象存储（OSS / S3 / MinIO）
- [ ] 代码生成器支持树形表与主子表
- [ ] OpenAPI 文档（swaggo）
- [ ] 数据权限扩展到更多业务模块

## 贡献

欢迎提交 Issue / PR。提交前请确保：

```bash
make check
pnpm --dir web build
```

## License

[MIT](LICENSE)
