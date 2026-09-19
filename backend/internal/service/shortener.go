package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"golang.org/x/sync/singleflight"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/pkg/hostname"
	"ashen-courier/internal/pkg/shortcode"
	"ashen-courier/internal/pkg/validator"
)

// 短链服务的边界常量。
const (
	// MaxTitleLength 是标题最大字符数。
	MaxTitleLength = 200
	// DefaultQueueSize 是点击统计写入队列的默认容量。
	DefaultQueueSize = 4096
	// writeTimeout 是单次统计写入的超时。
	writeTimeout = 2 * time.Second
	// drainTimeout 是优雅关闭时冲刷队列的最长等待。
	drainTimeout = 5 * time.Second
	// resolveTimeout 是回源的兜底超时。
	// store 层每个 PG 调用已经有 op 超时（PG_TIMEOUT，默认 3s），这里只是再兜一层：
	// 回源用的是 WithoutCancel 派生的 ctx，没有它就没有任何上限。
	resolveTimeout = 5 * time.Second
	// domainSnapshotTTL 是域名快照的最长存活时间。
	//
	// 取 30s 是刻意偏短的：域名是「配置」，改了之后最多 30 秒生效，
	// 而每 30 秒多一次针对一张几行小表的查询，代价可以忽略。
	domainSnapshotTTL = 30 * time.Second
)

// ShortenerConfig 是 Shortener 的构造参数。
type ShortenerConfig struct {
	// BaseURL 是对外基址（不带结尾斜杠），用于拼 short_url。
	BaseURL string
	// CacheTTL 是正向缓存的基础 TTL。
	CacheTTL time.Duration
	// NegativeTTL 是负缓存 TTL。
	NegativeTTL time.Duration
	// QueueSize 是统计写入队列容量，<=0 时取 DefaultQueueSize。
	QueueSize int
}

// Shortener 是短链核心服务。
type Shortener struct {
	links    domain.LinkRepository
	cache    domain.LinkCache
	recorder domain.ClickRecorder
	// domains 是自定义域名的来源。为空接口（nil）表示该部署不支持分域，
	// 此时一切请求都按默认域名处理 —— 单测里大量用例走的就是这条路径。
	domains domain.DomainRepository

	baseURL     string
	scheme      string
	cacheTTL    time.Duration
	negativeTTL time.Duration

	// domainsSnap 是「主机名 ↔ 域」的进程内快照，按 TTL 惰性刷新（见 snapshot）。
	//
	// 用原子指针而不是「互斥锁 + 普通字段」：刷新要查一次库（哪怕只是一张几行的小表），
	// 而读它的是跳转路径。用锁的话，刷新期间的所有请求都会堵在锁上；
	// 用原子指针，读永远是零成本的，正在刷新的那个 goroutine 也不会挡住别人。
	domainsSnap atomic.Pointer[domainSnapshot]
	// domainsRefreshing 保证同一时刻只有一个 goroutine 在刷新，避免击穿。
	domainsRefreshing atomic.Bool

	// queue 是跳转后的统计写入队列。有界：队满即丢弃并计数，
	// 保证跳转链路永远不会因为统计积压而阻塞。
	queue chan domain.ClickRecord
	// droppedClicks 统计因队列满而丢弃的次数。
	droppedClicks atomic.Int64
	// failedClicks 统计因 Redis 写入失败而丢弃的次数。
	failedClicks atomic.Int64
	// pgFallbacks 统计真实的回源次数（= 缓存击穿的观测口径）。
	pgFallbacks atomic.Int64

	// flight 合并同一短码的并发回源，防止热点短码刚发布时把 PG 打穿。
	flight singleflight.Group

	wg sync.WaitGroup
}

// NewShortener 构造短链服务。
//
// domains 为 nil 表示该部署只用默认域名：所有 Host 都按「默认域」解析，
// 与分域功能引入之前的行为逐字一致。
func NewShortener(links domain.LinkRepository, cache domain.LinkCache, recorder domain.ClickRecorder, domains domain.DomainRepository, cfg ShortenerConfig) *Shortener {
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = DefaultQueueSize
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	return &Shortener{
		links:       links,
		cache:       cache,
		recorder:    recorder,
		domains:     domains,
		baseURL:     baseURL,
		scheme:      schemeOf(baseURL),
		cacheTTL:    cfg.CacheTTL,
		negativeTTL: cfg.NegativeTTL,
		queue:       make(chan domain.ClickRecord, queueSize),
	}
}

// schemeOf 取出 baseURL 的 scheme 前缀（含 "://"），用于拼自定义域的短链。
//
// 为什么自定义域沿用 PUBLIC_BASE_URL 的 scheme，而不是写死 https：
// 写死 https 会让本机用 Host 头验收时拼出一个打不开的地址；
// 而生产部署的 PUBLIC_BASE_URL 本来就是 https，所以线上行为不受影响。
// 真需要「默认域 http、自定义域 https」这种组合时，正确的做法是给 domains 表加一列——
// 在那之前，一个隐含约定比一个多余的配置项更好维护。
func schemeOf(baseURL string) string {
	if strings.HasPrefix(baseURL, "https://") {
		return "https://"
	}
	return "http://"
}

