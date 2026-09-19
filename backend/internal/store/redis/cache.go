package redis

import (
	"context"
	"fmt"
	"time"

	"ashen-courier/internal/domain"
)

// 编译期断言：Cache / Recorder 分别实现 domain 的抽象。
var (
	_ domain.LinkCache     = (*Cache)(nil)
	_ domain.ClickRecorder = (*Recorder)(nil)
)

// Client 一个类型同时满足 worker 需要的三个端口（计数回刷 / 事件流 / 增量读取），
// 因此 cmd 层可以直接把同一个 *Client 传进 worker.Deps。
var (
	_ domain.ClickCounter     = (*Client)(nil)
	_ domain.ClickStream      = (*Client)(nil)
	_ domain.ClickDeltaReader = (*Client)(nil)
)

// Cache 是短码缓存的 Redis 实现。
type Cache struct {
	client *Client
}

// NewCache 构造缓存。
func NewCache(client *Client) *Cache { return &Cache{client: client} }

// cachedLinkWire 是缓存里的线格式。
//
// 与 domain.CachedLink 分开：线上格式是「已经发布过的数据」，
// 改字段名要当成一次不兼容变更；领域结构体可以自由重构。
type cachedLinkWire struct {
	ID        string     `json:"id"`
	ShortCode string     `json:"code"`
	TargetURL string     `json:"target"`
	Title     string     `json:"title,omitempty"`
	Status    int16      `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitzero"`
	// PasswordProtected 只记「有没有口令」；摘要留在 PG 里，缓存里不放（见 domain.CachedLink）。
	// 加字段是兼容变更（老条目解出来是 false）；禁的是改名。
	PasswordProtected bool `json:"password_protected,omitzero"`
}

// Get 用一次 MGET 同时探测正负缓存。三种结果：
//   - 命中正向：(*CachedLink, nil)
//   - 命中负缓存：(nil, ErrCacheKnownMissing)，调用方直接 404
//   - 都未命中：(nil, ErrCacheMiss)，调用方回源 PG
func (c *Cache) Get(ctx context.Context, code string) (*domain.CachedLink, error) {
	opCtx, cancel := c.client.opCtx(ctx)
	defer cancel()

	vals, err := c.client.rdb.MGet(opCtx, LinkKey(code), MissKey(code)).Result()
	if err != nil {
		return nil, fmt.Errorf("store.redis: get link cache %q: %w", code, err)
	}
	if len(vals) != 2 {
		return nil, fmt.Errorf("store.redis: link %q: %w", code, domain.ErrCacheMiss)
	}

	if raw, ok := vals[0].(string); ok && raw != "" {
		var wire cachedLinkWire
		if err := unmarshal(raw, &wire); err != nil {
			// 缓存内容损坏：按未命中处理，让调用方回源并覆盖
			return nil, fmt.Errorf("store.redis: decode link cache %q: %w", code, domain.ErrCacheMiss)
		}
		entry, err := wire.toDomain()
		if err != nil {
			return nil, fmt.Errorf("store.redis: decode link cache %q: %w", code, domain.ErrCacheMiss)
		}
		return entry, nil
	}
	if vals[1] != nil {
		return nil, fmt.Errorf("store.redis: link %q: %w", code, domain.ErrCacheKnownMissing)
	}
	return nil, fmt.Errorf("store.redis: link %q: %w", code, domain.ErrCacheMiss)
}

// Put 写正向缓存。
func (c *Cache) Put(ctx context.Context, link *domain.CachedLink, ttl time.Duration) error {
	payload, err := marshal(cachedLinkWire{
		ID:        link.ID.String(),
		ShortCode: link.ShortCode,
		TargetURL: link.TargetURL,
		Title:     link.Title,
		Status:    int16(link.Status),
		ExpiresAt: link.ExpiresAt,

		PasswordProtected: link.PasswordProtected,
	})
	if err != nil {
		return fmt.Errorf("store.redis: encode link cache %q: %w", link.ShortCode, err)
	}

	opCtx, cancel := c.client.opCtx(ctx)
	defer cancel()

	if err := c.client.rdb.Set(opCtx, LinkKey(link.ShortCode), payload, ttl).Err(); err != nil {
		return fmt.Errorf("store.redis: set link cache %q: %w", link.ShortCode, err)
	}
	return nil
}

// PutMissing 写短码负缓存。
func (c *Cache) PutMissing(ctx context.Context, code string, ttl time.Duration) error {
	opCtx, cancel := c.client.opCtx(ctx)
	defer cancel()

	if err := c.client.rdb.Set(opCtx, MissKey(code), "1", ttl).Err(); err != nil {
		return fmt.Errorf("store.redis: set miss cache %q: %w", code, err)
	}
	return nil
}

// Evict 删除短码的正负缓存。
func (c *Cache) Evict(ctx context.Context, codes ...string) error {
	if len(codes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(codes)*2)
	for _, code := range codes {
		keys = append(keys, LinkKey(code), MissKey(code))
	}

	opCtx, cancel := c.client.opCtx(ctx)
	defer cancel()

	if err := c.client.rdb.Del(opCtx, keys...).Err(); err != nil {
		return fmt.Errorf("store.redis: evict %d link caches: %w", len(codes), err)
	}
	return nil
}

// toDomain 把线格式转成领域结构体。
func (w cachedLinkWire) toDomain() (*domain.CachedLink, error) {
	id, err := parseUUID(w.ID)
	if err != nil {
		return nil, err
	}
	return &domain.CachedLink{
		ID:        id,
		ShortCode: w.ShortCode,
		TargetURL: w.TargetURL,
		Title:     w.Title,
		Status:    domain.LinkStatus(w.Status),
		ExpiresAt: w.ExpiresAt,

		PasswordProtected: w.PasswordProtected,
	}, nil
}

// Recorder 是点击统计的 Redis 实现：
// 一次 pipeline 同时完成「计数 +1」「标记 dirty」「写入 Stream」，只花一次往返。
type Recorder struct {
	client *Client
}

// NewRecorder 构造点击记录器。
func NewRecorder(client *Client) *Recorder { return &Recorder{client: client} }

// Record 记录一次点击。任一步失败都返回错误，但调用方只记日志、不影响跳转。
func (r *Recorder) Record(ctx context.Context, rec domain.ClickRecord) error {
	opCtx, cancel := r.client.opCtx(ctx)
	defer cancel()

	pipe := r.client.rdb.Pipeline()
	pipe.Incr(opCtx, ClickCounterKey(rec.Code))
	pipe.SAdd(opCtx, dirtySetKey, rec.Code)
	// XAdd 需要指针；(...)(rec) 由 new 表达式直接取地址，不额外起临时变量
	pipe.XAdd(opCtx, new(xaddArgs(rec)))

	if _, err := pipe.Exec(opCtx); err != nil {
		return fmt.Errorf("store.redis: record click %q: %w", rec.Code, err)
	}
	return nil
}
