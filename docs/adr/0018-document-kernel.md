# ERP 单据内核：泛型内核 + DocStore + 适配器

**Status**: accepted

ERP 12 类单据（purchase_order/in/return、sale_order/out/return、stock_in/out、stock_move、stock_check、finance_receipt/payment）的 7 段生命周期逐类复制：6,192 行 service、24 处状态认领、24 处「单据状态已变化」、48 处「已审核单据不可…」；#98 的并发修复（条件软删、锁序、反审核先认领）必须在 12 个文件各改一遍。决定引入「单据内核」：泛型 `doc.Kernel[H,I]` 拥有事务边界、状态认领、条件软删、单号同事务、finder 与状态机；每类只留适配器，声明元数据、单号/业务时间访问器、写侧校验与落账钩子。内核不依赖 `repository`，通过 `Store`/`Tx` 接口取数；`ApplyStockChange` 仍是唯一库存入账入口。

拒绝的替代：codegen（生成物审查/调试成本高，repo 的 codegen 另有用途）、模型接口（12 类 × ~16 个机械方法，业务接口污染 model 包）、只抽共享函数（F1 类修复仍需 12 处改）。内核先放 `internal/module/erp/service/doc`，不跨业务套件复用；第二个消费者出现再提升到 `internal/pkg`。

代价与约束：12 类 service 预计 6,192 → ~4,400 行（软目标，不达标不阻塞但 PR 需解释）；分三个 tracer（stock_in/out → purchase_in → finance_receipt）验证内核形状后再批量；现有 127 个 HTTP 测试原样通过是行为等价的硬门槛；候选 02 的泛型 store 实现同一 `Tx` 面。01a tracer（stock_in/out）已验证：内核 fake-store 单测、全量 ERP 集成测试、adapter conformance 表全绿，故本 ADR 转 `accepted`。

**12 类收口结果**：12 类 service 6,192 → **5,330** 行；加内核/存储基建 597 行（`doc` 包 + `doc_store.go` + `DocBase`）后为 **5,927**；9 个 repository 文件 2,356 → **1,657**。4,400 的软目标未达成：内核吸收的是写侧骨架（约 1,500 行中的一部分），而落账钩子与读侧 DTO 映射是固有差异；真正的收益是 F1 不变量（事务/认领/条件软删/锁序/状态机/finder）集中在一处、12 类共享同一 store/内核、repository 减少 699 行，以及新增一类单据的成本从 ~500 行降到 ~250–350 行。review 修复后合计 8,548 → 7,584（−964，−11.3%）。
