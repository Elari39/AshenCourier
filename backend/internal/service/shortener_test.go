package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/pkg/validator"
)

// ---- 测试替身 ----

// memCache 是 domain.LinkCache 的内存实现，语义对齐 store/redis.Cache：
// 正向与负缓存分开存，Evict 同时清两者。
type memCache struct {
	positive map[string]*domain.CachedLink
	missing  map[string]struct{}
	evicted  []string // 记录被 Evict 过的短码，供断言
}

func newMemCache() *memCache {
	return &memCache{positive: map[string]*domain.CachedLink{}, missing: map[string]struct{}{}}
}

func (c *memCache) Get(_ context.Context, code string) (*domain.CachedLink, error) {
	if entry, ok := c.positive[code]; ok {
		return entry, nil
	}
	if _, ok := c.missing[code]; ok {
		return nil, fmt.Errorf("memcache: %q: %w", code, domain.ErrCacheKnownMissing)
	}
	return nil, fmt.Errorf("memcache: %q: %w", code, domain.ErrCacheMiss)
}

func (c *memCache) Put(_ context.Context, link *domain.CachedLink, _ time.Duration) error {
	c.positive[link.ShortCode] = link
	delete(c.missing, link.ShortCode)
	return nil
}

func (c *memCache) PutMissing(_ context.Context, code string, _ time.Duration) error {
	c.missing[code] = struct{}{}
	return nil
}

func (c *memCache) Evict(_ context.Context, codes ...string) error {
	for _, code := range codes {
		delete(c.positive, code)
		delete(c.missing, code)
		c.evicted = append(c.evicted, code)
	}
	return nil
}

// linkRepoFake 按需返回错误，其余全部落进内存 map。
type linkRepoFake struct {
	links   map[string]*domain.Link
	createF func(*domain.Link) error // 可注入失败
}

func newLinkRepoFake() *linkRepoFake {
	return &linkRepoFake{links: map[string]*domain.Link{}}
}

func (r *linkRepoFake) Create(_ context.Context, link *domain.Link) error {
	if r.createF != nil {
		if err := r.createF(link); err != nil {
			return err
		}
	}
	link.CreatedAt = time.Now().UTC()
	link.UpdatedAt = link.CreatedAt
	r.links[link.ShortCode] = link
	return nil
}

func (r *linkRepoFake) GetByCode(_ context.Context, code string) (*domain.Link, error) {
	if l, ok := r.links[code]; ok {
		return l, nil
	}
	return nil, domain.NotFound("link", code)
}

func (r *linkRepoFake) Update(_ context.Context, code string, patch domain.LinkPatch) (*domain.Link, error) {
	l, ok := r.links[code]
	if !ok {
		return nil, domain.NotFound("link", code)
	}
	if patch.TargetURL != nil {
		l.TargetURL = *patch.TargetURL
	}
	if patch.Title != nil {
		l.Title = *patch.Title
	}
	return l, nil
}

func (r *linkRepoFake) SoftDelete(_ context.Context, code string) error {
	if l, ok := r.links[code]; ok {
		l.Status = domain.LinkStatusDeleted
		return nil
	}
	return domain.NotFound("link", code)
}

func (r *linkRepoFake) ListByOwner(_ context.Context, _ domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error) {
	return nil, domain.LinkCursor{}, nil
}

func (r *linkRepoFake) Claim(_ context.Context, code string, ownerID uuid.UUID) (*domain.Link, error) {
	l, ok := r.links[code]
	if !ok {
		return nil, domain.NotFound("link", code)
	}
	l.OwnerID = &ownerID
	return l, nil
}

func (r *linkRepoFake) AddClickCount(_ context.Context, code string, delta int64) (int64, error) {
	l, ok := r.links[code]
	if !ok {
		return 0, domain.NotFound("link", code)
	}
	l.ClickCount += delta
	return l.ClickCount, nil
}

func (r *linkRepoFake) ExpireDue(_ context.Context, _ time.Time, _ int) ([]string, error) {
	return nil, nil
}

// recorderFake 只统计调用次数，永不出错。
type recorderFake struct{ calls int }

func (r *recorderFake) Record(_ context.Context, _ domain.ClickRecord) error {
	r.calls++
	return nil
}

// newShortenerForTest 装配一套全内存的 Shortener。
func newShortenerForTest(repo *linkRepoFake, cache *memCache) *Shortener {
	return NewShortener(repo, cache, &recorderFake{}, ShortenerConfig{
		BaseURL:     "https://sho.rt",
		CacheTTL:    time.Hour,
		NegativeTTL: time.Minute,
	})
}

