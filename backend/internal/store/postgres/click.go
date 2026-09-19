package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ashen-courier/internal/domain"
)

// ClickStore 实现 domain.ClickRepository，
// 与 DB 共用同一个连接池，拆成独立类型是为了避免不同实体的同名方法互相覆盖。
type ClickStore struct {
	db *DB
}

// insertClickEventsSQL 用 NULLIF 把 Go 侧的空串落成 SQL NULL：
// 「没有 Referer」与「Referer 是空串」在统计里都该归为直接访问。
//
// ON CONFLICT 的冲突目标必须复述 partial 唯一索引的谓词
// （click_events_event_uid_key ... WHERE event_uid IS NOT NULL）——
// 少写 WHERE event_uid IS NOT NULL，PG 会直接报
// 「no unique or exclusion constraint matching the ON CONFLICT specification」。
//
// event_uid 为空串时 NULLIF 会把它变成 NULL：这类行（只可能来自手写 SQL 或旧版本
// worker）不参与去重，照常插入，而不是被当成「同一批」互相顶掉。
const insertClickEventsSQL = `
INSERT INTO click_events (link_id, short_code, occurred_at, referer, user_agent,
                          ip, country, device, browser, os, event_uid)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''),
        NULLIF($6, '')::inet, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''),
        NULLIF($11, ''))
ON CONFLICT (event_uid) WHERE event_uid IS NOT NULL DO NOTHING`

// InsertBatch 用 pgx.Batch 一次性提交整批明细。
//
// ⚠️ 注意：这不是事务。pgx 的 Batch 靠一次往返把多条语句发出去，但在没有显式
// 事务包裹时每条仍各自自动提交 —— 第 N 条失败时前 N-1 条已经落库，且后续语句
// 会被跳过。整体设计已声明接受 at-least-once：worker 在本批失败时不 ACK，
// 消息会重投；重投时被 event_uid 唯一索引挡掉的重复行由 ON CONFLICT DO NOTHING
// 静默跳过（RowsAffected=0，不是错误）。计数走 INCR 累加而不依赖明细条数。
// 若将来需要「整批原子」，得显式开 pgx.Tx（代价是牺牲批量吞吐）。
//
// 返回值保持 error：DO NOTHING 无法区分「插入」与「跳过」，
// 要统计去重次数得改用 RETURNING + Query 或另加计数。
func (s *ClickStore) InsertBatch(ctx context.Context, events []domain.ClickEvent) error {
	if len(events) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(insertClickEventsSQL,
			toPgUUID(e.LinkID), e.ShortCode, e.OccurredAt,
			e.Referer, e.UserAgent, e.IP, e.Country,
			e.Device, e.Browser, e.OS, e.EventUID,
		)
	}

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	br := s.db.pool.SendBatch(opCtx, batch)
	// 必须把每条结果都消费掉，否则连接无法释放
	for range len(events) {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("store.postgres: insert click events: %w", storageError(err))
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("store.postgres: close click batch: %w", storageError(err))
	}
	return nil
}

// clickEventColumns 是明细列表的列顺序，必须与 clickEventRow 的字段一一对应。
//
// ip 用 host() 取：inet 的文本形式可能带上掩码长度（"203.0.113.7/32"），
// host() 只出地址本身。其余可空列统一 coalesce 成空串，
// 让扫描目标固定是 string / int64，不需要一排 *string 再逐个人工解引用。
const clickEventColumns = `id, coalesce(event_uid, ''), link_id, short_code, occurred_at,
        coalesce(referer, ''), coalesce(user_agent, ''), coalesce(host(ip), ''),
        coalesce(country, ''), coalesce(device, ''), coalesce(browser, ''), coalesce(os, '')`

// clickEventRow 是 click_events 表的一行（明细列表用）。
type clickEventRow struct {
	id         int64
	eventUID   string
	linkID     pgtype.UUID
	shortCode  string
	occurredAt time.Time
	referer    string
	userAgent  string
	ip         string
	country    string
	device     string
	browser    string
	os         string
}

// dest 返回交给 rows.Scan 的扫描目标，顺序与 clickEventColumns 完全一致。
func (r *clickEventRow) dest() []any {
	return []any{
		&r.id, &r.eventUID, &r.linkID, &r.shortCode, &r.occurredAt,
		&r.referer, &r.userAgent, &r.ip, &r.country, &r.device, &r.browser, &r.os,
	}
}

// toDomain 把行数据转成领域实体。
func (r *clickEventRow) toDomain() domain.ClickEvent {
	return domain.ClickEvent{
		ID:         r.id,
		EventUID:   r.eventUID,
		LinkID:     fromPgUUID(r.linkID),
		ShortCode:  r.shortCode,
		OccurredAt: r.occurredAt,
		Referer:    r.referer,
		UserAgent:  r.userAgent,
		IP:         r.ip,
		Country:    r.country,
		Device:     r.device,
		Browser:    r.browser,
		OS:         r.os,
	}
}

