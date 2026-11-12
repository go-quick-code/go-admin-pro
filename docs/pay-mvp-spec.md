# pay MVP 规格与施工顺序

以 Go 在 go-admin-pro 内新建 pay（支付）模块，对标芋道 ruoyi-vue-pro 的 pay 模块（master 分支，兼容契约 C1：只对齐领域概念与功能能力，不对齐 API/DDL/前端）。本文件是 pay MVP 规则的唯一来源；面向 tracker 的摘要在 `docs/specs/pay-mvp.md`。范围与渠道策略见 ADR 0021（mock/wallet 先行、SPI 预留、示例业务唯一消费方、凭证平台级），分层先例见 ADR 0010（平台级渠道/租户级数据）。

## 1. 范围与施工顺序

**范围（八域 16 表，全量对标芋道 pay 的 8 域 14 表，另加 `pay_app_channel`、`pay_sequence` 两张本仓分层所需表）**：

1. 支付应用：应用（app_key/回调地址）+ 应用渠道绑定 + 平台级渠道凭证配置。
2. 支付订单：消费方下单、收银台发起支付、渠道单号映射、超时关单。
3. 退款单：按订单多次部分退款、渠道原路退回。
4. 转账单：wallet/mock 渠道转出、示例提现消费方。
5. 回调通知：商户回调任务 + 执行日志 + job 重试。
6. 钱包：余额、流水（唯一入账）、充值单、充值套餐。
7. 示例业务：示例订单（下单→支付→退款）、示例提现（申请→审批→转账）。
8. 渠道：SPI 按芋道 PayClient 四动作定形；v1 实现 mock + wallet，14 渠道码预留。

**明确不做**（对齐 ADR 0021）：微信/支付宝渠道实现（待商户资源另立工单）、业务套件接入（ERP 收付款单/CRM 回款走 pay）、member 会员用户与 C 端、独立 H5 收银台、多币种（单币种人民币）、手续费计算（字段保留、恒 0）、渠道凭证加密（沿用明文+脱敏先例，风险已记录）、回调手动重发、钱包冻结/预占语义。

**施工顺序**（单人串行，每步收口再进下一步）：

1. 骨架 + 应用与渠道：`pay` 模块五层结构、序列表、平台渠道（14 行种子）、应用 CRUD、应用渠道绑定、凭证脱敏三段式。
2. 钱包域：钱包表 + 流水唯一入账入口（行锁 + 非负双保险）、我的钱包自助端点（先于收银台：钱包渠道支付要扣它）。
3. 订单与收银台：下单幂等、收银台页、submit 发起支付（渠道校验）、mock 渠道（模拟回调触发）、钱包渠道（扣减入账）、支付成功入账函数（建回调任务）。
4. 回调通知：商户回调 job（重试序列）+ demo 回调端点（验签）+ 超时关单 job。
5. 退款：部分退款累计、wallet 回充、退款回调链路。
6. 充值与套餐：充值单挂支付订单、套餐赠送、充值退款余额校验。
7. 转账与示例提现：转账单、wallet 渠道入账、示例提现审批→转账。
8. Excel 导出 + 前端整装 + 演示数据 + 全量验收。

## 2. 模块边界与依赖

- 新增 `internal/module/pay`，分层与 crm/erp 相同：`api/ dto/ model/ repository/ service/`；service 层按后缀拆 `*_rules.go`（校验）、`channel/`（渠道 SPI 与 mock/wallet 实现）。
- 依赖只指向平台层（system 权限中间件、money/pkg、租户插件、testutil），**不依赖 crm/erp/oa/bpm**；demo 示例业务在 pay 模块内（`service/demo.go`），不外挂。
- 出站 HTTP 无（v1 两渠道均内部）；商户回调与未来渠道 SDK 复用 `net/http` + 超时上限的先例模式（socialHTTP：10s 超时、1MB body 上限）。
- 单号生成：模块内实现同构序列（新表 `pay_sequence`，机制复制 `erp_sequence`：事务内 `INSERT ... ON DUPLICATE KEY UPDATE last_value=last_value+1` 后同事务回读）。
- 金额一律走 `internal/pkg/money`（decimal(12,2)、分定点、JSON 十进制字符串）；不引 qty。
- 权限中间件复用 `RequirePermission`；模块暴露 `RegisterProtected(rg)` 与 **`RegisterPublic(rg)`**（渠道回调、demo 回调两个公开端点）由 `internal/router` 挂载；app 装配按 repo→service→接缝→`api.New` 顺序加入 `internal/app/app.go`。
- job handler 注册进现有 `JobScheduler`：`payNotifyRetry`、`payOrderTimeoutClose` 均走 `RegisterSystem`（跨租户逐租户扫描，照公海回收模式）。

