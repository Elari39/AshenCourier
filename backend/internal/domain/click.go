package domain

import (
	"context"
	"time"
	"uuid"
)

// ClickEvent 是一次跳转的明细事件。
//
// 写入路径完全异步：跳转时 XADD 进 Redis Stream，由 worker 批量落库。
// 投递语义是 at-least-once，因此同一 (link_id, occurred_at, ip) 可能重复；
// EventUID 是这条重复的兜底 —— 落库时按它做幂等去重。
type ClickEvent struct {
	// ID 由数据库 identity 生成（顺序写，避免索引页分裂），仅读取时填充。
	ID int64
	// EventUID 由 Stream 消息 ID 派生（形如 "1712345678901-0"），用于幂等：
	// 重投的同一批消息带着同一个 ID，唯一索引据此拒掉重复行。
	// 历史行（000002 迁移之前）为 NULL，写入侧永远非空。
	EventUID string
	// LinkID 是所属短链。
	LinkID uuid.UUID
	// ShortCode 冗余存一份，便于排查时不必回表。
	ShortCode string
	// OccurredAt 是跳转发生的时刻（服务端时间）。
	OccurredAt time.Time
	// Referer 是原始 Referer 头，可能为空。
	Referer string
	// UserAgent 是原始 UA，可能为空。
	UserAgent string
	// IP 是客户端 IP，非法或缺失时为空串，落库为 NULL。
	IP string
	// Country 是 GeoIP 结果，MVP 恒为空。
	Country string
	// Device / Browser / OS 由 internal/pkg/ua 解析得出。
	Device  string
	Browser string
	OS      string
}

// DailyCount 是某一天的点击数。
type DailyCount struct {
	// Date 是 UTC 日期（只取到日）。
	Date time.Time
	// Clicks 是该日点击数。
	Clicks int64
}

// BucketCount 是「某一维度某一取值」的计数，用于来源/设备/浏览器分布。
type BucketCount struct {
	// Name 是该维度的取值；来源维度为空串时表示直接访问。
	Name string
	// Clicks 是该取值下的点击数。
	Clicks int64
}

// StatsAggregate 是一次统计查询的全部结果。
//
// 注意：Daily / Referers 等是「窗口内的明细聚合」，不是界面上的「总点击」——
// 总点击由 service 用 links.click_count（PG 基线）+ Redis 待同步增量算出
// （全量、跨窗口）。响应里的 window_clicks 由 handler 对 Daily 求和得到，
// 因此这里不再单独跑一次 count(*)。
type StatsAggregate struct {
	// Daily 是按天趋势，按日期升序。
	Daily []DailyCount
	// Referers 是来源分布，按点击数降序。
	Referers []BucketCount
	// Devices 是设备分布，按点击数降序。
	Devices []BucketCount
	// Browsers 是浏览器分布，按点击数降序。
	Browsers []BucketCount
}

// ClickCursor 是点击明细的 keyset 游标：按 (occurred_at, id) 倒序翻页。
//
// 为什么必须带 ID：occurred_at 是毫秒精度，同一毫秒内的多条点击在时间上完全并列，
// 只比较时间的游标会在翻页边界上漏行或重复。ID 是 identity 主键（单调递增），
// 在时间相同时提供稳定的次序。
type ClickCursor struct {
	// OccurredAt 是上一页最后一条的发生时刻。
	OccurredAt time.Time
	// ID 是上一页最后一条的主键。
	ID int64
	// Valid 为 false 表示没有下一页（末页）。
	Valid bool
}

// ClickListQuery 描述一次点击明细的分页查询条件。
type ClickListQuery struct {
	// LinkID 是目标短链。
	LinkID uuid.UUID
	// Since 是窗口起点（含）；零值表示不限时间。
	Since time.Time
	// Device 是设备筛选（与 internal/pkg/ua 的输出一致）；空串表示不限。
	Device string
	// Limit 是本次最多返回多少条。
	Limit int
	// Cursor 是上一页末尾的位置（Valid=false 表示第一页）。
	Cursor ClickCursor
}

// StatsQuery 描述一次统计聚合的查询条件。
type StatsQuery struct {
	// LinkID 是目标短链。
	LinkID uuid.UUID
	// Since 是统计窗口起点（含）。
	Since time.Time
	// TopN 是每个分布维度最多返回多少条。
	TopN int
}

