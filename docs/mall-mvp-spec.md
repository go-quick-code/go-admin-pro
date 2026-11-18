# mall MVP 规格与施工顺序

以 Go 在 go-admin-pro 内新建 mall（商城）模块，对标芋道 ruoyi-vue-pro 的 mall/member 模块（master 分支，兼容契约 C1：只对齐领域概念与功能能力，不对齐 API/DDL/前端）。本文件是 mall MVP 规则的唯一来源；面向 tracker 的摘要在 `docs/specs/mall-mvp.md`。模块注册形状见 ADR 0024（Registrars/JobSpecs/前缀在场测试 + 401 探测网），编号原语见 ADR 0022（`internal/pkg/sequence.Next`），权限码收敛见 ADR 0023，租户表登记见 ADR 0019。

> **⚠️ 本文为 AFK 产物**（所有者睡前授权「直接实现 mall」，交互式 grilling 不可用）。§7 列出全部默认裁决，晨间复核后如有推翻，按推翻后口径修订并返工受影响票。

## 1. 范围与施工顺序

**范围（三域 14 表）**：芋道 mall 共 70+ 表（member_/product_/trade_/promotion_ 四前缀），MVP 取 member 会员、product 商品、trade 交易三域的管理端全量 + C 端最小自助闭环（注册/登录/商品浏览/购物车/下单/支付/我的订单/我的积分）；**promotion 营销域整体不做**（对齐 pay 先例的「明确不做」段）。会员/商品/订单均为租户级数据。

1. 会员域：会员用户（mobile + bcrypt 密码）、等级、收货地址、积分（配置 + 流水唯一入账入口）。
2. 商品域：分类（两级树）、品牌、属性（名+值）、SPU/SKU（多规格 JSON 快照、库存行锁）。
3. 交易域：购物车（加购合并）、订单（下单扣库存、编号 SO、收银台接 pay、取消、超时关单）、订单项（成交快照）。
4. 支付联动：mall 作为 pay `OrderSuccessHandler` 扩展点第二消费方（充值是第一），同事务置订单已支付；支付应用种子 `app_key=mall`。
5. C 端最小自助：会员注册/登录（公开组 + 模块内会员令牌自校验）、我的订单/购物车/积分端点。
6. 管理端：会员/等级/地址/积分调整、分类/品牌/属性/SPU 管理、订单列表/详情/取消/完成。
7. 超时关单 job（JobSpecs 三处声明，照 payOrderTimeoutClose 先例）。
8. 前端整装（api/mall.ts + views/mall 三域页）+ 演示数据 + 全量验收。

**明确不做**：promotion 域全量（优惠券/满减/秒杀/拼团/砍价/积分商城）、售后 after_sale、配送 delivery 与运费模板（postage 字段保留恒 0，照 pay fee_rate 先例）、分销/佣金、评价/收藏、C 端首页装修与搜索、发票、多币种（单币种人民币）、member 经验值体系（等级纯手工维护）、注册验证码（复用 system captcha 可后接）、真实 C 端 uniapp/H5（本仓前端仍是 admin SPA，C 端以 HTTP 端点交付能力）。

**施工顺序**（单人串行，每步收口再进下一步）：

1. 骨架 + 会员域：模块五层、迁移、`member_user/level/address/point_config/point_record`、管理端 CRUD + 积分调整（唯一入账入口）。
2. 商品域：分类/品牌/属性/SKU 价格库存校验规则 + SPU/SKU 管理端（上下架、多规格 JSON）。
3. 交易域（管理侧先收口）：购物车、下单（扣库存+订单项快照+编号）、订单列表/详情/取消/完成、超时关单 job。
4. 支付联动：pay app 种子、下单创建支付订单、收银台接线、`SetOrderSuccessHandler` 同事务联动（订单已支付）。
5. C 端自助：注册/登录/令牌、商品浏览、购物车、下单、支付（内部转 pay）、我的订单/积分。
6. 前端整装 + 演示数据 + Excel 导出 + 全量验收。

