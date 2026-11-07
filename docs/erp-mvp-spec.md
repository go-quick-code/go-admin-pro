# ERP MVP 规格与施工顺序

以 Go 在 go-admin-pro 内新建 ERP（进销存）模块，对标芋道 ruoyi-vue-pro 的 ERP 模块（master 分支，兼容契约 C1：只对齐领域概念与功能能力，不对齐 API/DDL/前端）。本文件是 ERP MVP 规则的唯一来源；面向 tracker 的摘要在 `docs/specs/erp-mvp.md`。域边界见 ADR 0015（主数据独立、业务流零打通），库存账模型见 ADR 0016（纯数量账 + 反审核反向流水），数量类型见 ADR 0017（毫单位定点）。

## 1. 范围与施工顺序

**范围（六大块，全量对标芋道 ERP）**：

1. 产品管理：产品分类（树）、产品单位、产品。
2. 库存管理：仓库、产品库存（仓库×产品余额）、出入库流水、其它入库单、其它出库单、库存调拨单、库存盘点单。
3. 采购管理：供应商、采购订单、采购入库、采购退货。
4. 销售管理：客户、销售订单、销售出库、销售退货。
5. 财务管理：结算账户、付款单、收款单。
6. ERP 首页统计：销售/采购汇总（今日/昨日/本月/今年 + 近 N 月趋势）。

**明确不做**（对齐芋道，均为芋道没有的能力）：成本核算与库存金额（ADR 0016 拒绝移动加权）、批次/序列号、多单位换算（单位只是名称字典）、多币种、税额计算（税率仅客户/供应商展示字段）、排行报表、移动端、总账/凭证/发票、bpm 审批接入。

**施工顺序**（单人串行，每步收口再进下一步）：

1. 骨架 + 主数据：`erp` 模块五层结构、qty 定点类型、序列表、分类/单位/产品/仓库/客户/供应商（手写确立样板）。
2. 库存核心：`erp_stock` + 流水 + 唯一入账入口（负库存禁令、SQL 双保险）+ 其它入库/出库单（用最简单据验证库存账）。
3. 采购三单：采购订单 → 采购入库 → 采购退货（进度回写与占用校验的第一套样板，其余单据复制此模式）。
4. 销售三单：同构复制。
5. 调拨 + 盘点。
6. 财务：结算账户 + 收付款单（核销回写与上限校验）。
7. 首页统计 + 库存报表页。
8. Excel 导出 + 前端整装 + 演示数据 + 全量验收。

## 2. 模块边界与依赖

- 新增 `internal/module/erp`，分层与 crm/oa 相同：`api/ dto/ model/ repository/ service/`；service 层按后缀拆 `*_rules.go`（校验）、`*_refs.go`（引用检查）。
- 依赖只指向平台层（system 权限中间件、Money/qty/pkg、租户插件、testutil），**不依赖 crm/oa/bpm**；crm/oa 也不依赖 erp。唯一跨域共享是平台底座（BaseEntity、租户、权限）。
- 单号生成不 import crm：在 erp 模块内实现同构的序列发放（新表 `erp_sequence`，机制复制 `crm_sequence`：事务内 `INSERT ... ON DUPLICATE KEY UPDATE last_value=last_value+1` 后同事务回读，唯一行锁串行化 + 一致加锁顺序防死锁）。
- 金额一律走 `internal/pkg/money`（decimal(12,2)、分定点、JSON 十进制字符串）；数量走新增 `internal/pkg/qty`（ADR 0017）。
- 权限中间件复用 system 的 `RequirePermission`，以函数字段注入 Handler；模块暴露 `RegisterProtected(rg)` 由 `internal/router` 挂载；app 装配按 repo→service→接缝→`api.New` 顺序加入 `internal/app/app.go`。

## 3. 表结构

通用：全部表带 BaseEntity 等价字段（`created_at/updated_at/deleted_at/creator/updater`）与 `tenant_id`；显式 `DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`；金额列 `decimal(12,2)`，数量列 `decimal(24,3)`。共 **34 张新表**，`EXPECTED_TABLES` 66 → **100**。

