# Verification

数据库相关改动（迁移、seed、时区配置）用一套固定流程验证，不靠手工拼步骤。

## 空库迁移 + 启动冒烟

```bash
# 建验证库（与开发库分开）
mysql -uroot -p123456 -e "CREATE DATABASE IF NOT EXISTS go_admin_pro_verify CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"

export GAP_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_verify?charset=utf8mb4&parseTime=True'

# 空库迁移往返
go run ./cmd/migrate -c configs/config.yaml up
go run ./cmd/migrate -c configs/config.yaml reset

# 启动冒烟：seed 写入 + 登录 + 用户分页
go run ./cmd/server -c configs/config.yaml
```

- 登录接口 `/admin-api/system/auth/login`（种子账号 `admin/123456`），业务接口带 `Authorization: Bearer <token>`。
- 验证库与开发库分开；`GAP_DATABASE_DSN` 可覆盖 `configs/config.yaml` 的 DSN。
- 后台启动服务先 `go build -o /tmp/server ./cmd/server` 再运行二进制；`kill` 掉 `go run` 包装进程不会杀掉子进程。
- 检查残留监听：`lsof -nP -iTCP:8080 -sTCP:LISTEN`。

## DSN 与业务时区（app.timezone）

DSN 的 `loc` 参数由 `app.timezone` 生成并覆盖，时区名 URL 编码（如 `Asia/Shanghai` → `loc=Asia%2FShanghai`），示例不再写 `loc=Local`；业务时区通过配置文件 `configs/config.yaml` 的 `app.timezone` 声明，无环境变量覆盖：

```bash
export GAP_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro?charset=utf8mb4&parseTime=True'
```

- 旧配置/旧文档里遗留的 `loc=Local` 仍兼容，但会被 `app.timezone` 覆盖；服务器 `TZ` 不再影响数据库时间的解释。依赖旧行为的环境把 `app.timezone` 设为原服务器时区即可，见 `docs/adr/0006-app-timezone.md` 的迁移注意。
- 验证时区行为：把 `app.timezone` 设为与服务器 `TZ` 不同的时区（如 `UTC`）再启动，导入无时区日期后核对落库的墙上时间与 Excel 输入一致。
- 验证启动期校验：`app.timezone` 设为非法值（如 `Not/AZone`）时启动必须失败，错误信息包含该值与期望格式。

## CI

CI 的 `backend` 流程跑 `make check`（fmt-check + build + vet + test）；默认 `go test ./...` 不依赖 MySQL。
