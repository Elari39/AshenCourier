package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"ashen-courier/internal/domain"
)

// ---- 测试替身：全部手写，与 service/shortener_test.go 的风格一致（不引 mock 库）----

// fakeCounter 记录计数回刷的全部调用，用来钉住「取走要不回来就按值归还」这条不变量。
type fakeCounter struct {
	dirty    []string
	dirtyErr error
	deltas   map[string]int64
	takeErr  map[string]error

	restored map[string]int64
	marked   []string
}

func newFakeCounter() *fakeCounter {
	return &fakeCounter{
		deltas:   map[string]int64{},
		takeErr:  map[string]error{},
		restored: map[string]int64{},
	}
}

func (c *fakeCounter) DirtyCodes(context.Context, int) ([]string, error) {
	if c.dirtyErr != nil {
		return nil, c.dirtyErr
	}
	return c.dirty, nil
}

func (c *fakeCounter) TakeDelta(_ context.Context, code string) (int64, error) {
	if err := c.takeErr[code]; err != nil {
		return 0, err
	}
	delta := c.deltas[code]
	delete(c.deltas, code)
	return delta, nil
}

func (c *fakeCounter) RestoreDelta(_ context.Context, code string, delta int64) error {
	c.restored[code] += delta
	c.deltas[code] += delta
	return nil
}

func (c *fakeCounter) MarkDirty(_ context.Context, codes ...string) error {
	c.marked = append(c.marked, codes...)
	return nil
}

// fakeCounts 模拟 PG 基线回写；failFor 用于按短码注入失败。
type fakeCounts struct {
	added   map[string]int64
	failFor map[string]error
}

func newFakeCounts() *fakeCounts {
	return &fakeCounts{added: map[string]int64{}, failFor: map[string]error{}}
}

func (c *fakeCounts) AddClickCount(_ context.Context, code string, delta int64) (int64, error) {
	if err := c.failFor[code]; err != nil {
		return 0, err
	}
	c.added[code] += delta
	return c.added[code], nil
}

// fakeClicks 模拟明细落库。
type fakeClicks struct {
	insertErr error
	inserted  [][]domain.ClickEvent
}

func (c *fakeClicks) InsertBatch(_ context.Context, events []domain.ClickEvent) error {
	if c.insertErr != nil {
		return c.insertErr
	}
	c.inserted = append(c.inserted, events)
	return nil
}

func (c *fakeClicks) Aggregate(context.Context, domain.StatsQuery) (*domain.StatsAggregate, error) {
	return nil, nil
}

// fakeStream 模拟 Stream 消费；acked 按批次记录 XACK 的 ID。
type fakeStream struct {
	readResult  *domain.ReadResult
	readErr     error
	claimResult *domain.ReadResult
	claimErr    error
	ackErr      error
	acked       [][]string
}

func (s *fakeStream) EnsureGroup(context.Context) error { return nil }

func (s *fakeStream) ReadClicks(context.Context, string, int64, time.Duration) (*domain.ReadResult, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.readResult, nil
}

func (s *fakeStream) AutoClaim(context.Context, string, time.Duration, int64) (*domain.ReadResult, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if s.claimResult == nil {
		return &domain.ReadResult{}, nil
	}
	return s.claimResult, nil
}

func (s *fakeStream) Ack(_ context.Context, ids ...string) error {
	if s.ackErr != nil {
		return s.ackErr
	}
	s.acked = append(s.acked, ids)
	return nil
}

// fakeSweeper 模拟过期扫描。
type fakeSweeper struct {
	codes []string
	err   error
}

func (s *fakeSweeper) ExpireDue(context.Context, time.Time, int) ([]string, error) {
	return s.codes, s.err
}

// fakeCache 只关心 Evict。
type fakeCache struct {
	evicted []string
	err     error
}

func (c *fakeCache) Get(context.Context, string) (*domain.CachedLink, error) {
	return nil, domain.ErrCacheMiss
}

func (c *fakeCache) Put(context.Context, *domain.CachedLink, time.Duration) error { return nil }

func (c *fakeCache) PutMissing(context.Context, string, time.Duration) error { return nil }

func (c *fakeCache) Evict(_ context.Context, codes ...string) error {
	if c.err != nil {
		return c.err
	}
	c.evicted = append(c.evicted, codes...)
	return nil
}