## 3. 表结构

通用：全部表带 BaseEntity 等价字段（`created_at/updated_at/deleted_at/creator/updater`）；租户级表带 `tenant_id`，平台级表（仅 `pay_channel`）不带；显式 `DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`；金额列走 Money（`decimal(12,2)`）。共 **16 张新表**，`EXPECTED_TABLES` 100 → **116**。状态数值对齐芋道枚举。

### 3.1 应用与渠道（3）

- `pay_channel`（**平台级**，登记进 `PlatformTables`）：`code`（UNIQUE，14 渠道码：`wx_pub/wx_lite/wx_app/wx_native/wx_wap/wx_bar/alipay_pc/alipay_wap/alipay_app/alipay_qr/alipay_bar/alipay_lite/mock/wallet`）、`status`（启用/停用）、`fee_rate` decimal(5,2)（纯记录，恒 0）、`remark`、`config` text（渠道凭证 JSON，查询脱敏/更新合并，照 message_channel 三段式）。种子预插 14 行：mock/wallet 启用、其余停用且 `config={}`。
- `pay_app`（租户级）：`app_key`（租户内 UNIQUE）、`name`、`status`、`remark`、`order_notify_url`、`refund_notify_url`、`transfer_notify_url`。
- `pay_app_channel`（租户级，绑定表）：`app_id`、`channel_code`；UNIQUE`(tenant_id, app_id, channel_code)`。应用可用渠道 = 绑定码 ∈ 平台启用渠道。

### 3.2 交易（5）

- `pay_order`：`app_id`、`channel_code`、`channel_user_id`、`channel_order_no`、`user_id`、`user_type`（2=管理员，1=会员预留）、`merchant_order_id`（消费方单号，UNIQUE`(tenant_id, app_id, merchant_order_id)` 幂等键）、`subject`、`body`、`notify_url`（下单时从应用取快照，可覆盖）、`price`（实付，服务端定）、`channel_fee_rate`/`channel_fee_price`（恒 0）、`status`（0 待支付/10 成功/20 已退款/30 已关闭）、`user_ip`、`expire_time`、`success_time`、`extension_id`、`no`（ZF 单号）、`refund_price`（已退累计，DEFAULT 0）。
- `pay_order_extension`（一次发起尝试一条）：`no`、`order_id`、`channel_code`、`user_ip`、`status`（0 处理中/10 成功/20 失败）、`channel_extras` JSON、`channel_error_code`、`channel_error_msg`、`channel_notify_data`（末次回调原文）。
- `pay_refund`：`no`（TK）、`app_id`、`channel_code`、`order_id`、`order_no`、`user_id`/`user_type`、`merchant_order_id`、`merchant_refund_id`（UNIQUE`(tenant_id, app_id, merchant_refund_id)`）、`notify_url`、`status`（0 待处理/10 成功/20 失败）、`pay_price`（原单实付快照）、`refund_price`（本次）、`reason`、`user_ip`、`channel_order_no`、`channel_refund_no`、`success_time`、`channel_error_code`/`channel_error_msg`/`channel_notify_data`。
- `pay_transfer`：`no`（ZZ）、`app_id`、`channel_code`、`user_id`/`user_type`（可空，外部账户无归属）、`merchant_transfer_id`（UNIQUE`(tenant_id, app_id, merchant_transfer_id)`）、`subject`、`price`、`user_account`、`user_name`、`status`（0 等待/5 进行中/10 成功/20 关闭）、`success_time`、`notify_url`、`user_ip`、`channel_extras` JSON、`channel_transfer_no`、`channel_error_code`/`channel_error_msg`/`channel_notify_data`。
- `pay_sequence`：`id` 自增 + UNIQUE`(tenant_id, biz_type, date_key)`、`last_value`。机制同 `erp_sequence`。

