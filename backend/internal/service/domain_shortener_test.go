package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
)

// 分域（自定义域名）的用例集。
//
// 这里盯的是三类**只能靠集成才暴露**的错误，它们的共同点是单看某一个函数都对，
// 拼起来才错：
//   - 缓存条目没带上所属域 → 自定义域的短链在缓存命中时被判成「串域」而 404
//   - Host 没归一化 → 域名登记了却匹配不上（只在带端口 / 大写 / 尾点时复现）
//   - 快照刷新失败退回 nil → 自定义域上的短链在 PG 抖动的那几十秒里集体 404

// 两个固定域 ID：写死而不是现生成，失败信息里能直接认出是哪个域。
var (
	domainA = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	domainB = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

// domainsOf 造一个登记了若干个域名的域名仓储。
func domainsOf(names ...string) *domainRepoFake {
	ids := []uuid.UUID{domainA, domainB}
	list := make([]domain.Domain, 0, len(names))
	for i, name := range names {
		list = append(list, domain.Domain{ID: ids[i], Name: name})
	}
	return &domainRepoFake{domains: list}
}

// linkIn 造一条挂在指定域下的短链（domainID 为 nil 表示默认域名）。
func linkIn(domainID *uuid.UUID, code string) *domain.Link {
	return &domain.Link{
		ShortCode: code,
		TargetURL: "https://example.com/target",
		Status:    domain.LinkStatusActive,
		DomainID:  domainID,
	}
}

// TestResolveIsDomainScoped 是分域的正确性核心：
// 同一个短码只在**它所属的那个域**上可解析。
func TestResolveIsDomainScoped(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	repo.links["abc1234"] = linkIn(&domainA, "abc1234")

	s := newShortenerWithDomainsForTest(repo, newMemCache(), domainsOf("a.local", "b.local"))
	ctx := t.Context()

	link, err := s.Resolve(ctx, "a.local", "abc1234")
	if err != nil {
		t.Fatalf("在所属域上解析应当成功，实际 %v", err)
	}
	if link.DomainID == nil || *link.DomainID != domainA {
		t.Fatalf("解析结果的 DomainID = %v，期望 %v", link.DomainID, domainA)
	}

	// 另外两个域都必须 404：默认域（Host 为空）与另一个已登记域
	for _, host := range []string{"", "b.local"} {
		if _, err := s.Resolve(ctx, host, "abc1234"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("host=%q 上的解析应当 NotFound（域隔离），实际 %v", host, err)
		}
	}
}

// TestResolveCustomDomainOnCacheHit 是上面那个缺陷的回归用例。
//
// 第一次解析走回源（顺带写缓存），第二次才走缓存 —— 而**只有第二次**会暴露
// 「缓存条目里没有 DomainID」：回源那条路是拿数据库行直接用的，域校验自然过；
// 缓存这条路如果解出来的 DomainID 恒为 nil，就会被 sameDomain 判成串域。
//
// 症状因此非常恶心：短链在 TTL 内「第一次能开、之后打不开」，TTL 一过又是好的。
func TestResolveCustomDomainOnCacheHit(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	repo.links["cacheme"] = linkIn(&domainA, "cacheme")

	cache := newMemCache()
	s := newShortenerWithDomainsForTest(repo, cache, domainsOf("a.local"))
	ctx := t.Context()

	// 第一次：回源
	if _, err := s.Resolve(ctx, "a.local", "cacheme"); err != nil {
		t.Fatalf("首次（回源）解析应当成功，实际 %v", err)
	}
	// 前置条件：确实已经写进缓存了，否则这个用例证明不了任何事
	// （键是「域 + 短码」，见 domain.LinkRef）
	entry, ok := cache.positive[cacheKey(&domainA, "cacheme")]
	if !ok {
		t.Fatal("前置条件不成立：首次解析后应写入正向缓存")
	}
	if entry.DomainID == nil {
		t.Fatal("缓存条目没带上 DomainID —— 命中时会被判成串域而 404")
	}

	// 第二次：命中缓存
	hit, err := s.Resolve(ctx, "a.local", "cacheme")
	if err != nil {
		t.Fatalf("缓存命中时解析失败（缓存条目丢了所属域？）：%v", err)
	}
	if hit.TargetURL != "https://example.com/target" {
		t.Fatalf("缓存命中拿到错误的目标地址：%q", hit.TargetURL)
	}
}

// TestResolveNormalizesHost 钉住 Host 归一化：域名只以「小写、无端口、无尾点」的形态入库，
// 而真实请求里这三种写法都会出现，任何一处没归一化的症状都是「登记了却 404」。
func TestResolveNormalizesHost(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	repo.links["norm123"] = linkIn(&domainA, "norm123")

	s := newShortenerWithDomainsForTest(repo, newMemCache(), domainsOf("a.local"))
	ctx := t.Context()

	for _, host := range []string{
		"a.local",
		"A.LOCAL",
		"a.local:8080",
		"a.local.",
		"  a.local  ",
		"a.local:443",
	} {
		if _, err := s.Resolve(ctx, host, "norm123"); err != nil {
			t.Errorf("Host=%q 应当归一成 a.local 并解析成功，实际 %v", host, err)
		}
	}
}

// TestShortURLUsesOwnDomain 覆盖拼对外短链：挂在自定义域上的短链用**它自己**的域名，
// 而默认域名的短链仍然用 PUBLIC_BASE_URL。
//
// 另外守住一条降级：域记录被删掉之后，short_url 退回默认域名而不是拼出一个
// 打不开的链接（宁可域名不对，不能点不开）。
func TestShortURLUsesOwnDomain(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	s := newShortenerWithDomainsForTest(repo, newMemCache(), domainsOf("a.local", "b.local"))
	ctx := t.Context()

	if got := s.ShortURL(ctx, linkIn(nil, "abc1234")); got != "https://sho.rt/abc1234" {
		t.Errorf("默认域名的短链 = %q，期望 https://sho.rt/abc1234", got)
	}
	if got := s.ShortURL(ctx, linkIn(&domainA, "abc1234")); got != "https://a.local/abc1234" {
		t.Errorf("自定义域名的短链 = %q，期望 https://a.local/abc1234", got)
	}

	// 域记录不存在（例如已被删除）：退回默认域名，仍然是一个能打开的链接
	gone := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	if got := s.ShortURL(ctx, linkIn(&gone, "abc1234")); got != "https://sho.rt/abc1234" {
		t.Errorf("域记录缺失时短链 = %q，期望退回默认域名 https://sho.rt/abc1234", got)
	}
	// nil 链接不该 panic（调用方漏判时的兜底）
	if got := s.ShortURL(ctx, nil); got != "" {
		t.Errorf("nil 链接应当返回空串，实际 %q", got)
	}
}

// TestCrossDomainProbeDoesNotPoisonNegativeCache 覆盖分域引入后**新出现**的一类污染。
//
// 负缓存的含义是「这个短码不存在」。分域之后它变成「这个短码**在这个域里**不存在」——
// 而缓存键如果还只是短码，一次跨域探测就会把「不属于这个域」记成「不存在」，
// 于是**短链在它自己的域上也会 404**，直到负缓存 TTL 过期。
//
// 触发它不需要攻击者：搜素引擎、聊天工具的链接预览、或者用户从默认域名手输一遍
// 都能写入这条错误的否定断言。
func TestCrossDomainProbeDoesNotPoisonNegativeCache(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	repo.links["poison1"] = linkIn(&domainA, "poison1")

	cache := newMemCache()
	s := newShortenerWithDomainsForTest(repo, cache, domainsOf("a.local"))
	ctx := t.Context()

	// 1) 有人在默认域名上探了一下这个属于 a.local 的短码
	if _, err := s.Resolve(ctx, "", "poison1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("默认域名上访问属于 a.local 的短码应当 NotFound，实际 %v", err)
	}

	// 2) 紧接着在它自己的域上访问 —— 必须仍然能解析
	if _, err := s.Resolve(ctx, "a.local", "poison1"); err != nil {
		t.Fatalf("跨域探测污染了负缓存：短链在自己的域上变成 404（%v）", err)
	}
}

// TestNegativeCacheIsScopedToDomain 是上面那条的正向对照：
// 负缓存该在**同一个域**上生效（否则短码扫描器会直接打到数据库）。
func TestNegativeCacheIsScopedToDomain(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	var lookups atomic.Int64
	repo.getByCodeF = func(_ context.Context, code string) (*domain.Link, error) {
		lookups.Add(1)
		return nil, domain.NotFound("link", code)
	}

	s := newShortenerWithDomainsForTest(repo, newMemCache(), domainsOf("a.local"))
	ctx := t.Context()

	// 在 a.local 上探测一个确实不存在的短码：回源一次并写下负缓存
	if _, err := s.Resolve(ctx, "a.local", "nosuch1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在的短码应当 NotFound，实际 %v", err)
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("首次探测应当回源 1 次，实际 %d 次", got)
	}

	// 同一个域上的第二次访问必须由负缓存挡住，不再回源
	if _, err := s.Resolve(ctx, "a.local", "nosuch1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("第二次访问应当仍然 NotFound，实际 %v", err)
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("第二次访问不该回源（说明没命中负缓存），累计回源 %d 次", got)
	}
}

