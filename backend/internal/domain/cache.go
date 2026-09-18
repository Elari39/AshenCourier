package domain

import (
	"context"
	"errors"
	"time"
	"uuid"
)

// 缓存读取结果哨兵错误。
var (
	// ErrCacheMiss 表示缓存里没有该短码，需要回源数据库。
	ErrCacheMiss = errors.New("cache miss")
	// ErrCacheKnownMissing 表示命中负缓存，即「确认不存在」，无需回源。
	ErrCacheKnownMissing = errors.New("known missing")
)

// CachedLink 是跳转路径所需的最小字段集。
//
// 刻意不复用 Link：缓存里不需要 owner_id / key_hash 这类字段，
// 少放敏感字段就少一份泄露面；同时让 store 层能自由决定线格式。
type CachedLink struct {
	// ID 用于异步写点击明细时关联 link_id。
	ID uuid.UUID
	// ShortCode 是短码本身。
	ShortCode string
	// TargetURL 是目标地址。
	TargetURL string
	// Title 便于调试。
	Title string
	// Status 是状态机取值。
	Status LinkStatus
	// ExpiresAt 为空表示永久有效。
	ExpiresAt *time.Time
}

// LinkCache 是短码缓存的抽象，实现在 internal/store/redis。
type LinkCache interface {
	// Get 读缓存；未命中返回包装了 ErrCacheMiss 的错误，
	// 命中负缓存返回包装了 ErrCacheKnownMissing 的错误。
	Get(ctx context.Context, code string) (*CachedLink, error)
	// Put 写正向缓存。
	Put(ctx context.Context, link *CachedLink, ttl time.Duration) error
	// PutMissing 写负缓存，抵御短码扫描器穿透到数据库。
	PutMissing(ctx context.Context, code string, ttl time.Duration) error
	// Evict 删除若干短码的正负缓存（改 / 删后必须调用）。
	Evict(ctx context.Context, codes ...string) error
}

// ClickRecord 是一次待记录的点击。
//
// 它同时是 Stream 的载荷类型：跳转路径用它入队，worker 从 Stream 里解析出同一个
// 类型、补上 UA 解析结果再落库。刻意不再另立一个「StreamEvent」——两者字段完全
// 一致，重复定义只会让字段悄悄漂移。
type ClickRecord struct {
	// Code 是短码。
	Code string
	// LinkID 是短链 UUID。
	LinkID uuid.UUID
	// OccurredAt 是服务端记录的跳转时刻。
	OccurredAt time.Time
	// IP / UserAgent / Referer 是原始上下文。
	IP        string
	UserAgent string
	Referer   string
}

// ClickRecorder 是跳转路径上的非阻塞统计写入抽象，实现在 internal/store/redis。
//
// 实现必须保证「快」：调用方在 302 响应之后异步调用，它失败只记日志。
type ClickRecorder interface {
	Record(ctx context.Context, rec ClickRecord) error
}