### 3.3 回调（2）

- `pay_notify_task`：`app_id`、`type`（1 支付/2 退款/3 转账）、`data_id`（对应单据 ID）、`merchant_order_id`/`merchant_refund_id`/`merchant_transfer_id`、`status`（0 等待/10 成功/20 彻底失败/21 请求成功但结果失败/22 请求失败）、`next_notify_time`、`last_execute_time`、`notify_times`、`max_notify_times`（=9）、`notify_url`。
- `pay_notify_log`（append-only）：`task_id`、`notify_times`（第几次）、`response`（响应摘要，截断 1KB）、`status`。

### 3.4 钱包（4）

- `pay_wallet`：`user_id`、`user_type`、`balance`、`total_recharge`、`total_expense`；UNIQUE`(tenant_id, user_id, user_type)`。**无冻结列**（v1 无冻结语义，差异清单）。
- `pay_wallet_transaction`（append-only）：`no`（LS）、`wallet_id`、`biz_type`（1 充值/2 充值退款/3 支付/4 支付退款/5 余额调整/6 转账）、`biz_id`（业务单据 ID）、`title`、`price`（正入负出）、`balance`（变动后快照）。
- `pay_wallet_recharge`：`wallet_id`、`total_price`（=pay+bonus）、`pay_price`、`bonus_price`、`package_id`（可空）、`pay_status`、`pay_order_id`、`pay_channel_code`、`pay_time`、`pay_refund_id`、`refund_status`（0 无/10 成功/20 失败）、`refund_total_price`/`refund_pay_price`/`refund_bonus_price`、`refund_time`。
- `pay_wallet_recharge_package`：`name`、`pay_price`、`bonus_price`、`status`。

### 3.5 示例（2）

- `pay_demo_order`：`user_id`、`spu_id`（演示商品硬编码字典，不建表）、`spu_name`、`price`、`pay_status`、`pay_order_id`、`pay_time`、`pay_channel_code`、`pay_refund_id`、`refund_price`、`refund_time`。
- `pay_demo_withdraw`：`subject`、`price`、`user_account`、`user_name`、`type`（1 钱包/2 支付宝（预留））、`status`（0 等待/10 成功/20 关闭）、`pay_transfer_id`、`transfer_channel_code`、`transfer_time`、`transfer_error_msg`。

## 4. 规则

### 4.1 渠道 SPI 与注册表

- `service/channel` 定义 SPI（概念等价芋道 PayClient）：`Code() string`、`Order(ctx, OrderReq) (OrderResp, error)`（统一下单，返回 displayMode：`none` mock 等回调 / `qrcode|url` 预留）、`ParseOrderNotify(ctx, req) (OrderNotify, error)`（验签+解析，产出成功/失败与渠道单号）、`Refund(ctx, RefundReq) (RefundResp, error)`、`ParseRefundNotify(ctx, req) (RefundNotify, error)`、`Transfer(ctx, TransferReq) (TransferResp, error)`（#137 追加的第五动作，wallet/mock 同步完成、无异步解析动作——异步渠道回调随真实渠道实现接入）。渠道配置为各渠道多态结构（`walletConfig{}`、`mockConfig{}`，mock 退款结果经 `refundResult` 键配置），JSON 存 `pay_channel.config`。
- 注册表 `map[string]Client`：v1 注册 mock、wallet；未实现渠道在「应用绑定渠道」与「submit」处即报 409「渠道未实现」。补渠道实现不改 SPI。
- 渠道码常量对齐芋道枚举 14 个；未实现渠道不得绑定到应用（绑定校验拒绝）。

### 4.2 下单与收银台