### 3.1 主数据（6）

- `erp_product_category`：`parent_id`（无限层级树）、`name`、`code`、`sort`、`status`。
- `erp_product_unit`：`name`、`status`。仅名称字典，无换算率。
- `erp_product`：`name`、`bar_code`、`category_id`、`unit_id`、`status`、`standard`（规格）、`expiry_day`（int，纯展示）、`weight`（decimal(12,3)，纯展示）、`purchase_price`/`sale_price`/`min_price`（Money 参考价，无逻辑）、`remark`。
- `erp_warehouse`：`name`、`address`、`principal`（负责人，纯展示）、`sort`、`status`。
- `erp_customer`：`name`、`contact`、`mobile`、`telephone`、`email`、`fax`、`remark`、`status`、`sort`、`tax_no`、`tax_percent`（decimal(5,2)，展示）、`bank_name`、`bank_account`、`bank_address`。
- `erp_supplier`：字段同 `erp_customer`。

### 3.2 库存（2）

- `erp_stock`：`product_id`、`warehouse_id`、`count` decimal(24,3) DEFAULT 0；租户内 UNIQUE`(tenant_id, product_id, warehouse_id)`；无金额列（ADR 0016）。
- `erp_stock_record`（流水，append-only，不更新不删除）：`product_id`、`warehouse_id`、`count`（正入负出）、`total_count`（变动后结存快照）、`biz_type`、`biz_id`、`biz_item_id`、`biz_no`。

### 3.3 采购（6）

- `erp_purchase_order` + `erp_purchase_order_items`：主表 `supplier_id`、`order_no`、`order_time`、`status`、`remark`；明细 `product_id`、`count`、`product_price`（Money）、`total_price`、`in_count`、`return_count`（回写进度，DEFAULT 0）。
- `erp_purchase_in` + `_items`：主表 `supplier_id`、`order_id`（必填，已审核采购订单）、`warehouse_id`、`in_time`、`status`、`total_price`（明细合计冗余）、`payment_price`（已核销回写，DEFAULT 0）；明细 `product_id`、`count`、`product_price`、`total_price`。
- `erp_purchase_return` + `_items`：主表 `supplier_id`、`in_id`（必填，已审核采购入库）、`warehouse_id`（取自入库单）、`return_time`、`status`、`total_price`、`refund_price`（DEFAULT 0）；明细同入库。

### 3.4 销售（6）

- `erp_sale_order` + `_items`：主表 `customer_id`、`sale_user_id`（销售员，纯记录）、`order_no`、`order_time`、`status`、`remark`；明细 `product_id`、`count`、`product_price`、`total_price`、`out_count`、`return_count`。
- `erp_sale_out` + `_items`：主表 `customer_id`、`order_id`（必填，已审核销售订单）、`warehouse_id`、`out_time`、`status`、`total_price`、`receipt_price`（DEFAULT 0）；明细同采购入库。
- `erp_sale_return` + `_items`：主表 `customer_id`、`out_id`（必填，已审核销售出库）、`warehouse_id`、`return_time`、`status`、`total_price`、`refund_price`；明细同销售出库。

### 3.5 杂项出入库 / 调拨 / 盘点（8）

- `erp_stock_in` + `_item`：主表 `warehouse_id`、`in_time`、`type`（字典）、`status`、`remark`；明细 `product_id`、`count`、`product_price`（记录用）。
- `erp_stock_out` + `_item`：同构，`out_time`、`type`。
- `erp_stock_move` + `_item`：主表 `from_warehouse_id`、`to_warehouse_id`（必须不同）、`move_time`、`status`；明细 `product_id`、`count`。
- `erp_stock_check` + `_item`：主表 `warehouse_id`、`check_time`、`status`、`remark`；明细 `product_id`、`actual_count`（实际清点数 ≥0）、`base_count`（创建时账面数快照，记录用）。

### 3.6 财务（5）

