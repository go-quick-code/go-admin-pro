# ---------- 前端构建 ----------
FROM node:22-alpine AS web-builder
WORKDIR /app/web
RUN npm install -g pnpm@11.22.0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ---------- 后端构建 ----------
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# ---------- 运行镜像 ----------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app
WORKDIR /app
COPY --from=go-builder /out/server ./server
COPY --from=go-builder /out/migrate ./migrate
COPY --from=web-builder /app/web/dist ./web/dist
COPY configs ./configs
RUN mkdir -p uploads && chown -R app:app /app
USER app
EXPOSE 8080
ENTRYPOINT ["./server", "-c", "configs/config.yaml"]
