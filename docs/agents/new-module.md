# 新增业务模块清单

接一个业务模块（mall 是下一个候选）的完整步骤，按 ADR 0024 的装配形状。注册类改动是「追加一行」；其余手抄步骤逐项列出，每项都注明兜底网。

## 注册（一行一件，ADR 0024）

1. **路由挂载**：模块 api 包实现 `RegisterProtected(rg *gin.RouterGroup)`（必需）；有公开端点再实现 `RegisterPublic`，有换票流再实现 `RegisterStream`（可选能力，router 按接口探测）。`internal/app/app.go` 把 handler append 进 `router.New` 的注册器列表——列表顺序即受保护组注册顺序；前缀与既有模块互不冲突即可，组序不影响路由匹配。
2. **演示种子**：新建 `internal/seed/<name>_demo.go`，`func init() { seed.RegisterDemo(seed.DemoOrder<Name>, seed<Name>Demo) }`；order 常量集中声明在 `seed.go`（步进 10），撞序号 init panic。只依赖基础种子的模块顺序无关；有依赖的写进常量注释（先例：OA 必须在 CRM 后——依赖演示组织 sales1/sales2/manager）。

漏接路由不会编译报错（签名不强制注册，ADR 0024 有论证）：兜底网是集成套件的**前缀在场测试**（`internal/testutil`）——每个模块前缀在 `engine.Routes()` 里必须 ≥1 条，缺一家即红。该网只防 `internal/testutil/routes_test.go` 里 prefixes 列表登记过的前缀：新模块接线时必须同步把自己的前缀 append 进该列表，不进列表即对新模块不设防。

## 手抄（逐项，各有兜底）

3. **迁移**：goose 编号顺延；表数量变化同步 `scripts/verify-migration.sh` 的 `EXPECTED_TABLES`（见 `verification.md`）。兜底：空库 `make verify-migration`。
4. **表登记**：`internal/pkg/database/tenant_tables.go` 逐张登记 `TenantTables`/`PlatformTables`（平台级表必须写理由）。兜底：启动时 `VerifyTenantTables` 精确分区校验，漏登拒绝启动（ADR 0019）。
5. **模块四层**：`internal/module/<name>/` 下 model → repository → service → api；不引 DI，依赖由 app.go 显式装配。
6. **定制装配**：service 的 Set* 接缝在 app.go 逐个接线（每模块 bespoke，无统一形状——统一化等于 DI 框架，已否决）。集成测试需要触达 service 时在 `App` 结构体加字段，注释写明哪个票、经该字段做什么（参照 `Pay`/`BPM` 字段注释）。
7. **配置**：`internal/config/config.go` 加模块配置段（按需）。注意 seed 包不读配置——时区/存储根/回调地址等由 app.New 显式注入（参照 `SetDemoLocation`/`SetDemoPayNotifyBase`）。
8. **基础种子**：`internal/seed/seed.go` 的 `Run` callsheet 按依赖顺序插一行——显式有序清单是有意保留的手工接线，不是遗漏（ADR 0024）。
9. **定时任务**：app.go `scheduler.RegisterSystem` 闭包（系统级 handler）+ `seedJobs` 种子行（handlerName/cron/状态字面量）。这是候选 C8 记录的三处手抄区（第三处在 seed_test 断言），暂无结构防线——handler 名与 cron 表达式两边手抄必须一致。
10. **菜单与权限**：seed 菜单/按钮 + 前端路由。兜底：权限码收敛测试（ADR 0023）——routeOnly 缺口（路由挂码、seed 无授予路径）一律修复不豁免。

## 验证

```bash
make check          # fmt + build + vet + test（含前缀在场测试以外的全部静态网）
make verify-migration  # 空库迁移往返 + 启动冒烟（见 verification.md）
```

演示种子改动跑 withDemo 链路（run-demo）人工核对；集成套件单独跑（`//go:build integration`，CI 的 Integration job）。
