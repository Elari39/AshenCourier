// Package worker 消费 Redis Stream 里的点击事件并回刷计数。
//
// 四个常驻 goroutine（对应 PLAN.md §6.3）：
//
//	A · Stream 消费：XREADGROUP → 批量 INSERT → XACK
//	B · 计数同步  ：每 2s 把 Redis 增量刷进 links.click_count
//	C · 兜底认领  ：每 30s XAUTOCLAIM 认领 idle > 60s 的 pending 消息
//	D · 过期清理  ：每小时把过期短链置为 disabled 并清缓存
//
// 投递语义是 at-least-once：极端情况下（处理完成但 ACK 前重启）明细可能重复插入，
// 但计数走 INCR 累加不受影响，只有明细条数可能多算。P1 可以用 event_uid 唯一索引去重。
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/pkg/ua"
	"ashen-courier/internal/store/postgres"
	"ashen-courier/internal/store/redis"
)

// DefaultConfig 的取值，全部可在 cmd/worker 里覆盖。
const (
	DefaultBatchSize      = int64(500)
	DefaultBlockTimeout   = 2 * time.Second
	DefaultCountSyncEvery = 2 * time.Second
	DefaultClaimEvery     = 30 * time.Second
	DefaultClaimMinIdle   = 60 * time.Second
	DefaultExpireEvery    = time.Hour
	DefaultExpireBatch    = 500
	DefaultErrorBackoff   = time.Second
)

// Config 是 Worker 的运行参数。
type Config struct {
	// Consumer 是消费者名，用于 XREADGROUP / XAUTOCLAIM 与 pending 归属。
	Consumer string
	// BatchSize 是单次 XREADGROUP 的批大小。
	BatchSize int64
	// BlockTimeout 是 XREADGROUP 的阻塞时长。
	BlockTimeout time.Duration
	// CountSyncEvery 是计数回刷周期。
	CountSyncEvery time.Duration
	// ClaimEvery 是认领 pending 的周期。
	ClaimEvery time.Duration
	// ClaimMinIdle 是判定「消费者已死」的空闲阈值。
	ClaimMinIdle time.Duration
	// ExpireEvery 是过期清理周期。
	ExpireEvery time.Duration
	// ExpireBatch 是单次过期清理的条数上限。
	ExpireBatch int
	// ErrorBackoff 是循环出错后的退避时长，避免故障时打爆日志。
	ErrorBackoff time.Duration
}

