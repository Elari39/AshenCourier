package handler

import (
	"io"
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
	// DeltaBatch 读取列表页尚未回刷进 PG 的计数增量；nil 表示列表只报 PG 基线。
	DeltaBatch domain.ClickDeltaBatchReader
	// Unlock 签发/校验短链访问口令的解锁凭据；nil 时带口令的链接一律不放行（fail-closed）。
	Unlock *service.LinkUnlocker
	// SecureCookies 为 true 时解锁 cookie 带 Secure（仅 https 部署）。
	SecureCookies bool

	// TrustProxy 为 true 时从 X-Real-IP 取客户端 IP。
	TrustProxy bool
	// CORSOrigins 是允许跨域的来源白名单（开发环境用）。
	CORSOrigins []string

	// PageSize / MaxPageSize 是链接列表的默认与最大页大小。
	PageSize    int
	MaxPageSize int
	// ClickPageSize / MaxClickPageSize 是点击明细列表的默认与最大页大小。
	ClickPageSize    int
	MaxClickPageSize int

	// 五条限流规则。
	RateLimitCreate   httpx.RateLimitRule
	RateLimitLogin    httpx.RateLimitRule
	RateLimitRedirect httpx.RateLimitRule
	RateLimitStats    httpx.RateLimitRule
	// RateLimitUnlock 是短链口令校验的配额，防在线爆破。
	RateLimitUnlock httpx.RateLimitRule
}

