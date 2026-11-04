# CRM MVP 规格与施工顺序

> 状态：待确认。里程碑仍是一个（全 CRM 一次交付）；下面的编号只是施工顺序，不是对外切片。

## 1. 施工顺序

1. **底座冲刺**（5–7 天）：迁移、模型底座、数据权限枚举、租户 fail-closed、CORS、事务、Excel、README。
2. **手写客户 + 联系人**（2–3 周）：端到端跑通 model / dto / repository / service / api / 权限 / 菜单 / Vue 页面 / 测试 / 演示数据，确立可复制的模式。
3. **升级 codegen**：把第 2 步的模式固化成生成器，生成产品、商机、合同、回款、跟进。
4. **补齐剩余实体 + 仪表盘**。
5. **Excel 导入导出铺到客户/联系人/产品，导出铺到客户/合同/回款**。
6. **硬化**：权限矩阵测试、金额不变量测试、性能与文档。

## 2. 底座冲刺任务单

| # | 任务 | 验收 |
|---|---|---|
| 1 | 引入 goose，写 `00001_baseline.sql` 固化现有 17 张表 | `make migrate-up` 与 `make migrate-down CONFIRM=yes` 从空库可往返；应用启动只校验版本 |
| 2 | 实体表加 `creator` / `updater`；关联表补 `tenant_id`；所有实体嵌入 `BaseEntity` | 17 张表迁移完成；`creator/updater` 默认 0（系统） |
| 3 | 数据范围枚举对齐芋道 1–5（1 全部 / 2 自定义 / 3 本部门 / 4 本部门及以下 / 5 仅本人），非法值返回 400 | 旧值迁移：2→4、4→5；越权回退测试覆盖 |
| 4 | 租户插件 fail-closed：无 tenant context 时必须显式 `tenant.Skip` | 无上下文查询返回错误；系统级路径显式 Skip |
| 5 | CORS 改 allowlist（`app.cors.allowed-origins`） | 非白名单 Origin 不返回 CORS 头 |
| 6 | 用户创建/更新、租户创建、角色菜单分配加事务 | 失败注入测试无残留 |
| 7 | 引入 excelize，封装导入/导出 helper：表头映射、校验错误、模板下载 | 单元测试覆盖解析与校验错误 |
| 8 | README 对齐代码：demoJob、字典缓存、codegen「一键生成」三处修正 | README 与代码一致 |

## 3. 表结构

所有实体表含 `id`、`tenant_id`、`created_at`、`updated_at`、`deleted_at`、`creator`、`updater`。关联表用复合主键 + `tenant_id`，不设软删除，变更由操作日志记录。`crm_customer_owner_history` 是追加型审计表，仅 `created_at/creator`；`crm_sequence` 是计数支撑表，只有业务字段——这两类不套用实体底座。

### 产品

| 表 | 关键字段 |
|---|---|
| `crm_product_category` | `parent_id`、`name`、`sort`、`status`(0 启用/1 停用) |
| `crm_product` | `category_id`、`name`、`code`（租户内唯一）、`unit`、`price` decimal(12,2)、`status`、`description` |

### 客户

| 表 | 关键字段 |
|---|---|
| `crm_customer` | `customer_no`（租户内唯一）、`name`、`industry`、`level`、`source`、`phone`、`website`、`address`、`status`(1 潜在/2 活跃/3 流失)、`owner_user_id`(可空)、`owner_dept_id`(可空)、`pool_enter_time`、`last_follow_time`、`release_reason`、`remark` |
| `crm_contact` | `customer_id`、`name`、`position`、`mobile`、`email`、`gender`、`decision_role`、`remark` |
| `crm_follow_up` | `biz_type`(1 客户/2 商机/3 合同)、`biz_id`、`type`、`content`、`next_follow_time` |
| `crm_customer_owner_history` | `customer_id`、`from_user_id`、`to_user_id`、`action`(1 领取/2 释放/3 转移/4 自动回收/5 创建)、`operator_user_id`、`reason`；仅 `created_at/creator` |

### 销售

