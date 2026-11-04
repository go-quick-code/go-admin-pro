# Verification

DB-backed changes（迁移、seed、认证、数据权限）用一条命令验证，不靠手工拼步骤。

## 空库迁移 + 启动冒烟

```bash
docker compose up -d mysql
docker exec go-admin-pro-mysql mysql -uroot -p123456 \
  -e "CREATE DATABASE IF NOT EXISTS go_admin_pro_verify CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
export GAP_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_verify?charset=utf8mb4&parseTime=True&loc=Local'
make verify-migration
```

`make verify-migration` 会：在空库上 `up → reset → up` 并校验表数量，然后启动服务、写入种子、用 `admin/123456` 登录、请求用户分页。它拒绝在非空库上运行，不会删除数据；重复执行前先 drop/recreate 验证库。

只验证迁移往返，可绕过启动冒烟：

```bash
go run ./cmd/migrate -c configs/config.yaml -expect-tables 17 verify
```

新增迁移导致表数量变化时，同步更新 `scripts/verify-migration.sh` 的 `EXPECTED_TABLES` 默认值；CI 继承脚本默认值，不再单独配置。

脚本依赖 `bash`、`curl`、`python3`、`lsof` 和 `go`。

## 手工调试注意

- 后台启动服务先 `go build -o /tmp/server ./cmd/server` 再运行二进制；`kill` 掉 `go run` 包装进程不会杀掉它的子进程。
- 检查残留监听：`lsof -nP -iTCP:18099 -sTCP:LISTEN`。
- 验证库与开发库分开；`GAP_DATABASE_DSN` 可覆盖 `configs/config.yaml` 的 DSN。
- 登录接口 `/admin-api/system/auth/login`，业务接口带 `Authorization: Bearer <token>`。

## CI

`.github/workflows/ci.yml` 的 `migration` job 用 MySQL 8 service 跑同一个脚本；`backend` job 跑 `make check`（fmt-check + build + vet + test）。