// Router 装配全部路由并套上中间件链。
//
// 路由表（与 PLAN.md §7 一致）：
//
//	GET    /healthz
//	GET    /metrics         ← Prometheus 文本格式（**不对外**，见 metricsHandler）
//	POST   /api/auth/register
//	POST   /api/auth/login
//	GET    /api/auth/me
//	POST   /api/links
//	GET    /api/links
//	GET    /api/links/{code}
//	PATCH  /api/links/{code}
//	DELETE /api/links/{code}
//	GET    /api/links/{code}/stats
//	GET    /api/links/{code}/clicks
//	GET    /api/links/{code}/qr.svg   ← 二维码图片（公开可读，见 qrHandler）
//	POST   /api/links/{code}/claim
//	GET    /{code}          ← 302 跳转；带口令且未解锁时是 200 口令页
//	POST   /{code}          ← 口令校验：成功 303 回 GET，失败 401
//
// 关于 /{code}：ServeMux 会优先匹配「更具体」的模式，因此 /api/... 与
// /healthz 天然胜过 /{code}，无需手动排序。handler 内部再排一次保留字做双保险。
//
// 未命中的路径有两套表现，刻意不同：
//   - `/api` 命名空间内 → 统一 JSON 错误体（未注册路径 404、方法不对 405），
//     由 httpx.APIErrorContract 在响应侧改写 ServeMux 内建的纯文本错误；
//   - 其余路径 → 落到 `GET /{code}` 兜底，按短码语义回「这条短链不存在」
//     或「页面不存在」（保留字），非 GET 则是 ServeMux 内建的 405 纯文本。
func Router(opts Options) http.Handler {
	authAPI := &authHandler{auth: opts.Auth}
	linkAPI := &linkHandler{
		shortener:   opts.Shortener,
		trustProxy:  opts.TrustProxy,
		pageSize:    opts.PageSize,
		maxPageSize: opts.MaxPageSize,
		deltas:      opts.DeltaBatch,
	}
	statsAPI := &statsHandler{shortener: opts.Shortener, stats: opts.Stats}
	clickAPI := &clickHandler{
		shortener: opts.Shortener,
		stats:     opts.Stats,
		pageSize:  opts.ClickPageSize,
		maxSize:   opts.MaxClickPageSize,
	}
	qrAPI := &qrHandler{shortener: opts.Shortener}
	redirectAPI := &redirectHandler{
		shortener:     opts.Shortener,
		unlocker:      opts.Unlock,
		trustProxy:    opts.TrustProxy,
		secureCookies: opts.SecureCookies,
	}

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

	// /metrics 与 /healthz 同源（同一次 Report），只换一种给 Prometheus 看的形态。
	// **不对外**：nginx 里 `location = /metrics { return 404; }` 挡住了公网，
	// 只有同一网络内的抓取方能到 —— 所以这里不需要鉴权，也不该加限流
	// （限流会把「每 15 秒抓一次」的固定开销变成配额消耗）。
	mux.Handle("GET /metrics", http.HandlerFunc(metricsHandler(opts.Health)))

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

	// ---- 统计（独立的宽松限流，防刷）----
	// 必须用独立规则而不是复用 RateLimitRedirect：限流键是
	// rl:<scope>:<ip>:<路径哈希>，scope 相同就意味着统计与真实跳转共享
	// 同一条配额 —— 详情页轮询统计会把用户的短链跳转也拖到 429。
	// 这里选 IP + 短码维度：看板刷得再勤，也只消耗该短码自己的配额。
	mux.Handle("GET /api/links/{code}/stats",
		limit(opts.RateLimitStats)(optionalUser(http.HandlerFunc(statsAPI.show))))

	// 明细与统计共用同一条限流规则：两者都是详情页刷出来的读请求，
	// 维度同为 IP + 短码 —— 翻页翻得再凶也只消耗该短码自己的配额，
	// 不会波及真实跳转的配额（scope 不同 → Redis 键不同）。
	mux.Handle("GET /api/links/{code}/clicks",
		limit(opts.RateLimitStats)(optionalUser(http.HandlerFunc(clickAPI.list))))

	// 二维码图片：公开可读（理由见 qrHandler），但仍挂上限流 ——
	// 复用统计那条规则（IP + 路径哈希）：既防刷，又不会和真实跳转共享配额
	// （scope 不同 ⇒ Redis 键不同；路径不同 ⇒ 哈希不同）。
	mux.Handle("GET /api/links/{code}/qr.svg",
		limit(opts.RateLimitStats)(http.HandlerFunc(qrAPI.serve)))

	// ---- 短码跳转：兜底模式 ----
	// 显式限定 GET：不加方法前缀时 POST /abc、DELETE /abc 也会命中这里
	// 并返回 302（顺带记一次点击）。ServeMux 的 "GET" 模式顺带匹配 HEAD。
	mux.Handle("GET /{code}", limit(opts.RateLimitRedirect)(http.HandlerFunc(redirectAPI.serve)))

	// 口令校验：方法与 GET 不同，因此两条模式并存互不遮蔽。
	// 限流按 IP 防在线爆破；scope 与 login/redirect 不同 ⇒ Redis 键独立，不会互相吃配额。
	mux.Handle("POST /{code}", limit(opts.RateLimitUnlock)(http.HandlerFunc(redirectAPI.unlock)))

	// APIErrorContract 贴在 mux 内侧：ServeMux 只在「路径没注册」时回 404、
	// 在「路径注册了但方法不对」时回 405，两者都是纯文本，绕过了统一错误体。
	// 它只改响应形态，不参与路由决策 —— 明确不注册 `/api/` 兜底模式，
	// 那样会把 405 降级成 404（见该函数注释）。
	return httpx.Chain(httpx.APIErrorContract(mux),
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

// metricsHandler 返回 /metrics 处理器（Prometheus 文本格式 0.0.4）。
//
// 与 healthHandler 的一处刻意差别：**PG / Redis 挂了也回 200**。
// healthz 用状态码告诉编排系统「别把流量给我」；而指标端点的职责是把当前观测值
// 交出去 —— `ashen_postgres_up 0` 正是这一轮抓取里最有价值的一条数据，
// 回 503 会让抓取端把整份响应丢掉，反而在最需要观测的时刻失去观测能力。
//
// 内容类型必须写成 `text/plain; version=0.0.4; charset=utf-8`：Prometheus 靠
// `version=` 参数判断该按哪个版本的文本格式解析，缺了它只能靠内容嗅探。
func metricsHandler(probe httpx.HealthProbe) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := httpx.HealthReport{Status: "ok", Postgres: "unknown", Redis: "unknown"}
		if probe != nil {
			report = probe.Report(r.Context())
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		// 与 JSON 响应同一套思路：这份文本不该被任何浏览器当别的类型去解释
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		if _, err := io.WriteString(w, httpx.RenderMetrics(report)); err != nil {
			// 状态码已经写出去了，客户端提前断开只能记一条日志
			slog.Debug("写出 /metrics 响应中断", "err", err)
		}
	}
}
