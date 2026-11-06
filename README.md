# go-admin-pro

[![CI](https://github.com/go-quick-code/go-admin-pro/actions/workflows/ci.yml/badge.svg)](https://github.com/go-quick-code/go-admin-pro/actions/workflows/ci.yml)
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

- **模块化单体**：`framework` 基础能力 + `system` / `infra` / `codegen` / `message` 业务模块，依赖单向、边界清晰。
- **认证与 RBAC**：JWT 登录、bcrypt 密码、角色绑定菜单、接口级权限码校验、超级管理员自动放行。
- **多租户**：GORM 插件自动追加 `tenant_id` 条件、插入自动回填；新建租户自动初始化部门 / 超级管理员角色 / `admin` 账号。
- **租户套餐与配额**：套餐定义菜单授权与用户数/存储/月短信/月邮件配额；平台租户保持通配，租户超管受套餐约束。
- **数据权限**：角色维度支持「全部 / 自定义部门 / 本部门 / 本部门及以下 / 仅本人」，用户、CRM 客户/商机/合同/回款与仪表盘已接入。
- **系统管理**：用户、角色、菜单（目录/菜单/按钮三级）、部门树、岗位、字典、租户、租户套餐、社交登录、敏感词、地区、登录日志、操作日志。
- **消息通道**：平台级短信/邮件渠道与模板，租户级发送日志；内置 log / SMTP / 阿里云短信 provider，密钥查询脱敏，支持失败重发。
- **社交登录**：微信开放平台 / 钉钉 / 企微 OAuth2 客户端登录；state + PKCE，未绑定账号不自动建号。
- **实时推送**：SSE + WebSocket 双传输，一次性票据认证；前端按 WS → SSE → 轮询降级。
- **OA 套件**：公告（受众/已读）、请假（类型/额度/半天粒度）、报销（明细/审批）、考勤（班次/考勤组/Web 打卡/结果派生/月度汇总）、日程、会议室预约、文档目录与 ACL；审批统一走 bpm。
- **bpm 与通知**：版本化线性审批链（角色/权限码候选池、部门负责人、转办/驳回/撤回）、通用待办、站内通知（待办/结果/公告/会议提醒）。
- **基础设施**：本地文件存储、参数配置、Cron 定时任务（6 段表达式，支持立即执行、重试、执行日志）、API 访问日志、数据库文档、监控面板。
- **代码生成器**：两种入口——元信息驱动的「owner-scoped 实体全栈脚手架」（model/dto/repository/service/api + 路由注册点 + 菜单/权限码种子 + 测试骨架 + ProTable 页面，配套装配说明），以及读取 `information_schema` 按表结构生成通用 CRUD 模板 ZIP；两者生成物都是起点，接入工程需按产物内说明手工放置并注册。
- **前端动态路由**：菜单由后端下发，前端按组件路径动态注册，按钮权限通过 `v-if` 控制。
- **开箱即用**：`make migrate-up` 按版本建表，启动时校验迁移版本并写入种子数据（菜单、角色、管理员、字典、请假类型、默认审批流程、定时任务、消息渠道/模板、租户套餐、省市地区）。

## 相关文档

- 术语表：[GLOSSARY.md](GLOSSARY.md)
- 架构决策：[docs/adr/](docs/adr/)
- 平台 MVP 详细规格：[docs/platform-mvp-spec.md](docs/platform-mvp-spec.md)
- OA MVP 规格：[docs/oa-mvp-spec.md](docs/oa-mvp-spec.md)
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
│   │   ├── system/             # 认证、用户、角色、菜单、部门、岗位、字典、租户/套餐、社交登录、敏感词、地区、日志
│   │   ├── infra/              # 文件、参数配置、定时任务、API 访问日志、数据库文档、监控面板
│   │   ├── message/            # 短信/邮件渠道与模板（平台级）、发送日志与发送配额（租户级）
│   │   ├── crm/                # 客户管理（CRM 第一条纵切，手写样板：model/dto/repository/service/api）
│   │   ├── bpm/                # 流程定义、实例、任务、审批记录、通用待办
│   │   ├── notify/             # 站内通知投递、未读数、已读状态、实时推送 HTTP 入口
│   │   ├── oa/                 # 公告、请假、报销、考勤、日程、会议室、文档
│   │   └── codegen/            # 代码生成器
│   └── pkg/                    # 框架能力
│       ├── database/           # GORM 连接、迁移校验 + 多租户/审计插件
│       ├── model/              # 实体底座 BaseEntity（时间戳、软删除、creator/updater）
│       ├── tenant/             # 租户上下文与 Skip
│       ├── jwt/ authn/         # 令牌与登录主体
│       ├── middleware/         # CORS、日志、Recovery
│       ├── logger/             # 全局 slog 装配（级别/格式）
│       ├── excel/              # Excel 导入导出与模板
│       ├── money/              # 金额值类型（int64 分，decimal(12,2)）
│       ├── datascope/          # 行级数据权限谓词引擎（角色范围 1–5、多角色并集）
│       ├── quota/              # 套餐限额与用量的中性类型（system 解释，消费模块依赖窄接口）
│       ├── push/               # 实时推送 hub 与一次性票据（SSE/WS 传输层在 notify 模块）
│       ├── iploc/              # IP 归属地定位（ip2region xdb；未配置数据文件时空实现）
│       ├── dbschema/           # information_schema 读取（codegen 与数据库文档共用）
│       ├── text/               # 跨模块文本小工具（按字符截断等）
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

访问 `http://localhost:5173`，Vite 会把 `/admin-api` 代理到 `http://127.0.0.1:8080`。
上传文件不再有公开静态目录，读取统一走鉴权下载端点 `GET /admin-api/infra/file/:id/download`。

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
种子之后写入 CRM 演示组织与业务数据，并写入平台能力演示数据（SMTP/阿里云短信
演示渠道、邮件/短信模板、标准版套餐兜底、敏感词示例、停用的社交客户端、演示
消息日志——渠道与客户端的配置和密钥全部是占位符，发送/登录前需在管理端改为
真实配置）；CRM 只有 `crm_customer` 为空时才写入，重复启动不会重复插入。
只想要基础种子（无演示数据）时正常 `make run` 即可。

种子是「空表才写」的一次性逻辑：菜单、角色、字典等各段种子在对应表非空时整体跳过。
已有库升级到新版本后，新增的菜单/按钮/字典不会自动补写（例如 `simple-list` 按钮
1014/1024/1054、`crm_product_unit` 字典），需手工补建或重建种子库（开发库可 drop
后执行 `make migrate-up` 再启动）。

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

### OA 快速开始

OA 与 CRM 共用同一条演示数据入口：`make run-demo` 会在基础种子与 CRM 演示
组织之后写入 OA 演示数据（请假类型、公告、请假、报销、考勤、日程、会议室、
文档）。只想要基础种子（默认班次与考勤组、默认审批流程、报销类别字典，无
OA 演示业务数据）时正常 `make run` 即可。

```bash
make migrate-up            # 建表（幂等，可重复执行）
make run-demo              # 启动后端并写入 CRM + OA + 平台演示数据
```

演示数据按表幂等：每个域以自己的业务表为哨兵（如 `oa_announcement`、
`oa_leave_request`、`oa_expense_claim`、`oa_attendance_punch`、
`oa_meeting_room`、`oa_document_folder`），已有行（含软删）时整段跳过，
重复启动不会重复插入；角色/用户/部门负责人/额度按业务键逐行补齐。平台
演示段按编码判重（渠道/模板/社交客户端）、敏感词按词判重、演示消息日志
仅在 `message_log` 为空时写入；管理端删除的演示渠道/模板/客户端不复活。

演示账号（租户 1，密码均为 `123456`）：

| 账号 | 视角 |
| --- | --- |
| `admin` | 超级管理员：全部 OA 数据、流程定义、考勤校正与全租户结果 |
| `demo` | 内置普通角色：OA 员工侧页面（公告/请假/报销/我的考勤/日程/会议/文档）；不含考勤结果管理页与校正/全租户按钮（spec §7.3 只授基础菜单） |
| `hr` | seed-demo 创建的 HR 演示角色：考勤结果（533）、校正（5331）、全租户可见（5332）、月度汇总（534）、请假类型/额度（522）、班次/考勤组管理 |
| `sales1` / `sales2` / `manager` | CRM 演示组织；请假/报销演示单据的发起人与部门负责人 |

OA 演示数据覆盖：

- 公告：草稿 / 已发布（置顶）/ 已撤回各一，已发布公告带自定义受众与已读回执。
- 请假：7 种请假类型（年假/事假/病假/调休/婚假/产假/陪产假）与演示额度；
  一条已通过年假（带完整 bpm 通过实例、任务与审批记录、额度扣减）与一条
  待审事假（部门负责人待办）。
- 报销：草稿与已通过各一（含明细行，表头合计 = 明细合计）；已通过单带
  `oa_expense_approval` 实例。
- 考勤：默认班次/考勤组之外补演示考勤组成员、近 5 个工作日打卡与结果
  （正常/迟到/早退/迟到且早退/缺卡），以及请假当天的「请假」结果。
- 日程：私有 / 部门 / 公开各一；会议室与预约：2 个会议室、2 条有效预约，
  每条预约自动生成 `source=room_booking` 的部门日程。
- 文档：公司制度（全员 ACL）→ 人事制度（继承）与研发资料（研发部 ACL）、
  市场部资料（市场部 ACL），2 条文件记录（文件同时落到
  `storage.local.base-path`，默认 `./uploads`）。

默认审批流程（随基础种子写入，已发布 v1，按 `tenant + key` 幂等且不覆盖
租户自行维护的版本）：

| key | 节点 1 | 节点 2 |
| --- | --- | --- |
| `crm_contract_approval` | 权限码 `crm:contract:approve` | — |
| `oa_leave_approval` | 发起人所在部门负责人 | 权限码 `oa:leave:manage` |
| `oa_expense_approval` | 发起人所在部门负责人 | 权限码 `oa:expense:manage` |

内置定时任务（`infra_job`）：`oaAttendanceDailyResult` 每天 01:00 生成前一日
考勤结果、`oaRoomBookingReminder` 每 5 分钟发送会议提醒，二者默认启用；
`recyclePoolCustomers`（公海回收，每天 03:00）与 `demoJob` 默认暂停。
种子在插入后显式回写 `status`，避免 GORM 对 `default:1` 零值列的省略导致
「默认启用」被落成暂停。

运维注意点：

- `oa_expense_category` 字典只种租户 1；请假类型由 `seed-demo` 写入。新建租户
  的 bootstrap 仍只有「总公司 / super_admin / admin」，需要手工补齐请假类型、
  考勤班次与考勤组、报销类别字典、默认审批流程后才能在 OA 中建单/提交。
- 打卡接口（`POST /oa/attendance/punch`）v1 以登录身份收口，未强制
  `oa:attendance:punch` 权限码；无启用考勤组时返回 400。
- 内置普通角色（`demo`）在 issue #63 补充了 529/5291–5296（我的请假与
  撤回/取消）、528（我的额度）与 533/5331/5332（考勤结果、校正、全租户
  可见），便于演示；生产可按需回收 5331/5332。
- 演示文档的文件记录写入 `storage.local.base-path`；若该目录不可写，种子会
  记录告警但保留记录，下载入口会按「文件不存在」返回 404。

OA 集成测试入口：

```bash
make test-integration           # 全量（含 internal/seed 的 OA 演示数据用例）
GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test?charset=utf8mb4&parseTime=True' \
  go test -tags=integration -p 1 ./internal/seed/ ./internal/module/oa/
```

## 核心设计

### 版本化迁移

表结构由 `migrations/` 下的 goose SQL 管理，迁移文件会内嵌进二进制：

- `make migrate-up`：按版本执行未应用的迁移，可重复执行。
- `make migrate-down` / `make migrate-reset`：回滚一个 / 全部版本，必须显式传 `CONFIRM=yes`。**回滚会执行 Down 段的 DROP，请先备份；当前全部迁移回滚等于删掉全部 57 张业务表。**
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

配置项：字段映射（`string/text/int8/int32/int64/money/datetime/bool` → Go 类型与前端组件，登记在 `entity_mapping.go`）、字典字段（前端 select 通过 `useDictOptions` 读系统字典）、枚举字段（模型常量 + service 400 校验）、owner 字段开关（`ownerScoped`，关闭则生成不带数据范围的简单 CRUD）、编号开关（`sequence`，owner-scoped 实体默认不带编号，开启走 `crm_sequence` 事务发放）、导入导出开关（`importExport`，仅生成接线位注释，不产出可用实现；接入参考 CRM 手写实现）。

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
| OA（公告/请假/报销/考勤/日程/会议室/文档） | `/admin-api/oa/*` |
| bpm（流程定义/待办/实例） | `/admin-api/bpm/*` |
| 站内通知 | `/admin-api/notify/*` |
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
| `database.seed-demo` | 是否写入 CRM + OA + 平台演示数据，默认 `false`（`GAP_SEED_DEMO=true` 或 `make run-demo` 开启） |
| `jwt.secret` / `jwt.expire` | JWT 密钥与有效期 |
| `security.captcha.enabled` | 是否启用图形验证码 |
| `security.login.max-fail` / `lock-duration` | 登录失败锁定策略 |
| `storage.local.base-path` | 本地文件存储根目录（文件不公开托管，读取走鉴权下载端点） |
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
make web-type-check # 前端类型检查
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
- 租户套餐的菜单授权改变租户超管语义：平台租户保持通配，其他租户按套餐；存量租户由迁移回填全部菜单。
- 消息渠道/社交客户端密钥在 DB 明文存储，仅 API 查询脱敏；未接入 KMS。
- 手动重发不带原始模板参数（`message_log` 不存 params）：对声明了变量的阿里云模板重发会被阿里云拒绝并按失败记录（重发内容以模板当前内容为准）。
- 配额校验无数据库唯一约束兜底：并发创建（用户/发送等）可能同时通过检查、轻微超限；单人后台并发概率低，超限只影响配额不影响安全边界。
- 实时推送的 hub 与票据在进程内，单实例有效；多实例需外部 pub/sub（ADR 0013）。前端始终保留轮询兜底。
- 社交登录只做 OAuth2 客户端，不做自家授权服务器；未绑定账号不自动建号。企微的 `agentid` 与 `user/get` 资料端点没有独立配置列：`agentid` 经 `authorize_url` 覆盖地址的 query 携带，`user/get` 取 `userinfo_url` 覆盖地址的 scheme+host 拼固定路径（见 `docs/guides/social-callback-setup.md`）。
- 短信/邮件不做自动重试、批量发送、附件；短信 provider 目前只实现阿里云。
- 地区库内置省/市两级，区县需导入；IP 定位需自备 ip2region xdb 文件，未配置时为空实现。
- 集成测试必须用 `GAP_TEST_DATABASE_DSN` 指向专用测试库：共享默认库 `go_admin_pro_test` 的 goose 账本已损坏（缺 version 23 行），直接在其上跑会因重复应用迁移而失败（开发环境遗留问题，不影响业务库）。
- 监控面板只做运行时聚合，不提供 Prometheus/pprof/链路追踪；数据库文档仅平台租户可见。
- bpm v1 只支持线性有序审批链：不做 BPMN 网关/并行/会签/条件表达式/可视化设计器；OA 只做公告、半天粒度请假、Web 打卡、报销审批、日程/会议室、文档 ACL，不做付款/预算/发票、Wiki/在线预览、GPS 考勤设备、外部日历同步。

## 后续计划

- [ ] Redis 缓存与登录态管理
- [x] Excel 导入导出（helper + CRM 客户/联系人/产品导入，客户/合同/产品/回款导出）
- [ ] 对象存储（OSS / S3 / MinIO）
- [ ] 代码生成器支持树形表与主子表
- [ ] OpenAPI 文档（swaggo）
- [x] 数据权限扩展到更多业务模块（CRM 客户/商机/合同/回款/公海已接入）
- [x] 平台能力：消息通道、社交登录、实时推送、租户套餐与配额、访问日志、敏感词、地区/IP、数据库文档、监控

## 贡献

欢迎提交 Issue / PR。提交前请确保：

```bash
make check
pnpm --dir web build
```

## License

[MIT](LICENSE)