- `erp_finance_account`：`name`、`no`、`remark`、`status`、`sort`、`default_status`（默认账户租户内唯一互斥）；**无余额列**。
- `erp_finance_receipt` + `_items`（收款单）：主表 `customer_id`、`account_id`、`receipt_user_id`（经手人）、`receipt_time`、`status`、`remark`；明细 `biz_type`（销售出库 / 销售退货）、`biz_id`、`price`（本次核销金额）。
- `erp_finance_payment` + `_items`（付款单）：镜像；明细 `biz_type`（采购入库 / 采购退货）、`biz_id`、`price`。

### 3.7 序列（1）

- `erp_sequence`：PK`(tenant_id, biz_type, date_key)`，`last_value`。机制同 `crm_sequence`。

## 4. 规则

### 4.1 审核状态机与反审核

- 十类单据（采购订单/入库/退货、销售订单/出库/退货、其它入库/出库、调拨、盘点、收/付款单——共 12 类）共用一个审核语义：`status` 10=未审核、20=已审核；只有审核动作落账。
- 已审核：**不可修改、不可删除**（409）；反审核可退回 10。
- 未审核：可修改、可删除；不动库存、不回写进度、不核销金额。
- **反审核**（20→10）：新增一条反向流水回滚库存（每个 biz_type 成对定义 `_CANCEL`，`count` 取负，原流水不删除）；回退订单进度（重算 `in_count/out_count/return_count`，只计已审核单）；收付款单反审核释放核销额度并重算单据已收/已付。
- **反审核禁令**（409）：
  - 收款单已核销的销售出库/退货 → 该出库/退货单不可反审核；付款单同。
  - 订单存在已审核入库/出库/退货单 → 订单不可反审核。
  - 入库单存在已审核退货单 → 入库单不可反审核；出库单同。
- 审核与反审核需要独立权限码 `erp:<doc>:approve`。

### 4.2 库存唯一入口与负库存禁令

- 所有库存数量变动**只走一个入账函数**（事务内：校验 → 更新 `erp_stock` → 插入 `erp_stock_record`）。12 类单据的审核/反审核、调拨的双向变动、盘点的差异调整全部经它，任何代码不得直改 `erp_stock`。
- **负库存禁止**（应用层 + SQL 双保险）：出库方向 `UPDATE erp_stock SET count = count - ? WHERE ... AND count >= ?`，影响行数为 0 → 409（提示产品/仓库/当前库存/变更量）；事务内先取仓库×产品行锁，多仓库调拨按仓库 ID 升序加锁防死锁。
- 数量 > 0、金额 = 单价 × 数量（128 位中间量）为单据明细不变量；明细金额合计 = 主表 `total_price`。

### 4.3 库存流水

- 流水正入负出，附变动后 `total_count` 快照与业务单据引用（type/id/item_id/no）；反审核新增 `_CANCEL` 反向流水，原流水保留 → 任何时刻库存余额 = 初始 + 流水代数和，可审计重放。
- biz_type 全集：采购入库/退货、销售出库/退货、其它入库/出库、调拨入/调拨出、盘点调整，各配 `_CANCEL` 对。

### 4.4 采购进度与占用

- 入库/退货**必须挂已审核订单**（`order_id` 必填）；退货必须挂已审核入库单。无订单的收发走其它入库/出库单（此为对芋道的收紧，芋道来源单可空；差异记清单）。
- **占用校验**：订单（项）剩余可入 = `count` −（未审核 + 已审核入库数量）−（未审核 + 已审核退货数量）；超量 409。未审核单也计入占用，防止超卖超采。
- **进度回写**：`in_count/return_count` 只统计**已审核**单；审批/反审核时重算。
- 删除未审核入库/退货单自然释放占用；反审核同理。

### 4.5 销售进度与占用

- 同 4.4 镜像：出库/退货挂已审核销售订单，退货挂已审核出库单；`out_count/return_count` 占用与回写同构。

### 4.6 调拨与盘点