// Start 启动统计写入协程。ctx 取消后会把队列里剩余事件尽力冲完再退出。
func (s *Shortener) Start(ctx context.Context) {
	s.wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				s.drain(ctx)
				return
			case rec := <-s.queue:
				s.write(ctx, rec)
			}
		}
	})
}

// Stop 等待写入协程退出。调用前应先取消传给 Start 的 context。
func (s *Shortener) Stop() { s.wg.Wait() }

// DroppedClicks 返回因队列满丢弃的点击次数。
func (s *Shortener) DroppedClicks() int64 { return s.droppedClicks.Load() }

// FailedClicks 返回因写入失败丢弃的点击次数。
func (s *Shortener) FailedClicks() int64 { return s.failedClicks.Load() }

// PGFallbacks 返回真实的回源次数（缓存未命中且真正打到 PG 的次数）。
//
// 它是缓存击穿的观测口径：热点短码刚发布时，这个数字的增长速度直接反映
// 有多少并发请求被打到了数据库上。
func (s *Shortener) PGFallbacks() int64 { return s.pgFallbacks.Load() }

// QueueLen 返回当前队列积压长度。
func (s *Shortener) QueueLen() int { return len(s.queue) }

// Actor 是发起请求的身份：登录用户、匿名持钥者，或两者都不是。
type Actor struct {
	// UserID 非空表示已登录。
	UserID *uuid.UUID
	// ManageKey 是 X-Manage-Key 头里的明文密钥。
	ManageKey string
}

// IsAuthenticated 判断是否带登录态。
func (a Actor) IsAuthenticated() bool { return a.UserID != nil }

// CreateInput 是创建短链的入参。
type CreateInput struct {
	// TargetURL 是用户提交的目标地址，会在服务层规范化。
	TargetURL string
	// CustomCode 是可选自定义别名。
	CustomCode string
	// Title 是可选标题。
	Title string
	// ExpiresAt 是可选过期时刻。
	ExpiresAt *time.Time
	// Tags 是可选标签（会在服务层校验并统一小写）。
	Tags []string
	// Password 是可选访问口令**明文**：服务层校验强度后只把 bcrypt 摘要落库。
	// 明文只活在本函数内，不落日志、不进缓存、不回响应。
	Password string
	// OwnerID 非空表示登录用户创建。
	OwnerID *uuid.UUID
	// ClientIP 记录创建者 IP，用于风控排查。
	ClientIP string
	// Domain 是可选的自定义域名（主机名形态，可带端口/大小写，内部会归一化）。
	// 留空表示挂在默认域名下。
	//
	// 只接受**已登记**的域名（domains 表里有的），否则 422：
	// 短链的对外形态是 {scheme}://{domain}/{code}，允许任意域名等于给用户一个
	// 指向别人站点的链接。
	Domain string
}

// ListInput 是「我的链接」列表的查询条件。
//
// 收成一个结构体而不是继续加参数：查询条件已经有五个（搜索词 / 标签 / 分页游标 /
// 页大小 / 上限），相邻的多个 string 参数在调用点极容易传错位。
type ListInput struct {
	// OwnerID 是列表归属的用户。
	OwnerID uuid.UUID
	// Query 是可选搜索词。
	Query string
	// Tag 是可选标签筛选。
	Tag string
	// Limit 是本页条数。
	Limit int
	// Cursor 是上一页的游标（空串表示第一页）。
	Cursor string
	// MaxLimit 是页大小上限。
	MaxLimit int
}

// CreateResult 是创建结果。
type CreateResult struct {
	// Link 是落库后的实体。
	Link *domain.Link
	// ShortURL 是拼好的短链，直接返回给用户。
	ShortURL string
	// ManageKey 仅在匿名创建时非空，且只在这一次响应里出现。
	ManageKey string
}

