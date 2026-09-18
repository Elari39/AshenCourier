package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"ashen-courier/internal/domain"
)

// ClickStore 实现 domain.ClickRepository，
// 与 DB 共用同一个连接池，拆成独立类型是为了避免不同实体的同名方法互相覆盖。
type ClickStore struct {
	db *DB
}

// insertClickEventsSQL 用 NULLIF 把 Go 侧的空串落成 SQL NULL：
// 「没有 Referer」与「Referer 是空串」在统计里都该归为直接访问。
const insertClickEventsSQL = `
INSERT INTO click_events (link_id, short_code, occurred_at, referer, user_agent,
                          ip, country, device, browser, os)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''),
        NULLIF($6, '')::inet, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''))`

// InsertBatch 用 pgx.Batch 一次性提交整批明细。
//
// 该操作本身不是原子的（Batch 在 PG 里是一条条执行的，除非包在显式事务里），
// 但整体包在隐式事务中由 pgx 处理：任一语句失败即返回错误，worker 会重投整批。
// at-least-once 语义下允许重复插入，计数不依赖明细条数，因此可接受。
func (s *ClickStore) InsertBatch(ctx context.Context, events []domain.ClickEvent) error {
	if len(events) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(insertClickEventsSQL,
			toPgUUID(e.LinkID), e.ShortCode, e.OccurredAt,
			e.Referer, e.UserAgent, e.IP, e.Country,
			e.Device, e.Browser, e.OS,
		)
	}

	br := s.db.pool.SendBatch(ctx, batch)
	// 必须把每条结果都消费掉，否则连接无法释放
	for range len(events) {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("store.postgres: insert click events: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("store.postgres: close click batch: %w", err)
	}
	return nil
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
		Daily:    []domain.DailyCount{},
		Referers: []domain.BucketCount{},
		Devices:  []domain.BucketCount{},
		Browsers: []domain.BucketCount{},
	}

	// 1) 窗口内明细总数
	const totalSQL = `SELECT count(*) FROM click_events WHERE link_id = $1 AND occurred_at >= $2`
	if err := s.db.pool.QueryRow(ctx, totalSQL, args...).Scan(&out.WindowClicks); err != nil {
		return nil, fmt.Errorf("store.postgres: stats total: %w", err)
	}

	// 2) 按天趋势
	const dailySQL = `
SELECT date_trunc('day', occurred_at)::date AS day, count(*) AS clicks
FROM click_events
WHERE link_id = $1 AND occurred_at >= $2
GROUP BY day
ORDER BY day`
	dailyRows, err := s.db.pool.Query(ctx, dailySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: stats daily: %w", err)
	}
	if out.Daily, err = collectDaily(dailyRows); err != nil {
		return nil, err
	}

	// 3) 来源分布
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

	// 4) 设备分布
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

	// 5) 浏览器分布
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
		return nil, fmt.Errorf("store.postgres: %s: %w", op, err)
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
			return nil, fmt.Errorf("store.postgres: stats daily: scan: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.postgres: stats daily: iterate: %w", err)
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
			return nil, fmt.Errorf("store.postgres: %s: scan: %w", op, err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.postgres: %s: iterate: %w", op, err)
	}
	return out, nil
}
