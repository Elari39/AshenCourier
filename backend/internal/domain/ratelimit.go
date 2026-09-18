package domain

import (
	"context"
	"time"
)

// RateDecision 是一次限流判定结果。
type RateDecision struct {
	// Allowed 为 true 表示放行。
	Allowed bool
	// Remaining 是本窗口剩余配额；降级放行时等于 limit。
	Remaining int64
	// RetryAfter 是被拒时建议的重试等待时间。
	RetryAfter time.Duration
}

// RateLimiter 是限流抽象，实现在 internal/store/redis。
//
// 约定：返回的 error 只表示「Redis 出问题」；此时实现必须放行
// （Decision.Allowed = true）并把降级次数暴露出来，绝不熔断自锁。
type RateLimiter interface {
	// Allow 判定 key 在 window 窗口内是否还有 limit 的配额。
	Allow(ctx context.Context, key string, limit int, window time.Duration) (RateDecision, error)
	// Disabled 返回是否处于全量放行（应急开关）。
	Disabled() bool
	// DegradeCount 返回因 Redis 故障降级的累计次数。
	DegradeCount() int64
}