## 2. 模块边界与依赖

- 新增 `internal/module/mall`，分层与 crm/erp/pay 相同（`api/ dto/ model/ repository/ service/`）；service 按域拆 `member.go/product.go/trade.go` + `*_rules.go` 纯校验。
- 依赖指向平台层：system（权限中间件）、money、sequence、ratelimit、testutil；**依赖 pay service**（mall 是 pay 的业务消费方，pay 在注册器列表中属平台能力域，不违反 ADR 0015 的业务套件零打通——pay 对 crm/erp/oa 仍零依赖，mall→pay 单向）。pay 侧零改动（`SetOrderSuccessHandler` 是既有扩展点）。
- 路由：管理端 `/mall/member/* /mall/product/* /mall/trade/*` 挂受保护组（`RegisterProtected`）；C 端 `/mall/app/*` 挂公开组（`RegisterPublic`，无登录、限速）——**会员令牌在 handler 内自校验**（api 层 helper 解析 Bearer 签名令牌 → 查 member_user 校验 status），不动 router 三组结构与 Backbone（C 端身份体系完整形态留后续票）。令牌 = HMAC-SHA256 签名的 `memberID.tenantID.exp`，密钥 `config.mall.jwt_secret`（空则拒启动，fail-closed）。
- 编号：`internal/pkg/sequence.Next`（ADR 0022 单源），biz_type=订单编号，前缀 `SO`。
- 金额一律 `internal/pkg/money`；积分是 int（不动 money）。
- app 装配：repo → service（`SetLocation`/`SetPaySvc`/`SetOrderSuccessHandler` 接线）→ api.New → registrars 列表 append `mallAPI`（列表序在 pay 之后）；testutil prefixes 列表同步 append `/mall`；JobSpecs 追加 `mallOrderTimeoutClose` 规格 + app bindings 绑定（三处声明，缺绑定拒启）。
- 前端路由 `/mall`：member/product/trade 三页组。

## 3. 表结构

通用：BaseEntity 等价字段 + `tenant_id`（全部租户级，mall 无平台级表）；`DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`；金额列 Money `decimal(12,2)`。共 **14 张新表**，`EXPECTED_TABLES` 116 → **130**。状态数值对齐芋道枚举。全部登记 `TenantTables`（ADR 0019）。

### 3.1 会员域（5）

- `member_user`：`mobile`（租户内 UNIQUE）、`password_hash`（bcrypt，照 system）、`nickname`、`avatar_url`、`status`（启用/停用）、`level_id`、`point_balance` int NOT NULL DEFAULT 0、`remark`。C 端注册自动建会员（mobile 唯一冲突 409）。
- `member_level`：`name`、`level` int（排序，UNIQUE(tenant_id, level)）、`experience` int（阈值，纯维护）、`status`、`remark`。种子一行「普通会员 level=1 experience=0」。
- `member_address`：`user_id`、`receiver_name`、`receiver_mobile`、`province`、`city`、`district`、`detail`、`is_default` tinyint。省市区存文本（不联动 system_area id，边界记录）。
- `member_point_config`（租户级单行）：`point_per_yuan` int NOT NULL DEFAULT 1（1 元 = N 积分，支付成功按实付入账）。
- `member_point_record`：`user_id`、`biz_type`（`order_pay/order_cancel/admin_adjust`）、`biz_id` bigint（订单 ID 或 0）、`point` int（正负）、`balance_after` int、`remark`。**唯一入账入口**（照 pay wallet 流水先例：事务内行锁读余额 + 非负 SQL 双保险 + 快照结存），余额可由流水重放验证。

### 3.2 商品域（5）