// TestCreateWithDomain 覆盖创建路径的域名入参：
// 已登记域名（含各种 Host 写法）被固化进链接，未登记域名报字段级 422 而不是静默落到默认域。
func TestCreateWithDomain(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	s := newShortenerWithDomainsForTest(repo, newMemCache(), domainsOf("a.local"))
	ctx := t.Context()

	result, err := s.Create(ctx, CreateInput{
		TargetURL:  "https://example.com/1",
		CustomCode: "withdm1",
		// 故意用带端口 + 大写的写法，验证创建路径也会归一化
		Domain: "A.LOCAL:8080",
	})
	if err != nil {
		t.Fatalf("在已登记域名上创建不应失败：%v", err)
	}
	if result.Link.DomainID == nil || *result.Link.DomainID != domainA {
		t.Fatalf("创建结果的 DomainID = %v，期望 %v", result.Link.DomainID, domainA)
	}
	if result.ShortURL != "https://a.local/withdm1" {
		t.Fatalf("创建结果里的 short_url = %q，期望 https://a.local/withdm1", result.ShortURL)
	}

	// 未登记域名：字段级 422，并且不该有副作用（不该落库）
	before := len(repo.links)
	_, err = s.Create(ctx, CreateInput{
		TargetURL:  "https://example.com/2",
		CustomCode: "withdm2",
		Domain:     "not-registered.local",
	})
	var invalid *domain.InvalidInputError
	if !errors.As(err, &invalid) {
		t.Fatalf("未登记域名应当报字段级 InvalidInputError，实际 %v", err)
	}
	if invalid.Field != "domain" {
		t.Errorf("错误字段名 = %q，期望 domain", invalid.Field)
	}
	if len(repo.links) != before {
		t.Error("未登记域名被拒绝后不应落库")
	}

	// 留空 = 默认域名（历史行为不变）
	def, err := s.Create(ctx, CreateInput{TargetURL: "https://example.com/3", CustomCode: "withdm3"})
	if err != nil {
		t.Fatalf("留空域名创建不应失败：%v", err)
	}
	if def.Link.DomainID != nil {
		t.Fatalf("留空域名的创建结果 DomainID = %v，期望 nil", def.Link.DomainID)
	}
	if def.ShortURL != "https://sho.rt/withdm3" {
		t.Fatalf("默认域名的 short_url = %q，期望 https://sho.rt/withdm3", def.ShortURL)
	}
}