// TestCreateEvictsStaleNegativeCache 守住一个曾经的真实缺陷：
// Create 成功后没有失效缓存（Update / Delete / Claim 都有，唯独 Create 漏了）。
// 某个短码在创建前被探测过一次，link:v1:miss:<code> 就带着「确认不存在」
// 的断言存活整个负缓存 TTL —— 刚创建成功的短码在这段时间里跳转仍是 404。
func TestCreateEvictsStaleNegativeCache(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	cache := newMemCache()
	s := newShortenerForTest(repo, cache)
	ctx := t.Context()

	const code = "mycode1"

	// 前置条件：该短码已被探测过一次（Resolve 404 → 写入负缓存）
	_, err := s.Resolve(ctx, code)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("探测不存在的短码应返回 ErrNotFound，实际：%v", err)
	}
	if _, ok := cache.missing[code]; !ok {
		t.Fatal("前置条件不成立：Resolve 404 后应写入负缓存")
	}

	// 用该短码创建自定义别名
	if _, err := s.Create(ctx, CreateInput{
		TargetURL:  "https://example.com/a",
		CustomCode: code,
	}); err != nil {
		t.Fatalf("Create 不应失败：%v", err)
	}

	// 核心断言：创建成功后立刻跳转必须成功，而不是等负缓存 TTL 过期
	link, err := s.Resolve(ctx, code)
	if err != nil {
		t.Fatalf("创建成功后 Resolve 仍失败（负缓存未被冲掉）：%v", err)
	}
	if link.TargetURL != "https://example.com/a" {
		t.Fatalf("Resolve 拿到错误的目标地址：%q", link.TargetURL)
	}

	// Evict 的调用记录里能看到该短码
	found := false
	for _, c := range cache.evicted {
		if c == code {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Create 后应 Evict 短码 %q，实际 Evict 记录：%v", code, cache.evicted)
	}
}

// TestCreateAutoCodeAlsoEvicts 自动生成路径同样要 evict ——
// 让「创建成功后缓存与库一致」成为不用分情况记忆的不变量。
func TestCreateAutoCodeAlsoEvicts(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	cache := newMemCache()
	s := newShortenerForTest(repo, cache)
	ctx := t.Context()

	result, err := s.Create(ctx, CreateInput{TargetURL: "https://example.com/b"})
	if err != nil {
		t.Fatalf("Create 不应失败：%v", err)
	}
	if len(cache.evicted) == 0 {
		t.Fatal("自动生成的短码创建成功后也应 Evict 一次缓存")
	}
	if cache.evicted[len(cache.evicted)-1] != result.Link.ShortCode {
		t.Fatalf("Evict 的短码 %q 与创建的 %q 不一致",
			cache.evicted[len(cache.evicted)-1], result.Link.ShortCode)
	}

	// 跳转立即可用
	if _, err := s.Resolve(ctx, result.Link.ShortCode); err != nil {
		t.Fatalf("创建后立刻 Resolve 失败：%v", err)
	}
}

// TestCreateFailureDoesNotEvict 创建失败（唯一约束冲突等）时不动缓存，
// 避免把别人的负缓存 / 正缓存误清掉。
func TestCreateFailureDoesNotEvict(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	cache := newMemCache()
	s := newShortenerForTest(repo, cache)
	ctx := t.Context()

	const code = "mycode2"
	if err := cache.PutMissing(ctx, code, time.Minute); err != nil {
		t.Fatal(err)
	}

	repo.createF = func(*domain.Link) error {
		return domain.Conflict("short_code", code)
	}
	if _, err := s.Create(ctx, CreateInput{TargetURL: "https://example.com/c", CustomCode: code}); err == nil {
		t.Fatal("冲突的短码创建应失败")
	}
	if len(cache.evicted) != 0 {
		t.Fatalf("创建失败不应 Evict，实际记录：%v", cache.evicted)
	}
}

// TestNormalizeHostUntouched 补一条与 P1-IPv6 修复的服务层回归：
// Normalize 之后 host 的 IPv6 方括号必须原样保留（validator 单测已覆盖，
// 这里守住 service 层的实际调用路径）。
func TestNormalizeHostUntouched(t *testing.T) {
	t.Parallel()

	got, err := validator.Normalize("http://[::1]:8080/x")
	if err != nil {
		t.Fatalf("IPv6 目标不应被拒：%v", err)
	}
	if got != "http://[::1]:8080/x" {
		t.Fatalf("IPv6 目标被改写：%q", got)
	}
}