// Create 创建短链。匿名创建会生成一次性管理密钥。
func (s *Shortener) Create(ctx context.Context, in CreateInput) (*CreateResult, error) {
	target, err := validator.Normalize(in.TargetURL)
	if err != nil {
		return nil, mapURLError(err)
	}

	title := strings.TrimSpace(in.Title)
	if len([]rune(title)) > MaxTitleLength {
		return nil, domain.Invalid("title", fmt.Sprintf("标题不能超过 %d 个字符", MaxTitleLength))
	}

	expiresAt, err := normalizeExpiry(in.ExpiresAt)
	if err != nil {
		return nil, err
	}

	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return nil, err
	}

	passwordHash, err := normalizeLinkPassword(in.Password)
	if err != nil {
		return nil, err
	}

	domainID, err := s.resolveCreateDomain(ctx, in.Domain)
	if err != nil {
		return nil, err
	}

	var manageKey string
	var keyHash []byte
	if in.OwnerID == nil {
		if manageKey, keyHash, err = NewManageKey(); err != nil {
			return nil, err
		}
	}

	base := &domain.Link{
		ShortCode: "",
		TargetURL: target,
		Title:     title,
		OwnerID:   in.OwnerID,
		DomainID:  domainID,
		KeyHash:   keyHash,

		PasswordHash: passwordHash,
		Status:       domain.LinkStatusActive,
		ExpiresAt:    expiresAt,
		Tags:         tags,
		CreatedIP:    in.ClientIP,
	}

	code := strings.TrimSpace(in.CustomCode)
	if code != "" {
		if err := shortcode.Validate(code); err != nil {
			return nil, mapCodeError(err)
		}
		base.ShortCode = code
		base.ID = uuid.NewV7()
		if err := s.links.Create(ctx, base); err != nil {
			return nil, err
		}
		// 冲掉可能存在的负缓存：短码在被创建前若被探测过（404 一次），
		// link:v2:miss:{域}:<code> 里就存着「确认不存在」的断言；不清掉的话，
		// 刚创建成功的短码在负缓存 TTL 内跳转仍是 404，与事实矛盾。
		// 注意带上所属域 —— 负缓存是按域写的，只按短码删不掉这一条。
		s.evict(ctx, domain.LinkRef{Code: base.ShortCode, DomainID: base.DomainID})
		return s.createdResult(ctx, base, manageKey), nil
	}

	// 自动生成：靠 links_short_code_key 唯一约束兜底，冲突就重试。
	// 7 位 Base62 有 3.5 万亿空间，重试 5 次仍冲突的概率可以忽略。
	var lastErr error
	for range shortcode.MaxAttempts {
		candidate, genErr := shortcode.Generate()
		if genErr != nil {
			return nil, genErr
		}
		link := *base
		link.ID = uuid.NewV7()
		link.ShortCode = candidate

		err := s.links.Create(ctx, &link)
		if err == nil {
			// 理论上随机短码不会撞上旧负缓存，但 DEL 一次成本可忽略，
			// 让「创建成功后缓存必然与库一致」成为无需分情况记忆的不变量。
			s.evict(ctx, domain.LinkRef{Code: link.ShortCode, DomainID: link.DomainID})
			return s.createdResult(ctx, &link, manageKey), nil
		}
		if _, ok := domain.AsConflict(err); !ok {
			return nil, err
		}
		lastErr = err
	}
	// 连续撞码说明随机源或唯一约束出了异常，是「内部耗尽」而非用户输入错误：
	// 因此报可重试的 503（客户端直接重试即可），而不是 409「请换一个短码」。
	// 这里 errors.Join 同时保留了最后一次 ErrConflict 供日志归因，映射优先级由
	// httpx.WriteDomainError 保证（ErrUnavailable 判在 ErrConflict 之前）。
	return nil, fmt.Errorf("service.shortener: 连续 %d 次短码冲突: %w", shortcode.MaxAttempts, errors.Join(domain.ErrUnavailable, lastErr))
}

// Resolve 走跳转路径：先读缓存，miss 才回源数据库，全程不写 PG。
//
// 返回的错误保证是领域错误：
//   - domain.ErrNotFound → 404
//   - domain.ErrGone     → 410
//   - domain.ErrInternal → 500（库里存在非 http/https 的脏数据）
//
// host 是请求的 Host 头（已归一化）；空串表示「按默认域名解析」。
func (s *Shortener) Resolve(ctx context.Context, host, code string) (*domain.Link, error) {
	if !shortcode.IsValidShape(code) {
		return nil, domain.NotFound("link", code)
	}

	// 第一步：这个请求落在哪个域。未登记的主机名（包括空 Host）一律按默认域处理 ——
	// 这正是「本机用 127.0.0.1 访问」与「反向代理用了别的 server_name」仍然可用的原因。
	domainID := s.domainIDForHost(ctx, host)

	link, err := s.fromCache(ctx, code, domainID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		link, err = s.resolveFromDB(ctx, code, domainID)
		if err != nil {
			return nil, err
		}
	}

	// 第二步：域校验。
	//
	// **不能省**：缓存是按短码存的（短码在引入分域后仍然全局唯一），
	// 所以命中的条目完全可能属于**另一个域**。少了这一条，
	// 从 A 域访问一个属于 B 域的短码会被正常 302 —— 访问者被送到错误的目标，
	// 比 404 严重得多（而且没有任何日志会提示）。
	if !sameDomain(link.DomainID, domainID) {
		return nil, domain.NotFound("link", code)
	}

	if err := link.Redirectable(time.Now()); err != nil {
		return nil, err
	}
	if !validator.IsAllowedTarget(link.TargetURL) {
		// 历史脏数据：说明有人绕过了应用层直接写库。拒绝跳转并告警。
		slog.Error("短链目标地址 scheme 非法，拒绝跳转",
			"code", code, "target", link.TargetURL, "link_id", link.ID.String())
		return nil, fmt.Errorf("service.shortener: resolve %q: 目标地址 scheme 非法: %w", code, domain.ErrInternal)
	}
	return link, nil
}

