package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

// PendingDelta 读取某短码尚未回刷到 PG 的计数增量；键不存在返回 0。
func (c *Client) PendingDelta(ctx context.Context, code string) (int64, error) {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	n, err := c.rdb.Get(opCtx, ClickCounterKey(code)).Int64()
	if err != nil {
		if isNil(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store.redis: pending delta %q: %w", code, err)
	}
	return n, nil
}

// PendingDeltas 用一次 MGET 读出多个短码尚未回刷进 PG 的增量。
//
// 为什么不是循环调 PendingDelta：列表页一页最多 100 条（MaxLinkPageSize），
// 逐条 GET 就是 100 次往返；MGET 一次拿完，列表接口的延迟不随页大小线性增长。
//
// 键不存在的短码不进结果（那表示「没有待同步增量」，不是错误）；
// 值解析不出来时按 0 处理并记 debug：统计口径掉一点，好过把列表打成 5xx。
func (c *Client) PendingDeltas(ctx context.Context, codes []string) (map[string]int64, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(codes))
	for _, code := range codes {
		keys = append(keys, ClickCounterKey(code))
	}

	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	values, err := c.rdb.MGet(opCtx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("store.redis: pending deltas (%d codes): %w", len(codes), err)
	}

	out := make(map[string]int64, len(codes))
	for i, raw := range values {
		if i >= len(codes) || raw == nil {
			continue
		}
		n, ok := deltaValue(raw)
		if !ok {
			slog.Debug("计数增量值无法解析，按 0 处理", "code", codes[i], "value", raw)
			continue
		}
		out[codes[i]] = n
	}
	return out, nil
}

// deltaValue 把 MGET 的返回值转成整数。go-redis 默认给 string，但换编解码器
// （或用 Do 手动发命令）时可能是 []byte 或 int64，这里一并认掉。
func deltaValue(raw any) (int64, bool) {
	switch v := raw.(type) {
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	case []byte:
		n, err := strconv.ParseInt(string(v), 10, 64)
		return n, err == nil
	case int64:
		return v, true
	default:
		return 0, false
	}
}

// DirtyCodes 返回最多 limit 个待回刷的短码。
// 用 SRANDMEMBER 而不是 SMEMBERS：dirty 集合可能很大，随机取样能让回刷进度更均匀。
func (c *Client) DirtyCodes(ctx context.Context, limit int) ([]string, error) {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	if limit <= 0 {
		limit = 1000
	}
	codes, err := c.rdb.SRandMemberN(opCtx, dirtySetKey, int64(limit)).Result()
	if err != nil {
		if isNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store.redis: dirty codes: %w", err)
	}
	return codes, nil
}

// TakeDelta 读取某短码当前的计数增量。
//
// ⚠️ 这里刻意**不删键** —— 这是 M3-1 要修的「崩溃少计」窗口：
//
//	旧实现是 SREM + GETDEL「取走即删」：进程若在 GETDEL 之后被 SIGKILL，
//	那批增量就永久少计（写库报错的路径还能按值归还，崩溃这条路不能）。
//
//	新实现只读，于是崩溃只剩两种结果：
//	  · 崩在写库之前 → 键里还有 delta、dirty 也还在 → 下一轮重做，**不丢**
//	  · 崩在写库之后、结算之前 → 基线多算一批（≤ 一个批次），是**重复累加**而非丢失
//
// 与 README 的口径一致：宁可重复累加，也不能丢。
// 结算归 SettleDelta；写库失败时**什么都不用还** —— 值一直留在键里。
//
// 返回 0 表示这一轮没有增量。
func (c *Client) TakeDelta(ctx context.Context, code string) (int64, error) {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	n, err := c.rdb.Get(opCtx, ClickCounterKey(code)).Int64()
	if err != nil {
		if isNil(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store.redis: take delta %q: %w", code, err)
	}
	return n, nil
}

// settleDeltaScript 原子地结算一批增量：
//  1. delta > 0 时减掉这批增量；
//  2. 减到 0（或更少）就把计数键删掉 —— 只 DECRBY 的话，每个被点过的短码都会永久
//     留下一个值为 0 的键（这些键没有 TTL），键数量随「历史上被点过的链接数」无限增长；
//  3. **只在没有残留时**才摘 dirty 标记（见下面的「为什么第 3 步不能无条件做」）。
//
// 整段必须原子：拆成「先减、后删」两条命令时，若两次之间有新点击（INCR），
// 那个 DEL 会把新点击一起删掉 —— 少计一次，正是本批次要消灭的东西。
//
// 为什么第 3 步不能无条件做：TakeDelta（GET）→ AddClickCount（PG）→ SettleDelta（本脚本）
// 是**三次独立往返**。若这中间来了新点击，键已经 INCR 到 delta+k 且重新 SADD 了 dirty，
// 而本脚本只知道 delta —— 减完还剩 k。此时若照旧摘掉 dirty，那 k 条就既不在 dirty 里、
// 又留在键里没人扫：基线落后 k 条，且键永不删除，只有等下一次点击重新 SADD 才能自愈
// （链接若从此无人再点，就永远差这几条）。
var settleDeltaScript = goredis.NewScript(`
local delta = tonumber(ARGV[1])
local left = 0
if delta > 0 then
  left = redis.call('DECRBY', KEYS[1], delta)
  if left <= 0 then
    redis.call('DEL', KEYS[1])
    left = 0
  end
end
-- 有残留就不摘 dirty：留给下一轮继续回刷。
if left <= 0 then
  redis.call('SREM', KEYS[2], ARGV[2])
end
return left
`)

// SettleDelta 在增量成功写进 PG 基线之后结算：减掉这批增量 + 摘掉 dirty 标记。
//
// 两步必须原子（Lua 由 Redis 单线程执行），否则会留下两种坏状态：
//   - 只减不摘：短码下一轮被再扫一次（读到 0，无害但白跑）
//   - 只摘不减：那批增量留在键里却再也不会被回刷 —— 等于少计
//
// delta <= 0 表示「这一轮没有增量」，此时只摘标记。
//
// ⚠️ 结算之后键里**可能仍有残留**（这轮之后又来了新点击）。此时 dirty 标记刻意保留：
// 残留还在键里，摘掉标记就等于让它们再也等不到下一次回刷。细节见 settleDeltaScript。
// 返回值仍然只是 error —— 残留量由下一轮的 TakeDelta 自然读到，上层无需感知。
func (c *Client) SettleDelta(ctx context.Context, code string, delta int64) error {
	if delta < 0 {
		delta = 0
	}

	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	keys := []string{ClickCounterKey(code), dirtySetKey}
	if _, err := settleDeltaScript.Run(opCtx, c.rdb, keys, delta, code).Result(); err != nil {
		return fmt.Errorf("store.redis: settle delta %q: %w", code, err)
	}
	return nil
}

// MarkDirty 把短码重新登记回 dirty 集合，用于 PG 写入失败后的重排队。
func (c *Client) MarkDirty(ctx context.Context, codes ...string) error {
	if len(codes) == 0 {
		return nil
	}
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	if err := c.rdb.SAdd(opCtx, dirtySetKey, anySlice(codes)...).Err(); err != nil {
		return fmt.Errorf("store.redis: mark dirty: %w", err)
	}
	return nil
}
