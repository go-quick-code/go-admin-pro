# mall MVP：会员 / 商品 / 交易（对标芋道 mall 三域最小闭环）

## Problem Statement

平台底座、CRM、OA、ERP、pay、平台治理已交付，架构第二轮扫描（C1–C8）收官，L4 清单上未动的业务套件还剩 mall / mp / report / ai。所有者指示直接进入 mall（商城）：它是芋道对标清单里体量最大的套件，也是 pay 模块等待中的第二个业务消费方（pay 至今只有示例业务自闭环）。

## Solution

一次交付三域 14 表最小闭环：会员域（用户/等级/地址/积分配置+流水）、商品域（分类/品牌/属性/SPU/SKU）、交易域（购物车/订单/订单项），支付经既有 pay 收银台与 `OrderSuccessHandler` 扩展点联动（pay 侧零改动），含管理端全量 + C 端最小自助（注册/登录/浏览/购物车/下单/支付/我的订单/积分）。promotion 营销域整体顺延。完整施工规格 `docs/mall-mvp-spec.md`（**AFK 产物，§7 默认裁决待所有者晨间复核**）。

## User Stories

1. 作为租户管理员，我想要维护会员（mobile/昵称/等级/状态），这样 B 端可管理 C 端用户。
2. 作为租户管理员，我想要手工调整某会员积分并留流水（正负均可），这样运营有积分工具。
3. 作为租户管理员，我想要配置 1 元 = N 积分的兑换比例，这样积分口径可控。
4. 作为租户管理员，我想要维护会员等级（阈值纯记录），这样会员有分层口径。
5. 作为会员，我想要注册/登录（mobile+密码），这样我有独立于管理端的 C 端身份。
6. 作为会员，我想要维护我的收货地址（默认地址），这样下单不用每次填。
7. 作为会员，我想要浏览上架商品（分类/品牌过滤、详情含 SKU），这样我能挑选商品。
8. 作为会员，我想要把 SKU 加入购物车（重复加购合并数量）、改数量、切换选中、删除，这样我能凑单。
9. 作为会员，我想要在结算页看到选中项、默认地址与金额试算，这样下单前金额透明。
10. 作为会员，我想要创建订单（下单即锁库存，库存不足 409），这样不会付款后没货。
11. 作为会员，我想要对待支付订单发起支付（收银台接 mock/wallet 渠道），这样订单能完成交易。
12. 作为会员，我想要支付成功后看到订单已支付且积分自动入账，这样交易与权益闭环。
13. 作为会员，我想要取消待支付订单（库存回补），这样误下单可撤。
14. 作为会员，我想要查看我的订单列表/详情（含成交快照）与积分流水，这样消费可追溯。
15. 作为会员，我想要 15 分钟未支付的订单自动关闭（库存回补），这样库存不被无限占住。
16. 作为租户管理员，我想要管理商品分类（两级树、叶子挂商品）与品牌，这样商品有组织结构。
17. 作为租户管理员，我想要维护属性（名+值）供多规格引用，这样规格口径统一。
18. 作为租户管理员，我想要创建 SPU（单/多规格）与 SKU（价格/库存/规格 JSON），这样商品建模完整。
19. 作为租户管理员，我想要上下架商品，这样只有上架商品可被购买。
20. 作为租户管理员，我想要订单列表/详情（含订单项快照）、管理员备注、取消待支付单、完成已支付单，这样交易有管理面。
21. 作为开发者，我想要积分与钱包同款「唯一入账入口」（行锁+非负双保险+快照结存），这样余额可由流水重放验证。
22. 作为开发者，我想要订单编号走 `internal/pkg/sequence.Next`（SO 前缀），这样与 CRM/ERP/pay 编号同源。
23. 作为开发者，我想要支付成功联动走 pay 既有 `SetOrderSuccessHandler` 扩展点（同事务、pay 侧零改动），这样不引新跨模块状态机。
24. 作为开发者，我想要 mall 单向依赖 pay service（平台能力域消费），这样不违反业务套件零打通（ADR 0015）。
25. 作为开发者，我想要超时关单走 JobSpecs 三处声明（缺绑定拒启，ADR 0024 补遗），这样任务接线有编译/启动期防线。
26. 作为开发者，我想要 C 端令牌 HMAC 自校验（公开组 + handler 内校验 + 限速，密钥缺失拒启动），这样不扩 router 组结构也有身份防线。
27. 作为安全，我想要客户端金额全部不采纳（服务端定 pay_money）、越权矩阵 404/403、上下架购买边界，这样 C 端攻击面有明确防线。
28. 作为运维，我想要 EXPECTED_TABLES=130 空库迁移往返 + 租户表登记强校验（14 表全租户级），这样升级可审计。

