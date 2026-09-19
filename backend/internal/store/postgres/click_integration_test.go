package postgres

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"ashen-courier/internal/domain"
)

// TestInsertBatchDedupByEventUID 守住 at-least-once 的兜底：重投的同一批消息
// 带着同一个 event_uid，partial 唯一索引 + ON CONFLICT DO NOTHING 必须把它们挡掉。
//
// 这条断言此前只有容器级验收（手工用显式 Stream ID 重投一条消息）走过，
// 一旦 ON CONFLICT 的谓词与索引对不上，PG 会直接报错而不是静默——两种失败都在这里现形。
func TestInsertBatchDedupByEventUID(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	clicks := db.Clicks()

	link := createLink(t, db, testCode(t, "it-uid"), nil)
	at := time.Now().UTC().Truncate(time.Millisecond)

	event := func(uid, device string) domain.ClickEvent {
		return domain.ClickEvent{
			EventUID:   uid,
			LinkID:     link.ID,
			ShortCode:  link.ShortCode,
			OccurredAt: at,
			Device:     device,
		}
	}

	if err := clicks.InsertBatch(ctx, []domain.ClickEvent{
		event("1700000000001-0", "desktop"),
		event("1700000000002-0", "mobile"),
	}); err != nil {
		t.Fatalf("首次插入: %v", err)
	}
	if got := countClicks(t, db, link.ID); got != 2 {
		t.Fatalf("首次插入后应有 2 条明细，实际 %d", got)
	}

	// 重投：第一条与上一批同 uid（会被挡掉），第二条是新的
	if err := clicks.InsertBatch(ctx, []domain.ClickEvent{
		event("1700000000001-0", "desktop"),
		event("1700000000003-0", "desktop"),
	}); err != nil {
		t.Fatalf("重投: %v", err)
	}
	if got := countClicks(t, db, link.ID); got != 3 {
		t.Errorf("重投后应当只多出 1 条（同 uid 的那条被 DO NOTHING 跳过），实际 %d", got)
	}

	// event_uid 为空串的行不参与去重（只可能来自手写 SQL 或旧版本 worker）：
	// NULLIF 把它们落成 NULL，partial 索引不覆盖 NULL，因此两条都该进去
	if err := clicks.InsertBatch(ctx, []domain.ClickEvent{
		event("", "bot"),
		event("", "bot"),
	}); err != nil {
		t.Fatalf("插入无 uid 的历史风格行: %v", err)
	}
	if got := countClicks(t, db, link.ID); got != 5 {
		t.Errorf("空 event_uid 的行不该互相顶掉，期望 5 条，实际 %d", got)
	}
}

