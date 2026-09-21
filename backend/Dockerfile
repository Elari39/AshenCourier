# syntax=docker/dockerfile:1.7
# ============================================================
# AshenCourier 后端镜像：golang 构建 → distroless 运行
#
# api 与 worker 用同一个镜像、不同入口：
#   api    → 默认 ENTRYPOINT /app/api
#   worker → compose 里用 `entrypoint: ["/app/worker"]` 覆盖
#            （注意不能用 `command`：那只是 ENTRYPOINT 的参数，
#              会变成 `/app/api /app/worker`，详见 docker-compose.yml 的注释）
#
# 构建上下文是 backend/ 本身：
#   docker build -t ashen-courier-backend ./backend
# ============================================================

# ---------- 阶段 1：构建 ----------
FROM golang:1.27-alpine AS build
WORKDIR /src

# 先只拷依赖清单，最大化层缓存
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev

# 静态编译：目标镜像里没有 libc，CGO 必须关掉
ENV CGO_ENABLED=0 GOOS=linux
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/api    ./cmd/api && \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/worker ./cmd/worker

# ---------- 阶段 2：运行时 ----------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api    /app/api
COPY --from=build /out/worker /app/worker

# nonroot（uid 65532）：容器内不需要任何特权
USER nonroot:nonroot

EXPOSE 8080

# distroless 里没有 shell 也没有 curl，所以健康检查只能靠二进制自己：
# -healthcheck 会请求本机 /healthz 并按其结果决定退出码
#
# start-period=35s 而不是 5s：启动时若 PG / Redis 还没就绪，进程会在内部退避重试
# 最多 30 秒（见 internal/startup），这期间它**活着但还没开始监听**。
# 用 5s 的话，每 10s 一次的探针会在这个窗口里攒够 3 次失败，报出一个假的 unhealthy。
# 这里只需盖住「启动到能服务」的上限，不影响常驻期的探活灵敏度。
HEALTHCHECK --interval=10s --timeout=4s --start-period=35s --retries=3 \
    CMD ["/app/api", "-healthcheck"]

ENTRYPOINT ["/app/api"]
