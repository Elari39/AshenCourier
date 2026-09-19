// Command api 是 AshenCourier 的 HTTP 服务入口。
//
// 启动顺序：载入配置 → 建连接池 → 组装 service → 启动统计写入协程
// →（可选）内嵌 worker → 装配路由 → 起 HTTP 服务并阻塞。
// 收到 SIGINT / SIGTERM 后优雅关闭：先停 HTTP（等在途请求），
// 再停 worker，最后冲刷统计队列。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ashen-courier/internal/config"
	"ashen-courier/internal/domain"
	"ashen-courier/internal/handler"
	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
	"ashen-courier/internal/store/postgres"
	"ashen-courier/internal/store/redis"
	"ashen-courier/internal/worker"
)

// version 由构建时注入：-ldflags "-X main.version=1.2.3"。
var version = "dev"

func main() {
	// 容器的 HEALTHCHECK 会以这个模式调用自己：
	// distroless 镜像里没有 shell / curl，健康检查只能靠二进制自带。
	healthcheck := flag.Bool("healthcheck", false, "健康检查模式：探测本机 /healthz 后按其结果退出")
	probeBase := flag.String("probe-base", "http://127.0.0.1:8080", "健康检查探测的基址")
	flag.Parse()

	if *healthcheck {
		os.Exit(probeHealth(*probeBase))
	}

	if err := run(); err != nil {
		slog.Error("api 进程退出", "err", err)
		os.Exit(1)
	}
}

