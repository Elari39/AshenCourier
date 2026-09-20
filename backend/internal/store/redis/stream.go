package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"uuid"

	goredis "github.com/redis/go-redis/v9"

	"ashen-courier/internal/domain"
)

// 消息字段名。用短名是因为 Stream 里每条消息都会重复这些键，能省内存。
const (
	fieldCode     = "code"
	fieldLinkID   = "link_id"
	fieldOccurred = "ts"
	fieldIP       = "ip"
	fieldUA       = "ua"
	fieldReferer  = "ref"
)

// defaultMaxDeliveryAttempts 是一条消息被判为「毒消息」之前的重投次数上限。
//
// 取 5 的理由：真正的瞬时故障（PG 抖动、连接被重置）一两轮就过去，
// 而永久性失败（外键冲突、字段超长）会在每一轮都失败 —— 5 次足以区分二者，
// 又不至于让一次长时间的 PG 故障把大批正常消息误判成毒消息丢掉。
// worker 侧可用 Config.MaxDeliveryAttempts 覆盖。
const defaultMaxDeliveryAttempts = int64(5)

// ErrMalformedEvent 表示 Stream 里存在无法解析的消息。
// worker 会把这些消息的 ID 一并 ACK 掉 —— 不这么做的话，
// 一条脏消息会永远卡在 pending 里，每轮都被重新投递。
var ErrMalformedEvent = errors.New("malformed click event")

// 值类型（domain.ClickRecord / domain.StreamMessage / domain.ReadResult）定义在
// domain 包：它们是「一次点击」的领域表示，而本包只负责线格式与解析。
// 这样 worker 依赖的是端口而不是具体实现，也就能用手写 fake 单测。

// xaddArgs 把一条点击记录编码成 XADD 参数。
//
// 用 MAXLEN ~ streamMaxLen 做近似裁剪：消费持续落后时最多保留 10 万条，防止 OOM。
// LinkID 直接由调用方（跳转时的缓存）带过来，消费端无需回表。
func xaddArgs(rec domain.ClickRecord) goredis.XAddArgs {
	return goredis.XAddArgs{
		Stream: streamKey,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: []any{
			fieldCode, rec.Code,
			fieldLinkID, rec.LinkID.String(),
			fieldOccurred, strconv.FormatInt(rec.OccurredAt.UnixNano(), 10),
			fieldIP, rec.IP,
			fieldUA, rec.UserAgent,
			fieldReferer, rec.Referer,
		},
	}
}

// EnsureGroup 幂等地创建消费组。BUSYGROUP（已存在）视为成功。
func (c *Client) EnsureGroup(ctx context.Context) error {
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := c.rdb.XGroupCreateMkStream(opCtx, streamKey, consumerGroup, "0").Err()
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("store.redis: ensure consumer group: %w", err)
}

// ReadClicks 以消费组身份阻塞读取新消息。
// block 传 0 表示不阻塞（立即返回）。
func (c *Client) ReadClicks(ctx context.Context, consumer string, count int64, block time.Duration) (*domain.ReadResult, error) {
	if count <= 0 {
		count = 500
	}
	// 阻塞读的时间要长于 Redis 侧 block，因此这里不套 op 超时
	streams, err := c.rdb.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: consumer,
		Streams:  []string{streamKey, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err != nil {
		if isNil(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return &domain.ReadResult{}, nil
		}
		return nil, fmt.Errorf("store.redis: xreadgroup: %w", err)
	}
	return parseStreams(streams), nil
}

// Ack 确认消息已落库。
func (c *Client) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	if err := c.rdb.XAck(opCtx, streamKey, consumerGroup, ids...).Err(); err != nil {
		return fmt.Errorf("store.redis: xack %d messages: %w", len(ids), err)
	}
	return nil
}

// AutoClaim 认领空闲超过 minIdle 的 pending 消息，防止消费者崩溃后事件永久滞留。
func (c *Client) AutoClaim(ctx context.Context, consumer string, minIdle time.Duration, count int64) (*domain.ReadResult, error) {
	if count <= 0 {
		count = 200
	}
	msgs, _, err := c.rdb.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream:   streamKey,
		Group:    consumerGroup,
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    count,
	}).Result()
	if err != nil {
		if isNil(err) {
			return &domain.ReadResult{}, nil
		}
		return nil, fmt.Errorf("store.redis: xautoclaim: %w", err)
	}
	return parseMessages(msgs), nil
}

