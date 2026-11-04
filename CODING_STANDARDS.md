# 编码标准

审查用清单，只保留需要判断的约定。能在 CI 或检查命令里稳定执行的规则交给 `make check` 与 CI，这里不重复。

## 包与边界

- 跨模块复用的能力放 `internal/pkg/<name>`；只服务一个模块的逻辑留在该模块内。
- 新增 `internal/pkg` 子目录时，同步 README 架构树。
- 公共包写 package doc：用途、最小用法、限制。
- `internal/pkg` 保持 HTTP 无关；gin 适配集中在 `response`、`middleware`。
- `internal/pkg` 不反向依赖 `internal/module/*`。

## 测试

- 测试写在公共 seam：验证导出函数或 HTTP 契约的输入输出，不测私有函数。
- `internal/pkg` 的公共包用外部测试包（`package foo_test`）；只测内部协作用同包。
- 纯函数测试不连数据库；集成测试用构建标签隔离。

## 错误与文案

- 用户可见错误用中文，说清哪个值、哪一行不合法，并给出期望格式。
- 调用方需要 `errors.Is` 分支的错误用哨兵错误，用 `%w` 包装底层原因。
- 发送给用户的文案不暴露内部实现细节。

## 时间与金额

- 无时区时间按调用方的 Location 解释（Excel 导入默认 `time.Local`）；同一字段的写入、读取、展示保持一致。
- 金额落库用 `decimal(12,2)`，Go 侧不用浮点做累加。

## 依赖

- 新增依赖前先确认标准库能否满足；引入后执行 `go mod tidy`，直接依赖放 require 首块。