// resolveFromDB 回源数据库，并用 singleflight 合并同一短码的并发回源。
//
// 为什么需要它：缓存 miss 时每个请求都会回源一次。负缓存只能挡住「已确认不存在」，
// 挡不住「刚出现的热点」—— 短链刚发布或被大规模转发时，同一个短码的 N 个并发请求
// 会打 N 次 PG，而这正是最容易被压垮的时刻。
//
// 三个刻意的选择：
//   - key 是「域 + 短码」；**不调 Forget**：缓存写失败交给 TTL 自愈，少一条需要推理的路径
//   - 回源用 WithoutCancel 派生的 ctx：等待者可能先取消，但回源本身不该被某一个请求的
//     取消打断 —— 否则其余等待者会拿到「context canceled」而不是真实结果
//   - 失败结果同样会被共享（singleflight 的语义），由负缓存兜住 404，不会形成击穿循环
func (s *Shortener) resolveFromDB(ctx context.Context, code string, domainID *uuid.UUID) (*domain.Link, error) {
	// key 里带上域：同一个短码在不同域是**两个独立的结果**，
	// 只按短码合并会让先到的那个域的结果「喂」给另一个域的等待者。
	v, err, _ := s.flight.Do(flightKey(domainID, code), func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveTimeout)
		defer cancel()

		s.pgFallbacks.Add(1)

		link, err := s.links.GetByCodeInDomain(fetchCtx, code, domainID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				s.putMissing(fetchCtx, code, domainID)
			}
			return nil, err
		}
		s.putCache(fetchCtx, link)
		return link, nil
	})
	if err != nil {
		return nil, err
	}

	link, ok := v.(*domain.Link)
	if !ok {
		// 只有回源函数返回了非 *domain.Link 才可能走到这里；当成内部错误而不是 panic ——
		// 这里在跳转热路径上，panic 会被 Recover 兜住但会丢掉整次请求。
		return nil, fmt.Errorf("service.shortener: resolve %q: 回源结果类型异常 %T: %w", code, v, domain.ErrInternal)
	}
	return link, nil
}

// VerifyPassword 校验某条短链的访问口令（只给 POST /{code} 用）。
//
// 与跳转路径刻意不同：这里走**库读**而不是缓存 —— 缓存里只存「有没有口令」的布尔，
// 摘要始终留在 PG（Redis 转储泄露不该 enable 离线爆破）。代价是每次解锁多一次库读，
// 而这条路径有按 IP 的限流兜着，本来也不是热路径。
//
// 返回值：不存在/已删除 → domain.ErrNotFound；已停用/已过期 → domain.ErrGone；
// 口令不对 → domain.ErrUnauthorized；**没设口令 → nil**（无需解锁）。
//
// host 与 Resolve 一致：只在**该域内**找这条短链。否则可以从 A 域提交口令
// 去解锁一条属于 B 域的短链 —— 那等于绕过了域隔离。
func (s *Shortener) VerifyPassword(ctx context.Context, host, code, plain string) error {
	link, err := s.links.GetByCodeInDomain(ctx, code, s.domainIDForHost(ctx, host))
	if err != nil {
		return err
	}
	if err := link.Redirectable(time.Now()); err != nil {
		return err
	}
	if !link.HasPassword() {
		return nil
	}
	if !CheckLinkPassword(link.PasswordHash, plain) {
		return fmt.Errorf("service.shortener: unlock %q: %w", code, domain.ErrUnauthorized)
	}
	return nil
}

// Get 读取短链详情（管理端使用，不校验权限，由调用方先做鉴权）。
func (s *Shortener) Get(ctx context.Context, code string) (*domain.Link, error) {
	return s.links.GetByCode(ctx, code)
}

// List 列出某用户的短链，返回列表与下一页游标（末页为空串）。
func (s *Shortener) List(ctx context.Context, in ListInput) ([]domain.Link, string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if in.MaxLimit > 0 {
		limit = min(limit, in.MaxLimit)
	}
	cursor, err := decodeCursor(in.Cursor)
	if err != nil {
		return nil, "", err
	}

	links, next, err := s.links.ListByOwner(ctx, domain.LinkFilter{
		OwnerID: in.OwnerID,
		Query:   in.Query,
		Tag:     normalizeTagFilter(in.Tag),
		Limit:   limit,
		Cursor:  cursor,
	})
	if err != nil {
		return nil, "", err
	}
	return links, encodeCursor(next), nil
}

