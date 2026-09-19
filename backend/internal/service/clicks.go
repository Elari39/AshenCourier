package service

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/pkg/ua"
)

// 点击明细分页相关常量。
const (
	// DefaultClickPageSize 是明细接口的默认每页条数。
	DefaultClickPageSize = 20
	// MaxClickPageSize 是每页条数上限。明细行带 UA 与来源，一页几百条就够看了；
	// 再大只是把「翻页」变成「一次性拉库」，对数据库和前端渲染都不划算。
	MaxClickPageSize = 100
	// clickCursorLayout 是游标里时间部分的格式。用带纳秒的 RFC3339Nano：
	// 秒级精度会把同一秒内的多行压成同一个游标值。
	clickCursorLayout = time.RFC3339Nano
)

// clickDevices 是明细接口允许的设备筛选值。
//
// 直接复用 internal/pkg/ua 的常量而不是另抄一份字面量：筛选值必须与解析器
// 写入 click_events.device 的取值完全一致，抄一份就多一处会漂移的地方。
var clickDevices = map[string]struct{}{
	ua.DeviceDesktop: {},
	ua.DeviceMobile:  {},
	ua.DeviceTablet:  {},
	ua.DeviceBot:     {},
	ua.DeviceUnknown: {},
}

// ClickListInput 是一次点击明细查询的入参。
type ClickListInput struct {
	// Limit 是每页条数；<=0 用 DefaultClickPageSize。
	Limit int
	// MaxLimit 是页大小上限（来自配置）；<=0 表示不额外收紧。
	MaxLimit int
	// Cursor 是上一页返回的游标；空串表示第一页。
	Cursor string
	// Days 是时间窗口天数；<=0 用 DefaultStatsDays。
	Days int
	// Device 是设备筛选；空串表示全部。
	Device string
}

// ClickListResult 是一次点击明细查询的结果。
type ClickListResult struct {
	// Events 是时间倒序的明细，长度不超过实际生效的 limit。
	Events []domain.ClickEvent
	// NextCursor 是下一页游标；空串表示已经到底。
	NextCursor string
	// Days / Since 是本页实际生效的窗口，回给前端显示口径。
	Days  int
	Since time.Time
}

// ListClicks 分页读取某条短链的点击明细。
//
// 窗口口径与 ForLink 完全一致（今天 0 点往前推 days-1 天）：明细列表和
// 「窗口内点击」是同一块界面上并排的两个数字，起点不同就会看起来互相矛盾。
func (s *Stats) ListClicks(ctx context.Context, link *domain.Link, in ClickListInput) (*ClickListResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultClickPageSize
	}
	limit = min(limit, MaxClickPageSize)
	if in.MaxLimit > 0 {
		limit = min(limit, in.MaxLimit)
	}

	days := in.Days
	if days <= 0 {
		days = DefaultStatsDays
	}
	days = min(days, MaxStatsDays)

	cursor, err := decodeClickCursor(in.Cursor)
	if err != nil {
		return nil, err
	}
	device, err := normalizeDevice(in.Device)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1))

	events, next, err := s.clicks.ListByLink(ctx, domain.ClickListQuery{
		LinkID: link.ID,
		Since:  since,
		Device: device,
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		return nil, err
	}

	return &ClickListResult{
		Events:     events,
		NextCursor: encodeClickCursor(next),
		Days:       days,
		Since:      since,
	}, nil
}

// normalizeDevice 校验设备筛选值；空串表示不筛。
func normalizeDevice(raw string) (string, error) {
	device := strings.ToLower(strings.TrimSpace(raw))
	if device == "" {
		return "", nil
	}
	if _, ok := clickDevices[device]; !ok {
		return "", domain.Invalid("device", "device 只能是 desktop、mobile、tablet、bot 或 unknown")
	}
	return device, nil
}

// encodeClickCursor 把 (occurred_at, id) 编码成对前端不透明的游标。
//
// 与链接列表的游标同源（shortener.encodeCursor）：base64url + `|` 分隔，
// 只是第二部分从 uuid 换成明细的自增主键。不透明是有意的 ——
// 前端不该依赖游标的内部结构，否则后端就没法在不破坏兼容的前提下换分页键。
func encodeClickCursor(c domain.ClickCursor) string {
	if !c.Valid {
		return ""
	}
	raw := c.OccurredAt.UTC().Format(clickCursorLayout) + "|" + strconv.FormatInt(c.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeClickCursor 解析游标；空串表示第一页。
// 任何解析失败都回 422（domain.Invalid）：游标是后端自己发的，
// 对不上只可能是被手改过或跨版本乱用，静默当成第一页会让前端无限翻同一页。
func decodeClickCursor(raw string) (domain.ClickCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.ClickCursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return domain.ClickCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	tsPart, idPart, found := strings.Cut(string(decoded), "|")
	if !found {
		return domain.ClickCursor{}, domain.Invalid("cursor", "分页游标非法")
	}

	occurredAt, err := time.Parse(clickCursorLayout, tsPart)
	if err != nil {
		return domain.ClickCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return domain.ClickCursor{}, domain.Invalid("cursor", "分页游标非法")
	}
	return domain.ClickCursor{OccurredAt: occurredAt, ID: id, Valid: true}, nil
}
