#!/usr/bin/env bash
# 在空的、可丢弃的 MySQL 库上验证迁移往返与启动链路。
#
# 前置：
#   export GAP_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_verify?charset=utf8mb4&parseTime=True&loc=Local'
# 数据库必须已存在且为空；本脚本拒绝在非空库上运行，不会删除任何数据。
#
# 依赖：bash、curl、python3、lsof、go。
#
# 可选：
#   EXPECTED_TABLES=23  断言 up 后的业务表数量（默认 23；CI 继承此默认值；
#                       00003 新增 system_role_dept，00004 新增 crm_customer、crm_sequence，
#                       00005 新增 crm_contact，00006 新增 crm_customer_owner_history，
#                       00007 新增 crm_follow_up）
#   VERIFY_PORT=18099   启动冒烟使用的端口
set -euo pipefail

: "${GAP_DATABASE_DSN:?set GAP_DATABASE_DSN to a disposable empty MySQL database}"

EXPECTED_TABLES="${EXPECTED_TABLES:-23}"
PORT="${VERIFY_PORT:-18099}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

tmpdir="$(mktemp -d)"
server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid"
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

require_free_port() {
  if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "port $PORT is already in use; set VERIFY_PORT to a free port"
    exit 1
  fi
}

# 先确认端口可用，避免迁移往返跑完后才因端口冲突失败、库已变非空。
require_free_port

echo "== migration roundtrip (expected tables: $EXPECTED_TABLES) =="
go run ./cmd/migrate -c configs/config.yaml -expect-tables "$EXPECTED_TABLES" verify

echo "== boot + seed + auth smoke (port $PORT) =="
go build -o "$tmpdir/server" ./cmd/server
require_free_port
GAP_APP_PORT="$PORT" GAP_SEED=true "$tmpdir/server" -c configs/config.yaml >"$tmpdir/server.log" 2>&1 &
server_pid=$!

healthy=false
for _ in $(seq 1 60); do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited during startup:"
    cat "$tmpdir/server.log"
    exit 1
  fi
  if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
    healthy=true
    break
  fi
  sleep 1
done
if [[ "$healthy" != true ]]; then
  echo "server did not become healthy:"
  cat "$tmpdir/server.log"
  exit 1
fi

if ! token="$(
  curl -fsS -X POST "http://127.0.0.1:$PORT/admin-api/system/auth/login" \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"123456","tenantId":1}' |
    python3 -c 'import sys, json; d = json.load(sys.stdin); assert d["code"] == 0, d; print(d["data"]["accessToken"])'
)"; then
  echo "login failed:"
  cat "$tmpdir/server.log"
  exit 1
fi
if [[ -z "$token" ]]; then
  echo "login did not return an access token"
  exit 1
fi

if ! curl -fsS "http://127.0.0.1:$PORT/admin-api/system/user/page?pageNo=1&pageSize=10" \
  -H "Authorization: Bearer $token" |
  python3 -c 'import sys, json; d = json.load(sys.stdin); assert d["code"] == 0, d; assert d["data"]["total"] >= 1, d; print("user page ok: total=%d" % d["data"]["total"])'; then
  echo "user page check failed:"
  cat "$tmpdir/server.log"
  exit 1
fi

echo "verify-migration: OK"
