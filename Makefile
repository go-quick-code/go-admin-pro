APP_NAME := go-admin-pro
CONFIG   := configs/config.yaml
BIN      := bin/server
MIGRATE  := bin/migrate
version ?= 1
# 集成测试专用库（#3）：基座会拒绝在非 go_admin_pro_test 库上运行，防止清表误伤开发库。
# 刻意不用 GAP_DATABASE_DSN：那个名字会被 config.Load 读取，泄漏进其他包的测试。
GAP_TEST_DATABASE_DSN ?= root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test?charset=utf8mb4&parseTime=True

.PHONY: help run run-demo build test test-integration check vet fmt fmt-check tidy migrate-up migrate-down migrate-reset migrate-status migrate-stamp migrate-create verify-migration web-install web-dev web-build web-type-check build-all backfill-contract-approval docker-up docker-down docker-logs clean

help:
	@echo "go-admin-pro 常用命令："
	@echo "  make run          启动后端（go run）"
	@echo "  make run-demo     启动后端并写入 CRM + OA + 平台演示数据（GAP_SEED_DEMO=true）"
	@echo "  make build        构建后端二进制到 $(BIN)"
	@echo "  make migrate-up   执行全部待执行迁移"
	@echo "  make migrate-down 回滚一个迁移版本"
	@echo "  make migrate-reset 回滚全部迁移版本"
	@echo "  make migrate-status 查看迁移状态"
	@echo "  make migrate-stamp version=1 把已有库标记为指定版本（不执行 DDL）"
	@echo "  make migrate-create name=xxx 新建迁移文件"
	@echo "  make verify-migration 空库迁移往返 + 启动/登录冒烟（需要 GAP_DATABASE_DSN）"
	@echo "  make test         运行 Go 测试"
	@echo "  make test-integration 运行集成测试（构建标签 integration）"
	@echo "  make backfill-contract-approval 为存量待审合同补建 bpm 审批实例（幂等）"
	@echo "  make check        fmt-check + build + vet + test"
	@echo "  make vet          go vet 静态检查"
	@echo "  make fmt          gofmt 格式化"
	@echo "  make tidy         go mod tidy"
	@echo "  make web-install  安装前端依赖"
	@echo "  make web-dev      启动前端开发服务器"
	@echo "  make web-build    构建前端产物到 web/dist"
	@echo "  make build-all    构建前端 + 后端"
	@echo "  make docker-up    启动 MySQL 容器"
	@echo "  make docker-down  停止并移除容器"
	@echo "  make clean        清理构建产物"

run:
	go run ./cmd/server -c $(CONFIG)

# CRM + OA + 平台演示环境（#21/#63/#85 一条命令路径）：演示数据默认关闭，这里显式开启。
run-demo:
	GAP_SEED_DEMO=true go run ./cmd/server -c $(CONFIG)

build:
	go build -trimpath -o $(BIN) ./cmd/server
	go build -trimpath -o $(MIGRATE) ./cmd/migrate

test:
	go test ./...

# 存量待审合同的 bpm 审批实例补建（issue #47）：幂等，重复执行不会重复创建。
backfill-contract-approval:
	go run ./cmd/backfill-contract-approval -c $(CONFIG)

# 集成测试基座在 #3 落地；没有 integration 用例时明确失败，避免假绿。
test-integration:
	@pkgs="$$(go list -tags=integration -f '{{.Dir}}' ./...)" || exit 1; \
	if [ -z "$$pkgs" ]; then echo "go list returned no packages"; exit 1; fi; \
	if ! grep -rhs --include='*_test.go' -e '^//go:build' -e '^// +build' $$pkgs | grep -v '!integration' | grep -q 'integration'; then \
		echo "no integration tests found (build tag integration)"; exit 1; \
	fi
	# -p 1 串行跑各包：集成测试包间会清空同一个测试库，并行必然互相冲掉数据。
	GAP_TEST_DATABASE_DSN="$(GAP_TEST_DATABASE_DSN)" go test -tags=integration -p 1 ./...

check: fmt-check build vet test

vet:
	go vet ./...

fmt:
	@pkgs="$$(go list -tags=integration -f '{{.Dir}}' ./...)" || exit 1; \
	if [ -z "$$pkgs" ]; then echo "go list returned no packages"; exit 1; fi; \
	gofmt -w $$pkgs

fmt-check:
	@pkgs="$$(go list -tags=integration -f '{{.Dir}}' ./...)" || exit 1; \
	if [ -z "$$pkgs" ]; then echo "go list returned no packages"; exit 1; fi; \
	out="$$(gofmt -l $$pkgs)" || exit 1; \
	if [ -n "$$out" ]; then echo "gofmt required:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

migrate-up:
	go run ./cmd/migrate -c $(CONFIG) up

migrate-down:
	@if [ "$$CONFIRM" != "yes" ]; then echo "refusing to roll back: set CONFIRM=yes (down executes the migration's DROP statements)"; exit 1; fi
	go run ./cmd/migrate -c $(CONFIG) down

migrate-reset:
	@if [ "$$CONFIRM" != "yes" ]; then echo "refusing to reset: set CONFIRM=yes (reset drops all migrated objects)"; exit 1; fi
	go run ./cmd/migrate -c $(CONFIG) reset

migrate-status:
	go run ./cmd/migrate -c $(CONFIG) status

migrate-stamp:
	go run ./cmd/migrate -c $(CONFIG) stamp $(version)

migrate-create:
	go run ./cmd/migrate create $(name)

verify-migration:
	./scripts/verify-migration.sh

web-install:
	cd web && pnpm install

web-dev:
	cd web && pnpm dev

web-build:
	cd web && pnpm build

web-type-check:
	cd web && pnpm type-check

build-all: web-build build

docker-up:
	docker compose up -d mysql

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f mysql

clean:
	rm -rf bin web/dist uploads/*
