package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
)

// clickRepoFake 记录明细查询条件并返回预置结果。
//
// 内嵌 nil 接口是有意的：Stats 在明细路径上只该调 ListByLink，
// 万一将来顺手用到 Aggregate（统计聚合），这里会立刻 panic 而不是返回零值。
type clickRepoFake struct {
	domain.ClickRepository
	got    []domain.ClickListQuery
	events []domain.ClickEvent
	next   domain.ClickCursor
	err    error
}

func (r *clickRepoFake) ListByLink(_ context.Context, q domain.ClickListQuery) ([]domain.ClickEvent, domain.ClickCursor, error) {
	r.got = append(r.got, q)
	if r.err != nil {
		return nil, domain.ClickCursor{}, r.err
	}
	return r.events, r.next, nil
}

// newClickStats 造一个只依赖明细仓储的 Stats：明细路径不碰 links 与增量，
// 传 nil 同时也是断言 —— 真被用到会 panic。
func newClickStats(clicks domain.ClickRepository) *Stats {
	return NewStats(nil, clicks, nil)
}

// utcToday 返回「今天」的 UTC 零点。窗口末端应该正好落在这里：
// 窗口从今天零点往前推 days-1 天，今天是最后一天（而不是被排除在外）。
func utcToday() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// TestClickCursorDecode 守住「游标坏了必须 422」这条契约。
//
// 静默退化成第一页是更糟的选择：前端会拿着同一个坏游标无限请求第一页，
// 表现为「加载更多」按钮永远转圈而列表不增长。
func TestClickCursorDecode(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
	encode := func(raw string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(raw))
	}

	tests := []struct {
		name        string
		raw         string
		want        domain.ClickCursor
		wantInvalid bool
	}{
		{name: "空串 = 第一页", raw: "", want: domain.ClickCursor{}},
		{name: "纯空白 = 第一页", raw: "   ", want: domain.ClickCursor{}},
		{
			name: "合法游标",
			raw:  encode(ts.Format(time.RFC3339Nano) + "|42"),
			want: domain.ClickCursor{OccurredAt: ts, ID: 42, Valid: true},
		},
		{name: "不是 base64", raw: "!!!not-base64!!!", wantInvalid: true},
		{name: "缺分隔符", raw: encode("2026-03-04T05:06:07Z"), wantInvalid: true},
		{name: "时间格式不对", raw: encode("03/04/2026|42"), wantInvalid: true},
		{name: "id 不是数字", raw: encode(ts.Format(time.RFC3339Nano) + "|abc"), wantInvalid: true},
		{name: "id 为 0", raw: encode(ts.Format(time.RFC3339Nano) + "|0"), wantInvalid: true},
		{name: "id 为负", raw: encode(ts.Format(time.RFC3339Nano) + "|-7"), wantInvalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeClickCursor(tt.raw)
			if tt.wantInvalid {
				invalid, ok := errors.AsType[*domain.InvalidInputError](err)
				if !ok {
					t.Fatalf("decodeClickCursor(%q) 的错误 = %v（%T），期望 *domain.InvalidInputError", tt.raw, err, err)
				}
				if invalid.Field != "cursor" {
					t.Fatalf("错误字段 = %q，期望 cursor", invalid.Field)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeClickCursor(%q) 不该报错：%v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("decodeClickCursor(%q) = %+v，期望 %+v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestClickCursorRoundTrip 守住编码端的两个细节：纳秒必须留下、时间必须归一化成 UTC。
func TestClickCursorRoundTrip(t *testing.T) {
	t.Parallel()

	// 同一秒内的两条点击只差纳秒：秒级精度的游标会让它们指向同一个位置，
	// 翻页就原地打转（第二页永远从第一页的末尾开始）。
	ts := time.Date(2026, 3, 4, 5, 6, 7, 987654321, time.UTC)
	raw := encodeClickCursor(domain.ClickCursor{OccurredAt: ts, ID: 7, Valid: true})
	if raw == "" {
		t.Fatal("有效游标不该编码成空串")
	}

	got, err := decodeClickCursor(raw)
	if err != nil {
		t.Fatalf("解码自己编码的游标失败：%v", err)
	}
	if !got.OccurredAt.Equal(ts) || got.ID != 7 || !got.Valid {
		t.Fatalf("往返后 = %+v，期望 %+v", got, domain.ClickCursor{OccurredAt: ts, ID: 7, Valid: true})
	}

	// 非 UTC 输入也要落在同一时刻：游标里的字符串必须是带时区的绝对时间
	zone := time.FixedZone("UTC+8", 8*3600)
	shanghai := encodeClickCursor(domain.ClickCursor{OccurredAt: ts.In(zone), ID: 7, Valid: true})
	if shanghai != raw {
		t.Fatalf("同一时刻在不同时区下编码出不同游标：%q vs %q", shanghai, raw)
	}

	if got := encodeClickCursor(domain.ClickCursor{}); got != "" {
		t.Fatalf("末页游标 = %q，期望空串", got)
	}
}

// TestListClicksDefaultsAndLimits 守住分页参数的三层收敛：
// 默认值 → 服务端上限 → 配置上限（后者只收紧、不放大）。
func TestListClicksDefaultsAndLimits(t *testing.T) {
	t.Parallel()

	link := &domain.Link{ID: uuid.NewV7(), ShortCode: "abc1234"}

	tests := []struct {
		name       string
		in         ClickListInput
		wantLimit  int
		wantDays   int
		wantDevice string
		wantField  string // 非空表示期望 422，且字段名要对得上
	}{
		{name: "全部默认", in: ClickListInput{}, wantLimit: DefaultClickPageSize, wantDays: DefaultStatsDays},
		{name: "limit 生效", in: ClickListInput{Limit: 5, Days: 7}, wantLimit: 5, wantDays: 7},
		{name: "limit 超服务端上限被夹住", in: ClickListInput{Limit: 5000}, wantLimit: MaxClickPageSize, wantDays: DefaultStatsDays},
		{name: "配置上限只收紧不放大", in: ClickListInput{Limit: 50, MaxLimit: 5}, wantLimit: 5, wantDays: DefaultStatsDays},
		{name: "配置上限大于请求时不生效", in: ClickListInput{Limit: 3, MaxLimit: 50}, wantLimit: 3, wantDays: DefaultStatsDays},
		{name: "days 超上限被夹住", in: ClickListInput{Days: 100000}, wantLimit: DefaultClickPageSize, wantDays: MaxStatsDays},
		{name: "负数 days 用默认", in: ClickListInput{Days: -3}, wantLimit: DefaultClickPageSize, wantDays: DefaultStatsDays},
		{name: "device 去空白并折叠小写", in: ClickListInput{Device: "  MOBILE "}, wantLimit: DefaultClickPageSize, wantDays: DefaultStatsDays, wantDevice: "mobile"},
		{name: "device=unknown 是合法筛选值", in: ClickListInput{Device: "unknown"}, wantLimit: DefaultClickPageSize, wantDays: DefaultStatsDays, wantDevice: "unknown"},
		{name: "device 非法", in: ClickListInput{Device: "tv"}, wantField: "device"},
		{name: "cursor 非法", in: ClickListInput{Cursor: "!!!not-base64!!!"}, wantField: "cursor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &clickRepoFake{}
			stats := newClickStats(repo)

			got, err := stats.ListClicks(t.Context(), link, tt.in)
			if tt.wantField != "" {
				invalid, ok := errors.AsType[*domain.InvalidInputError](err)
				if !ok {
					t.Fatalf("错误 = %v（%T），期望 *domain.InvalidInputError", err, err)
				}
				if invalid.Field != tt.wantField {
					t.Fatalf("错误字段 = %q，期望 %q", invalid.Field, tt.wantField)
				}
				// 参数都没通过校验，不该碰数据库
				if len(repo.got) != 0 {
					t.Fatalf("参数非法却查了仓储 %d 次", len(repo.got))
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}

			if len(repo.got) != 1 {
				t.Fatalf("仓储被查询 %d 次，期望 1 次", len(repo.got))
			}
			q := repo.got[0]
			if q.Limit != tt.wantLimit {
				t.Errorf("仓储收到的 limit = %d，期望 %d", q.Limit, tt.wantLimit)
			}
			if q.Device != tt.wantDevice {
				t.Errorf("仓储收到的 device = %q，期望 %q", q.Device, tt.wantDevice)
			}
			if q.LinkID != link.ID {
				t.Errorf("仓储收到的 link_id = %v，期望 %v", q.LinkID, link.ID)
			}
			if got.Days != tt.wantDays {
				t.Errorf("结果里的 days = %d，期望 %d", got.Days, tt.wantDays)
			}

			// 窗口：UTC 零点整，且「今天」也算一天（since + days-1 = 今天零点）
			if q.Since.Location() != time.UTC {
				t.Errorf("窗口起点时区 = %v，期望 UTC", q.Since.Location())
			}
			if h, m, s := q.Since.Clock(); h != 0 || m != 0 || s != 0 || q.Since.Nanosecond() != 0 {
				t.Errorf("窗口起点 = %v，期望 UTC 零点整", q.Since)
			}
			if end := q.Since.AddDate(0, 0, tt.wantDays-1); !end.Equal(utcToday()) {
				t.Errorf("窗口末端 = %v，期望今天零点 %v（今天也算一天）", end, utcToday())
			}
			if !got.Since.Equal(q.Since) {
				t.Errorf("结果里的 since = %v，与仓储收到的不一致（%v）", got.Since, q.Since)
			}
		})
	}
}

// TestListClicksPagination 守住游标在两侧的传递：进来的是解开的元组，出去的是下一页。
func TestListClicksPagination(t *testing.T) {
	t.Parallel()

	link := &domain.Link{ID: uuid.NewV7(), ShortCode: "abc1234"}
	first := time.Date(2026, 3, 4, 5, 6, 7, 1, time.UTC)
	second := first.Add(-time.Millisecond)

	events := []domain.ClickEvent{
		{ID: 9, LinkID: link.ID, ShortCode: link.ShortCode, OccurredAt: first, Device: "mobile"},
		{ID: 8, LinkID: link.ID, ShortCode: link.ShortCode, OccurredAt: second, Device: "desktop"},
	}
	repo := &clickRepoFake{
		events: events,
		next:   domain.ClickCursor{OccurredAt: second, ID: 8, Valid: true},
	}
	stats := newClickStats(repo)

	// 第一页：不传游标 → 仓储收到 Valid=false
	page1, err := stats.ListClicks(t.Context(), link, ClickListInput{Limit: 2})
	if err != nil {
		t.Fatalf("第一页不该报错：%v", err)
	}
	if repo.got[0].Cursor.Valid {
		t.Fatalf("第一页不该带游标，实际收到 %+v", repo.got[0].Cursor)
	}
	if len(page1.Events) != 2 {
		t.Fatalf("第一页明细数 = %d，期望 2", len(page1.Events))
	}
	if page1.NextCursor == "" {
		t.Fatal("还有下一页时不该返回空游标")
	}

	// 第二页：把上一页的游标原样传回去，仓储必须收到解开的元组
	page2, err := stats.ListClicks(t.Context(), link, ClickListInput{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("第二页不该报错：%v", err)
	}
	got := repo.got[1].Cursor
	if !got.Valid || got.ID != 8 || !got.OccurredAt.Equal(second) {
		t.Fatalf("第二页游标 = %+v，期望 {OccurredAt: %v, ID: 8, Valid: true}", got, second)
	}
	_ = page2

	// 末页：仓储说没有下一页 → 编码成空串（前端据此隐藏「加载更多」）
	repo.next = domain.ClickCursor{}
	last, err := stats.ListClicks(t.Context(), link, ClickListInput{Limit: 2})
	if err != nil {
		t.Fatalf("末页不该报错：%v", err)
	}
	if last.NextCursor != "" {
		t.Fatalf("末页游标 = %q，期望空串", last.NextCursor)
	}
}

// TestListClicksPropagatesRepoError 仓储故障必须原样上抛（由 handler 映射成 5xx/503），
// 不能悄悄返回空列表 —— 那会让用户以为「这段时间没有点击」。
func TestListClicksPropagatesRepoError(t *testing.T) {
	t.Parallel()

	boom := errors.New("pg: connection refused")
	stats := newClickStats(&clickRepoFake{err: boom})

	_, err := stats.ListClicks(t.Context(), &domain.Link{ID: uuid.NewV7()}, ClickListInput{})
	if !errors.Is(err, boom) {
		t.Fatalf("错误 = %v，期望原样上抛 %v", err, boom)
	}
}

// TestNormalizeDevice 只钉一件事：筛选值必须与 ua 写入 click_events.device 的取值一致。
func TestNormalizeDevice(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"desktop", "mobile", "tablet", "bot", "unknown", " MOBILE ", "Bot"} {
		if _, err := normalizeDevice(ok); err != nil {
			t.Errorf("normalizeDevice(%q) 报错：%v", ok, err)
		}
	}
	for _, bad := range []string{"tv", "smart-tv", "mobile,desktop"} {
		if _, err := normalizeDevice(bad); err == nil {
			t.Errorf("normalizeDevice(%q) 该报错", bad)
		}
	}
	if got, err := normalizeDevice(""); err != nil || got != "" {
		t.Errorf("空串应表示不筛选，得到 (%q, %v)", got, err)
	}
	if got, _ := normalizeDevice("  TABLET "); !strings.EqualFold(got, "tablet") {
		t.Errorf("去空白与折叠小写失败：%q", got)
	}
}
