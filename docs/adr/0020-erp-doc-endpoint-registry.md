# ERP 单据端点：类型注册表是路由、权限与菜单的唯一事实

**Status**: accepted

12 类 ERP 单据的 HTTP 端点、六个动作权限码、菜单与按钮 ID、Excel 文件名由 `internal/module/erp/doctype` 的静态注册表 `All()` 单一声明：每条目携带路由段、标题、图标、排序、菜单 ID、按钮前缀、权限前缀与 Excel 文件名；`erp/api` 为每条目挂一组薄闭包（Page/Get/Create/Update/Status/Delete/Export）组成 `docSpec`，`RegisterProtected` 循环注册七条路由；`internal/seed` 从同一注册表投影出 12 个菜单行与 60 个按钮行。权限码由 `PermPrefix` 派生为 `query/create/update/delete/approve/export` 六码，不提供逐码覆盖；按钮 ID 由 `MenuID*10+{1..5}` 派生；`SaleDocUpdateStatusReq` / `FinanceDocUpdateStatusReq` 收成单一 `dto.DocUpdateStatusReq`（线上 payload 本就相同）。

原因：12 个 handler 文件逐字节同构，唯一实质差异是 update-status 的 DTO 别名；84 条路由的重复本身不痛，痛在 111 个权限码分散在路由、seed 与前端三处手写——今天恰好相等，却没有任何检查。漏一处不会编译失败，只会让非管理员在运行时拿到 403。注册表让「新增一类单据」变成一行元数据加七个薄闭包，并让路由、菜单、权限从结构上不可能分叉。保障分三层：注册表与 gin 路由树比对（每组恰好七条、方法与路径逐一匹配）；`go/parser` 扫描 `internal/module/erp/api` 全部 `requirePermission("erp:...")` 字面量，与 `seedMenus()` 的 ERP 权限集合双向比对（覆盖主数据、只读与统计共 134 条路由）；seed 投影的 72 行（12 菜单 + 60 按钮）以 golden CSV 做全字段快照，重构前后逐字段相等。

拒绝的替代：泛型 `DocSpec[P,C,U]`（Page/Get/Create 的返回类型本来就要擦成 `any`，泛型只保住 binding 一处，却让异构注册表消失、注册点退回 12 条显式调用，违背「不为泛型而泛型」）；codegen 生成 12 个 api 文件（事实藏进生成物，审查要在模板与产物之间来回，且本仓 codegen 面向新实体而非存量重构）；只抽 `registerDocRoutes` helper 而保留 12 个文件（权限码仍是两份，安全收益为零）；把主数据、只读与统计一并纳入注册表（形状不齐：`/list` 与 `/page` 并存、无 approve、统计四条路由共用一个码，硬塞会让规格长出一堆可选字段；它们已由 AST 检查覆盖）。

已知边界：前端 72 个权限字面量不纳入检查——前端没有「漏一处 = 403」的失败模式，`ProTable` 由 `perm-prefix` 派生的六个幽灵码（`erp:stock:*`、`erp:stock-record:*`）不渲染任何按钮；前端权限事实留给候选 04 菜单目录收口。注册表与 `docSpec` 不一致只在单测中失败，不在启动期 panic——这是静态切片上的程序员错误，CI 必挂，换成运行时崩溃没有安全收益。seed 的「只插不改、软删不复活」语义不变。

相关：ADR 0018（单据内核）、ADR 0019（租户表登记）、候选 04（菜单目录）。