- 下单 `POST /pay/order/create`：服务端定金额（`price` 不收客户端覆盖业务价的路径——消费方服务端传入并自负其责）、写 `merchant_order_id` 幂等（撞 UNIQUE 返回原单不报错）。`notify_url` 默认取应用对应类型回调地址。
- 收银台页：`GET /pay/cashier/order?appKey&merchantOrderId`（登录 + 本人订单校验）返回订单与可用渠道（应用绑定 ∧ 平台启用 ∧ 已实现）；`POST /pay/order/submit {orderId, channelCode}`：校验订单 0 待支付且未过期 → 创建 extension（处理中）→ 渠道 `Order`。
  - **wallet 渠道**：同事务内扣钱包（见 4.3）成功 → 订单置 10、`success_time`、写 extension 成功、创建支付回调任务；余额不足 409。
  - **mock 渠道**：`Order` 返回 `displayMode=none`，订单停在待支付；收银台展示「模拟回调」按钮（成功/失败），触发 `POST /pay/mock/notify`（登录 + 订单归属本人 + 订单渠道为 mock），走与真实渠道回调完全相同的解析→入账→建任务链路。
- 过期订单 submit 409；待支付订单可重复 submit（每次新 extension，wallet 渠道余额不足不产生部分扣减）。
- 支付成功入账（三条路径共用一个函数）：订单状态认领 `UPDATE ... WHERE id=? AND status=0`（0 行 = 已处理，幂等成功返回）→ `success_time` → extension 置成功 → `pay_notify_task` 建任务（type=1）。

### 4.3 钱包唯一入账入口

- 所有余额变动**只走一个入账函数**（事务内：锁 wallet 行 → 校验 → 条件 UPDATE → 插流水）：`UPDATE pay_wallet SET balance = balance + ? ... [AND balance + ? >= 0]`，影响 0 行 → 409（提示当前余额/变更量）；流水插 `pay_wallet_transaction`（快照变动后 `balance`）。任何代码不得直改 `pay_wallet.balance`。出向（支付、充值退款扣回）条件 UPDATE 双保险，对齐 ERP 负库存禁令。
- 懒创建：首次支付/查询无钱包则插入（UNIQUE 兜底并发，冲突重读）。
- 流水 `biz_type` 六类（充值/充值退款/支付/支付退款/余额调整/转账）；`UPDATE_BALANCE`（5）预留枚举，v1 无管理端调余额入口（差异清单）。
- 支付：`price` 负向入账（biz=3）；退款回加（biz=4）；充值入账 `pay_price + bonus_price`（biz=1）；充值退款扣回 `refund_total_price`（biz=2，校验扣后 ≥0）；转账入账（biz=6）。

### 4.4 退款

- 发起：挂订单须 10 成功；`refund_price` > 0 且 `order.refund_price + 本次 ≤ price`（409 超额）；`merchant_refund_id` 幂等同下单；创建退款单 0 待处理 → 渠道 `Refund`。
  - **wallet 渠道**：同事务回加余额（biz=4）→ 退款单 10 成功 → 订单 `refund_price` 累计 → 建退款回调任务（type=2）。
  - **mock 渠道**：模拟成功/失败两分支（demo 退款默认走 mock 成功；失败分支供测试）。
- 全额退完（累计 = price）订单置 20 已退款；部分退款订单保持 10。订单 20/30 后禁止再发起退款。
- 退款成功后 `pay_wallet_recharge` 联动见 4.7。

### 4.5 商户回调任务与重试

- 支付/退款/转账成功统一建任务（type=1/2/3），`max_notify_times=9`；重试间隔序列 `{15, 15, 30, 180, 1800, 1800, 1800, 3600}` 秒（第 N 次失败后取第 N 个间隔；首次 + 8 重试共 9 次，第 9 次仍失败置 20 彻底失败）——照芋道 `NOTIFY_FREQUENCY`。
- job `payNotifyRetry`（每分钟，RegisterSystem 逐租户）：扫 `status IN (0,21,22) AND next_notify_time <= now`；POST JSON 到 `notify_url`，header 带 `Pay-Signature = HMAC-SHA256(rawBody, 服务端回调密钥)`（密钥来自 configs，缺省值仅 dev；缺失时 job 拒发并记日志——fail-closed 照阿里云 endpoint 先例）；每次执行写 `pay_notify_log`；HTTP 2xx 且响应体 `{"code":0}` → 任务 10 成功并联动消费方状态；2xx 但业务码非 0 → 21；超时/非 2xx → 22；21/22 均继续重试。
- 同一任务并发认领：`UPDATE ... WHERE id=? AND status IN (0,21,22)` 条件更新防重发。

