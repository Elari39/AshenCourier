package httpx

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ashen-courier/internal/domain"
)

// RateLimitDimension 决定限流键的构造维度。
type RateLimitDimension int

const (
	// RateLimitByIP 只按客户端 IP 限流（创建、登录、跳转）。
	RateLimitByIP RateLimitDimension = iota
	// RateLimitByIPAndCode 按 IP + 短码限流（统计等按资源维度的接口）。
	RateLimitByIPAndCode
)

// RateLimitRule 描述一条限流规则。
type RateLimitRule struct {
	// Scope 是键命名空间，如 "create" / "login" / "redirect"。
	Scope string
	// Limit 是窗口内允许的次数。
	Limit int
	// Window 是窗口长度。
	Window time.Duration
	// Dimension 是键的构造维度。
	Dimension RateLimitDimension
}

// RateLimit 返回限流中间件。
//
// 语义：
//   - Redis 故障时限流器会放行并返回 error，这里只记 warn 日志 —— 降级而非熔断
//   - 被拒时返回 429 + Retry-After（秒），前端据此提示「请 X 秒后重试」
func RateLimit(limiter domain.RateLimiter, rule RateLimitRule, trustProxy bool, logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rateLimitKey(rule, r, trustProxy)

			decision, err := limiter.Allow(r.Context(), key, rule.Limit, rule.Window)
			if err != nil {
				// 降级：放行但留痕，方便排查「为什么限流没生效」
				logger.Warn("限流器降级，本次请求直接放行",
					"scope", rule.Scope, "err", err,
					"request_id", RequestIDFrom(r.Context()))
			}

			if decision.Allowed {
				next.ServeHTTP(w, r)
				return
			}

			retryAfter := RetryAfterHeader(decision.RetryAfter)
			w.Header().Set("Retry-After", retryAfter)
			logger.Warn("请求被限流",
				"scope", rule.Scope, "key", key,
				"retry_after_seconds", retryAfter,
				"request_id", RequestIDFrom(r.Context()))
			WriteError(w, r, http.StatusTooManyRequests, "rate_limited",
				fmt.Sprintf("操作过于频繁，请 %s 秒后重试", retryAfter), "")
		})
	}
}

// rateLimitKey 构造限流键。
// 键里的维度值做了一层编码，避免短码里的 ':' 之类字符造成键空间歧义。
func rateLimitKey(rule RateLimitRule, r *http.Request, trustProxy bool) string {
	ip := ClientIP(r, trustProxy)

	var sb strings.Builder
	sb.Grow(len(rule.Scope) + len(ip) + 24)
	sb.WriteString("rl:")
	sb.WriteString(rule.Scope)
	sb.WriteString(":")
	sb.WriteString(ip)

	if rule.Dimension == RateLimitByIPAndCode {
		// 直接用路径最后一段作为资源标识；ServeMux 的 {code} 参数在中间件里拿不到，
		// 但限流只需要一个稳定的区分度，crc 一下避免长路径撑爆键
		sb.WriteString(":")
		sb.WriteString(shortHash(r.URL.Path))
	}
	return sb.String()
}

// shortHash 返回路径的短哈希，用于构造紧凑且定长的限流键。
// 这里用 FNV-1a，非加密用途，够快够省。
func shortHash(s string) string {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var h uint64 = offset64
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= prime64
	}
	return fmt.Sprintf("%x", h)
}
