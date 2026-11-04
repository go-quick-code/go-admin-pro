APP_NAME := go-admin-pro
CONFIG   := configs/config.yaml
BIN      := bin/server
MIGRATE  := bin/migrate
version ?= 1

.PHONY: help run build test test-integration test-race check vet fmt fmt-check tidy migrate-up migrate-down migrate-reset migrate-status migrate-stamp migrate-create web-install web-dev web-build web-type-check build-all docker-up docker-down docker-logs clean

help:
	@echo "go-admin-pro 常用命令："
	@echo "  make run          启动后端（go run）"
	@echo "  make build        构建后端二进制到 $(BIN)"
	@echo "  make migrate-up   执行全部待执行迁移"
	@echo "  make migrate-down 回滚一个迁移版本"
	@echo "  make migrate-reset 回滚全部迁移版本"
	@echo "  make migrate-status 查看迁移状态"
	@echo "  make migrate-stamp version=1 把已有库标记为指定版本（不执行 DDL）"
	@echo "  make migrate-create name=xxx 新建迁移文件"
	@echo "  make test         运行 Go 测试"
	@echo "  make test-integration 运行集成测试（构建标签 integration）"
	@echo "  make test-race    运行 Go 测试（竞态检测）"
	@echo "  make check        提交前检查：fmt-check + build + vet + test-race"
	@echo "  make vet          go vet 静态检查"
	@echo "  make fmt          gofmt 格式化"
	@echo "  make fmt-check    检查格式（不写文件）"
	@echo "  make tidy         go mod tidy"
	@echo "  make web-install  安装前端依赖"
	@echo "  make web-dev      启动前端开发服务器"
	@echo "  make web-build    构建前端产物到 web/dist"
	@echo "  make web-type-check 前端类型检查"
	@echo "  make build-all    构建前端 + 后端"
	@echo "  make docker-up    启动 MySQL 容器"
	@echo "  make docker-down  停止并移除容器"
	@echo "  make clean        清理构建产物"

run:
	go run ./cmd/server -c $(CONFIG)

build:
	go build -trimpath -o $(BIN) ./cmd/server
	go build -trimpath -o $(MIGRATE) ./cmd/migrate

test:
	go test ./...

# 集成测试基座在 #3 落地；此 target 先固定命令入口。
test-integration:
	go test -tags=integration ./...

test-race:
	go test -race ./...

check: fmt-check build vet test-race

vet:
	go vet ./...

fmt:
	gofmt -w $$(go list -f '{{.Dir}}' ./...)

fmt-check:
	@out="$$(gofmt -l $$(go list -f '{{.Dir}}' ./...))"; \
	if [ -n "$$out" ]; then echo "gofmt required:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

migrate-up:
	go run ./cmd/migrate -c $(CONFIG) up

migrate-down:
	go run ./cmd/migrate -c $(CONFIG) down

migrate-reset:
	go run ./cmd/migrate -c $(CONFIG) reset

migrate-status:
	go run ./cmd/migrate -c $(CONFIG) status

migrate-stamp:
	go run ./cmd/migrate -c $(CONFIG) stamp $(version)

migrate-create:
	go run ./cmd/migrate create $(name)

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