### 4.6 渠道回调端点（公开）

- `POST /pay/channel-notify/:channelCode` 公开路由（`RegisterPublic`，挂 IP 限速照社交回调先例）：按渠道码取 Client 验签解析 → 复用 4.2 支付成功入账函数（幂等）→ 按 yudao 语义响应渠道要求的应答体。验签失败 4xx；未实现/停用渠道 404。
- mock 触发端点 `POST /pay/mock/notify` **不公开**：登录 + 订单归属本人 + 渠道为 mock（防代他人确认支付）。
- 公开端点不记访问日志（router 现有结构使然）；安全审查条目见 §7。

### 4.7 充值与套餐

- 充值单创建：钱包本人发起，选套餐（`pay_price`/`bonus_price` 快照进单）或自定义 `pay_price`（`bonus_price`=0）；同步创建 `pay_order`（subject=钱包充值，挂本人 user_id/user_type）。
- **充值禁用钱包渠道**（submit 时按 `pay_wallet_recharge.pay_order_id` 反查校验，409：自引用扣→充净零，防套利绕圈）。
- 支付成功回调（type=1 命中充值单）：钱包入账 `pay_price + bonus_price`（biz=1，total_recharge 累计）+ 充值单 `pay_status=1/pay_time`。
- 充值退款：发起退款挂充值单对应支付订单；成功回调（type=2）扣回钱包 `refund_total_price`（校验扣后 ≥0，否则退款单置 20 失败——余额不足以退时钱已退渠道会负资产，v1 用「先校验后退款」挡住）、`refund_*` 三列回写、total_recharge 相应调减。

### 4.8 转账与示例提现

- 转账发起（消费方服务端）：`merchant_transfer_id` 幂等；wallet 渠道 `user_account` = 目标用户标识（user_id），同事务入账目标钱包（biz=6）+ 置 10 成功 + 建回调任务（type=3）；mock 渠道模拟成功。**转账不校验应用渠道绑定**（#137 决策：绑定守卫是收银台 submit 的支付语义，示例提现审批直接以配置应用发起转账，绑定与否不影响资金事实）。转账为平台出账，v1 无平台资金账本（差异清单）。
- 示例提现：申请（WAITING）→ 管理端审批 `PUT /pay/demo-withdraw/:id/approve`（权限 `pay:demo-withdraw:approve`）→ 创建转账单 → 转账成功回调联动提现单 10 成功；拒绝/关闭置 20。

### 4.9 编号

- 单号 = 前缀 + `yyyyMMdd`（业务时区）+ 至少 4 位序号（超 9999 扩位），`pay_sequence` 事务内发放，客户端提交的 `no` 一律不采纳；`merchant_*` 单号由消费方提供并作幂等键。
- 前缀：支付订单 `ZF`、退款单 `TK`、转账单 `ZZ`、钱包流水 `LS`；extension `no` 复用支付订单号加发起序（`ZF...-1`）。`biz_type` 与表名一致。

### 4.10 删除与引用约束

- 应用：有订单/退款/转账引用禁删（409）；有渠道绑定须先解绑。渠道绑定可增删（纯绑定关系）。
- 平台渠道：**禁删**（14 行种子常驻，只启停）；停用后已有订单不受影响，新 submit 拒绝。
- 充值套餐：被充值单引用禁删（软删同义，409）。
- 交易单据（订单/退款/转账/流水/回调任务/日志）不提供删除——资金事实 append-only；列表不给出删除按钮。
- 钱包不可删（有流水即事实）；用户停用后钱包留存。

### 4.11 数据权限与自助端点

- pay 管理页（应用/订单/退款/转账/回调/钱包/套餐）为管理员域，**不设 owner 行级数据权限**（记录无创建人语义；ADR 0004 不适用，记差异）；越权防线靠归属校验（4.2/4.6/4.7 本人订单）与租户隔离。
- 自助端点（我的钱包/我的流水/我的充值/发起充值/收银台/submit/mock 触发/demo 创建）只挂登录，服务层强制 `user_id` 归属过滤；管理端点挂权限码。