- `product_category`：`parent_id`（0=根，两级树，叶子才可挂 SPU）、`name`（同级 UNIQUE）、`sort`、`status`。
- `product_brand`：`name`（租户内 UNIQUE）、`pic_url`、`status`、`remark`。
- `product_property`：`name`（租户内 UNIQUE）。
- `product_property_value`：`property_id`、`name`（UNIQUE(property_id, name)）。
- `product_spu`：`category_id`、`brand_id`、`name`（租户内 UNIQUE）、`pic_url`、`slider_pic_urls` JSON、`detail` text、`spec_type`（单规格/多规格）、`market_price` Money、`status`（下架/上架；**只有上架可被购买**）、`sort`。
- `product_sku`：`spu_id`、`properties` JSON（`[{propertyId,propertyName,valueId,valueName}]`，单规格为空数组）、`price` Money（售价）、`market_price` Money、`pic_url`、`stock` int NOT NULL DEFAULT 0、`first` tinyint（SPU 展示主 SKU）。

### 3.3 交易域（4）

- `trade_cart`：`user_id`、`spu_id`、`sku_id`、`count` int、`selected` tinyint；UNIQUE`(tenant_id, user_id, sku_id)`（重复加购合并数量）。
- `trade_order`：`no`（SO 编号，租户内 UNIQUE）、`user_id`、`status`（**0 待支付 / 10 已支付 / 20 已完成 / 30 已取消 / 40 已关闭**，对齐芋道数值）、`type`（0 正常，预留）、`user_remark`、`admin_remark`、`pay_order_id`、`pay_order_no`、`pay_channel_code`、`pay_time`、`cancel_time`、`cancel_reason`、`close_time`、`close_reason`、`total_quantity` int、`item_money` Money、`discount_money` Money（预留恒 0）、`postage` Money（预留恒 0）、`pay_money` Money（= item − discount + postage，服务端定）、收货快照 `receiver_name/receiver_mobile/receiver_area（province city district 拼接）/receiver_detail`。
- `trade_order_item`：`order_id`、`user_id`、`spu_id`、`sku_id`、`spu_name`、`sku_pic`、`sku_properties` JSON（**成交快照**）、`price` Money（成交单价）、`count` int、`subtotal` Money。
- （购物车 `trade_cart` 计入本域，见上）域合计 3 表 + 会员 5 + 商品 5 = 13？——修正：**member 5 + product 5（category/brand/property/value/spu/sku 是 6）+ trade 3（cart/order/item）= 14**。

### 3.4 状态机与不变量

- 订单状态机（纯函数校验，`trade_rules.go`）：
  - 待支付 →(用户取消) 已取消；待支付 →(job 超时 15 分钟) 已关闭；待支付 →(支付成功) 已支付；已支付 →(管理员完成) 已完成。其余迁移一律 409。
  - 已支付/已完成 不可取消不可关闭；已取消/已关闭 终态。
- 库存不变量：**下单即扣减**（事务内 `UPDATE product_sku SET stock = stock - ? WHERE id=? AND stock >= ?` 行锁，影响行数 0 → 409「库存不足」）；取消/关单回补（同形状 `stock + ?`）。选下单扣而非支付扣：避免「钱已收、库存不足」死结（芋道支持两种策略，MVP 取其一）。
- 积分不变量：余额只经唯一入账入口变动；`point_balance >= 0` SQL 双保险；流水快照结存可重放。
- 金额不变量：`pay_money = item_money − discount_money + postage`（服务端计算，客户端金额不采纳）；订单项 `subtotal = price × count`。
- 幂等：C 端重复提交同购物车结算产生**新订单**（不做幂等键，芋道亦然）；支付幂等由 pay 认领保证（同一 mall 订单只允许一笔在途支付单，再次支付先作废旧支付单或复用未支付 pay 单——MVP：复用待支付的既有 pay 单）。

## 4. API（摘要）

管理端（受保护组，权限码前缀 `mall:member:* / mall:product:* / mall:trade:*`）：