// TestSnapshotFallsBackToPreviousOnRefreshFailure 覆盖快照刷新的降级：
//
//   - 刷新失败时必须用**上一份成功的**快照。退回「所有 Host 都是默认域」会让
//     自定义域上的短链在 PG 抖动的这几十秒里集体 404 —— 比用一份 30 秒前的
//     域名表糟糕得多。
//   - 从未成功过（首刷就失败）时只能退回「没有自定义域」：此时宁可让自定义域
//     404（本来就还没能力服务它），也不能放行任何跨域访问。
func TestSnapshotFallsBackToPreviousOnRefreshFailure(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	repo.links["keepme1"] = linkIn(&domainA, "keepme1")

	domainsRepo := domainsOf("a.local")
	s := newShortenerWithDomainsForTest(repo, newMemCache(), domainsRepo)
	ctx := t.Context()

	// 首刷成功，快照建立
	if _, err := s.Resolve(ctx, "a.local", "keepme1"); err != nil {
		t.Fatalf("首刷后解析应当成功，实际 %v", err)
	}

	// 让下一次刷新失败，并把已建立的快照推老（超过 TTL 才会触发刷新）
	domainsRepo.listErr = errors.New("pg 抖了一下")
	prev := s.snapshot(ctx)
	if prev == nil {
		t.Fatal("前置条件不成立：应当已经有一份可用快照")
	}
	prev.loadedAt = time.Now().Add(-2 * domainSnapshotTTL)
	s.domainsSnap.Store(prev)

	if _, err := s.Resolve(ctx, "a.local", "keepme1"); err != nil {
		t.Fatalf("刷新失败后应当继续用上一份快照，实际 %v", err)
	}

	// 首刷就失败的形态：不登记任何域
	fresh := newShortenerWithDomainsForTest(newLinkRepoFake(), newMemCache(), &domainRepoFake{
		listErr: errors.New("pg 挂了"),
	})
	if got := fresh.domainByHost(ctx, "a.local"); got != nil {
		t.Fatalf("首刷失败时应当退回「没有自定义域」，实际拿到 %v", got)
	}
}