### 4.12 Excel 导出

- 只做导出：支付订单、退款单、转账单、钱包流水、充值单列表；复用 `internal/pkg/excel` 的 `Export[T]` 与 `sendExcel`（Money 经 TextMarshaler 开箱即用，日期按业务时区），权限码 `pay:<entity>:export`。

## 5. API 清单

统一前缀 `/pay`，信封 `{code,data,msg}`，分页 `PageQuery/PageResult`，仓储固定 `id DESC`，更新 `PUT /pay/<entity>`（id 在 body，与 CRM/ProTable 一致）。

- **应用**：`GET /pay/app/page`、`GET /pay/app/:id`、`POST /pay/app`、`PUT /pay/app`、`DELETE /pay/app/:id`；`GET /pay/app/:id/channels`、`PUT /pay/app/:id/channels`（全量替换绑定码数组）。
- **渠道（平台级，requirePlatform 照 message_channel 先例）**：`GET /pay/channel/page`、`PUT /pay/channel/:id`（config 脱敏合并）；租户侧只读 `GET /pay/channel/list`（code/名称/状态，无凭证）。
- **订单**：`POST /pay/order/create`（登录）、`GET /pay/order/page`（管理）、`GET /pay/order/:id`、`GET /pay/cashier/order`（本人）、`POST /pay/order/submit`（本人）、`GET /pay/order/export-excel`。
- **退款**：`POST /pay/refund/create`、`GET /pay/refund/page`、`GET /pay/refund/:id`、`GET /pay/refund/export-excel`。
- **转账**：`POST /pay/transfer/create`、`GET /pay/transfer/page`、`GET /pay/transfer/:id`、`GET /pay/transfer/export-excel`。
- **回调通知**：`GET /pay/notify-task/page`、`GET /pay/notify-task/:id`（含日志）。
- **钱包**：`GET /pay/wallet/page`（管理）、`GET /pay/wallet/my`、`GET /pay/wallet-transaction/page`（管理）、`GET /pay/wallet-transaction/my`、`GET /pay/wallet-transaction/export-excel`。
- **充值**：`POST /pay/wallet-recharge/create`（本人）、`GET /pay/wallet-recharge/page`（管理）、`GET /pay/wallet-recharge/my`、套餐 CRUD `/pay/wallet-recharge-package`、`GET /pay/wallet-recharge-package/list`（自助充值面板数据源）。
- **示例**：`POST /pay/demo-order/create`、`GET /pay/demo-order/page`、`PUT /pay/demo-order/:id/refund`、`GET /pay/demo-order/spus`（示例订单页商品字典数据源）、`POST /pay/demo-withdraw/create`、`GET /pay/demo-withdraw/page`、`PUT /pay/demo-withdraw/:id/approve`；mock 触发 `POST /pay/mock/notify`。
- **公开端点**：`POST /pay/channel-notify/:channelCode`、`POST /pay/demo/notify/order`、`POST /pay/demo/notify/refund`、`POST /pay/demo/notify/transfer`（demo 回调三端点验 `Pay-Signature`）。

## 6. 前端页面

- 菜单驱动路由（`views/pay/<entity>/index.vue`），列表页用 `ProTable`，详情 drawer + descriptions；收银台为布局内独立操作页（照 OA 打卡页形态：订单信息卡 + 渠道选择 + 模拟回调按钮）。
- 页面清单（12 页；#136 按自助端点补充 823 我的充值，spec §7 菜单清单未给自助页留槽位——收口决策记录于菜单种子）：支付应用（详情含渠道绑定 tab + 凭证只读脱敏展示）、支付订单、退款单、转账单、回调通知（任务列表 + 日志子表）、渠道配置（平台级）、钱包管理（钱包列表 + 流水抽屉）、充值套餐、我的充值（823 自助：发起充值 + 我的充值单）、示例订单（发起支付/发起退款）、示例提现（审批/关闭）、收银台。
- 交易单据页无新增/编辑/删除按钮（append-only）；示例订单/提现的操作按钮走独立权限码；`hasPerm` 显隐；`pnpm type-check` + `pnpm build`。

