package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/pkg/validator"
)

// ---- 测试替身 ----

// memCache 是 domain.LinkCache 的内存实现，语义对齐 store/redis.Cache：
// 正向与负缓存分开存，Evict 同时清两者。
//
// 带锁是因为并发用例（singleflight 击穿防护）会同时读写它 ——
// 生产实现是 Redis，本来就是并发安全的。
type memCache struct {
	mu       sync.Mutex
	positive map[string]*domain.CachedLink
	missing  map[string]struct{}
	evicted  []string // 记录被 Evict 过的短码，供断言
}

func newMemCache() *memCache {
	return &memCache{positive: map[string]*domain.CachedLink{}, missing: map[string]struct{}{}}
}

func (c *memCache) Get(_ context.Context, code string) (*domain.CachedLink, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.positive[code]; ok {
		return entry, nil
	}
	if _, ok := c.missing[code]; ok {
		return nil, fmt.Errorf("memcache: %q: %w", code, domain.ErrCacheKnownMissing)
	}
	return nil, fmt.Errorf("memcache: %q: %w", code, domain.ErrCacheMiss)
}

func (c *memCache) Put(_ context.Context, link *domain.CachedLink, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.positive[link.ShortCode] = link
	delete(c.missing, link.ShortCode)
	return nil
}

func (c *memCache) PutMissing(_ context.Context, code string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.missing[code] = struct{}{}
	return nil
}

func (c *memCache) Evict(_ context.Context, codes ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, code := range codes {
		delete(c.positive, code)
		delete(c.missing, code)
		c.evicted = append(c.evicted, code)
	}
	return nil
}

