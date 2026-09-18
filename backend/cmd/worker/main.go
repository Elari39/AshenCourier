// Command worker 是点击事件的独立消费者进程。
//
// 与 api 用同一份代码（internal/worker），只是入口不同：
// 生产环境下 api 的 WORKER_ENABLED=false，由本容器独占消费 Stream。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ashen-courier/internal/config"
	"ashen-courier/internal/store/postgres"
	"ashen-courier/internal/store/redis"
	"ashen-courier/internal/worker"
)

// version 由构建时注入：-ldflags "-X main.version=1.2.3"。
var version = "dev"

// statsEvery 是运行指标的打点周期。
const statsEvery = 60 * time.Second

func main() {
	// 容器的 HEALTHCHECK 会以这个模式调用自己：distroless 镜像里没有 shell，
	// 而 Dockerfile 的默认健康检查是 /app/api -healthcheck，对 worker 不适用。
	healthcheck := flag.Bool("healthcheck", false, "健康检查模式：探测 PG / Redis 后按其结果退出")
	flag.Parse()

	if *healthcheck {
		os.Exit(probeDependencies())
	}

	if err := run(); err != nil {
		slog.Error("worker 进程退出", "err", err)
		os.Exit(1)
	}
}

// probeDependencies 探测 worker 依赖的 PostgreSQL 与 Redis，返回进程退出码（0 = 健康）。
// worker 没有 HTTP 端口，所以不能用「请求 /healthz」那种探针。
func probeDependencies() int {
	cfg, err := config.LoadFor(config.RoleWorker)
	if err != nil {
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pg, err := postgres.Open(ctx, postgres.Options{
		DSN:       cfg.DatabaseURL,
		MaxConns:  2,
		OpTimeout: cfg.PGTimeout,
	})
	if err != nil {
		return 1
	}
	defer pg.Close()
	if err := pg.Ping(ctx); err != nil {
		return 1
	}

	rdb, err := redis.Open(ctx, redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
		Timeout:  cfg.RedisTimeout,
	})
	if err != nil {
		return 1
	}
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(ctx); err != nil {
		return 1
	}
	return 0
}

func run() error {
	// worker 不签发令牌、不拼对外短链，因此不强制校验 JWT_SECRET / PUBLIC_BASE_URL
	cfg, err := config.LoadFor(config.RoleWorker)
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	consumer := consumerName()
	logger.Info("启动 AshenCourier worker",
		"version", version, "consumer", consumer, "redis", cfg.RedisAddr)

	pg, err := postgres.Open(ctx, postgres.Options{
		DSN:       cfg.DatabaseURL,
		MaxConns:  8,
		OpTimeout: cfg.PGTimeout,
	})
	if err != nil {
		return err
	}
	defer pg.Close()

	rdb, err := redis.Open(ctx, redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
		Timeout:  cfg.RedisTimeout,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			logger.Warn("关闭 Redis 客户端失败", "err", err)
		}
	}()
	logger.Info("Redis 能力探测完成", "native_increx", rdb.SupportsINCREX())

	wk := worker.New(pg.Links(), pg.Clicks(), rdb, worker.Config{Consumer: consumer}, logger)
	wk.Start(ctx)

	// 定期打点，便于观测消费速率与是否有积压
	reportTicker := time.NewTicker(statsEvery)
	defer reportTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("收到退出信号，等待当前批次处理完成")
			wk.Stop()
			logStats(logger, wk, rdb)
			logger.Info("worker 已退出")
			return nil
		case <-reportTicker.C:
			logStats(logger, wk, rdb)
		}
	}
}

// logStats 打点一次运行指标。
func logStats(logger *slog.Logger, wk *worker.Worker, rdb *redis.Client) {
	stats := wk.Stats()
	attrs := []any{
		"consumed", stats.Consumed,
		"claimed", stats.Claimed,
		"malformed", stats.Malformed,
		"count_synced", stats.CountSynced,
		"expired", stats.Expired,
		"errors", stats.Errors,
	}
	// 探针失败不影响打点
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if n, err := rdb.StreamLen(probeCtx); err == nil {
		attrs = append(attrs, "stream_len", n)
	}
	if n, err := rdb.PendingCount(probeCtx); err == nil {
		attrs = append(attrs, "stream_pending", n)
	}
	logger.Info("worker 运行指标", attrs...)
}

// newLogger 构造 JSON 结构化日志器。
func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// consumerName 生成消费者名：hostname-pid，多副本部署时天然唯一。
func consumerName() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}