- 调拨：审核时同一事务内出仓 `count` 取负入账、入仓取正入账，两笔流水同额成对；反审核各生成反向流水。源仓不足 409；两仓必须不同。
- 盘点：明细填 `actual_count`（实际数）；审核时逐行按 `差异 = actual_count − 当时账面数` 经唯一入口调整（差正为盘盈入、差负为盘亏出，各一条流水）；审核后账面数 = 实际数。`base_count` 是创建时的账面快照，仅记录不参与计算（审核以当时账面为准）。

### 4.7 收付款核销

- 收款单明细只能挂**已审核**的销售出库（收）/销售退货（退）；付款单镜像挂采购入库/采购退货。
- **上限校验**（审核时 409）：单据明细 `price` 合计 + 该单据已被其它已审核收/付款核销的金额 ≤ 单据 `total_price`。
- 审核后回写：`sale_out.receipt_price` / `sale_return.refund_price` / `purchase_in.payment_price` / `purchase_return.refund_price` = 该单据被已审核收付款核销的合计；反审核重算。
- 结算账户：纯信息 + 默认账户唯一互斥（置默认时取消原默认）；**余额不落库**，账户列表提供"余额 = 该账户已审核收付款流水净额"的实时汇总查询列（对芋道的小步增强，纯展示）。

### 4.8 编号

- 单号 = 前缀 + `yyyyMMdd`（业务时区）+ 至少 4 位序号（超 9999 扩位），由 `erp_sequence` 事务内发放，客户端提交的单号一律不采纳。
- 前缀（2 字母拼音，对齐 CRM 的 KH/HT 风格）：采购订单 `CG`、采购入库 `RK`、采购退货 `RT`、销售订单 `XS`、销售出库 `CK`、销售退货 `ST`、其它入库 `QR`、其它出库 `QC`、调拨 `DB`、盘点 `PD`、收款单 `SK`、付款单 `FK`。`biz_type` 与表名一致。

### 4.9 删除与引用约束

- 单据：已审核不可删不可改；未审核可删。未审核单据已被下游单据引用时禁删（409，防悬挂）：采购订单被入库（含其退货）引用、采购入库被退货引用、销售订单被出库（含其退货）引用、销售出库被退货引用；须按引用方向逆序拆单后方可删除。
- 主数据被引用禁删（409）：产品被任何单据明细引用；客户/供应商被任何单据引用；仓库被 `erp_stock` 非零记录或任何流水引用；结算账户被收付款单引用；分类/单位被产品引用。（对芋道的收紧：芋道客户/供应商直接删；差异记清单。）
- 软删除；流水表不删。

### 4.10 数据权限

- 套用 ADR-0004：ERP 单据与主数据的 owner = 创建人（BaseEntity `creator` 之外如有 owner 语义，按 crm 的 owner 字段模式落地，可转移——ERP 无公海，owner 恒有值）；角色数据范围上限过滤列表与导出；统计接口同样过滤。
- 仓库不做独立权限（芋道同）；`principal` 仅展示。

### 4.11 Excel 导出

- 只做导出，不做导入（差异记清单）。产品、客户、供应商、库存、出入库流水、十二类单据列表提供导出；复用 `internal/pkg/excel` 的 `Export[T]` 与 `sendExcel` 模式（Money/qty 经 TextMarshaler 开箱即用，日期按业务时区），权限码 `erp:<entity>:export`。

## 5. API 清单

统一前缀 `/erp`，信封 `{code,data,msg}`，分页 `PageQuery/PageResult`，仓储固定 `id DESC`。