// withDefaults 补齐未设置的字段。
func (c Config) withDefaults() Config {
	if c.Consumer == "" {
		c.Consumer = "worker-1"
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.BlockTimeout <= 0 {
		c.BlockTimeout = DefaultBlockTimeout
	}
	if c.CountSyncEvery <= 0 {
		c.CountSyncEvery = DefaultCountSyncEvery
	}
	if c.ClaimEvery <= 0 {
		c.ClaimEvery = DefaultClaimEvery
	}
	if c.ClaimMinIdle <= 0 {
		c.ClaimMinIdle = DefaultClaimMinIdle
	}
	if c.ExpireEvery <= 0 {
		c.ExpireEvery = DefaultExpireEvery
	}
	if c.ExpireBatch <= 0 {
		c.ExpireBatch = DefaultExpireBatch
	}
	if c.ErrorBackoff <= 0 {
		c.ErrorBackoff = DefaultErrorBackoff
	}
	return c
}

// Stats 是 worker 的运行计数，供 /healthz 与日志打点。
type Stats struct {
	// Consumed 是累计消费（并 ACK）的消息数。
	Consumed int64
	// Malformed 是累计丢弃的脏消息数。
	Malformed int64
	// Claimed 是累计通过 XAUTOCLAIM 认领的消息数。
	Claimed int64
	// CountSynced 是累计回刷的短码次数。
	CountSynced int64
	// Expired 是累计置为失效的短链数。
	Expired int64
	// Errors 是累计错误次数。
	Errors int64
}

// Worker 是点击事件的消费者。
type Worker struct {
	links  *postgres.LinkStore
	clicks *postgres.ClickStore
	rdb    *redis.Client
	cache  *redis.Cache

	cfg Config
	log *slog.Logger

	consumed    atomic.Int64
	malformed   atomic.Int64
	claimed     atomic.Int64
	countSynced atomic.Int64
	expired     atomic.Int64
	errors      atomic.Int64

	wg sync.WaitGroup
}

// New 构造 Worker。
func New(links *postgres.LinkStore, clicks *postgres.ClickStore, rdb *redis.Client, cfg Config, logger *slog.Logger) *Worker {
	return &Worker{
		links:  links,
		clicks: clicks,
		rdb:    rdb,
		cache:  redis.NewCache(rdb),
		cfg:    cfg.withDefaults(),
		log:    logger,
	}
}

// Start 启动四个后台循环。
func (w *Worker) Start(ctx context.Context) {
	if err := w.rdb.EnsureGroup(ctx); err != nil {
		w.errors.Add(1)
		w.log.Error("创建消费组失败，Stream 消费将无法启动", "err", err)
	} else {
		w.log.Info("消费组就绪", "consumer", w.cfg.Consumer)
	}

	w.wg.Go(func() { w.consumeLoop(ctx) })
	w.wg.Go(func() { w.countSyncLoop(ctx) })
	w.wg.Go(func() { w.claimLoop(ctx) })
	w.wg.Go(func() { w.expireLoop(ctx) })
}

// Stop 等待全部循环退出。调用前应先取消传给 Start 的 context。
func (w *Worker) Stop() { w.wg.Wait() }

// Stats 返回运行计数快照。
func (w *Worker) Stats() Stats {
	return Stats{
		Consumed:    w.consumed.Load(),
		Malformed:   w.malformed.Load(),
		Claimed:     w.claimed.Load(),
		CountSynced: w.countSynced.Load(),
		Expired:     w.expired.Load(),
		Errors:      w.errors.Load(),
	}
}

// consumeLoop 是 A · Stream 消费循环。
func (w *Worker) consumeLoop(ctx context.Context) {
	w.log.Info("已启动 Stream 消费循环",
		"consumer", w.cfg.Consumer, "batch", w.cfg.BatchSize, "block", w.cfg.BlockTimeout)

	for ctx.Err() == nil {
		result, err := w.rdb.ReadClicks(ctx, w.cfg.Consumer, w.cfg.BatchSize, w.cfg.BlockTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.errors.Add(1)
			w.log.Error("读取点击事件失败", "err", err)
			sleepCtx(ctx, w.cfg.ErrorBackoff)
			continue
		}
		w.handleBatch(ctx, result, "consume")
	}
}

// claimLoop 是 C · 兜底认领循环。
func (w *Worker) claimLoop(ctx context.Context) {
	w.log.Info("已启动 pending 认领循环",
		"every", w.cfg.ClaimEvery, "min_idle", w.cfg.ClaimMinIdle)

	w.everyFixed(ctx, "claim-pending", w.cfg.ClaimEvery, func(ctx context.Context) error {
		result, err := w.rdb.AutoClaim(ctx, w.cfg.Consumer, w.cfg.ClaimMinIdle, w.cfg.BatchSize)
		if err != nil {
			return err
		}
		if len(result.Messages) == 0 && len(result.MalformedIDs) == 0 {
			return nil
		}
		w.claimed.Add(int64(len(result.Messages)))
		w.log.Warn("认领到滞留的 pending 消息 —— 可能有消费者异常退出",
			"count", len(result.Messages), "malformed", len(result.MalformedIDs))
		w.handleBatch(ctx, result, "claim")
		return nil
	})
}

// countSyncLoop 是 B · 计数同步循环。
func (w *Worker) countSyncLoop(ctx context.Context) {
	w.log.Info("已启动计数同步循环", "every", w.cfg.CountSyncEvery)

	w.everyFixed(ctx, "sync-count", w.cfg.CountSyncEvery, func(ctx context.Context) error {
		codes, err := w.rdb.DirtyCodes(ctx, 1000)
		if err != nil {
			return err
		}
		if len(codes) == 0 {
			return nil
		}

		synced := 0
		var retry []string
		for _, code := range codes {
			delta, err := w.rdb.TakeDelta(ctx, code)
			if err != nil {
				// 取不到增量：把标记放回去，下一轮重试
				retry = append(retry, code)
				continue
			}
			if delta == 0 {
				continue
			}
			if _, err := w.links.AddClickCount(ctx, code, delta); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					// 短码已被删除，增量无处可去，直接丢弃并告警
					w.log.Warn("短码已不存在，丢弃待同步的点击增量", "code", code, "delta", delta)
					continue
				}
				// 写库失败：把增量还回去，下一轮重试（宁可重复累加也不能丢）
				if err := w.rdb.MarkDirty(ctx, code); err != nil {
					w.log.Error("重新标记 dirty 失败，该批增量可能丢失", "code", code, "err", err)
				}
				retry = append(retry, code)
				continue
			}
			synced++
		}

		if len(retry) > 0 {
			if err := w.rdb.MarkDirty(ctx, retry...); err != nil {
				w.log.Error("重新标记 dirty 失败", "err", err)
			}
		}
		if synced > 0 {
			w.countSynced.Add(int64(synced))
			w.log.Debug("计数同步完成", "links", synced, "scanned", len(codes))
		}
		return nil
	})
}

