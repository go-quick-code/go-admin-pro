# 消息通道：平台级渠道/模板，租户级发送日志

**Status**: accepted

短信与邮件以独立模块 `message` 落地，渠道（provider 配置）与模板是平台级资源，发送日志按租户隔离；provider 通过注册表接入，内置 `log`（开发用）、`smtp`（stdlib `net/smtp` + STARTTLS）、`aliyun_sms`（stdlib HTTP + RPC 签名），不引入云厂商 SDK。原因：凭据属于平台运营方，租户不应看到；发送日志是租户用量与审计的依据，必须走租户插件；stdlib 实现避免为两家 provider 引入大依赖。代价：渠道配置的密钥在 DB 明文存储，只做 API 查询脱敏，KMS 留后续；本轮不做自动重试、批量发送与附件。

相关：ADR 0013（推送）、`docs/platform-mvp-spec.md` §4.3。
