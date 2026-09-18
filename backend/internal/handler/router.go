package handler

import (
	"log/slog"
	"net/http"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// Options 是 Router 的全部装配依赖。
type Options struct {
	// Logger 是结构化日志器。
	Logger *slog.Logger
	// Auth 是鉴权服务。
	Auth *service.Auth
	// Shortener 是短链服务。
	Shortener *service.Shortener
	// Stats 是统计服务。
	Stats *service.Stats
	// Health 汇总 /healthz 的探针。
	Health httpx.HealthProbe
	// Limiter 是限流器，nil 表示不限流。
	Limiter domain.RateLimiter

	// TrustProxy 为 true 时从 X-Real-IP 取客户端 IP。
	TrustProxy bool
	// CORSOrigins 是允许跨域的来源白名单（开发环境用）。
	CORSOrigins []string

	// PageSize / MaxPageSize 是链接列表的默认与最大页大小。
	PageSize    int
	MaxPageSize int

	// 三条限流规则。
	RateLimitCreate   httpx.RateLimitRule
	RateLimitLogin    httpx.RateLimitRule
	RateLimitRedirect httpx.RateLimitRule
}

// Router 装配全部路由并套上中间件链。
//
// 路由表（与 PLAN.md §7 一致）：
//
//	GET    /healthz
//	POST   /api/auth/register
//	POST   /api/auth/login
//	GET    /api/auth/me
//	POST   /api/links
//	GET    /api/links
//	GET    /api/links/{code}
//	PATCH  /api/links/{code}
//	DELETE /api/links/{code}
//	GET    /api/links/{code}/stats
//	POST   /api/links/{code}/claim
//	GET    /{code}          ← 302 跳转，兜底模式
//
// 关于 /{code}：ServeMux 会优先匹配「更具体」的模式，因此 /api/... 与
// /healthz 天然胜过 /{code}，无需手动排序。handler 内部再排一次保留字做双保险。
func Router(opts Options) http.Handler {
	authAPI := &authHandler{auth: opts.Auth}
	linkAPI := &linkHandler{
		shortener:   opts.Shortener,
		trustProxy:  opts.TrustProxy,
		pageSize:    opts.PageSize,
		maxPageSize: opts.MaxPageSize,
	}
	statsAPI := &statsHandler{shortener: opts.Shortener, stats: opts.Stats}
	redirectAPI := &redirectHandler{shortener: opts.Shortener, trustProxy: opts.TrustProxy}

	requireUser := requireAuth(opts.Auth)
	optionalUser := optionalAuth(opts.Auth)
	limit := func(rule httpx.RateLimitRule) httpx.Middleware {
		if opts.Limiter == nil {
			// 不限流时返回直通中间件，保持中间件链形状一致
			return func(next http.Handler) http.Handler { return next }
		}
		return httpx.RateLimit(opts.Limiter, rule, opts.TrustProxy, opts.Logger)
	}

	mux := http.NewServeMux()

	// ---- 基础设施 ----
	mux.Handle("GET /healthz", http.HandlerFunc(healthHandler(opts.Health)))

	// ---- 鉴权（登录接口限流防撞库）----
	mux.Handle("POST /api/auth/register", limit(opts.RateLimitLogin)(http.HandlerFunc(authAPI.register)))
	mux.Handle("POST /api/auth/login", limit(opts.RateLimitLogin)(http.HandlerFunc(authAPI.login)))
	mux.Handle("GET /api/auth/me", requireUser(http.HandlerFunc(authAPI.me)))

	// ---- 短链（创建可选登录；创建接口按 IP 限流）----
	mux.Handle("POST /api/links", limit(opts.RateLimitCreate)(optionalUser(http.HandlerFunc(linkAPI.create))))
	mux.Handle("GET /api/links", requireUser(http.HandlerFunc(linkAPI.list)))
	mux.Handle("GET /api/links/{code}", optionalUser(http.HandlerFunc(linkAPI.get)))
	mux.Handle("PATCH /api/links/{code}", optionalUser(http.HandlerFunc(linkAPI.update)))
	mux.Handle("DELETE /api/links/{code}", optionalUser(http.HandlerFunc(linkAPI.remove)))
	mux.Handle("POST /api/links/{code}/claim", requireUser(http.HandlerFunc(linkAPI.claim)))

	// ---- 统计（按 IP 做宽松限流，防刷）----
	mux.Handle("GET /api/links/{code}/stats",
		limit(opts.RateLimitRedirect)(optionalUser(http.HandlerFunc(statsAPI.show))))

	// ---- 短码跳转：兜底模式，必须最后注册 ----
	mux.Handle("/{code}", limit(opts.RateLimitRedirect)(http.HandlerFunc(redirectAPI.serve)))

	return httpx.Chain(mux,
		httpx.Recover(opts.Logger),
		httpx.RequestID,
		httpx.AccessLog(opts.Logger, opts.TrustProxy),
		httpx.CORS(opts.CORSOrigins...),
	)
}

// healthHandler 返回 /healthz 处理器。
// PG / Redis 任一异常 → 503，让编排系统（compose healthcheck、K8s）能感知。
func healthHandler(probe httpx.HealthProbe) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if probe == nil {
			httpx.WriteJSON(w, r, http.StatusOK, httpx.HealthReport{
				Status:   "ok",
				Postgres: "unknown",
				Redis:    "unknown",
			})
			return
		}

		report := probe.Report(r.Context())
		status := http.StatusOK
		if report.Status != "ok" {
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "2")
		}
		httpx.WriteJSON(w, r, status, report)
	}
}