// Update 修改短链并主动失效缓存。
func (s *Shortener) Update(ctx context.Context, code string, patch domain.LinkPatch) (*domain.Link, error) {
	if patch.IsEmpty() {
		return nil, domain.Invalid("body", "没有需要更新的字段")
	}
	if patch.TargetURL != nil {
		normalized, err := validator.Normalize(*patch.TargetURL)
		if err != nil {
			return nil, mapURLError(err)
		}
		patch.TargetURL = &normalized
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if len([]rune(title)) > MaxTitleLength {
			return nil, domain.Invalid("title", fmt.Sprintf("标题不能超过 %d 个字符", MaxTitleLength))
		}
		patch.Title = &title
	}
	if patch.ExpiresAt != nil {
		expiresAt, err := normalizeExpiry(patch.ExpiresAt)
		if err != nil {
			return nil, err
		}
		patch.ExpiresAt = expiresAt
	}
	if patch.Tags != nil {
		tags, err := normalizeTags(*patch.Tags)
		if err != nil {
			return nil, err
		}
		if tags == nil {
			// 显式清空：指针指向空切片（与「不动」的 nil 指针区分开）
			tags = []string{}
		}
		patch.Tags = &tags
	}
	if patch.PasswordHash != nil {
		// 空串 = 清除口令；非空则校验强度并摘要化。明文到此为止。
		hash, err := normalizeLinkPassword(*patch.PasswordHash)
		if err != nil {
			return nil, err
		}
		patch.PasswordHash = new(hash)
	}

	link, err := s.links.Update(ctx, code, patch)
	if err != nil {
		return nil, err
	}
	s.evict(ctx, domain.LinkRef{Code: link.ShortCode, DomainID: link.DomainID})
	return link, nil
}

// Delete 软删除短链并失效缓存。
func (s *Shortener) Delete(ctx context.Context, code string) error {
	// 先读一次只为拿到所属域：缓存键是「域 + 短码」，不知道域就删不掉正确的条目，
	// 结果是「删除后跳转仍走缓存」直到 TTL 过期。管理端不是热点路径，这一次读划算。
	// SoftDelete 自己也会校验存在性，所以这里不额外承担「先查再删」的竞态代价。
	link, err := s.links.GetByCode(ctx, code)
	if err != nil {
		return err
	}
	if err := s.links.SoftDelete(ctx, code); err != nil {
		return err
	}
	s.evict(ctx, domain.LinkRef{Code: link.ShortCode, DomainID: link.DomainID})
	return nil
}

// Claim 把匿名链接挂到登录用户名下。
func (s *Shortener) Claim(ctx context.Context, code string, ownerID uuid.UUID) (*domain.Link, error) {
	link, err := s.links.Claim(ctx, code, ownerID)
	if err != nil {
		return nil, err
	}
	s.evict(ctx, domain.LinkRef{Code: link.ShortCode, DomainID: link.DomainID})
	return link, nil
}

// Authorize 校验 Actor 是否有权管理该链接。
// 放行条件：所有者匹配，或持有匹配的管理密钥。
func (s *Shortener) Authorize(link *domain.Link, actor Actor) error {
	if actor.UserID != nil && link.OwnerID != nil && *link.OwnerID == *actor.UserID {
		return nil
	}
	if ManageKeyMatches(link.KeyHash, actor.ManageKey) {
		return nil
	}
	return fmt.Errorf("service.shortener: authorize %q: %w", link.ShortCode, domain.ErrForbidden)
}

// RecordClick 把一次点击投进有界队列。队满即丢弃 —— 跳转优先于统计。
func (s *Shortener) RecordClick(rec domain.ClickRecord) {
	select {
	case s.queue <- rec:
	default:
		s.droppedClicks.Add(1)
	}
}

// ShortURL 拼出对外短链。
//
// 挂在自定义域上的短链用它自己的域名；域不存在（已被删）或本来就是默认域时退回
// PUBLIC_BASE_URL —— 宁可给一个「换了域名但能打开」的链接，也不要给一个打不开的。
//
// scheme 沿用 PUBLIC_BASE_URL 的（见 schemeOf）。
func (s *Shortener) ShortURL(ctx context.Context, link *domain.Link) string {
	if link == nil {
		return ""
	}
	if link.DomainID != nil {
		if name := s.domainName(ctx, *link.DomainID); name != "" {
			return s.scheme + name + "/" + link.ShortCode
		}
	}
	return s.baseURL + "/" + link.ShortCode
}

// ---- 分域（自定义域名）----
//
// 这一节的四个函数是「Host 属于哪个域」的全部逻辑。它们都在跳转路径上，
// 因此设计目标是：读操作零成本、绝不阻塞、PG 故障时不用比旧数据更差的答案。