- **主数据**（分类/单位/产品/仓库/客户/供应商）：`GET page`（分类用 `GET list` 全量、前端组树）、`GET :id`、`POST`、`PUT :id`、`DELETE :id`、`GET export-excel`；产品单位/分类树形删除走引用校验。
- **单据**（12 类同构）：`GET page`（带状态/时间/单号/往来单位筛选）、`GET :id`（带头+明细+进度/核销信息）、`POST`、`PUT :id`（仅未审核）、`DELETE :id`（仅未审核）、`PUT update-status`（审核/反审核，幂等目标态）、`GET export-excel`。
- **库存**：`GET /erp/stock/page`（仓库×产品余额）、`GET /erp/stock/get-count`（产品全仓合计）、`GET /erp/stock-record/page`（流水按 biz_type/仓库/产品/时间筛选）、库存导出。
- **统计**：`GET /erp/sale-statistics/summary`、`/time-summary`；`GET /erp/purchase-statistics/summary`、`/time-summary`。口径：销售额 = Σ已审核销售出库 `total_price`（按 `out_time` 落区间）− Σ已审核销售退货 `total_price`（按 `return_time`）；采购 = 采购入库 − 采购退货（`in_time`/`return_time`）；summary 返回今日/昨日/本月/今年；time-summary 返回近 N 个月（默认 6）按月。

## 6. 前端页面

- 菜单驱动路由（`views/erp/<entity>/index.vue`），列表页用 `ProTable`（columns/form-items/search-items/api/perm-prefix），详情用 drawer + tabs + descriptions，明细行编辑器做成 ERP 共用组件（产品选择、数量、单价、行金额联动）。
- 页面清单：首页统计（卡片 + 趋势图，复用 CRM 仪表盘图表做法）；产品分类（树）、产品单位、产品、仓库；产品库存、出入库流水（查询页）；其它入库/出库、调拨、盘点；供应商、采购订单/入库/退货；客户、销售订单/出库/退货；结算账户（含余额列）、付款单、收款单。
- 单据页操作：新增/编辑/删除（未审核）、**审核 / 反审核**（`hasPerm('erp:<doc>:approve')`，反审核带确认）、导出；挂单字段（订单/入库/出库）只能从已审核单中选择。
- 权限显隐用 `userStore.hasPerm`；`pnpm type-check` + `pnpm build`。

## 7. 迁移号、菜单、权限码、种子

- **迁移**：00029 起按域拆 6 个迁移（主数据 / 库存+序列 / 采购 / 销售 / 杂项出入库 / 财务），每迁移一个主题、带 Down 段；`scripts/verify-migration.sh` 的 `EXPECTED_TABLES` 默认值改 **100**。
- **菜单**：写 seed 不写迁移（纪律：开工前 grep `seed.go` 去重再取号）。顶层 dir **700**（ERP 进销存）；分配：701 首页统计；710 产品分类、711 产品单位、712 产品；720 仓库、721 产品库存、722 出入库明细、723 其它入库、724 其它出库、725 库存调拨、726 库存盘点；730 供应商、731 采购订单、732 采购入库、733 采购退货；740 客户、741 销售订单、742 销售出库、743 销售退货；750 结算账户、751 付款单、752 收款单。按钮 = 菜单ID×10+n（query/create/update/delete/approve/export）。
- **权限码**：`erp:<entity>:query|create|update|delete|approve|export`（库存页 query/export，统计页 query）。内置普通角色（role id=2）**默认不授予**任何 ERP 菜单（财务与库存敏感，仅管理员与后续自定义角色）；seed 幂等策略沿用现有（菜单按 ID 只插缺失项）。
- **字典**：其它入库/出库类型字典（取值实施时对齐芋道字典种子）。
- **演示数据**：`make run-demo` 扩展 `erp_demo`（表哨兵幂等），跑通完整链路：建仓库/产品 → 采购订单 → 采购入库 → 盘点 → 销售订单 → 销售出库 → 收款 → 付款 → 首页统计可见。

## 8. 测试矩阵

### 8.1 Seam

- **主 seam：HTTP API + 真实 MySQL**。复用 `internal/testutil`（真实 router/JWT/权限/租户中间件，`//go:build integration`），走 `env.Request` 断言信封/错误码/编号格式；与 crm/oa 同构，不引入测试后门。按域拆文件：product / stock / purchase / sale / misc-stock（调拨盘点）/ finance / statistics 的 `*_http_integration_test.go`。
- **次 seam：纯函数表驱动测试**。qty 定点运算与解析、金额=单价×数量、占用/剩余计算、盘点差异、核销上限计算、编号日期键。
- **前端**：不新增组件测试框架；type-check + build + 手工冒烟。