| 表 | 关键字段 |
|---|---|
| `crm_opportunity` | `opportunity_no`、`customer_id`、`name`、`stage`(1 初步/2 需求/3 报价/4 谈判/5 赢单/6 输单)、`expected_amount`、`expected_close_date`、`owner_user_id`、`owner_dept_id`、`remark` |
| `crm_opportunity_product` | `opportunity_id`、`product_id`、`quantity`、`price`、`amount` |
| `crm_contract` | `contract_no`、`customer_id`、`opportunity_id`(可空)、`name`、`amount`、`sign_date`、`start_date`、`end_date`、`status`(1 草稿/2 待审/3 已签/4 作废)、`owner_user_id`、`owner_dept_id`、`remark` |
| `crm_contract_product` | `contract_id`、`product_id`、`quantity`、`price`、`amount` |

### 回款

| 表 | 关键字段 |
|---|---|
| `crm_receivable_plan` | `contract_id`、`period_no`、`amount`、`due_date`、`remark` |
| `crm_receivable_record` | `plan_id`、`contract_id`、`amount`、`receipt_date`、`method`、`remark` |
| `crm_sequence` | `tenant_id`、`biz_type`、`date_key`、`last_value`，唯一键 `(tenant_id, biz_type, date_key)` |

`crm_receivable_plan` 不存状态与已收金额；列表用 SUM 子查询派生「待回款/部分回款/已回款/逾期」。

### 平台补充

`system_role_dept`：`role_id`、`dept_id`、`tenant_id`，复合主键，支撑自定义部门数据权限。

## 4. 规则

### 4.1 数据权限

- 每个角色产出谓词：全部→无过滤；自定义→`owner_dept_id IN (角色部门)`；本部门→`owner_dept_id = 我的部门`；本部门及以下→`owner_dept_id IN (我的部门 + 子部门)`；仅本人→`owner_user_id = 我`。
- 多角色取并集；任一角色「全部」则全可见。
- 客户/商机/合同直接按 `owner_*` 过滤；联系人/跟进/回款 join 父记录后套用父谓词。
- 公海记录 `owner_user_id IS NULL`，对同租户内有 `crm:customer:pool:view` 的人可见，不受数据范围过滤。
- 负责人变更写 `crm_customer_owner_history`；`owner_dept_id` 随 owner 同步快照。

### 4.2 公海

- 手动释放：owner 置空，写 `pool_enter_time`、`release_reason`、历史。
- 自动回收：`infra_job` 按租户配置执行；条件为「owner 非空、`COALESCE(last_follow_time, created_at)` 超过 N 天（默认 30）、无未作废合同」。从未跟进的新客户按创建时间计算，不会被排除在回收规则外。
- 认领：校验 `crm:customer:claim`、认领上限、客户仍在公海；写入新 owner 与部门，清空池字段，写历史。
- 认领上限：指 `owner_user_id = 当前用户` 的未删除客户总数，不区分是否成交；默认 50。
- 配置落点：回收天数与认领上限存 `infra_config` 的租户级 key `crm.pool.recycle_days`、`crm.pool.claim_limit`，缺省 30 / 50。
- 跟进任一子记录都刷新客户的 `last_follow_time`。

### 4.3 金额不变量

- 合同有行项目时，`amount = SUM(行项目金额)`；无行项目允许手填。
- `SUM(回款计划金额) ≤ 合同金额`；`SUM(回款记录金额) ≤ 计划金额`。
- 金额非负，`decimal(12,2)`，v1 单币种 CNY。
- 违反时服务层返回业务错误，不静默截断。

### 4.4 状态机

- 合同：草稿 → 待审（提交）→ 已签（审批通过）→ 可作废；待审可驳回回草稿。v1 用 `ApprovalService` 直通实现，bpm 上线后替换。
- 商机：阶段 1–4 为进行中，5 赢单、6 输单为终态。
- 客户「已成交」由是否存在已签合同派生，不单独存状态。
- 回款计划状态全部派生，不落库。

### 4.5 删除约束

- 客户存在未作废合同时禁止删除。
- 产品被商机/合同行项目引用时禁止删除。
- 合同存在回款记录时禁止作废/删除；计划存在回款记录时禁止删除。
- 全部软删除；子记录不级联硬删，父记录删除后不可见。

## 5. API 清单

统一前缀 `/admin-api/crm`，沿用 `{code,data,msg}`、`pageNo/pageSize`、`Authorization: Bearer`。