// domainSnapshot 是「主机名 ↔ 域」的一次性快照。
//
// 双向映射一起建：按主机名找是为了跳转解析，按 ID 找是为了拼 short_url
// （链接上带的是 domain_id）。两者都来自同一次 ListAll，因此天然一致。
type domainSnapshot struct {
	byName   map[string]domain.Domain
	byID     map[uuid.UUID]domain.Domain
	loadedAt time.Time
}

// domainIDForHost 返回 host 对应的域 ID；未登记或无法判定时返回 nil（= 默认域名）。
func (s *Shortener) domainIDForHost(ctx context.Context, host string) *uuid.UUID {
	d := s.domainByHost(ctx, host)
	if d == nil {
		return nil
	}
	id := d.ID
	return &id
}

// domainByHost 在快照里按主机名找域。
//
// host 是**原始** Host 头（可能带端口、大写、尾点），归一化就在这一层做：
// handler 手里只有 r.Host，让每个调用方各自归一化等于把同一段规则抄两遍，
// 而漏抄的那一次症状是「域名明明登记了，短链却 404」——只在特定 Host 写法下复现。
// hostname.Normalize 是幂等的，所以已经归一化过的入参（如创建时的域名）再走一遍无害。
//
// 归一化后为空时返回 nil：HTTP/1.0 的请求可以不带 Host 头，
// 这种请求按默认域处理才是对的（而不是 404）。
func (s *Shortener) domainByHost(ctx context.Context, host string) *domain.Domain {
	if s.domains == nil {
		return nil
	}
	name := hostname.Normalize(host)
	if name == "" {
		return nil
	}
	snap := s.snapshot(ctx)
	if snap == nil {
		return nil
	}
	d, ok := snap.byName[name]
	if !ok {
		return nil
	}
	return &d
}

// domainName 返回域的主机名；域不存在（已被删）或未配置分域时返回空串。
func (s *Shortener) domainName(ctx context.Context, id uuid.UUID) string {
	if s.domains == nil {
		return ""
	}
	snap := s.snapshot(ctx)
	if snap == nil {
		return ""
	}
	return snap.byID[id].Name
}

// snapshot 取当前快照，必要时刷新。
//
// 三条不变量：
//   - 读路径永不阻塞：别的 goroutine 正在刷新时，直接返回当前（可能过期的）快照
//   - 同一时刻只有一个刷新（CAS 保证），不会在 TTL 到期的那一刻被并发请求打穿
//   - 刷新失败时返回**上一次成功的**快照，而不是 nil —— 退回「所有 Host 都是默认域」
//     会让自定义域上的短链在 PG 抖动的这几十秒里集体 404，那比用一份 30 秒前的域名表糟得多
func (s *Shortener) snapshot(ctx context.Context) *domainSnapshot {
	if s.domains == nil {
		return nil
	}

	current := s.domainsSnap.Load()
	if current != nil && time.Since(current.loadedAt) < domainSnapshotTTL {
		return current
	}

	if !s.domainsRefreshing.CompareAndSwap(false, true) {
		return current
	}
	defer s.domainsRefreshing.Store(false)

	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveTimeout)
	defer cancel()

	list, err := s.domains.ListAll(fetchCtx)
	if err != nil {
		slog.Warn("刷新域名快照失败，继续使用上一份", "err", err, "has_previous", current != nil)
		return current
	}

	next := &domainSnapshot{
		byName:   make(map[string]domain.Domain, len(list)),
		byID:     make(map[uuid.UUID]domain.Domain, len(list)),
		loadedAt: time.Now(),
	}
	for _, d := range list {
		next.byName[d.Name] = d
		next.byID[d.ID] = d
	}
	s.domainsSnap.Store(next)
	return next
}

// sameDomain 判断两个「可空域 ID」是否指同一个域（nil 表示默认域名）。
//
// 单独抽出来是因为 nil 在这里是**值**而不是「缺失」：nil 与 nil 必须算同一个域。
// 写成普通的指针比较（a == b）恰好也对，但那样读代码的人要停下来想一秒 ——
// 而这正是「默认域」这个概念最容易被改错的地方。
//
// 它到底在防什么（实测出来的边界，别再猜）：
//   - **首次访问**（缓存未命中）的域隔离由 SQL 的 `domain_id IS NOT DISTINCT FROM $2`
//     负责 —— 查不到就是查不到，压根到不了这里。实测把它改成恒 true，
//     容器级验收依然全绿：跨域请求读的缓存键不同，永远走回源那条路。
//   - **缓存命中**才是它唯一能生效的地方：用缓存里的 DomainID 与本次请求的域比对。
//     在「缓存键按域分开」之后这类命中本不该发生，所以它是**第二道防线**而非主防线。
//   - 之所以不能删：缓存键的构造是多进程共享的契约（api 写、worker 失效、将来还可能有别的读者），
//     一旦某处忘了带域，串味会**静默**发生 —— 访问者被送到另一个域的目标，比 404 严重得多。
//     留一道进程内、与键格式无关的校验，代价只是一次指针比较。
func sameDomain(a, b *uuid.UUID) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// flightKey 是 singleflight 的合并键：域 + 短码。
// 用 \x00 分隔，避免「域 ID 的某个后缀 + 短码」这种拼接歧义。
func flightKey(domainID *uuid.UUID, code string) string {
	if domainID == nil {
		return "-\x00" + code
	}
	return domainID.String() + "\x00" + code
}

