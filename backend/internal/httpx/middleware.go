package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Middleware 是标准的 http.Handler 装饰器。
type Middleware func(http.Handler) http.Handler

// Chain 从外到内依次包裹：Chain(h, a, b) 的调用顺序是 a → b → h。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, mw := range slices.Backward(mws) {
		h = mw(h)
	}
	return h
}

// RequestID 注入请求 ID：优先复用上游（nginx）传入的，否则新生成一个。
// 值会写回响应头，方便用户报障时直接给出。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Recover 兜底 panic：记 error 日志 + 回 500，保证单个请求的崩溃不会带崩进程。
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler 是 net/http 约定的「静默中断」，不该当故障处理
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					logger.Error("请求处理发生 panic",
						"panic", rec,
						"method", r.Method, "path", r.URL.Path,
						"request_id", RequestIDFrom(r.Context()))
					WriteError(w, r, http.StatusInternalServerError, "internal", "服务器内部错误", "")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog 记录结构化访问日志。跳转接口是热路径，这里刻意只保留必要字段。
// trustProxy 决定客户端 IP 取自 X-Real-IP 还是 RemoteAddr。
func AccessLog(logger *slog.Logger, trustProxy bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			logger.LogAttrs(r.Context(), levelFor(rec.status), "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("latency", time.Since(start)),
				slog.String("ip", ClientIP(r, trustProxy)),
				slog.String("request_id", RequestIDFrom(r.Context())),
			)
		})
	}
}

// CORS 按白名单回显 Origin。只在开发（前端跑在 5173）时真正生效；
// 生产是同源部署，走不到这里。
func CORS(allowedOrigins ...string) Middleware {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o != "" {
			allowed[o] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				// 命中白名单才回显 ACAO；但**无论是否命中都要 Vary: Origin**——
				// 否则共享缓存会把「没带 CORS 头」的响应回给允许的来源（或反过来），
				// 这类缓存投毒在 CORS 里是经典问题。
				w.Header().Add("Vary", "Origin")
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
			// 预检只可能发生在 /api 下：跨域 fetch 的是接口，而短码跳转是导航，
			// 不会发预检。其他路径的 OPTIONS 交给 mux，让它照常回 405，
			// 而不是拿一个假的 204 掩盖「这个路径不支持 OPTIONS」。
			if r.Method == http.MethodOptions && strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers",
					"Content-Type, Authorization, X-Manage-Key, X-Request-Id")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 记录状态码与响应字节数，供访问日志使用。
//
// 用独立的 wrote 标志而不是拿 status 当哨兵：初始 status 就是 200，
// 若处理器先用 Write 隐式写出 200 再调 WriteHeader(500)（net/http 会忽略
// 第二次调用），拿 status 判断会把「已隐式 200」误认成「还没写头」，
// 于是日志记成 500 而客户端实际收到 200。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

// WriteHeader 记录首次状态码（后续重复调用被忽略，与 net/http 语义一致）。
func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Write 记录写入字节数；首次写入意味着隐式 200。
func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush 透传 Flush，避免破坏流式响应（虽然本项目暂时没有 SSE）。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.ResponseController 能拿到底层 writer。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// levelFor 按状态码决定日志级别：5xx 用 error，4xx 用 warn，其余 info。
func levelFor(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// sanitizeRequestID 校验上游传来的请求 ID：只接受可打印 ASCII 且不超长。
// 不校验的话，攻击者可以往日志里注入换行与伪造内容。
func sanitizeRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return ""
	}
	for i := range len(raw) {
		c := raw[i]
		if c < 0x21 || c > 0x7e {
			return ""
		}
	}
	return raw
}

// RetryAfterHeader 把重试等待时间转成 Retry-After 头要求的秒数（向上取整，至少 1）。
func RetryAfterHeader(d time.Duration) string {
	seconds := int64(d.Seconds())
	if d > time.Duration(seconds)*time.Second {
		seconds++
	}
	return strconv.FormatInt(max(seconds, 1), 10)
}