// newTestWorker 装配一个全 fake 的 Worker；日志丢弃，避免测试输出被噪音淹没。
func newTestWorker(counter *fakeCounter, counts *fakeCounts, clicks *fakeClicks, stream *fakeStream, cache *fakeCache, sweeper *fakeSweeper) *Worker {
	return New(Deps{
		Counts:  counts,
		Sweeper: sweeper,
		Clicks:  clicks,
		Counter: counter,
		Stream:  stream,
		Cache:   cache,
	}, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// ---- 计数回刷 ----

// TestSyncCountsRestoresDeltaOnWriteFailure 守住上一轮 P1 的修复：
// TakeDelta 是「取走即删」，写库失败必须按值归还，只补 dirty 标记会让这批点击永久丢失。
func TestSyncCountsRestoresDeltaOnWriteFailure(t *testing.T) {
	t.Parallel()

	counter := newFakeCounter()
	counter.dirty = []string{"abc1234"}
	counter.deltas["abc1234"] = 7

	counts := newFakeCounts()
	counts.failFor["abc1234"] = errors.New("pg: connection reset by peer")

	w := newTestWorker(counter, counts, &fakeClicks{}, &fakeStream{}, &fakeCache{}, &fakeSweeper{})

	if err := w.syncCounts(t.Context()); err != nil {
		t.Fatalf("单个短码写库失败不该让整轮报错：%v", err)
	}
	if got := counter.restored["abc1234"]; got != 7 {
		t.Fatalf("RestoreDelta 归还的增量 = %d, want 7", got)
	}
	if got := counter.deltas["abc1234"]; got != 7 {
		t.Fatalf("归还后计数键里应重新有 7，实际 %d", got)
	}
	if len(counter.marked) != 0 {
		t.Fatalf("写库失败不该退化成 MarkDirty（它只补标记、不含数值）：%v", counter.marked)
	}
	if got := w.Stats().CountSynced; got != 0 {
		t.Fatalf("失败的短码不该计入 CountSynced，实际 %d", got)
	}
}

// TestSyncCountsDropsDeltaWhenLinkGone：短码已被删除时增量无处可去，
// 只能丢弃并告警，但绝不能假装成功或反复归还。
func TestSyncCountsDropsDeltaWhenLinkGone(t *testing.T) {
	t.Parallel()

	counter := newFakeCounter()
	counter.dirty = []string{"gone123"}
	counter.deltas["gone123"] = 3

	counts := newFakeCounts()
	counts.failFor["gone123"] = domain.NotFound("link", "gone123")

	w := newTestWorker(counter, counts, &fakeClicks{}, &fakeStream{}, &fakeCache{}, &fakeSweeper{})

	if err := w.syncCounts(t.Context()); err != nil {
		t.Fatalf("短码已删除不该让整轮报错：%v", err)
	}
	if got := counter.restored["gone123"]; got != 0 {
		t.Fatalf("短码已删除时不该归还增量（还回去下一轮还是会失败），实际归还 %d", got)
	}
	if got := w.Stats().CountSynced; got != 0 {
		t.Fatalf("被丢弃的增量不该计入 CountSynced，实际 %d", got)
	}
}

// TestSyncCountsReMarksDirtyWhenTakeFails：取增量失败时把短码放回 dirty，下一轮重试。
func TestSyncCountsReMarksDirtyWhenTakeFails(t *testing.T) {
	t.Parallel()

	counter := newFakeCounter()
	counter.dirty = []string{"abc1234"}
	counter.takeErr["abc1234"] = errors.New("redis: i/o timeout")

	counts := newFakeCounts()
	w := newTestWorker(counter, counts, &fakeClicks{}, &fakeStream{}, &fakeCache{}, &fakeSweeper{})

	if err := w.syncCounts(t.Context()); err != nil {
		t.Fatalf("取增量失败不该让整轮报错：%v", err)
	}
	if len(counter.marked) != 1 || counter.marked[0] != "abc1234" {
		t.Fatalf("取增量失败的短码应被 MarkDirty，实际 %v", counter.marked)
	}
	if len(counts.added) != 0 {
		t.Fatalf("取不到增量就不该写库，实际 %v", counts.added)
	}
}

// TestSyncCountsSuccessAndZeroDelta：正常路径累加进基线；增量为 0 的短码直接跳过。
func TestSyncCountsSuccessAndZeroDelta(t *testing.T) {
	t.Parallel()

	counter := newFakeCounter()
	counter.dirty = []string{"a1234", "b1234"}
	counter.deltas["a1234"] = 3
	counter.deltas["b1234"] = 0

	counts := newFakeCounts()
	w := newTestWorker(counter, counts, &fakeClicks{}, &fakeStream{}, &fakeCache{}, &fakeSweeper{})

	if err := w.syncCounts(t.Context()); err != nil {
		t.Fatalf("syncCounts 不应失败：%v", err)
	}
	if got := counts.added["a1234"]; got != 3 {
		t.Fatalf("a1234 基线增量 = %d, want 3", got)
	}
	if _, ok := counts.added["b1234"]; ok {
		t.Fatal("增量为 0 的短码不该写库")
	}
	if got := w.Stats().CountSynced; got != 1 {
		t.Fatalf("CountSynced = %d, want 1", got)
	}
}

// TestSyncCountsSurfacesDirtyListFailure：连 dirty 列表都拿不到时，整轮必须报错，
// 由定时器记 errors —— 不能静默当成「没有要回刷的短码」。
func TestSyncCountsSurfacesDirtyListFailure(t *testing.T) {
	t.Parallel()

	counter := newFakeCounter()
	counter.dirtyErr = errors.New("redis: connection refused")

	w := newTestWorker(counter, newFakeCounts(), &fakeClicks{}, &fakeStream{}, &fakeCache{}, &fakeSweeper{})

	if err := w.syncCounts(t.Context()); err == nil {
		t.Fatal("dirty 列表取不到时应报错")
	}
}

// ---- 批量落库与 ACK ----

// TestHandleBatchDoesNotAckOnInsertFailure：落库失败必须不 ACK —— 这批消息仍是
// pending，等认领循环把它们捞回来重投（投递语义是 at-least-once）。
func TestHandleBatchDoesNotAckOnInsertFailure(t *testing.T) {
	t.Parallel()

	stream := &fakeStream{}
	clicks := &fakeClicks{insertErr: errors.New("pg: too many connections")}
	w := newTestWorker(newFakeCounter(), newFakeCounts(), clicks, stream, &fakeCache{}, &fakeSweeper{})

	w.handleBatch(t.Context(), &domain.ReadResult{
		Messages: []domain.StreamMessage{{ID: "1-0", Event: domain.ClickRecord{Code: "abc1234"}}},
	}, "consume")

	if len(stream.acked) != 0 {
		t.Fatalf("落库失败时不该 ACK，实际 ACK 了 %v", stream.acked)
	}
	if got := w.Stats().Consumed; got != 0 {
		t.Fatalf("落库失败不该计入 Consumed，实际 %d", got)
	}
	if got := w.Stats().Errors; got != 1 {
		t.Fatalf("落库失败应记一次 errors，实际 %d", got)
	}
}

// TestHandleBatchAcksValidMessages：成功落库后 ACK 同一批 ID。
func TestHandleBatchAcksValidMessages(t *testing.T) {
	t.Parallel()

	stream := &fakeStream{}
	clicks := &fakeClicks{}
	w := newTestWorker(newFakeCounter(), newFakeCounts(), clicks, stream, &fakeCache{}, &fakeSweeper{})

	w.handleBatch(t.Context(), &domain.ReadResult{
		Messages: []domain.StreamMessage{
			{ID: "1-0", Event: domain.ClickRecord{Code: "abc1234", UserAgent: uaChrome}},
			{ID: "2-0", Event: domain.ClickRecord{Code: "abc1234", UserAgent: uaIPhone}},
		},
	}, "consume")

	if len(clicks.inserted) != 1 || len(clicks.inserted[0]) != 2 {
		t.Fatalf("应一次性落库 2 条明细，实际 %v", clicks.inserted)
	}
	if got := clicks.inserted[0][0].Device; got != "desktop" {
		t.Fatalf("UA 解析结果没带上：device = %q, want desktop", got)
	}
	if len(stream.acked) != 1 || len(stream.acked[0]) != 2 {
		t.Fatalf("应 ACK 两个 ID，实际 %v", stream.acked)
	}
	if got := w.Stats().Consumed; got != 2 {
		t.Fatalf("Consumed = %d, want 2", got)
	}
}

// TestHandleBatchAcksMalformedIDs：脏消息必须被 ACK，否则会永远卡在 pending 里每轮重投。
func TestHandleBatchAcksMalformedIDs(t *testing.T) {
	t.Parallel()

	stream := &fakeStream{}
	w := newTestWorker(newFakeCounter(), newFakeCounts(), &fakeClicks{}, stream, &fakeCache{}, &fakeSweeper{})

	w.handleBatch(t.Context(), &domain.ReadResult{MalformedIDs: []string{"9-0", "10-0"}}, "claim")

	if len(stream.acked) != 1 || len(stream.acked[0]) != 2 {
		t.Fatalf("脏消息应被 ACK，实际 %v", stream.acked)
	}
	if got := w.Stats().Malformed; got != 2 {
		t.Fatalf("Malformed = %d, want 2", got)
	}
}

// TestClaimPendingPropagatesError：认领失败必须冒给定时器，由它记 errors 并退避。
func TestClaimPendingPropagatesError(t *testing.T) {
	t.Parallel()

	stream := &fakeStream{claimErr: errors.New("redis: xautoclaim failed")}
	w := newTestWorker(newFakeCounter(), newFakeCounts(), &fakeClicks{}, stream, &fakeCache{}, &fakeSweeper{})

	if err := w.claimPending(t.Context()); err == nil {
		t.Fatal("认领失败时应返回错误")
	}
}

// ---- 过期清理 ----

// TestExpireLinksEvictsCache：置为 disabled 之后必须顺手失效缓存，
// 否则过期短链在缓存 TTL 内还能继续跳转。
func TestExpireLinksEvictsCache(t *testing.T) {
	t.Parallel()

	sweeper := &fakeSweeper{codes: []string{"a1234", "b1234"}}
	cache := &fakeCache{}
	w := newTestWorker(newFakeCounter(), newFakeCounts(), &fakeClicks{}, &fakeStream{}, cache, sweeper)

	if err := w.expireLinks(t.Context()); err != nil {
		t.Fatalf("expireLinks 不应失败：%v", err)
	}
	if len(cache.evicted) != 2 {
		t.Fatalf("应失效两个短码的缓存，实际 %v", cache.evicted)
	}
	if got := w.Stats().Expired; got != 2 {
		t.Fatalf("Expired = %d, want 2", got)
	}
}

// uaChrome / uaIPhone 是 toClickEvent 断言用的真实 UA 片段。
const (
	uaChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	uaIPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1"
)