// ---- 内部实现 ----

// createdResult 组装创建结果。
func (s *Shortener) createdResult(ctx context.Context, link *domain.Link, manageKey string) *CreateResult {
	return &CreateResult{
		Link:      link,
		ShortURL:  s.ShortURL(ctx, link),
		ManageKey: manageKey,
	}
}

// resolveCreateDomain 把创建入参里的域名解析成域 ID；留空返回 nil（= 默认域名）。
//
// 只认已登记的域名：短链的对外形态是 {scheme}://{domain}/{code}，
// 允许任意域名等于给用户一个指向别人站点的链接（而且我们并不知道那个域名是否属于本服务）。
// 未登记时报字段级 422，让用户能看出是哪一项不对。
func (s *Shortener) resolveCreateDomain(ctx context.Context, raw string) (*uuid.UUID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	name := hostname.Normalize(trimmed)
	if name == "" {
		return nil, domain.Invalid("domain", "域名格式不正确")
	}
	d := s.domainByHost(ctx, name)
	if d == nil {
		return nil, domain.Invalid("domain", fmt.Sprintf("域名 %s 尚未登记", name))
	}
	id := d.ID
	return &id, nil
}

// fromCache 读缓存。返回 (nil, nil) 表示需要回源 ——
// 真未命中与 Redis 故障都归入这一类：故障时降级为直查 PG，跳转仍可用。
//
// 按「域 + 短码」读：正负缓存的键都带域，所以这里必须把**本次请求的域**传下去。
// 传错（例如恒传 nil）的后果是负缓存把「别的域里不存在」当成「本域不存在」，
// 于是短链在自己的域上也 404（见 domain.LinkRef）。
func (s *Shortener) fromCache(ctx context.Context, code string, domainID *uuid.UUID) (*domain.Link, error) {
	entry, err := s.cache.Get(ctx, code, domainID)
	switch {
	case err == nil:
		return cachedToLink(entry), nil
	case errors.Is(err, domain.ErrCacheKnownMissing):
		return nil, domain.NotFound("link", code)
	default:
		slog.Debug("短码缓存未命中，回源数据库", "code", code, "err", err)
		return nil, nil
	}
}

// putCache 回填正向缓存。TTL 会按剩余有效期收缩，避免缓存比实体活得更久。
func (s *Shortener) putCache(ctx context.Context, link *domain.Link) {
	ttl := s.ttlFor(link)
	if ttl <= 0 {
		return
	}
	entry := &domain.CachedLink{
		ID:        link.ID,
		ShortCode: link.ShortCode,
		TargetURL: link.TargetURL,
		Title:     link.Title,
		Status:    link.Status,
		ExpiresAt: link.ExpiresAt,
		// 缓存里只带「有没有口令」这一个布尔，摘要不出库（见 domain.CachedLink）
		PasswordProtected: link.HasPassword(),
		// 所属域必须一起缓存：命中之后要靠它做域校验。
		// 漏了这一行的症状是「自定义域的短链第一次能开、TTL 内之后全 404」——
		// 回源那条路拿的是数据库行所以是对的，只有缓存命中才会踩。
		DomainID: link.DomainID,
	}
	if err := s.cache.Put(ctx, entry, ttl); err != nil {
		slog.Warn("回填短码缓存失败", "code", link.ShortCode, "err", err)
	}
}

// putMissing 写负缓存，抵御短码扫描器。
//
// 意义是「该短码在**该域内**不存在」而不是「不存在」：分域之后两者不是一回事。
// 少了 domainID 就会把「不属于这个域」记成「不存在」，症状是短链在它自己的域上 404。
func (s *Shortener) putMissing(ctx context.Context, code string, domainID *uuid.UUID) {
	if s.negativeTTL <= 0 {
		return
	}
	if err := s.cache.PutMissing(ctx, code, domainID, s.negativeTTL); err != nil {
		slog.Warn("写短码负缓存失败", "code", code, "err", err)
	}
}

// evict 失效缓存；失败只记日志 —— 缓存会在 TTL 后自愈。
//
// 按「域 + 短码」失效：缓存键的坐标系与这里必须一致，否则删掉的不是读到的那个。
func (s *Shortener) evict(ctx context.Context, ref domain.LinkRef) {
	if err := s.cache.Evict(ctx, ref); err != nil {
		slog.Warn("失效短码缓存失败，将在 TTL 后自愈", "code", ref.Code, "err", err)
	}
}