### 8.2 重点场景

| 领域 | 场景 |
|---|---|
| 库存 | 审核才落账；出库超库存 409；**并发扣减**（两请求同时扣同仓同品，一成一 409，无负库存）；反审核新增反向流水、原流水保留、余额恢复；流水快照 `total_count` 正确 |
| 采购 | 审核回写 `in_count`（只计已审核）；未审核入库占用；超订单数量 409；退货挂入库、超入库量 409；订单反审核在有已审核入库时 409；入库反审核回滚进度与库存 |
| 销售 | 同采购镜像；出库后库存减少；销售退货回 `return_count` 与库存 |
| 调拨/盘点 | 调拨一增一减同额、同仓 409、源仓不足 409；盘点盘盈/盘亏调整、审核后账面=实际 |
| 财务 | 收款挂未审核出库 409；核销超总额 409；反审核收款释放额度并重算 `receipt_price`；出库有已审核收款时反审核 409；默认账户唯一互斥；余额列=净额 |
| 删除/引用 | 已审核单据改/删 409；产品/客户/供应商/仓库/账户被引用删 409；分类/单位被产品引用删 409 |
| 编号 | `^(CG\|RK\|RT\|XS\|CK\|ST\|QR\|QC\|DB\|PD\|SK\|FK)\d{8}\d{4,}$`；客户端伪造单号不采纳；跨日重置；租户隔离 |
| 统计 | 口径（出库−退货、时间区间）；今日/昨日/本月/今年；近 N 月趋势；数据权限过滤；租户隔离 |
| Excel | 导出列与中文文件名；金额/数量/日期格式（既有 CRM 约束体系） |
| 迁移/种子 | 空库迁移往返 `EXPECTED_TABLES=100`；菜单 700 段 seed 幂等；普通角色无 ERP 菜单 |

### 8.3 验收命令

```bash
make check
make test-integration
make verify-migration
make web-type-check
make web-build
```

## 9. 验收标准

1. 空库迁移往返通过，`EXPECTED_TABLES=100`；全部 ERP 表带租户/审计字段。
2. 五条验收命令全绿。
3. 演示数据可一键重建，`make run-demo` 后 ERP 首页统计、库存、单据、财务页面可见完整业务链路数据。
4. 六大块功能按本 spec 规则可用：主数据 CRUD + 引用约束；十二类单据从创建→审核→（反审核）→删除约束全链路；库存账全程无负数、可由流水重放验证；核销不超总额；编号规则成立。
5. 不变量均有 HTTP 集成测试覆盖（8.2 全表），普通角色默认无 ERP 权限。

## 10. 暂缓 / 开放问题

- **成本核算/移动加权/毛利**：backlog；上成本必须翻转 ADR 0016 的反审核策略，单独立项。
- **批次/序列号/多单位换算/多币种/税额计算**：芋道均无，暂缓；多单位将来需动全部单据明细，属大改。
- **bpm 审批接入 ERP 单据**：芋道 enum 有 TODO，本版不跟进（Q3 拍板）；将来作为 OA/bpm 的演示场景。
- **CRM↔ERP 打通**（合同→销售订单、客户/产品主数据归一）：ADR 0015 拒绝 v1，后续独立集成票。
- **默认仓标志**：调研未确认芋道仓库是否带 default；实施核实，有则按芋道补（唯一互斥）。
- **其它入库/出库类型字典取值**：实施时对齐芋道字典。
- **排行报表 / 移动端 / 应收应付账龄**：backlog。
- **与芋道差异清单**（写实现 PR 描述）：删除约束收紧（客户/供应商/仓库/账户）；出入库单必须挂已审核订单（芋道可空，保守收紧，其它出入库单可覆盖无单场景）；账户余额实时查询列；不做导入；不跟进 BPM TODO。