## Implementation Decisions

- 新模块 `internal/module/mall`（五层，service 按域拆文件 + `*_rules.go` 纯校验）；单模块四域对齐 erp/pay 先例，不拆 member/product/trade 三模块。
- 依赖：平台层（system/money/sequence/ratelimit）+ **pay service 单向依赖**（mall→pay，pay 侧零改动；`OrderSuccessHandler` 是既有扩展点，充值是第一消费方）。
- 表：member_user/level/address/point_config/point_record + product_category/brand/property/property_value/spu/sku + trade_cart/order/order_item，全租户级，全部登记 TenantTables；EXPECTED_TABLES 116→130；迁移 00040 起。
- 编号 `sequence.Next`（SO 前缀）；金额 Money；积分 int；订单状态机数值对齐芋道（0 待支付/10 已支付/20 已完成/30 已取消/40 已关闭）。
- 下单即扣库存（行锁条件 UPDATE，不足 409），取消/关单回补；积分按支付实付入账（point_per_yuan）。
- C 端 `/mall/app/*` 挂公开组 + 会员令牌 handler 自校验（HMAC，`config.mall.jwt_secret` 缺失拒启动）+ 限速；不动 router 三组结构与 Backbone。
- 管理端 `/mall/member|product|trade` 挂受保护组，权限码 `mall:*:*`（ADR 0023 收敛检查覆盖）；注册器 append 进 registrars 列表 + prefixes 列表（ADR 0024）。
- 超时关单 job：JobSpecs + bindings + `RunOrderTimeoutClose` 三处声明（系统级，照 payOrderTimeoutClose）；mall 自建 `SetBusinessClock` atomic.Value 时钟缝（照 pay 先例，注释对齐）。
- 菜单 9xx 段（900 会员中心/910 商品/920 交易订单）；pay 应用种子 `app_key=mall` 绑 mock+wallet。
- 前端 `api/mall.ts` + `views/mall/{member,product,trade}` 七页。

## Testing Decisions

- 最高缝：`testutil.NewIntegrationEnv` 全量 app HTTP 契约测试（真实 router + Auth/权限/租户插件 + MySQL），先例 pay/crm/erp 集成测试；C 端公开端点同走真实 router。
- 纯函数：状态机/金额/库存校验落 `trade_rules.go` 等外部测试包，先例 crm/erp rules_test。
- 必测路径：注册/登录/令牌失败矩阵、积分并发唯一入账、下单并发不超卖、支付联动全链（mock 回调→已支付+积分+库存）、取消/关单回补、超时关单拨钟、越权矩阵、上下架购买边界。
- 401 探测网与 prefixes 在场测试随 `/mall` 加入自动覆盖新组。

## Out of Scope

promotion 营销域全量（优惠券/满减/秒杀/拼团/砍价/积分商城/签到）、售后 after_sale、配送 delivery 与运费模板（postage 预留恒 0）、分销/佣金、评价/收藏、C 端首页装修与搜索、发票、多币种、member 经验值体系、注册验证码、真实 C 端 uniapp/H5、mp/report/ai 套件。

## Further Notes

- **AFK 默认裁决待复核**（docs/mall-mvp-spec.md §7）：三域裁剪、C 端令牌形状、下单扣库存、积分口径、9xx 菜单段、130 表——晨间复核如有推翻按新口径返工。
- 芋道 mall 共 70+ 表，本 MVP 14 表是能力子集；对标缺口以「明确不做」清单显式化，后续票按需补。