// probeHealth 请求本机 /healthz，返回进程退出码（0 = 健康）。
func probeHealth(base string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	logger.Info("启动 AshenCourier api",
		"version", version, "addr", cfg.HTTPAddr,
		"worker_enabled", cfg.WorkerEnabled, "trust_proxy", cfg.TrustProxy,
		"database", maskDSN(cfg.DatabaseURL), "redis", cfg.RedisAddr)

	// 收到 SIGINT / SIGTERM 时取消 ctx，触发全链路优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- 依赖 ----
	pg, err := postgres.Open(ctx, postgres.Options{
		DSN:       cfg.DatabaseURL,
		MaxConns:  16,
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

	// ---- 领域仓储与 service ----
	links := pg.Links()
	cache := redis.NewCache(rdb)
	shortener := service.NewShortener(links, cache, redis.NewRecorder(rdb), service.ShortenerConfig{
		BaseURL:     cfg.PublicBaseURL,
		CacheTTL:    cfg.CacheTTL,
		NegativeTTL: cfg.NegativeTTL,
	})
	authSvc := service.NewAuth(pg.Users(), cfg.JWTSecret, cfg.JWTExpiry)
	statsSvc := service.NewStats(links, pg.Clicks(), rdb)
	limiter := redis.NewLimiter(rdb)
	// 限流应急开关：打开后全量放行。刻意做成启动期开关而不是运行期端点 ——
	// 它只在限流组件本身出问题时用，重启一次完全可接受。
	if cfg.RateLimitDisabled {
		limiter.Disable()
		logger.Warn("限流应急开关已打开（RATE_LIMIT_DISABLED=true），本次启动全量放行")
	}

	// 统计写入协程用**独立**的 ctx，并且要到 HTTP 优雅关闭结束之后才取消。
	//
	// 直接复用上面的 ctx 会有一个静默丢数据的窗口：信号一到，写协程立刻 drain 并退出，
	// 而此刻 http.Server.Shutdown 还在等在途请求 —— 这些请求记录进来的点击只会堆在
	// 队列里被进程带走，既不写 Redis，也不计入 dropped_clicks。
	statsCtx, stopStats := context.WithCancel(context.WithoutCancel(ctx))
	defer stopStats() // 兜底：正常路径下已在关闭流程里显式调用过
	shortener.Start(statsCtx)
	defer shortener.Stop() // 兜底：正常路径下已在关闭流程里显式等待过

	// ---- 内嵌 worker（本地开发形态）----
	var embedded *worker.Worker
	if cfg.WorkerEnabled {
		embedded = worker.New(worker.Deps{
			Counts:  links,
			Sweeper: links,
			Clicks:  pg.Clicks(),
			Counter: rdb,
			Stream:  rdb,
			Cache:   cache,
		}, worker.Config{
			Consumer: consumerName(),
		}, logger.With("component", "worker"))
		embedded.Start(ctx)
		logger.Info("已内嵌启动 worker（WORKER_ENABLED=true）", "consumer", consumerName())
	}
	defer func() {
		if embedded != nil {
			embedded.Stop()
		}
	}()

	// ---- HTTP ----
	probe := &healthProbe{
		version:       version,
		pg:            pg,
		rdb:           rdb,
		shortener:     shortener,
		limiter:       limiter,
		worker:        embedded,
		workerEnabled: embedded != nil,
		startedAt:     time.Now(),
	}

	router := handler.Router(handler.Options{
		Logger:      logger,
		Auth:        authSvc,
		Shortener:   shortener,
		Stats:       statsSvc,
		Health:      probe,
		Limiter:     limiter,
		DeltaBatch:  rdb,
		TrustProxy:  cfg.TrustProxy,
		CORSOrigins: corsOrigins(cfg.PublicBaseURL),
		PageSize:    cfg.LinkPageSize,
		MaxPageSize: cfg.MaxLinkPageSize,
		// 点击明细复用列表的分页配置：两者都是「一页 N 条」的表格，
		// 分成两组配置只会让运维多记一个旋钮，实际也很少需要分别调。
		ClickPageSize:    cfg.LinkPageSize,
		MaxClickPageSize: cfg.MaxLinkPageSize,

		RateLimitCreate: httpx.RateLimitRule{
			Scope: "create", Limit: cfg.RateLimitCreatePerMin, Window: time.Minute,
			Dimension: httpx.RateLimitByIP,
		},
		RateLimitLogin: httpx.RateLimitRule{
			Scope: "login", Limit: cfg.RateLimitLoginPerWindow, Window: cfg.RateLimitLoginWindow,
			Dimension: httpx.RateLimitByIP,
		},
		RateLimitRedirect: httpx.RateLimitRule{
			Scope: "redirect", Limit: cfg.RateLimitRedirectPerMin, Window: time.Minute,
			Dimension: httpx.RateLimitByIP,
		},
		// 统计单独一条规则：scope 不同 → Redis 键独立，不会与真实跳转互相吃配额；
		// 维度取 IP+短码，看板轮询只消耗该短码自己的配额。
		RateLimitStats: httpx.RateLimitRule{
			Scope: "stats", Limit: cfg.RateLimitStatsPerMin, Window: time.Minute,
			Dimension: httpx.RateLimitByIPAndCode,
		},
	})

	srv := httpx.NewServer(httpx.ServerConfig{
		Addr:            cfg.HTTPAddr,
		Handler:         router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	// 等待「信号到达」或「服务自身异常退出」，两条路都会让 Run 返回
	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("收到退出信号")
		runErr = <-errCh
	case runErr = <-errCh:
	}

	// 到这里 HTTP 已不再接新请求、在途请求也已结束，此刻才让统计协程把队列冲完。
	// Stop 内部是 wg.Wait，会一直等到 drain 结束（最长 drainTimeout）。
	stopStats()
	shortener.Stop()

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	logger.Info("api 已退出")
	return nil
}

// healthProbe 汇总 /healthz 的全部探针。
type healthProbe struct {
	version       string
	pg            *postgres.DB
	rdb           *redis.Client
	shortener     *service.Shortener
	limiter       domain.RateLimiter
	worker        *worker.Worker
	workerEnabled bool
	startedAt     time.Time
}

// Report 采集一次健康报告。任一依赖异常 → status=degraded（HTTP 503）。
func (p *healthProbe) Report(ctx context.Context) httpx.HealthReport {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	report := httpx.HealthReport{
		Status:            "ok",
		Version:           p.version,
		Postgres:          "ok",
		Redis:             "ok",
		WorkerEnabled:     p.workerEnabled,
		DroppedClicks:     p.shortener.DroppedClicks(),
		FailedClicks:      p.shortener.FailedClicks(),
		QueueLen:          p.shortener.QueueLen(),
		PGFallbacks:       p.shortener.PGFallbacks(),
		RateLimitDegraded: p.limiter.DegradeCount(),
		RateLimitByNative: p.rdb.SupportsINCREX(),
		RateLimitDisabled: p.limiter.Disabled(),
		UptimeSeconds:     int64(time.Since(p.startedAt).Seconds()),
	}

	if err := p.pg.Ping(probeCtx); err != nil {
		report.Postgres = "error"
		report.Errors = append(report.Errors, "postgres 不可用")
	}
	if err := p.rdb.Ping(probeCtx); err != nil {
		report.Redis = "error"
		report.Errors = append(report.Errors, "redis 不可用")
	}
	if report.Postgres != "ok" || report.Redis != "ok" {
		report.Status = "degraded"
	}

	// 以下为观测指标，取不到不影响 status
	if n, err := p.rdb.StreamLen(probeCtx); err == nil {
		report.StreamLen = n
	}
	if n, err := p.rdb.PendingCount(probeCtx); err == nil {
		report.StreamPending = n
	}
	if p.worker != nil {
		stats := p.worker.Stats()
		report.ConsumedClicks = stats.Consumed
		report.WorkerErrors = stats.Errors
	}
	return report
}

// newLogger 构造 JSON 结构化日志器。
func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// consumerName 生成 worker 消费者名：hostname-pid，多副本部署时天然唯一。
func consumerName() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// corsOrigins 允许短链自身的对外源跨域调用（开发时前端跑在别的端口）。
func corsOrigins(baseURL string) []string {
	origin := httpx.OriginOf(baseURL)
	if origin == "" {
		return nil
	}
	return []string{origin}
}

// maskDSN 把连接串里的口令替换成 ***，避免写进日志。
func maskDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<无法解析>"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}
