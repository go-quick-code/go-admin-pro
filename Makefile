APP_NAME := go-admin-pro
CONFIG   := configs/config.yaml
BIN      := bin/server

.PHONY: help run build test vet fmt tidy web-install web-dev web-build build-all docker-up docker-down docker-logs clean

help:
	@echo "go-admin-pro 常用命令："
	@echo "  make run         启动后端（go run）"
	@echo "  make build       构建后端二进制到 $(BIN)"
	@echo "  make test        运行 Go 测试"
	@echo "  make vet         go vet 静态检查"
	@echo "  make fmt         gofmt 格式化"
	@echo "  make tidy        go mod tidy"
	@echo "  make web-install 安装前端依赖"
	@echo "  make web-dev     启动前端开发服务器"
	@echo "  make web-build   构建前端产物到 web/dist"
	@echo "  make build-all   构建前端 + 后端"
	@echo "  make docker-up   启动 MySQL 容器"
	@echo "  make docker-down 停止并移除容器"
	@echo "  make clean       清理构建产物"

run:
	go run ./cmd/server -c $(CONFIG)

build:
	go build -trimpath -o $(BIN) ./cmd/server

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

tidy:
	go mod tidy

web-install:
	cd web && pnpm install

web-dev:
	cd web && pnpm dev

web-build:
	cd web && pnpm build

build-all: web-build build

docker-up:
	docker compose up -d mysql

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f mysql

clean:
	rm -rf bin web/dist uploads/*