// ListByLink 用 keyset 分页列出某短链的点击明细（时间倒序）。
//
// 为什么不用 OFFSET：明细表在持续追加，翻页期间新插入的行会把 OFFSET 的窗口
// 整体推移 —— 同一行可能被读两次，也可能整行被跳过。keyset 以「上一页最后一行的
// 位置」为锚点，天生免疫这个问题，代价是只能顺序翻页（明细页正好只需要顺序翻）。
//
// 游标比较写成行值比较 (occurred_at, id) < ($2, $3)：它同时覆盖
// 「时间更早」与「时间相同但 id 更小」两种情况，与 ORDER BY 的排序键严格对应；
// 展开成 occurred_at < $2 OR (occurred_at = $2 AND id < $3) 只是同一件事的啰嗦写法。
func (s *ClickStore) ListByLink(ctx context.Context, q domain.ClickListQuery) ([]domain.ClickEvent, domain.ClickCursor, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}

	args := []any{toPgUUID(q.LinkID)}
	where := []string{"link_id = $1"}

	if !q.Since.IsZero() {
		args = append(args, q.Since)
		where = append(where, "occurred_at >= $"+strconv.Itoa(len(args)))
	}
	if device := strings.TrimSpace(q.Device); device != "" {
		// 与统计的分布口径保持一致：device 为空串 / NULL 归入 unknown 桶，
		// 否则「设备分布里点 unknown」与「按 unknown 筛选」会得到不同的条数。
		args = append(args, device)
		where = append(where, "coalesce(nullif(device, ''), 'unknown') = $"+strconv.Itoa(len(args)))
	}
	if q.Cursor.Valid {
		args = append(args, q.Cursor.OccurredAt, q.Cursor.ID)
		where = append(where, "(occurred_at, id) < ($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+")")
	}

	// 多取 1 条用于探测下一页
	args = append(args, q.Limit+1)
	sql := `SELECT ` + clickEventColumns + ` FROM click_events WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY occurred_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	rows, err := s.db.pool.Query(opCtx, sql, args...)
	if err != nil {
		return nil, domain.ClickCursor{}, fmt.Errorf("store.postgres: list click events: %w", storageError(err))
	}
	defer rows.Close()

	events := make([]domain.ClickEvent, 0, q.Limit)
	for rows.Next() {
		var r clickEventRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, domain.ClickCursor{}, fmt.Errorf("store.postgres: scan click event: %w", storageError(err))
		}
		events = append(events, r.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, domain.ClickCursor{}, fmt.Errorf("store.postgres: iterate click events: %w", storageError(err))
	}

	var next domain.ClickCursor
	if len(events) > q.Limit {
		events = events[:q.Limit]
		last := events[len(events)-1]
		next = domain.ClickCursor{OccurredAt: last.OccurredAt, ID: last.ID, Valid: true}
	}
	return events, next, nil
}

// SQL 片段：来源主机名从原始 Referer 里现算，避免额外的规范化列。
//  1. 去掉 scheme（^[a-z]+://）
//  2. 取第一个 '/' 之前的部分
//  3. 去掉 userinfo 与端口
const refererHostExpr = `regexp_replace(
        regexp_replace(
            split_part(regexp_replace(coalesce(referer, ''), '^[a-zA-Z][a-zA-Z0-9+.-]*://', ''), '/', 1),
            '^.*@', ''),
        ':[0-9]+$', '')`

// Aggregate 执行窗口内的多维聚合。五个查询串行执行，总耗时在几十毫秒量级。
func (s *ClickStore) Aggregate(ctx context.Context, q domain.StatsQuery) (*domain.StatsAggregate, error) {
	if q.TopN <= 0 {
		q.TopN = 10
	}
	args := []any{toPgUUID(q.LinkID), q.Since}

	out := &domain.StatsAggregate{
		Daily:     []domain.DailyCount{},
		Referers:  []domain.BucketCount{},
		Devices:   []domain.BucketCount{},
		Browsers:  []domain.BucketCount{},
		Countries: []domain.BucketCount{},
	}

	// 1) 按天趋势
	//
	// occurred_at 是 timestamptz，date_trunc('day', ...) 会先按**会话时区**转换再截断。
	// 而 Go 侧 service.fillDaily 是按 UTC 日历日补齐和比对的，两者只在会话时区为
	// UTC 时一致 —— 官方 PG 镜像默认就是 UTC 所以看不出来，一旦给容器设了 TZ
	// （或换成时区跟随实例配置的托管库），跨日边界的点击会被归到错的那一天。
	// 因此显式固定 UTC 日历日。
	const dailySQL = `