// expireLoop 是 D · 过期清理循环。
func (w *Worker) expireLoop(ctx context.Context) {
	w.log.Info("已启动过期清理循环", "every", w.cfg.ExpireEvery, "batch", w.cfg.ExpireBatch)

	w.everyFixed(ctx, "expire-links", w.cfg.ExpireEvery, func(ctx context.Context) error {
		codes, err := w.links.ExpireDue(ctx, time.Now().UTC(), w.cfg.ExpireBatch)
		if err != nil {
			return err
		}
		if len(codes) == 0 {
			return nil
		}
		if err := w.cache.Evict(ctx, codes...); err != nil {
			// 缓存失效失败不算致命：缓存 TTL 最多 1 小时，且跳转时会二次校验状态
			w.log.Warn("过期短链的缓存失效失败，将在 TTL 后自愈", "err", err)
		}
		w.expired.Add(int64(len(codes)))
		w.log.Info("已把过期短链置为 disabled", "count", len(codes))
		return nil
	})
}

// handleBatch 落库一批消息并 ACK。
// source 只用于日志，区分「正常消费」与「认领补投」。
func (w *Worker) handleBatch(ctx context.Context, result *redis.ReadResult, source string) {
	if len(result.Messages) == 0 && len(result.MalformedIDs) == 0 {
		return
	}

	if len(result.Messages) > 0 {
		events := make([]domain.ClickEvent, 0, len(result.Messages))
		ids := make([]string, 0, len(result.Messages))
		for _, msg := range result.Messages {
			events = append(events, toClickEvent(msg.Event))
			ids = append(ids, msg.ID)
		}

		if err := w.clicks.InsertBatch(ctx, events); err != nil {
			// 不 ACK：这批消息仍是 pending，等认领循环把它们捞回来重投
			w.errors.Add(1)
			w.log.Error("批量写入点击明细失败，本批不 ACK 等待重投",
				"count", len(events), "source", source, "err", err)
			return
		}
		if err := w.rdb.Ack(ctx, ids...); err != nil {
			w.errors.Add(1)
			w.log.Error("XACK 失败，明细可能被重复投递", "count", len(ids), "err", err)
			return
		}
		w.consumed.Add(int64(len(events)))
	}

	// 脏消息必须一并 ACK，否则会永远卡在 pending 里每轮重投
	if len(result.MalformedIDs) > 0 {
		w.malformed.Add(int64(len(result.MalformedIDs)))
		w.log.Warn("丢弃无法解析的点击消息", "count", len(result.MalformedIDs), "ids", result.MalformedIDs)
		if err := w.rdb.Ack(ctx, result.MalformedIDs...); err != nil {
			w.errors.Add(1)
			w.log.Error("ACK 脏消息失败", "err", err)
		}
	}
}

// everyFixed 每 interval 执行一次 fn，直到 ctx 取消。
//
// 用 time.Tick 而不是 NewTicker：Go 1.23 起不可达的 ticker 会被 GC 回收，
// 循环退出后这个 channel 就没人引用了，不需要手动 Stop。
func (w *Worker) everyFixed(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	tick := time.Tick(interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			start := time.Now()
			if err := fn(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				w.errors.Add(1)
				w.log.Error("定时任务执行失败", "job", name, "err", err, "elapsed", time.Since(start))
				continue
			}
			w.log.Debug("定时任务完成", "job", name, "elapsed", time.Since(start))
		}
	}
}

// toClickEvent 把 Stream 事件补上 UA 解析结果，转成待落库的明细。
func toClickEvent(ev redis.StreamEvent) domain.ClickEvent {
	info := ua.Parse(ev.UserAgent)
	return domain.ClickEvent{
		LinkID:     ev.LinkID,
		ShortCode:  ev.Code,
		OccurredAt: ev.OccurredAt,
		Referer:    ev.Referer,
		UserAgent:  ev.UserAgent,
		IP:         ev.IP,
		Country:    "", // 预留：GeoIP，MVP 恒为空
		Device:     info.Device,
		Browser:    info.Browser,
		OS:         info.OS,
	}
}

// sleepCtx 是可被取消的休眠，避免关闭时白等一个退避周期。
func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
