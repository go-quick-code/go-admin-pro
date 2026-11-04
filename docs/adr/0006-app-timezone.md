# 应用统一业务时区：app.timezone 驱动导入与 DSN loc

**Status**: accepted

所有「无时区」时间的解释统一走应用级配置 `app.timezone`（IANA 时区名，默认 `Asia/Shanghai`），启动时用标准库 `time.LoadLocation` 校验，非法值（如 `Not/AZone`）直接启动失败并给出可读错误。数据库 DSN 的 `loc` 参数由同一配置生成（`config.ApplyDSNLoc`：DSN 已有 `loc` 时覆盖、没有时追加、时区名 URL 编码为 `Asia%2FShanghai`），在 `config.Load` 里注入，服务器与 `cmd/migrate` 共用同一份 DSN，不再依赖 `time.Local`；业务侧获取 `*time.Location` 只走 `cfg.Location()` 这个单一访问点，CRM Excel 导入把它传给 helper 的 `ImportOptions.Location`。原因：此前导入日期的墙上时间取决于部署环境的 `TZ` 隐性约定，环境一变日期就漂移；DSN `loc=Local` 与业务时区各自为政时，同一份 Excel 在不同环境落库结果不同。把时区变成显式配置并在启动期校验，偏移问题从「上线后追查」变成「启动即报错」，导入侧与数据库侧共用同一个值，不再出现两边不一致。代价：时区是应用级单值，不支持按租户或按字段的多时区；部署环境的 `TZ` 不再影响 DB 时间的解释，依赖旧行为的环境需要把 `app.timezone` 设为原服务器时区；配置里遗留的 `loc=Local` 仍兼容（会被覆盖），示例文档已不再示范该写法。

**运维注意**：运行镜像 alpine 已安装 `tzdata`，`LoadLocation` 可直接解析 IANA 名；若未来换用不带 tzdata 的基础镜像（scratch/distroless），需要给二进制内嵌 `time/tzdata` 或在镜像里装 tzdata，否则合法时区名也会启动失败。容器内 `TZ` 环境变量不再承担业务时区职责，只影响日志等本地展示；改业务时区只改 `app.timezone`，改完需要核对存量数据是否按旧时区写入（本决策不迁移存量数据，只约束今后的写入与解释）。