// TestListByLinkKeysetPagination 盯住迁移 000004 要修的那个洞：
// 同一毫秒（这里是同一秒，粒度更粗）内的多条明细在时间上完全并列，
// 只用 occurred_at 做游标会在翻页边界上漏行或重复。
func TestListByLinkKeysetPagination(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	clicks := db.Clicks()

	link := createLink(t, db, testCode(t, "it-clk"), nil)

	const (
		total = 12
		page  = 5
		tie   = 3 // 每 3 条共用一个 occurred_at
	)
	devices := []string{"", "mobile", "desktop"}
	base := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)

	events := make([]domain.ClickEvent, 0, total)
	for i := range total {
		events = append(events, domain.ClickEvent{
			EventUID:   fmt.Sprintf("170000009%04d-0", i),
			LinkID:     link.ID,
			ShortCode:  link.ShortCode,
			OccurredAt: base.Add(time.Duration(i/tie) * time.Second),
			Device:     devices[i%len(devices)],
		})
	}
	if err := clicks.InsertBatch(ctx, events); err != nil {
		t.Fatalf("插入明细: %v", err)
	}

	// 这条用例的意义全在「存在并列时间」上：样本一旦失去并列，把 id 兜底删掉也不会红
	var ties int64
	if err := db.pool.QueryRow(ctx,
		"SELECT count(*) FROM (SELECT occurred_at FROM click_events WHERE link_id = $1 "+
			"GROUP BY occurred_at HAVING count(*) > 1) AS ties", toPgUUID(link.ID)).Scan(&ties); err != nil {
		t.Fatalf("统计并列时间: %v", err)
	}
	if ties == 0 {
		t.Fatal("样本没有构造出并列 occurred_at，这条用例就失去意义了")
	}

	// 权威顺序：与 store 的 ORDER BY occurred_at DESC, id DESC 同源，但完全由 SQL 给出
	wantRows, err := db.pool.Query(ctx,
		"SELECT id FROM click_events WHERE link_id = $1 ORDER BY occurred_at DESC, id DESC", toPgUUID(link.ID))
	if err != nil {
		t.Fatalf("查询权威顺序: %v", err)
	}
	defer wantRows.Close()

	var want []int64
	for wantRows.Next() {
		var id int64
		if err := wantRows.Scan(&id); err != nil {
			t.Fatalf("扫描权威顺序: %v", err)
		}
		want = append(want, id)
	}
	if err := wantRows.Err(); err != nil {
		t.Fatalf("遍历权威顺序: %v", err)
	}
	if len(want) != total {
		t.Fatalf("样本准备失败：期望 %d 条，实际 %d", total, len(want))
	}

	var got []int64
	var cursor domain.ClickCursor
	for {
		gotPage, next, err := clicks.ListByLink(ctx, domain.ClickListQuery{LinkID: link.ID, Limit: page, Cursor: cursor})
		if err != nil {
			t.Fatalf("明细分页: %v", err)
		}
		if len(gotPage) > page {
			t.Fatalf("一页最多 %d 条，实际 %d", page, len(gotPage))
		}
		for i := range gotPage {
			got = append(got, gotPage[i].ID)
		}
		if !next.Valid {
			break
		}
		cursor = next
		if len(got) > total {
			t.Fatalf("游标没有收敛：已取 %d 条（total=%d）", len(got), total)
		}
	}

	if !slices.Equal(got, want) {
		t.Errorf("keyset 分页结果与权威顺序不一致（并列时间上漏行 / 重复会落到这里）：\n got  = %v\n want = %v", got, want)
	}

	// 设备筛选的口径必须与统计的分布一致：空 device 归入 unknown 桶
	for _, tc := range []struct {
		device string
		want   int
	}{
		{"mobile", 4},
		{"desktop", 4},
		{"unknown", 4}, // device 为空串的那些
		{"bot", 0},
	} {
		gotPage, _, err := clicks.ListByLink(ctx, domain.ClickListQuery{LinkID: link.ID, Device: tc.device, Limit: 50})
		if err != nil {
			t.Fatalf("按 device=%q 筛选: %v", tc.device, err)
		}
		if len(gotPage) != tc.want {
			t.Errorf("device=%q 期望 %d 条，实际 %d", tc.device, tc.want, len(gotPage))
		}
	}

	// Since 是「窗口起点（含）」：base+3s 之后只剩最后一组 3 条
	windowed, _, err := clicks.ListByLink(ctx, domain.ClickListQuery{
		LinkID: link.ID,
		Since:  base.Add(3 * time.Second),
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("按 Since 筛选: %v", err)
	}
	if len(windowed) != tie {
		t.Errorf("Since=base+3s 期望命中 %d 条，实际 %d", tie, len(windowed))
	}
}

// TestAggregateDayBoundaryIsUTC 守住聚合 SQL 里那句 AT TIME ZONE 'UTC'。
//
// date_trunc('day', timestamptz) 默认按**会话时区**截断，而 service 是按 UTC 日历日
// 补齐 Trend 的；容器默认 UTC 时看不出差别，一旦设了 TZ 就会把跨日边界的点击归错天。
func TestAggregateDayBoundaryIsUTC(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	clicks := db.Clicks()

	link := createLink(t, db, testCode(t, "it-agg"), nil)

	// 固定日期而不是「现在」：跨日边界要的是确定性，而不是「恰好没跨过午夜」
	day1 := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 0, 30, 0, 0, time.UTC)

	if err := clicks.InsertBatch(ctx, []domain.ClickEvent{
		{EventUID: "1700000100001-0", LinkID: link.ID, ShortCode: link.ShortCode, OccurredAt: day1,
			Referer: "https://news.example/post/1", Browser: "Chrome"},
		{EventUID: "1700000100002-0", LinkID: link.ID, ShortCode: link.ShortCode, OccurredAt: day1,
			Device: "mobile", Browser: "Safari"},
		{EventUID: "1700000100003-0", LinkID: link.ID, ShortCode: link.ShortCode, OccurredAt: day2,
			Device: "desktop", Browser: "Chrome"},
	}); err != nil {
		t.Fatalf("插入明细: %v", err)
	}

	got, err := clicks.Aggregate(ctx, domain.StatsQuery{
		LinkID: link.ID,
		Since:  time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
		TopN:   10,
	})
	if err != nil {
		t.Fatalf("聚合: %v", err)
	}

	if len(got.Daily) != 2 {
		t.Fatalf("跨 UTC 日边界的两天应当聚成 2 个点，实际 %d：%+v", len(got.Daily), got.Daily)
	}
	wantDay1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantDay2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if d := got.Daily[0]; !d.Date.Equal(wantDay1) || d.Clicks != 2 {
		t.Errorf("第一个点 = %v / %d 次，期望 %v / 2 次（23:30Z 必须留在当天）", d.Date, d.Clicks, wantDay1)
	}
	if d := got.Daily[1]; !d.Date.Equal(wantDay2) || d.Clicks != 1 {
		t.Errorf("第二个点 = %v / %d 次，期望 %v / 1 次", d.Date, d.Clicks, wantDay2)
	}

	// 空 device 归入 unknown 桶，与明细页 ?device=unknown 的筛选口径一致
	if !hasBucket(got.Devices, "unknown", 1) {
		t.Errorf("空 device 应当归入 unknown 桶，实际 %+v", got.Devices)
	}
	if !hasBucket(got.Devices, "mobile", 1) || !hasBucket(got.Devices, "desktop", 1) {
		t.Errorf("设备分布缺少 mobile / desktop：%+v", got.Devices)
	}
	if !hasBucket(got.Browsers, "Chrome", 2) {
		t.Errorf("浏览器分布应当聚成 Chrome=2，实际 %+v", got.Browsers)
	}
	// 来源按主机名聚合：去掉 scheme 与路径，且识别不出主机的归到空串（直接访问）
	if !hasBucket(got.Referers, "news.example", 1) {
		t.Errorf("Referer 应当聚合成主机名 news.example，实际 %+v", got.Referers)
	}
}

