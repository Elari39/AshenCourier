package redis

import (
	"context"
	"fmt"
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
