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

// ErrMalformedEvent 表示 Stream 里存在无法解析的消息。
// worker 会把这些消息的 ID 一并 ACK 掉 —— 不这么做的话，
// 一条脏消息会永远卡在 pending 里，每轮都被重新投递。
var ErrMalformedEvent = errors.New("malformed click event")

// StreamEvent 是一条待落库的点击事件。
type StreamEvent struct {
	// Code 是短码。
	Code string
	// LinkID 是短链 UUID，直接来自缓存，避免消费端回表。
	LinkID uuid.UUID
	// OccurredAt 是服务端记录的跳转时刻。
	OccurredAt time.Time
	// IP / UserAgent / Referer 是原始上下文，解析留给 worker。
	IP        string
	UserAgent string
	Referer   string
}

// StreamMessage 是一条已解析的 Stream 消息（附带 Stream ID，用于 XACK）。
type StreamMessage struct {
	// ID 是 Stream 消息 ID，形如 "1712345678901-0"。
	ID string
	// Event 是解析后的事件。
	Event StreamEvent
}

// ReadResult 是一次消费的结果。
// MalformedIDs 里的消息解析失败，但必须一并 ACK，否则会毒化消费组。
type ReadResult struct {
	// Messages 是成功解析的消息。
	Messages []StreamMessage
	// MalformedIDs 是解析失败的消息 ID。
	MalformedIDs []string
}

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
func (c *Client) ReadClicks(ctx context.Context, consumer string, count int64, block time.Duration) (*ReadResult, error) {
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
			return &ReadResult{}, nil
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
func (c *Client) AutoClaim(ctx context.Context, consumer string, minIdle time.Duration, count int64) (*ReadResult, error) {
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
			return &ReadResult{}, nil
		}
		return nil, fmt.Errorf("store.redis: xautoclaim: %w", err)
	}
	return parseMessages(msgs), nil
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

// parseStreams 把 XReadGroup 的返回值摊平成 ReadResult。
func parseStreams(streams []goredis.XStream) *ReadResult {
	out := &ReadResult{}
	for _, s := range streams {
		merged := parseMessages(s.Messages)
		out.Messages = append(out.Messages, merged.Messages...)
		out.MalformedIDs = append(out.MalformedIDs, merged.MalformedIDs...)
	}
	return out
}

// parseMessages 逐条解析消息；解析失败的只记录 ID，不返回错误。
func parseMessages(msgs []goredis.XMessage) *ReadResult {
	out := &ReadResult{}
	for _, m := range msgs {
		ev, err := parseMessage(m)
		if err != nil {
			out.MalformedIDs = append(out.MalformedIDs, m.ID)
			continue
		}
		out.Messages = append(out.Messages, StreamMessage{ID: m.ID, Event: ev})
	}
	return out
}

// parseMessage 把一条 XMessage 转成 StreamEvent。
func parseMessage(m goredis.XMessage) (StreamEvent, error) {
	var ev StreamEvent

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
