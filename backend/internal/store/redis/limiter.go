package redis

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"ashen-courier/internal/domain"
)

// incrWindowScript 是限流的回落实现：INCR + 首次置过期。
//
// 原子性由 Redis 单线程执行 Lua 保证。之所以还需要它：
// 服务端可能是 Redis 8.8 以下版本，没有原生 INCREX 命令。
var incrWindowScript = goredis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// Limiter 是基于 Redis 的固定窗口计数器限流器。
//
// 降级策略：Redis 不可用时不熔断自锁 —— 全量放行并累计降级次数，
// 由 /healthz 暴露出来。宁可短暂失去限流，也不让整站 5xx。
type Limiter struct {
	client *Client
	// disabled 为 true 时全量放行。由 RATE_LIMIT_DISABLED 在启动时打开，
	// 并通过 /healthz 的 rate_limit_disabled 暴露出来。
	disabled atomic.Bool
	// degradeCount 统计因 Redis 故障而降级的次数。
	degradeCount atomic.Int64
}

// NewLimiter 构造限流器。
func NewLimiter(client *Client) *Limiter {
	return &Limiter{client: client}
}

// Allow 判定一次请求是否放行。key 由调用方按「维度:取值」构造，例如 "rl:create:1.2.3.4"。
//
// 返回的 error 只表示「Redis 出问题了」，此时 domain.RateDecision.Allowed 一定为 true ——
// 调用方应当记 warn 日志并继续放行。
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (domain.RateDecision, error) {
	if limit <= 0 || window <= 0 {
		// 配置不当视为不限流，但这是配置错误，交由调用方观测
		return domain.RateDecision{Allowed: true, Remaining: int64(limit)}, nil
	}
	if l.disabled.Load() {
		return domain.RateDecision{Allowed: true, Remaining: int64(limit)}, nil
	}

	count, err := l.incr(ctx, key, window)
	if err != nil {
		l.degradeCount.Add(1)
		return domain.RateDecision{Allowed: true, Remaining: int64(limit)}, err
	}

	if count > int64(limit) {
		return domain.RateDecision{Allowed: false, RetryAfter: l.ttl(ctx, key, window)}, nil
	}
	return domain.RateDecision{Allowed: true, Remaining: max(int64(limit)-count, 0)}, nil
}

// Disable 打开应急开关，全量放行。
func (l *Limiter) Disable() { l.disabled.Store(true) }

// Disabled 返回当前是否处于全量放行。
func (l *Limiter) Disabled() bool { return l.disabled.Load() }

// DegradeCount 返回因 Redis 故障而降级的累计次数。
func (l *Limiter) DegradeCount() int64 { return l.degradeCount.Load() }

// incr 递增窗口计数，返回递增后的值。
// 优先用 Redis 8.8+ 的原生 INCREX；探测不可用或执行失败时回落到 Lua 脚本。
func (l *Limiter) incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	opCtx, cancel := l.client.opCtx(ctx)
	defer cancel()

	millis := window.Milliseconds()
	if millis <= 0 {
		millis = 1
	}

	if l.client.increx {
		// INCREX key BYINT 1 PX <window> ENX
		//   ENX = 仅当键当前没有 TTL 时才设置过期 → 恰好是「固定窗口」语义
		// 回复是 [新值, 本次实际生效的增量]，**不是**「是否已带 TTL」——
		// 见 https://redis.io/docs/latest/commands/increx/ ；这里只取第一个元素，
		// 增量恒为 1，第二个元素没有信息量。
		vals, err := l.client.rdb.Do(opCtx, "INCREX", key, "BYINT", 1, "PX", millis, "ENX").Int64Slice()
		if err == nil && len(vals) > 0 {
			return vals[0], nil
		}
		// 执行失败（例如命令被 ACL 禁用）：静默回落到脚本，避免限流形同虚设
	}

	n, err := incrWindowScript.Run(opCtx, l.client.rdb, []string{key}, millis).Int64()
	if err != nil {
		return 0, fmt.Errorf("store.redis: rate limit incr %q: %w", key, err)
	}
	return n, nil
}

// ttl 读取键剩余存活时间，取不到时回落到整个窗口。
func (l *Limiter) ttl(ctx context.Context, key string, window time.Duration) time.Duration {
	opCtx, cancel := l.client.opCtx(ctx)
	defer cancel()

	ttl, err := l.client.rdb.PTTL(opCtx, key).Result()
	if err != nil || ttl <= 0 {
		return window
	}
	return ttl
}