SELECT date_trunc('day', occurred_at AT TIME ZONE 'UTC')::date AS day, count(*) AS clicks
FROM click_events
WHERE link_id = $1 AND occurred_at >= $2
GROUP BY day
ORDER BY day`
	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	dailyRows, err := s.db.pool.Query(opCtx, dailySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: stats daily: %w", storageError(err))
	}
	if out.Daily, err = collectDaily(dailyRows); err != nil {
		return nil, err
	}

	// 2) 来源分布
	refererSQL := `
SELECT ` + refererHostExpr + ` AS host, count(*) AS clicks
FROM click_events
WHERE link_id = $1 AND occurred_at >= $2
GROUP BY host
ORDER BY clicks DESC, host
LIMIT $3`
	if out.Referers, err = s.queryBuckets(ctx, refererSQL, args, q.TopN, "stats referers"); err != nil {
		return nil, err
	}

	// 3) 设备分布
	const deviceSQL = `
SELECT coalesce(nullif(device, ''), 'unknown') AS bucket, count(*) AS clicks
FROM click_events
WHERE link_id = $1 AND occurred_at >= $2
GROUP BY bucket
ORDER BY clicks DESC, bucket
LIMIT $3`
	if out.Devices, err = s.queryBuckets(ctx, deviceSQL, args, q.TopN, "stats devices"); err != nil {
		return nil, err
	}

	// 4) 浏览器分布
	const browserSQL = `
SELECT coalesce(nullif(browser, ''), 'unknown') AS bucket, count(*) AS clicks
FROM click_events
WHERE link_id = $1 AND occurred_at >= $2
GROUP BY bucket
ORDER BY clicks DESC, bucket
LIMIT $3`
	if out.Browsers, err = s.queryBuckets(ctx, browserSQL, args, q.TopN, "stats browsers"); err != nil {
		return nil, err
	}

	// 5) 国家分布（M5-2）
	//
	// 这里刻意**不**用其他维度那套 `coalesce(nullif(x, ''), 'unknown')`：
	// device / browser 的 unknown 含义是「解析了但没认出来」，而 country 的空值里
	// 混着「这个部署根本没配 GeoIP 库」这一大类 —— 把它聚成一个 100% 的「未知」条，
	// 用户会以为是解析失败，而不是「我没开这个功能」。
	// 所以只返回**已知国家**：没有已知国家的点击不进这一维，列表为空时前端整块隐藏。
	const countrySQL = `
SELECT country AS bucket, count(*) AS clicks
FROM click_events
WHERE link_id = $1 AND occurred_at >= $2 AND coalesce(country, '') <> ''
GROUP BY bucket
ORDER BY clicks DESC, bucket
LIMIT $3`
	if out.Countries, err = s.queryBuckets(ctx, countrySQL, args, q.TopN, "stats countries"); err != nil {
		return nil, err
	}

	return out, nil
}

// queryBuckets 执行一个「文本分组 + 计数」查询。
// args 复用前两个参数（link_id, since），额外拼接 topN。
func (s *ClickStore) queryBuckets(ctx context.Context, sql string, baseArgs []any, topN int, op string) ([]domain.BucketCount, error) {
	args := make([]any, 0, len(baseArgs)+1)
	args = append(args, baseArgs...)
	args = append(args, topN)

	rows, err := s.db.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: %s: %w", op, storageError(err))
	}
	return collectBuckets(rows, op)
}

// collectDaily 读取「日期 + 计数」的行。
func collectDaily(rows pgx.Rows) ([]domain.DailyCount, error) {
	defer rows.Close()

	out := make([]domain.DailyCount, 0, 32)
	for rows.Next() {
		var d domain.DailyCount
		if err := rows.Scan(&d.Date, &d.Clicks); err != nil {
			return nil, fmt.Errorf("store.postgres: stats daily: scan: %w", storageError(err))
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.postgres: stats daily: iterate: %w", storageError(err))
	}
	return out, nil
}

// collectBuckets 读取「文本分组 + 计数」的行。
func collectBuckets(rows pgx.Rows, op string) ([]domain.BucketCount, error) {
	defer rows.Close()

	out := make([]domain.BucketCount, 0, 16)
	for rows.Next() {
		var b domain.BucketCount
		if err := rows.Scan(&b.Name, &b.Clicks); err != nil {
			return nil, fmt.Errorf("store.postgres: %s: scan: %w", op, storageError(err))
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.postgres: %s: iterate: %w", op, storageError(err))
	}
	return out, nil
}