| 模块 | 端点 | 权限码 |
|---|---|---|
| 产品分类 | `GET /product-category/tree`、`POST/PUT/DELETE /product-category` | `crm:product-category:*` |
| 产品 | `GET /product/page`、`GET /product/:id`、`POST/PUT/DELETE /product`、`PUT /product/status`、`GET /product/export-excel`、`POST /product/import` | `crm:product:*` |
| 客户 | `GET /customer/page`、`GET /customer/:id`、`POST/PUT/DELETE /customer`、`PUT /customer/transfer`、`PUT /customer/release`、`PUT /customer/claim`、`GET /customer/pool/page`、`GET /customer/export-excel`、`POST /customer/import` | `crm:customer:*`、`crm:customer:pool:view`、`crm:customer:claim`、`crm:customer:transfer` |
| 联系人 | `GET /contact/page`、`GET /contact/:id`、`POST/PUT/DELETE /contact`、`POST /contact/import` | `crm:contact:*` |
| 跟进 | `GET /follow-up/page`、`POST/PUT/DELETE /follow-up` | `crm:follow-up:*` |
| 商机 | `GET /opportunity/page`、`GET /opportunity/:id`、`POST/PUT/DELETE /opportunity`、`PUT /opportunity/stage` | `crm:opportunity:*` |
| 合同 | `GET /contract/page`、`GET /contract/:id`、`POST/PUT/DELETE /contract`、`PUT /contract/status`、`GET /contract/export-excel` | `crm:contract:*`、`crm:contract:approve` |
| 回款计划 | `GET /receivable/plan/page`、`POST/PUT/DELETE /receivable/plan`、`GET /receivable/plan/export-excel` | `crm:receivable:*` |
| 回款记录 | `GET /receivable/record/page`、`POST/DELETE /receivable/record` | `crm:receivable:*` |
| 仪表盘 | `GET /dashboard/summary` | `crm:dashboard:view` |

## 6. 前端页面

1. CRM 仪表盘：4 卡片 + 商机阶段漏斗 + 月度合同/回款趋势。
2. 产品管理：分类树 + 产品列表/表单。
3. 客户管理：列表、详情（联系人/跟进/商机/合同 tab）、新建/编辑、转移/释放/领取、导入导出。
4. 公海：列表、认领。
5. 商机管理：列表、详情、阶段推进、产品行项目。
6. 合同管理：列表、详情、行项目、状态流转、附件、导出。
7. 回款管理：计划列表、记录录入、逾期标识、导出。
8. 菜单与按钮权限由后端种子下发，权限码与 API 表一致。

## 7. 导入导出

- 导入：客户、联系人、产品；模板下载；5000 行/10MB 上限；先全量校验，有错整批拒绝并返回行号+原因。
- 导出：客户、合同、回款计划（含已收/逾期）。
- 导出遵循当前数据权限。

## 8. 验收标准

- `go build ./... && go vet ./... && go test ./...` 全绿；`make test-integration` 通过。
- `make migrate-up` 从空库建出全部表；seed 写入菜单、权限、演示数据。
- 一个销售能完成：建客户 → 加联系人 → 写跟进 → 建商机 → 推进阶段 → 建合同（行项目）→ 拆回款计划 → 登记回款 → 看仪表盘。
- 数据权限：销售只看自己/本部门；主管看下级；自定义部门生效；公海按权限可见。
- 公海：释放、认领、30 天自动回收、认领上限全部可验证；从未跟进的新客户按创建时间参与回收，认领上限按 owner 名下客户总数计算。
- 非法数据范围返回 400，绝不回退「全部」。
- Excel 导入校验错误可读；导出内容与权限一致。

## 9. 暂缓 / 开放问题

- OA 领域模型（公告、考勤、日程、会议室、文档）留到 CRM 交付后单独一轮。
- codegen 升级的详细设计在客户+联系人完成后单独一轮。
- 字典种子（行业、来源、客户级别、跟进类型、回款方式）由实现时提案，随代码评审。
- 通知/提醒（跟进提醒、公海回收通知）不在 CRM v1。
- 附件复用 `infra_file`，合同附件在 v1 内。