// StreamMessage 是一条已解析的 Stream 消息（附带 Stream ID，用于 XACK）。
type StreamMessage struct {
	// ID 是 Stream 消息 ID，形如 "1712345678901-0"。
	ID string
	// Event 是解析后的点击。
	Event ClickRecord
}

// ReadResult 是一次消费的结果。
type ReadResult struct {
	// Messages 是成功解析的消息。
	Messages []StreamMessage
	// MalformedIDs 是解析失败的消息 ID。它们必须一并 ACK，否则一条脏消息会永远
	// 卡在 pending 里，每轮都被重新投递。
	MalformedIDs []string
}

// ClickCounter 是计数回刷所需的原子操作，实现在 internal/store/redis。
//
// 这组操作是「计数不丢」的全部关键路径，语义在 M3-1 调整为**补偿式**：
//
//	TakeDelta 只读不删（键里的增量一直留着）→ 写库 → SettleDelta 结算（减掉 + 摘 dirty）
//
// 这样进程崩在「写库之后、结算之前」只会让基线**重复累加一批**，而不是把增量丢掉；
// 崩在写库之前则原样重做。抽成端口是为了让 worker 能用手写 fake 单测这些不变量。
type ClickCounter interface {
	// DirtyCodes 返回最多 limit 个待回刷的短码。
	DirtyCodes(ctx context.Context, limit int) ([]string, error)
	// TakeDelta 读取某短码当前的计数增量（**不删键**，也不摘 dirty）；无增量返回 0。
	TakeDelta(ctx context.Context, code string) (int64, error)
	// SettleDelta 在增量成功落库后结算：计数键减去这批增量并摘掉 dirty 标记（原子）。
	SettleDelta(ctx context.Context, code string, delta int64) error
	// MarkDirty 把短码重新登记回 dirty 集合。
	MarkDirty(ctx context.Context, codes ...string) error
}

// ClickStream 是点击事件流的消费端口，实现在 internal/store/redis。
type ClickStream interface {
	// EnsureGroup 幂等地创建消费组。
	EnsureGroup(ctx context.Context) error
	// ReadClicks 以消费组身份读取新消息；block 传 0 表示不阻塞。
	ReadClicks(ctx context.Context, consumer string, count int64, block time.Duration) (*ReadResult, error)
	// AutoClaim 认领空闲超过 minIdle 的 pending 消息，防止消费者崩溃后事件永久滞留。
	AutoClaim(ctx context.Context, consumer string, minIdle time.Duration, count int64) (*ReadResult, error)
	// Ack 确认消息已落库。
	Ack(ctx context.Context, ids ...string) error
}

// ClickDeltaReader 读取尚未回刷进 PG 的计数增量，实现在 internal/store/redis。
//
// 界面上的「总点击」= links.click_count（PG 基线）+ 这个待同步增量，
// 这样 worker 还没刷进来时数字也不会看起来「卡住」。
type ClickDeltaReader interface {
	// PendingDelta 返回某短码尚未回刷的增量；键不存在返回 0。
	PendingDelta(ctx context.Context, code string) (int64, error)
}

// ClickDeltaBatchReader 一次读取多个短码的待同步增量，实现在 internal/store/redis。
//
// 与 ClickDeltaReader 并存而不是把它替换掉：详情页只需要一个短码（单键 GET 最省），
// 列表页有 N 条（一页最多 MaxLinkPageSize 条），逐条 GET 会变成 N 次往返 ——
// 这里用一次 MGET 拿完，避免把列表接口的延迟拖成 N 倍。
type ClickDeltaBatchReader interface {
	// PendingDeltas 返回这批短码尚未回刷的增量，只包含**有增量**的短码；
	// 键不存在的短码不出现在结果里（调用方按 0 处理即可，与 map 零值语义一致）。
	PendingDeltas(ctx context.Context, codes []string) (map[string]int64, error)
}

// ClickRepository 是点击明细仓储接口，实现在 internal/store/postgres。
type ClickRepository interface {
	// InsertBatch 在一个事务里批量插入明细；events 为空时直接返回 nil。
	InsertBatch(ctx context.Context, events []ClickEvent) error
	// Aggregate 执行窗口内的多维聚合。
	Aggregate(ctx context.Context, q StatsQuery) (*StatsAggregate, error)
	// ListByLink 按 (occurred_at, id) 倒序分页列出某短链的点击明细，
	// 并返回下一页游标（末页 Valid=false）。
	ListByLink(ctx context.Context, q ClickListQuery) ([]ClickEvent, ClickCursor, error)
}
