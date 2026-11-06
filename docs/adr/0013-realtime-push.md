# 实时推送：SSE + WebSocket 共用 hub，一次性票据认证，单实例

**Status**: accepted

站内通知增加实时推送：`pkg/push` 提供统一的连接 hub、一次性票据（30 秒 TTL）与事件模型；传输层同时提供 SSE 与 WebSocket，前端按 WS → SSE → 60 秒轮询降级。浏览器 `EventSource`/WebSocket 不能带 `Authorization` 头，因此先用 Bearer 换一次性票据，再用票据建立流，避免 JWT 进入 URL/日志。发布发生在业务事务提交之后，`Create` 自动发布，`CreateInTx` 的生产者在提交后显式 `Publish`；推送是 best-effort，轮询始终保留。原因：通知是单向事件，SSE 足够且零依赖；WebSocket 为后续 IM/双向通道预留；票据方案兼顾浏览器限制与安全。代价：hub 与票据在进程内，多实例部署时其他实例的连接收不到事件，需要 Redis/NATS pub/sub，本轮不实现；事务回滚可能产生一次瞬时事件，以前端刷新为准。

相关：ADR 0009（被本 ADR 部分取代）、ADR 0010（消息通道）、`docs/platform-mvp-spec.md` §4.5。