// ttlFor 计算缓存 TTL：不超过剩余有效期，也不超过基础 TTL。
func (s *Shortener) ttlFor(link *domain.Link) time.Duration {
	ttl := s.cacheTTL
	if link.ExpiresAt != nil {
		if remaining := time.Until(*link.ExpiresAt); remaining < ttl {
			ttl = remaining
		}
	}
	return ttl
}

// write 把一条点击记录写进 Redis。ctx 可能已被取消，故用 WithoutCancel 派生。
func (s *Shortener) write(ctx context.Context, rec domain.ClickRecord) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	if err := s.recorder.Record(writeCtx, rec); err != nil {
		s.failedClicks.Add(1)
		slog.Warn("点击统计写入失败，已丢弃该次统计", "code", rec.Code, "err", err)
	}
}

// drain 在退出前把队列里剩余的事件尽力写完。
func (s *Shortener) drain(ctx context.Context) {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drainTimeout)
	defer cancel()

	drained := 0
	for {
		select {
		case rec := <-s.queue:
			s.write(drainCtx, rec)
			drained++
		default:
			// 这条日志是关停顺序的验收依据：它必须出现在 HTTP 优雅关闭完成之后
			slog.Info("统计写入队列已排空", "drained", drained)
			return
		}
	}
}

// cachedToLink 把缓存条目转成只含跳转所需字段的 Link。
// 注意 OwnerID / KeyHash / PasswordHash 一定是空 —— 缓存里本来就没有它们；
// 口令只以 PasswordProtected 这一个布尔的形式出现在跳转路径上。
func cachedToLink(entry *domain.CachedLink) *domain.Link {
	return &domain.Link{
		ID:        entry.ID,
		ShortCode: entry.ShortCode,
		TargetURL: entry.TargetURL,
		Title:     entry.Title,
		Status:    entry.Status,
		ExpiresAt: entry.ExpiresAt,

		PasswordProtected: entry.PasswordProtected,
		// 带上所属域：Resolve 的域校验读的就是它
		DomainID: entry.DomainID,
	}
}

// normalizeExpiry 校验过期时间必须是未来时刻。
func normalizeExpiry(expiresAt *time.Time) (*time.Time, error) {
	if expiresAt == nil {
		return nil, nil
	}
	utc := expiresAt.UTC()
	if !utc.After(time.Now()) {
		return nil, domain.Invalid("expires_at", "过期时间必须晚于当前时间")
	}
	return &utc, nil
}

// mapURLError 把 validator 的哨兵错误翻译成字段级领域错误。
func mapURLError(err error) error {
	switch {
	case errors.Is(err, validator.ErrEmpty):
		return domain.Invalid("target_url", "请输入要缩短的链接")
	case errors.Is(err, validator.ErrTooLong):
		return domain.Invalid("target_url", fmt.Sprintf("链接长度不能超过 %d 个字符", validator.MaxURLLength))
	case errors.Is(err, validator.ErrBadScheme):
		return domain.Invalid("target_url", "仅支持 http/https 链接")
	case errors.Is(err, validator.ErrNoHost):
		return domain.Invalid("target_url", "链接缺少域名")
	case errors.Is(err, validator.ErrMalformed):
		return domain.Invalid("target_url", "链接格式无法解析")
	default:
		return err
	}
}

// mapCodeError 把 shortcode 的哨兵错误翻译成字段级领域错误。
func mapCodeError(err error) error {
	switch {
	case errors.Is(err, shortcode.ErrTooShort):
		return domain.Invalid("custom_code", fmt.Sprintf("自定义短码至少 %d 位", shortcode.MinLength))
	case errors.Is(err, shortcode.ErrTooLong):
		return domain.Invalid("custom_code", fmt.Sprintf("自定义短码最多 %d 位", shortcode.MaxLength))
	case errors.Is(err, shortcode.ErrBadChar):
		return domain.Invalid("custom_code", "自定义短码只能包含字母、数字、连字符与下划线")
	case errors.Is(err, shortcode.ErrReserved):
		return domain.Invalid("custom_code", "该短码是系统保留字，请换一个")
	default:
		return err
	}
}

// encodeCursor 把 keyset 游标编码成对前端不透明的字符串。
// 末页（Valid=false）编码为空串，前端据此停止翻页。
func encodeCursor(c domain.LinkCursor) string {
	if !c.Valid {
		return ""
	}
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor 解析游标；空串表示第一页。
func decodeCursor(raw string) (domain.LinkCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.LinkCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return domain.LinkCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	ts, idPart, found := strings.Cut(string(decoded), "|")
	if !found {
		return domain.LinkCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return domain.LinkCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return domain.LinkCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	return domain.LinkCursor{CreatedAt: createdAt, ID: id, Valid: true}, nil
}