// ReapDeadLetters 找出重投次数已达上限的 pending 消息并直接 ACK 丢弃。
//
// 为什么要有这条路：「写库失败就不 ACK」在**永久性**失败上会翻车 —— 一条语义非法的消息
// （例如 link_id 指向的短链已被硬删，插入撞外键）永远写不进去，于是它每轮都被
// AutoClaim 捞回来重投，形成带错误日志的死循环，还会把同批的正常消息一起卡住。
//
// 重投次数取自 Stream 自己的 PEL（Redis 维护的 delivery count），所以跨进程重启依然成立；
// 放在 worker 内存里计数是行不通的 —— 重启就归零，而毒消息还在那儿。
//
// limit 是**单轮**扫描上限：PEL 可能很大，一次扫完会把 Redis 拖住。
// 扫不完的部分留给下一轮（这是个周期性任务，会收敛）。
func (c *Client) ReapDeadLetters(ctx context.Context, maxRetries int64, limit int64) ([]string, error) {
	if maxRetries <= 0 {
		maxRetries = defaultMaxDeliveryAttempts
	}
	if limit <= 0 {
		limit = 1000
	}

	var dead []string
	start := "-"

	for int64(len(dead)) < limit {
		opCtx, cancel := c.opCtx(ctx)
		entries, err := c.rdb.XPendingExt(opCtx, &goredis.XPendingExtArgs{
			Stream: streamKey,
			Group:  consumerGroup,
			Start:  start,
			End:    "+",
			Count:  limit - int64(len(dead)),
		}).Result()
		cancel()

		if err != nil {
			// PEL 为空时 XPENDING 返回空切片而不是错误，走到这里就是真故障
			return nil, fmt.Errorf("store.redis: xpending ext: %w", err)
		}
		if len(entries) == 0 {
			break
		}

		last := ""
		for _, e := range entries {
			last = e.ID
			if e.RetryCount >= maxRetries {
				dead = append(dead, e.ID)
				if int64(len(dead)) >= limit {
					break
				}
			}
		}

		// XPendingExt 的 start 是闭区间，必须把游标推到 last 之后，否则死循环。
		next, ok := nextStreamID(last)
		if !ok {
			break
		}
		start = next
	}

	if len(dead) == 0 {
		return nil, nil
	}
	// 丢弃 = 直接 ACK（XACK 不看归属，只要 ID 在 PEL 里就能摘掉）
	if err := c.Ack(ctx, dead...); err != nil {
		return nil, err
	}
	return dead, nil
}

// nextStreamID 把 "1712345678901-0" 推进成 "1712345678901-1"，用于给 XPENDING 翻页。
// 解析不出来（消息 ID 形态变了）时返回 false，调用方据此停止扫描而不是死循环。
func nextStreamID(id string) (string, bool) {
	ms, seq, ok := strings.Cut(id, "-")
	if !ok {
		return "", false
	}
	n, err := strconv.ParseInt(seq, 10, 64)
	if err != nil {
		return "", false
	}
	return ms + "-" + strconv.FormatInt(n+1, 10), true
}

// StreamLen 返回 Stream 当前长度，用于 /healthz 暴露积压情况。
func (c *Client) StreamLen(ctx context.Context) (int64, error) {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	n, err := c.rdb.XLen(opCtx, streamKey).Result()
	if err != nil {
		return 0, fmt.Errorf("store.redis: xlen: %w", err)
	}
	return n, nil
}

// PendingCount 返回消费组中未 ACK 的消息数。
func (c *Client) PendingCount(ctx context.Context) (int64, error) {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	info, err := c.rdb.XPending(opCtx, streamKey, consumerGroup).Result()
	if err != nil {
		if isNil(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store.redis: xpending: %w", err)
	}
	return info.Count, nil
}

// parseStreams 把 XReadGroup 的返回值摊平成 domain.ReadResult。
func parseStreams(streams []goredis.XStream) *domain.ReadResult {
	out := &domain.ReadResult{}
	for _, s := range streams {
		merged := parseMessages(s.Messages)
		out.Messages = append(out.Messages, merged.Messages...)
		out.MalformedIDs = append(out.MalformedIDs, merged.MalformedIDs...)
	}
	return out
}

// parseMessages 逐条解析消息；解析失败的只记录 ID，不返回错误。
func parseMessages(msgs []goredis.XMessage) *domain.ReadResult {
	out := &domain.ReadResult{}
	for _, m := range msgs {
		ev, err := parseMessage(m)
		if err != nil {
			out.MalformedIDs = append(out.MalformedIDs, m.ID)
			continue
		}
		out.Messages = append(out.Messages, domain.StreamMessage{ID: m.ID, Event: ev})
	}
	return out
}

// parseMessage 把一条 XMessage 转成 domain.ClickRecord。
func parseMessage(m goredis.XMessage) (domain.ClickRecord, error) {
	var ev domain.ClickRecord

	code, ok := stringField(m.Values, fieldCode)
	if !ok || code == "" {
		return ev, fmt.Errorf("store.redis: message %s: %w: 缺少 %s", m.ID, ErrMalformedEvent, fieldCode)
	}
	ev.Code = code

	linkIDRaw, ok := stringField(m.Values, fieldLinkID)
	if !ok {
		return ev, fmt.Errorf("store.redis: message %s: %w: 缺少 %s", m.ID, ErrMalformedEvent, fieldLinkID)
	}
	linkID, err := uuid.Parse(linkIDRaw)
	if err != nil {
		return ev, fmt.Errorf("store.redis: message %s: %w: link_id=%q 非法", m.ID, ErrMalformedEvent, linkIDRaw)
	}
	ev.LinkID = linkID

	tsRaw, ok := stringField(m.Values, fieldOccurred)
	if !ok {
		return ev, fmt.Errorf("store.redis: message %s: %w: 缺少 %s", m.ID, ErrMalformedEvent, fieldOccurred)
	}
	nanos, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil || nanos <= 0 {
		return ev, fmt.Errorf("store.redis: message %s: %w: ts=%q 非法", m.ID, ErrMalformedEvent, tsRaw)
	}
	ev.OccurredAt = time.Unix(0, nanos).UTC()

	// 可选字段：缺失就是空串
	ev.IP, _ = stringField(m.Values, fieldIP)
	ev.UserAgent, _ = stringField(m.Values, fieldUA)
	ev.Referer, _ = stringField(m.Values, fieldReferer)

	return ev, nil
}

// stringField 从 XMessage.Values 里取字符串字段。
func stringField(values map[string]any, key string) (string, bool) {
	v, ok := values[key]
	if !ok {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	default:
		return "", false
	}
}