## 7. 迁移号、菜单、权限码、种子、安全审查

- **迁移**：00035 应用与渠道（`pay_channel/pay_app/pay_app_channel/pay_sequence`，4 表）、00036 钱包（`pay_wallet/pay_wallet_transaction`，2 表）、00037 交易与回调（`pay_order/pay_order_extension/pay_refund/pay_transfer/pay_notify_task/pay_notify_log`，6 表）、00038 充值（`pay_wallet_recharge/pay_wallet_recharge_package`，2 表）、00039 示例（`pay_demo_order/pay_demo_withdraw`，2 表）；每迁移一个主题、带 Down 段；`EXPECTED_TABLES` 改 **116**。
- **租户登记**：`TenantTables` += 15 张租户表；`PlatformTables` += `pay_channel`（"平台级支付渠道凭证配置"）——`VerifyTenantTables` 启动强校验（ADR 0019）。
- **菜单**：顶层 dir **800**（支付中心）；分配：810 支付应用、811 支付订单、812 退款单、813 转账单、814 回调通知、820 渠道配置（平台）、821 钱包管理、822 充值套餐、823 我的充值（#136 补充分配的自助页）、830 示例订单、831 示例提现、840 收银台。按钮 = 菜单ID×10+n（n=1..5：create/update/delete/approve/export，按页面实际授予；query 为菜单权限码）。纪律照旧：开工前 grep `seed.go` 去重。
- **权限码**：`pay:app:*`、`pay:channel:query|update`（平台）、`pay:order:query|export`、`pay:refund:query|export`、`pay:transfer:query|export`、`pay:notify-task:query`、`pay:wallet:query|export`、`pay:wallet-recharge-package:*`、`pay:wallet-recharge:query`、`pay:demo-order:create|query|refund`、`pay:demo-withdraw:create|query|approve|close`（refund/close 为页面操作按钮补种的独立码，决策记录见菜单种子注释）。内置普通角色（role id=2）**默认不授予**任何 pay 菜单（资金敏感，照 ERP 先例）。
- **job 种子**：`payNotifyRetry`（启用，每分钟）、`payOrderTimeoutClose`（启用，每 5 分钟，扫 0 待支付且 `expire_time < now` 置 30 已关闭）。
- **演示数据**：`make run-demo` 扩展 `pay_demo`（表哨兵幂等，行 ID 5001 起）：演示应用 1 个（app_key=demo，绑定 mock/wallet）、充值套餐 2 档、示例订单与示例提现若干、演示钱包（管理员本人）与流水；跑通下单→收银台→mock 回调→商户回调→退款全链路。
- **安全审查（强制专项）**：渠道凭证脱敏不回显；公开回调端点（限速/验签/幂等/应答体不泄内部信息）；商户回调 HMAC 签名与 fail-closed；金额全部服务端定（收银台/submit 不收价格）；越权矩阵（跨租户/跨应用/非本人订单/mock 代付/回调伪造）；日志与 Excel 不落凭证；SQL 注入面（唯一入账入口的参数化 UPDATE）。

## 8. 测试矩阵

### 8.1 Seam

- **主 seam：HTTP API + 真实 MySQL**。复用 `internal/testutil`（`//go:build integration`），走 `env.Request` 断言信封/错误/单号格式；按域拆文件：app_channel / order_cashier / wallet / notify / refund / recharge / transfer_demo / public_endpoint 的 `*_http_integration_test.go`。
- **次 seam：纯函数表驱动测试**。重试间隔序列（第 N 次失败 → 下一间隔、第 9 次置失败）、单号日期键、部分退款累计判定、钱包变动校验。
- **job seam**：`payNotifyRetry`/`payOrderTimeoutClose` 以 handler 直调（构造到期任务/过期订单），不走真实 cron。
- **前端**：type-check + build + 手工冒烟。

### 8.2 重点场景