// linkRepoFake 按需返回错误，其余全部落进内存 map。
type linkRepoFake struct {
	links map[string]*domain.Link
	// getByCodeF 非空时接管 GetByCode（并发用例要在这里插入同步点与控制回源次数）。
	getByCodeF func(ctx context.Context, code string) (*domain.Link, error)
	createF    func(*domain.Link) error // 可注入失败
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

func (r *linkRepoFake) GetByCode(ctx context.Context, code string) (*domain.Link, error) {
	if r.getByCodeF != nil {
		return r.getByCodeF(ctx, code)
	}
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
	if patch.PasswordHash != nil {
		// 与真实仓储（postgres）一致：落库之后 PasswordProtected 也跟着变
		l.PasswordHash = *patch.PasswordHash
		l.PasswordProtected = *patch.PasswordHash != ""
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

// TestStopDrainsQueueBeforeReturning 钉住关停顺序所依赖的前提：
// Stop() 返回时队列必须已经冲完（内部是 wg.Wait）。
//
// cmd/api 因此可以「先停 HTTP、再 stopStats()、最后 Stop()」——在途请求在这之前
// 记录进来的点击都还能落进 Redis，而不是被进程直接带走（F5 的静默丢失窗口）。
func TestStopDrainsQueueBeforeReturning(t *testing.T) {
	t.Parallel()

	recorder := &recorderFake{}
	s := NewShortener(newLinkRepoFake(), newMemCache(), recorder, ShortenerConfig{
		BaseURL:     "https://sho.rt",
		CacheTTL:    time.Hour,
		NegativeTTL: time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	const pending = 5
	for range pending {
		s.RecordClick(domain.ClickRecord{
			Code:       "abc1234",
			OccurredAt: time.Now().UTC(),
		})
	}

	cancel()
	s.Stop()

	if recorder.calls != pending {
		t.Fatalf("Stop 返回时队列应已冲完：写入 %d 条，实际 %d 条", pending, recorder.calls)
	}
	if got := s.QueueLen(); got != 0 {
		t.Fatalf("Stop 返回后队列长度 = %d, want 0", got)
	}
}

// TestCreateCollisionExhaustionIsRetryable 守住 409/503 的映射优先级：
// 自动生成短码连续撞上唯一约束，说明随机源或约束索引出了问题，是「内部耗尽」
// 而不是用户输入错误。若让 errors.Join 里的 *ConflictError 抢先命中映射，
// 用户会收到 409「该短链已被占用，请换一个」——可他根本没提供短码。
func TestCreateCollisionExhaustionIsRetryable(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	cache := newMemCache()
	s := newShortenerForTest(repo, cache)
	ctx := t.Context()

	// 让每一次落库都撞唯一约束，逼 Create 走完 MaxAttempts 次重试
	repo.createF = func(link *domain.Link) error {
		return domain.Conflict("short_code", link.ShortCode)
	}

	_, err := s.Create(ctx, CreateInput{TargetURL: "https://example.com/boom"})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("连续短码冲突应报可重试的 ErrUnavailable，实际：%v", err)
	}
	// 最后一次冲突仍要能被 errors.Is 到，否则日志里只剩一句「连续 5 次冲突」而无从归因
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("错误里应保留最后一次冲突，实际：%v", err)
	}
	if !strings.Contains(err.Error(), "短码冲突") {
		t.Fatalf("错误文案应说明是短码冲突，实际：%v", err)
	}
}

// TestNormalizeHostUntouched 补一条与 P1-IPv6 修复的服务层回归：
// Normalize 之后 host 的 IPv6 方括号必须原样保留（validator 单测已覆盖，
// 这里守住 service 层的实际调用路径）。
// TestResolveSingleflightCollapsesConcurrentMisses 守住缓存击穿防护（M3-2）：
// 同一个短码的 N 个并发 miss 只该回源一次。
//
// 负缓存只能挡住「已确认不存在」，挡不住「刚出现的热点」——
// 热点短链刚发布时这 N 次回源就是 N 次 PG 查询，恰好是最容易被压垮的时刻。
func TestResolveSingleflightCollapsesConcurrentMisses(t *testing.T) {
	t.Parallel()

	const (
		code    = "hot1234"
		workers = 50
	)

	var calls atomic.Int64
	// 用「第一次回源卡住」制造确定的并发窗口：leader 进到回源里之后，
	// 其余 goroutine 必然已经排在 singleflight 上（而不是在缓存里各查一次）。
	entered := make(chan struct{})
	release := make(chan struct{})

	repo := newLinkRepoFake()
	repo.getByCodeF = func(context.Context, string) (*domain.Link, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return &domain.Link{
			ID:        uuid.NewV7(),
			ShortCode: code,
			TargetURL: "https://example.com/hot",
			Status:    domain.LinkStatusActive,
		}, nil
	}
	s := newShortenerForTest(repo, newMemCache())

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			_, errs[i] = s.Resolve(context.Background(), code)
		})
	}

	<-entered
	// 给等待者一点时间真正排到 singleflight 上（即便没排上，它们也会命中缓存写回，
	// 所以下面「只回源一次」的断言不受影响，这个 sleep 只是让覆盖更确定）
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个并发请求失败：%v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("并发 %d 个请求回源了 %d 次，期望 1 次（singleflight 没生效）", workers, got)
	}
	if got := s.PGFallbacks(); got != 1 {
		t.Fatalf("pg_fallbacks = %d，期望 1（它是击穿的观测口径）", got)
	}
}

// TestResolveSingleflightSharesFailure：失败结果也会被共享 —— 一份 404 不该变成
// N 次回源，而且必须写进负缓存（否则下一波请求又会击穿）。
func TestResolveSingleflightSharesFailure(t *testing.T) {
	t.Parallel()

	const (
		code    = "miss123"
		workers = 20
	)

	var calls atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})

	repo := newLinkRepoFake()
	repo.getByCodeF = func(context.Context, string) (*domain.Link, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil, domain.NotFound("link", code)
	}
	cache := newMemCache()
	s := newShortenerForTest(repo, cache)

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			_, errs[i] = s.Resolve(context.Background(), code)
		})
	}

	<-entered
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("第 %d 个请求的错误 = %v，期望 ErrNotFound（失败结果应被共享）", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("并发 %d 个 miss 回源了 %d 次，期望 1 次", workers, got)
	}

	// 负缓存必须已经写好：再打一次不该回源
	if _, err := s.Resolve(t.Context(), code); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("第二次请求错误 = %v，期望 ErrNotFound", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("负缓存没生效：回源次数变成了 %d", got)
	}
}

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