// hasBucket 判断分布里是否存在「某个取值 + 某个计数」。
func hasBucket(buckets []domain.BucketCount, name string, clicks int64) bool {
	return slices.ContainsFunc(buckets, func(b domain.BucketCount) bool {
		return b.Name == name && b.Clicks == clicks
	})
}

// TestAggregateCountriesExcludesEmpty 守住国家分布与其他维度**不一样**的那条口径（M5-2）。
//
// device / browser 的空值要归进 unknown 桶，含义是「解析了但没认出来」；
// 而 country 的空值里混着「这个部署根本没配 GeoIP 库文件」这一大类 ——
// 把它聚成一条 100% 的「未知」，用户会以为是解析失败而不是「我没开这个功能」。
// 所以国家维度只返回已知国家：列表为空 = 没开这个功能，前端据此整块隐藏。
func TestAggregateCountriesExcludesEmpty(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	clicks := db.Clicks()
	at := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)

	// ---- 情形一：有已知国家 + 有查不到的点击 ----
	mixed := createLink(t, db, testCode(t, "it-geo"), nil)
	if err := clicks.InsertBatch(ctx, []domain.ClickEvent{
		{EventUID: "1700000200001-0", LinkID: mixed.ID, ShortCode: mixed.ShortCode, OccurredAt: at, Country: "CN"},
		{EventUID: "1700000200002-0", LinkID: mixed.ID, ShortCode: mixed.ShortCode, OccurredAt: at, Country: "CN"},
		{EventUID: "1700000200003-0", LinkID: mixed.ID, ShortCode: mixed.ShortCode, OccurredAt: at, Country: "US"},
		// 未配置 GeoIP 时每一行都是这样：国家为空
		{EventUID: "1700000200004-0", LinkID: mixed.ID, ShortCode: mixed.ShortCode, OccurredAt: at},
	}); err != nil {
		t.Fatalf("插入明细: %v", err)
	}

	got, err := clicks.Aggregate(ctx, domain.StatsQuery{LinkID: mixed.ID, Since: at.Add(-time.Hour), TopN: 10})
	if err != nil {
		t.Fatalf("聚合: %v", err)
	}

	if len(got.Countries) != 2 {
		t.Fatalf("国家分布应只有 2 个已知国家，实际 %+v", got.Countries)
	}
	if got.Countries[0].Name != "CN" || got.Countries[0].Clicks != 2 {
		t.Errorf("应按点击数降序且 CN 在前，实际 %+v", got.Countries)
	}
	if !hasBucket(got.Countries, "US", 1) {
		t.Errorf("缺少 US=1：%+v", got.Countries)
	}
	if hasBucket(got.Countries, "unknown", 1) {
		t.Errorf("国家维度不该出现 unknown 桶（那是「没配 GeoIP」的形态，不是一种国家）：%+v", got.Countries)
	}

	// ---- 情形二：整条链接都没有国家（= 未部署 GeoIP 的形态）----
	// 必须是**空切片**而不是 nil：handler 会把它转成 JSON 的空数组，
	// nil 会被序列化成 null，前端就得多写一层判空。
	noGeo := createLink(t, db, testCode(t, "it-geo0"), nil)
	if err := clicks.InsertBatch(ctx, []domain.ClickEvent{
		{EventUID: "1700000200005-0", LinkID: noGeo.ID, ShortCode: noGeo.ShortCode, OccurredAt: at},
	}); err != nil {
		t.Fatalf("插入明细: %v", err)
	}

	gotNoGeo, err := clicks.Aggregate(ctx, domain.StatsQuery{LinkID: noGeo.ID, Since: at.Add(-time.Hour), TopN: 10})
	if err != nil {
		t.Fatalf("聚合: %v", err)
	}
	if len(gotNoGeo.Countries) != 0 {
		t.Errorf("没有已知国家时应当为空，实际 %+v", gotNoGeo.Countries)
	}
	if gotNoGeo.Countries == nil {
		t.Error("应当是空切片而不是 nil（否则 JSON 会序列化成 null）")
	}
}

