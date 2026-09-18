package service

import (
	"context"
	"log/slog"
	"time"

	"ashen-courier/internal/domain"
)

// 统计窗口相关常量。
const (
	// DefaultStatsDays 是统计接口默认窗口天数。
	DefaultStatsDays = 30
	// MaxStatsDays 是窗口天数上限，避免一次拉一年明细把数据库拖垮。
	MaxStatsDays = 365
	// StatsTopN 是每个分布维度返回的条目数。
	StatsTopN = 10
	// dateLayout 是趋势图横轴的日期格式。
	dateLayout = "2006-01-02"
)

// Stats 负责点击统计的聚合。
type Stats struct {
	links  domain.LinkRepository
	clicks domain.ClickRepository
	delta  domain.ClickDeltaReader
}

// NewStats 构造统计服务。
func NewStats(links domain.LinkRepository, clicks domain.ClickRepository, delta domain.ClickDeltaReader) *Stats {
	return &Stats{links: links, clicks: clicks, delta: delta}
}

// StatsResult 是一次统计查询的结果，可直接映射成 API 响应。
type StatsResult struct {
	// TotalClicks = PG 基线 + Redis 待同步增量（全量、跨窗口）。
	TotalClicks int64
	// Days 是实际使用的窗口天数。
	Days int
	// Since 是窗口起点（UTC 当天 0 点往前推 Days-1 天）。
	Since time.Time
	// Daily 是补齐后的连续日趋势，长度恒等于 Days。
	Daily []domain.DailyCount
	// Referers / Devices / Browsers 是三个维度的 Top-N 分布。
	Referers []domain.BucketCount
	Devices  []domain.BucketCount
	Browsers []domain.BucketCount
}

// ForLink 聚合某条短链在最近 days 天内的统计。
func (s *Stats) ForLink(ctx context.Context, link *domain.Link, days int) (*StatsResult, error) {
	if days <= 0 {
		days = DefaultStatsDays
	}
	days = min(days, MaxStatsDays)

	// 窗口从「今天 0 点」往前推 days-1 天，保证今天也算作一天
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1))

	agg, err := s.clicks.Aggregate(ctx, domain.StatsQuery{
		LinkID: link.ID,
		Since:  since,
		TopN:   StatsTopN,
	})
	if err != nil {
		return nil, err
	}

	result := &StatsResult{
		TotalClicks: link.ClickCount,
		Days:        days,
		Since:       since,
		Daily:       fillDaily(agg.Daily, since, days),
		Referers:    agg.Referers,
		Devices:     agg.Devices,
		Browsers:    agg.Browsers,
	}

	// 基线之外再叠加尚未落库的增量；读不到就退化成「只报基线」，
	// 数字略滞后好过整个接口 5xx。
	delta, err := s.delta.PendingDelta(ctx, link.ShortCode)
	if err != nil {
		slog.Warn("读取待同步点击增量失败，总点击将只反映 PG 基线",
			"code", link.ShortCode, "err", err)
	} else {
		result.TotalClicks += delta
	}

	return result, nil
}

// fillDaily 把聚合结果补齐成连续 days 天，缺失日期补 0 ——
// 折线图需要等距横轴，不能只画有数据的那些天。
func fillDaily(rows []domain.DailyCount, since time.Time, days int) []domain.DailyCount {
	byDay := make(map[string]int64, len(rows))
	for _, r := range rows {
		byDay[r.Date.UTC().Format(dateLayout)] = r.Clicks
	}

	out := make([]domain.DailyCount, 0, days)
	for i := range days {
		day := since.AddDate(0, 0, i)
		out = append(out, domain.DailyCount{
			Date:   day,
			Clicks: byDay[day.Format(dateLayout)],
		})
	}
	return out
}
