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

// LinkRef 在缓存里唯一定位一条短链：**域 + 短码**。
//
// 为什么不是只靠短码：分域之后，「同一个短码」在两个域下可以是两条不同的短链
// （N6-2 会让它成立），于是只按短码定位会读到别人的条目。
// 更早、更隐蔽的一种错是负缓存：按短码记的「不存在」会被跨域访问污染 ——
// 在默认域名上探一下 a.local 的短码，就会把「不属于默认域」记成「不存在」，
// 结果这条短链**在它自己的域上也 404**，直到负缓存 TTL 过期。
//
// DomainID 为 nil 表示默认域名（PUBLIC_BASE_URL 指向的那个），是一个有值的状态。
type LinkRef struct {
	// Code 是短码。
	Code string
	// DomainID 是所属自定义域名；nil 表示默认域名。
	DomainID *uuid.UUID
}

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
	// DomainID 是所属自定义域名；nil 表示默认域名（PUBLIC_BASE_URL 指向的那个）。
	//
	// **不能省**：跳转路径命中缓存后要校验「这个短码属于本次请求的域吗」，
	// 少了它缓存条目解出来的 DomainID 恒为 nil，于是所有挂在自定义域上的短链
	// 都会在**缓存命中**时被判成「串域」而 404 —— 回源的那一次却是好的，
	// 症状会随 TTL 在「能开 / 打不开」之间来回跳。
	//
	// 加字段是兼容变更：000006 之前写入的老条目解出来是 nil，而那时本来就没有自定义域。
	DomainID *uuid.UUID
	// Status 是状态机取值。
	Status LinkStatus
	// ExpiresAt 为空表示永久有效。
	ExpiresAt *time.Time
	// PasswordProtected 表示该短链需要口令才能跳转。
	//
	// 缓存里**只放这一个布尔**，不放 bcrypt 摘要：Redis 转储泄露不该 enable 离线爆破，
	// 而跳转路径只需要知道「有没有口令」。真正的比对发生在 POST /{code}（库读 + IP 限流）。
	PasswordProtected bool
}

// LinkCache 是短码缓存的抽象，实现在 internal/store/redis。
//
// 三个方法的定位单位都是「域 + 短码」（见 LinkRef），**不要退化成只按短码**：
// 缓存键与失效目标必须是同一个坐标系，否则读到的与删掉的不是同一条。
type LinkCache interface {
	// Get 读缓存；未命中返回包装了 ErrCacheMiss 的错误，
	// 命中负缓存返回包装了 ErrCacheKnownMissing 的错误。
	Get(ctx context.Context, code string, domainID *uuid.UUID) (*CachedLink, error)
	// Put 写正向缓存。键取自 link.DomainID —— 键与条目里的域天然一致。
	Put(ctx context.Context, link *CachedLink, ttl time.Duration) error
	// PutMissing 写负缓存（「该短码在**该域内**不存在」），抵御短码扫描器穿透到数据库。
	PutMissing(ctx context.Context, code string, domainID *uuid.UUID, ttl time.Duration) error
	// Evict 删除若干短链的正负缓存（改 / 删 / 过期后必须调用）。
	Evict(ctx context.Context, refs ...LinkRef) error
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