| 领域 | 场景 |
|---|---|
| 下单 | 幂等（同 merchant_order_id 返回原单）；跨应用/跨租户同号不冲突；金额服务端定 |
| 收银台 | 过期 submit 409；未绑定渠道 409；停用渠道 409；未实现渠道 409；重复 submit 新 extension；订单关闭后 submit 409 |
| 钱包 | 支付下单即扣；余额不足 409；**并发双扣**（两请求同钱包，一成一 409，无负余额）；流水快照 balance 正确；懒创建并发（两请求同触发，单钱包行）；退款回加 |
| 回调 | mock 触发走全链路（订单 SUCCESS → 任务建立 → job POST → demo 回调 → 状态联动）；重试序列逐跳断言；9 次后 20 失败；重复回调幂等（状态认领 0 行不重复入账/重复建任务）；签名伪造 4xx；签名正确 2xx |
| 退款 | 部分退款累计 ≤ 实付（超 409）；全额退完订单置 20、部分退保持 10；已退款/已关闭订单再退 409；wallet 退款回加并发安全 |
| 充值 | wallet 渠道充值 409；套餐赠送入账（balance、total_recharge）；充值退款余额不足拒（409 不发起）；成功扣回三列回写 |
| 转账 | 审批后创建转账单；wallet 渠道入账目标钱包（biz=6）；回调联动提现单成功；merchant_transfer_id 幂等 |
| 越权 | 非本人订单收银台/submit/mock 触发 403；跨租户订单/钱包 404；公开端点无登录可达 |
| 编号 | `^(ZF|TK|ZZ|LS)\d{8}\d{4,}$`；跨日重置；租户隔离；客户端伪造 no 不采纳 |
| Excel | 导出列与中文文件名；Money/日期格式（既有约束体系） |
| 迁移/种子 | 空库迁移往返 `EXPECTED_TABLES=116`；租户表校验通过（pay_channel 平台级、14 行种子）；菜单 800 段 seed 幂等；普通角色无 pay 菜单 |

### 8.3 验收命令

```bash
make check
make test-integration
make verify-migration
make web-type-check
make web-build
```

## 9. 验收标准

1. 空库迁移往返通过，`EXPECTED_TABLES=116`；15 张租户表 + 1 张平台表登记校验通过。
2. 五条验收命令全绿。
3. 演示数据一键重建：`make run-demo` 后可完整走通 示例下单 → 收银台选渠道 → mock 模拟回调 → 商户回调重试 → demo 状态联动 → 发起退款 → 钱包充值（套餐赠送）→ 示例提现审批转账，全部状态与流水正确。
4. 八域功能按本 spec 规则可用：应用与渠道绑定、下单幂等、钱包唯一入账（无负余额、快照可重放）、部分退款累计、回调重试序列、超时关单。
5. 不变量均有 HTTP 集成测试覆盖（8.2 全表）；普通角色默认无 pay 权限；公开端点攻击面经安全审查专项核验。

## 10. 暂缓 / 开放问题

- **微信/支付宝渠道实现**：SPI 已定形，待商户资源另立工单；届时补渠道 config 结构与回调验签，不动内核。
- **业务套件接入**：ERP 收付款单/CRM 回款对接 pay 收银，另立里程碑（涉及核销双向状态）。
- **member 会员**：钱包 user_type=1 预留；member 模块立项时同表扩展。
- **渠道凭证加密**：全局密钥加密工单（message/social/pay 三处一起收口），本期风险显式接受。
- **手续费计算**：字段保留恒 0；随真实渠道实现一并落地。
- **回调手动重发**：管理端暂无入口；任务失败可由 job 自然重试，必要时补「立即重试」按钮。
- **钱包手动调余额**：`UPDATE_BALANCE` 类型预留无入口；运营需要时以唯一入账入口加管理端按钮，另立小票。
- **与芋道差异清单**（写实现 PR 描述）：`pay_channel` 平台级 + `pay_app_channel` 绑定表（芋道每应用各配渠道）；新增 `pay_sequence`；drop `channel_id` 只存 `channel_code`；钱包无 `freeze_price`（无冻结语义）；转账无平台资金账本；管理端无手动调余额/手动重发；错误码 HTTP 语义；单号前缀 ZF/TK/ZZ/LS。
