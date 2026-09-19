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

	baseURL     string
	cacheTTL    time.Duration
	negativeTTL time.Duration

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
func NewShortener(links domain.LinkRepository, cache domain.LinkCache, recorder domain.ClickRecorder, cfg ShortenerConfig) *Shortener {
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = DefaultQueueSize
	}
	return &Shortener{
		links:       links,
		cache:       cache,
		recorder:    recorder,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		cacheTTL:    cfg.CacheTTL,
		negativeTTL: cfg.NegativeTTL,
		queue:       make(chan domain.ClickRecord, queueSize),
	}
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
		// link:v1:miss:<code> 里就存着「确认不存在」的断言；不清掉的话，
		// 刚创建成功的短码在负缓存 TTL 内跳转仍是 404，与事实矛盾。
		s.evict(ctx, base.ShortCode)
		return s.createdResult(base, manageKey), nil
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
			s.evict(ctx, link.ShortCode)
			return s.createdResult(&link, manageKey), nil
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
func (s *Shortener) Resolve(ctx context.Context, code string) (*domain.Link, error) {
	if !shortcode.IsValidShape(code) {
		return nil, domain.NotFound("link", code)
	}

	link, err := s.fromCache(ctx, code)
	if err != nil {
		return nil, err
	}
	if link == nil {
		link, err = s.resolveFromDB(ctx, code)
		if err != nil {
			return nil, err
		}
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
//   - key 就是短码；**不调 Forget**：缓存写失败交给 TTL 自愈，少一条需要推理的路径
//   - 回源用 WithoutCancel 派生的 ctx：等待者可能先取消，但回源本身不该被某一个请求的
//     取消打断 —— 否则其余等待者会拿到「context canceled」而不是真实结果
//   - 失败结果同样会被共享（singleflight 的语义），由负缓存兜住 404，不会形成击穿循环
func (s *Shortener) resolveFromDB(ctx context.Context, code string) (*domain.Link, error) {
	v, err, _ := s.flight.Do(code, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveTimeout)
		defer cancel()

		s.pgFallbacks.Add(1)

		link, err := s.links.GetByCode(fetchCtx, code)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				s.putMissing(fetchCtx, code)
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
func (s *Shortener) VerifyPassword(ctx context.Context, code, plain string) error {
	link, err := s.links.GetByCode(ctx, code)
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
	s.evict(ctx, code)
	return link, nil
}

// Delete 软删除短链并失效缓存。
func (s *Shortener) Delete(ctx context.Context, code string) error {
	if err := s.links.SoftDelete(ctx, code); err != nil {
		return err
	}
	s.evict(ctx, code)
	return nil
}

// Claim 把匿名链接挂到登录用户名下。
func (s *Shortener) Claim(ctx context.Context, code string, ownerID uuid.UUID) (*domain.Link, error) {
	link, err := s.links.Claim(ctx, code, ownerID)
	if err != nil {
		return nil, err
	}
	s.evict(ctx, code)
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
func (s *Shortener) ShortURL(code string) string {
	return s.baseURL + "/" + code
}

// ---- 内部实现 ----

// createdResult 组装创建结果。
func (s *Shortener) createdResult(link *domain.Link, manageKey string) *CreateResult {
	return &CreateResult{
		Link:      link,
		ShortURL:  s.ShortURL(link.ShortCode),
		ManageKey: manageKey,
	}
}

// fromCache 读缓存。返回 (nil, nil) 表示需要回源 ——
// 真未命中与 Redis 故障都归入这一类：故障时降级为直查 PG，跳转仍可用。
func (s *Shortener) fromCache(ctx context.Context, code string) (*domain.Link, error) {
	entry, err := s.cache.Get(ctx, code)
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
	}
	if err := s.cache.Put(ctx, entry, ttl); err != nil {
		slog.Warn("回填短码缓存失败", "code", link.ShortCode, "err", err)
	}
}

// putMissing 写负缓存，抵御短码扫描器。
func (s *Shortener) putMissing(ctx context.Context, code string) {
	if s.negativeTTL <= 0 {
		return
	}
	if err := s.cache.PutMissing(ctx, code, s.negativeTTL); err != nil {
		slog.Warn("写短码负缓存失败", "code", code, "err", err)
	}
}

// evict 失效缓存；失败只记日志 —— 缓存会在 TTL 后自愈。
func (s *Shortener) evict(ctx context.Context, code string) {
	if err := s.cache.Evict(ctx, code); err != nil {
		slog.Warn("失效短码缓存失败，将在 TTL 后自愈", "code", code, "err", err)
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