// TestAddClickCount 守住计数回刷的写入口：累加返回的是**累加后**的总值，
// worker 依赖这个返回值来判断基线是否推进。
func TestAddClickCount(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	links := db.Links()

	code := testCode(t, "it-cnt")
	createLink(t, db, code, nil)

	total, err := links.AddClickCount(ctx, code, 3)
	if err != nil {
		t.Fatalf("累加 3: %v", err)
	}
	if total != 3 {
		t.Errorf("第一次累加后应返回 3，实际 %d", total)
	}

	total, err = links.AddClickCount(ctx, code, 4)
	if err != nil {
		t.Fatalf("累加 4: %v", err)
	}
	if total != 7 {
		t.Errorf("第二次累加后应返回 7，实际 %d", total)
	}

	if _, err := links.AddClickCount(ctx, testCode(t, "it-nocnt"), 1); err == nil {
		t.Fatal("累加不存在的短码必须报错")
	} else if _, ok := errors.AsType[*domain.NotFoundError](err); !ok {
		t.Fatalf("期望 *domain.NotFoundError，实际 %v", err)
	}
}

// TestExpireDue 守住过期扫描的边界：只动「已过期且仍为 active」的行，
// 已经是 disabled 的不重复处理、还没过期的不碰。
func TestExpireDue(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	links := db.Links()

	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	// 让「应被处理」的那条挂在自定义域上：这样才验证得到
	// **返回的引用带着所属域**（worker 要按「域 + 短码」失效缓存，
	// 返回空域会让自定义域上的过期短链在缓存 TTL 内继续跳转）。
	dom := registerDomain(t, db, "expire-"+testSuffix(t)+".example", nil)

	dueCode := testCode(t, "it-due")
	createLink(t, db, dueCode, func(l *domain.Link) {
		l.ExpiresAt = &past
		l.DomainID = &dom.ID
	})

	disabledCode := testCode(t, "it-dis")
	createLink(t, db, disabledCode, func(l *domain.Link) {
		l.ExpiresAt = &past
		l.Status = domain.LinkStatusDisabled
	})

	futureCode := testCode(t, "it-fut")
	createLink(t, db, futureCode, func(l *domain.Link) { l.ExpiresAt = &future })

	refs, err := links.ExpireDue(ctx, now, 100)
	if err != nil {
		t.Fatalf("扫描过期: %v", err)
	}
	hasCode := func(code string) bool {
		return slices.ContainsFunc(refs, func(r domain.LinkRef) bool { return r.Code == code })
	}
	if !hasCode(dueCode) {
		t.Errorf("已过期且 active 的短码应当被处理，实际返回 %v", refs)
	}
	if hasCode(disabledCode) {
		t.Errorf("已经是 disabled 的不该被重复处理：%v", refs)
	}
	if hasCode(futureCode) {
		t.Errorf("还没过期的不该被处理：%v", refs)
	}

	// 返回的引用必须带上所属域，否则 worker 删不掉那条缓存
	due := slices.IndexFunc(refs, func(r domain.LinkRef) bool { return r.Code == dueCode })
	if due < 0 {
		t.Fatalf("结果里应当有 %q：%v", dueCode, refs)
	}
	if refs[due].DomainID == nil || *refs[due].DomainID != dom.ID {
		t.Errorf("过期引用的 DomainID = %v，期望 %v（域丢了就删不掉缓存）", refs[due].DomainID, dom.ID)
	}

	got, err := links.GetByCode(ctx, dueCode)
	if err != nil {
		t.Fatalf("读取 %q: %v", dueCode, err)
	}
	if got.Status != domain.LinkStatusDisabled {
		t.Errorf("处理后状态应当变成 disabled，实际 %s", got.Status)
	}
	// 跳转语义随之变成 410
	if err := got.Redirectable(now); !errors.Is(err, domain.ErrGone) {
		t.Errorf("过期链接的 Redirectable 应当报 ErrGone（→410），实际 %v", err)
	}
}