- 会员：分页/详情/创建/更新/停启用/积分调整（POST /mall/member/adjust-point：user_id/point/remark）、等级 CRUD、地址列表（按会员）。
- 商品：分类树+CRUD（叶子校验）、品牌 CRUD、属性+值 CRUD、SPU 分页/详情/创建/更新/上下架/删除（删除约束：有订单引用不可删）。
- 交易：订单分页（状态/编号/会员/时间过滤）/详情（含 items）/管理员备注/取消（待支付）/完成（已支付）。

C 端（公开组 `/mall/app/*`，Bearer 会员令牌，限速）：

- 注册（mobile+password+nickname）/登录（mobile+password → 令牌）/我的信息。
- 商品：上架 SPU 分页（分类/品牌过滤）、SPU 详情（含 SKU 列表）。
- 购物车：加购/数量修改/选中切换/删除/我的购物车。
- 订单：确认结算页（选中项+默认地址+金额试算）、创建订单（收货地址 id + 备注）、我的订单分页/详情/取消、发起支付（返回 pay 收银台参数；C 端支付经 mall 端点内部转 pay，mock 渠道自动回调）、我的积分流水。

## 5. 前端

- `web/src/api/mall.ts`：mall 全部 API 函数。
- `web/src/views/mall/member/index.vue`（会员分页+积分调整弹窗+等级字典）、`views/mall/member/level.vue`、`views/mall/product/category.vue`（树表）、`views/mall/product/brand.vue`、`views/mall/product/property.vue`、`views/mall/product/spu.vue`（列表+表单抽屉：多规格编辑器）、`views/mall/trade/order.vue`（分页+详情抽屉+取消/完成）。
- 菜单种子：9xx 段——900 会员中心（member/level/address 页）、910 商品（category/brand/property/spu 页）、920 交易订单（order 页）；按钮按 `菜单ID*10+n`（先 grep seed.go 去重）。C 端能力无菜单（无 admin UI）。
- 权限码收敛（ADR 0023）：routeOnly 缺口一律修复不豁免。

## 6. 验收

- `make check` 全绿；`make verify-migration` 空库迁移往返（130 表）+ 启动冒烟。
- 集成测试（真实 router + MySQL，独立库）：会员注册/登录/令牌失败矩阵；积分唯一入账（并发调整不重不漏、非负）；商品上下架购买边界；下单扣库存（并发下单不超卖）；支付联动（mock 回调 → 订单已支付 + 积分入账 + 库存已扣）；取消/关单回补；超时关单 job（GAP 时钟缝同款拨钟手法——mall 复用 pay 的 `SetBusinessClock`？否：mall 自建包级写缝照 pay 形态，或 job 直接扫 DB 时间字段用拨钟？——**照 pay 先例建 mall `SetBusinessClock` atomic.Value 缝**，注释对齐）；越权矩阵（会员 A 操作会员 B 资源 404/403）。
- 401 探测网与 prefixes 在场测试随新组自动覆盖（/mall 加入 prefixes 列表）。
- 全量集成 `-p 1` 零 FAIL；CI 四项绿。

## 7. 默认裁决（AFK，晨间复核）

1. **MVP = member+product+trade 三域**，promotion 整体顺延（芋道 mall 70+ 表，一次通宵只能保三域质量）。
2. **C 端身份 = 模块内 HMAC 令牌自校验**（公开组 + handler 内校验），不动 router 组结构；完整 member 认证中间件/多端点体系留后续。
3. **下单扣库存**（非支付扣），避免收款后库存死结；超卖防线 = 行锁条件 UPDATE。
4. **支付联动 = pay `SetOrderSuccessHandler` 第二消费方**，同事务置已支付 + 积分入账；pay 侧零改动。
5. **mall 依赖 payservice 单向**（平台能力域消费，非业务套件打通）。
6. **积分 = 支付成功按实付入账**（point_per_yuan 配置），无过期/清零。
7. **菜单 9xx 段**（900/910/920），迁移号 00040 起，EXPECTED_TABLES 116→130。
8. **postage/discount 字段预留恒 0**；省市区存文本不联动 system_area。
