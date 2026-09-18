package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

// TakeDelta 原子地取走某短码当前的计数增量。
//
// ⚠️ 内部顺序是「先 SREM 再 GETDEL」，而不是直觉上的「先取值再移除」：
//
//	若先 GETDEL 再 SREM，存在丢更新窗口 ——
//	GETDEL 之后、SREM 之前若发生一次新点击（INCR + SADD），
//	SREM 会把这枚新点击重新摘掉 dirty 标记，那 +1 就再也不会被回刷。
//
//	反过来先 SREM 则两种交错都安全：
//	  · 新点击的 SADD 排在 SREM 之后 → dirty 里仍有它，下一轮补刷（GETDEL 得 0，无害）
//	  · 新点击的 SADD 排在 SREM 之前 → 它的 INCR 也必然在 GETDEL 之前，增量被一并取走
//
// 返回 0 表示这一轮没有增量（上一轮刚取过、dirty 标记尚未清理时会这样）。
func (c *Client) TakeDelta(ctx context.Context, code string) (int64, error) {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	pipe := c.rdb.Pipeline()
	pipe.SRem(opCtx, dirtySetKey, code)
	get := pipe.GetDel(opCtx, ClickCounterKey(code))

	if _, err := pipe.Exec(opCtx); err != nil && !isNil(err) {
		return 0, fmt.Errorf("store.redis: take delta %q: %w", code, err)
	}

	n, err := get.Int64()
	if err != nil {
		if isNil(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store.redis: take delta %q: %w", code, err)
	}
	return n, nil
}

// RestoreDelta 把「已被 TakeDelta 取走、但未能落库」的增量按值归还，并重新登记 dirty。
//
// 只补 dirty 标记是不够的：TakeDelta 用 GETDEL 把计数键删掉了，若只把短码塞回
// dirty 集合，下一轮 GETDEL 会得到 0，于是命中「本轮无增量」而跳过 —— 那部分点击
// 就永久丢失了（明细已通过 Stream 落库，基线增量却没了，两个口径再也对不上）。
// 因此必须把值本身写回去。
func (c *Client) RestoreDelta(ctx context.Context, code string, delta int64) error {
	if delta == 0 {
		return nil
	}
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	pipe := c.rdb.Pipeline()
	pipe.IncrBy(opCtx, ClickCounterKey(code), delta)
	pipe.SAdd(opCtx, dirtySetKey, code)

	if _, err := pipe.Exec(opCtx); err != nil {
		return fmt.Errorf("store.redis: restore delta %q: %w", code, err)
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
