package postgres

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"ashen-courier/internal/domain"
)

// TestMigrationsApply 守住全部 up 迁移跑完之后数据库的实际形状。
//
// 断言「结果对象」而不是「跑没跑过」：进程退出码不会告诉你索引建错了名字，
// 也不会告诉你某个迁移把上一个迁移建的索引顺手删掉了。
func TestMigrationsApply(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()

	for _, table := range []string{"users", "links", "click_events"} {
		var exists bool
		if err := db.pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			t.Fatalf("查询表 %s: %v", table, err)
		}
		if !exists {
			t.Errorf("跑完全部迁移后应当存在表 %s", table)
		}
	}

	var hasView bool
	if err := db.pool.QueryRow(ctx,
		"SELECT count(*) = 1 FROM pg_views WHERE schemaname = 'public' AND viewname = 'link_click_totals'").Scan(&hasView); err != nil {
		t.Fatalf("查询对账视图: %v", err)
	}
	if !hasView {
		t.Error("跑完全部迁移后应当存在视图 link_click_totals（人工对账用）")
	}

	indexDefs := map[string]string{}
	rows, err := db.pool.Query(ctx, "SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = 'public'")
	if err != nil {
		t.Fatalf("查询索引: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("扫描索引: %v", err)
		}
		indexDefs[name] = def
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历索引: %v", err)
	}

	for _, name := range []string{"click_events_event_uid_key", "links_tags_gin", "click_events_link_time_id_idx"} {
		if _, ok := indexDefs[name]; !ok {
			t.Errorf("跑完全部迁移后应当存在索引 %s", name)
		}
	}
	if _, ok := indexDefs["click_events_link_time_idx"]; ok {
		t.Error("旧索引 click_events_link_time_idx 应当已被 000004 删除：它是新索引的前缀，留着只会让最热的追加路径多维护一棵 B-tree")
	}
	// partial 谓词必须还在：store 的 ON CONFLICT 冲突推断要复述它
	// （WHERE event_uid IS NOT NULL），谓词一旦丢失，InsertBatch 会直接报
	// 「no unique or exclusion constraint matching the ON CONFLICT specification」
	if def := indexDefs["click_events_event_uid_key"]; !strings.Contains(def, "event_uid IS NOT NULL") {
		t.Errorf("event_uid 唯一索引必须是 partial（WHERE event_uid IS NOT NULL），实际定义：%s", def)
	}
}

// TestLinkRoundTrip 覆盖 links 最基本的写读往返：列清单（linkColumns）、扫描目标
// （dest）与领域实体三处对应关系一旦错位，这里就会红。
func TestLinkRoundTrip(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	links := db.Links()

	code := testCode(t, "it-rt")
	expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
	created := createLink(t, db, code, func(l *domain.Link) {
		l.TargetURL = "https://example.com/it/round-trip?x=1"
		l.Title = "集成测试"
		l.Tags = []string{"ops", "dev"}
		l.ExpiresAt = &expires
		l.CreatedIP = "203.0.113.7"
	})

	got, err := links.GetByCode(ctx, code)
	if err != nil {
		t.Fatalf("读取 %q: %v", code, err)
	}

	if got.ID != created.ID {
		t.Errorf("id = %v，期望 %v", got.ID, created.ID)
	}
	if got.TargetURL != "https://example.com/it/round-trip?x=1" {
		t.Errorf("target_url = %q", got.TargetURL)
	}
	if got.Title != "集成测试" {
		t.Errorf("title = %q", got.Title)
	}
	if !slices.Equal(got.Tags, []string{"ops", "dev"}) {
		t.Errorf("tags = %v，期望 [ops dev]", got.Tags)
	}
	if got.Status != domain.LinkStatusActive {
		t.Errorf("status = %s，期望 active", got.Status)
	}
	if got.CreatedIP != "203.0.113.7" {
		t.Errorf("created_ip = %q（inet 经 host() 取出，不该带 /32 掩码长度）", got.CreatedIP)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("expires_at = %v，期望 %v", got.ExpiresAt, expires)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("created_at / updated_at 应当由数据库填好")
	}

	if _, err := links.GetByCode(ctx, testCode(t, "it-miss")); err == nil {
		t.Fatal("读取不存在的短码必须报错")
	} else if _, ok := errors.AsType[*domain.NotFoundError](err); !ok {
		t.Fatalf("不存在时应当是 *domain.NotFoundError，实际 %v", err)
	}
}

// TestLinkUpdateSemantics 守住 Update 的三种「不传 / 传空 / 传值」语义。
// 这些语义靠 SQL 里的 COALESCE 与指针的 nil 区分，是最容易在重构中被改坏的地方。
func TestLinkUpdateSemantics(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	links := db.Links()

	t.Run("只改标题时其它列不动", func(t *testing.T) {
		code := testCode(t, "it-up")
		expires := time.Now().UTC().Add(time.Hour)
		createLink(t, db, code, func(l *domain.Link) {
			l.Tags = []string{"ops"}
			l.ExpiresAt = &expires
		})

		updated, err := links.Update(ctx, code, domain.LinkPatch{Title: new("新标题")})
		if err != nil {
			t.Fatalf("更新标题: %v", err)
		}
		if updated.Title != "新标题" {
			t.Errorf("title = %q，期望「新标题」", updated.Title)
		}
		if updated.Status != domain.LinkStatusActive {
			t.Errorf("status = %s，未传 status 时不该被改动", updated.Status)
		}
		if !slices.Equal(updated.Tags, []string{"ops"}) {
			t.Errorf("tags = %v，未传 tags 时不该被清空", updated.Tags)
		}
		if updated.ExpiresAt == nil {
			t.Error("未传 expires_at 且未传 clear_expires 时不该清掉过期时间")
		}
	})

	t.Run("clear_expires 把过期时间置空", func(t *testing.T) {
		code := testCode(t, "it-clr")
		expires := time.Now().UTC().Add(time.Hour)
		createLink(t, db, code, func(l *domain.Link) { l.ExpiresAt = &expires })

		updated, err := links.Update(ctx, code, domain.LinkPatch{ClearExpires: true})
		if err != nil {
			t.Fatalf("清除过期时间: %v", err)
		}
		if updated.ExpiresAt != nil {
			t.Errorf("clear_expires 之后不该还有过期时间：%v", updated.ExpiresAt)
		}
	})

	t.Run("tags 传空切片是清空，nil 是不动", func(t *testing.T) {
		code := testCode(t, "it-tags")
		createLink(t, db, code, func(l *domain.Link) { l.Tags = []string{"ops", "dev"} })

		kept, err := links.Update(ctx, code, domain.LinkPatch{Title: new("改标题")})
		if err != nil {
			t.Fatalf("只改标题: %v", err)
		}
		if !slices.Equal(kept.Tags, []string{"ops", "dev"}) {
			t.Errorf("tags 指针为 nil 时不该动标签：%v", kept.Tags)
		}

		cleared, err := links.Update(ctx, code, domain.LinkPatch{Tags: new([]string{})})
		if err != nil {
			t.Fatalf("清空标签: %v", err)
		}
		if len(cleared.Tags) != 0 {
			t.Errorf("tags 指向空切片应当清空，实际 %v", cleared.Tags)
		}
	})

	t.Run("不存在的短码返回 NotFound", func(t *testing.T) {
		if _, err := links.Update(ctx, testCode(t, "it-noup"), domain.LinkPatch{Title: new("x")}); err == nil {
			t.Fatal("更新不存在的短码必须报错")
		} else if _, ok := errors.AsType[*domain.NotFoundError](err); !ok {
			t.Fatalf("期望 *domain.NotFoundError，实际 %v", err)
		}
	})
}

// TestListByOwnerKeysetPagination 用「与权威 SQL 逐项比对」的方式验证 keyset 分页：
// 只要漏行、重复或次序不对，比对就会失败 —— 比逐个断言页大小更难被糊弄过去。
func TestListByOwnerKeysetPagination(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	links := db.Links()

	owner := createUser(t, db).ID
	const (
		total = 5
		page  = 2
	)
	for i := range total {
		createLink(t, db, testCode(t, "it-page"), func(l *domain.Link) {
			l.OwnerID = &owner
			l.Title = fmt.Sprintf("第 %d 条", i)
		})
	}

	// 权威顺序：与 store 的 ORDER BY created_at DESC, id DESC 同源，但完全由 SQL 给出
	wantRows, err := db.pool.Query(ctx,
		"SELECT short_code FROM links WHERE owner_id = $1 AND status <> 3 ORDER BY created_at DESC, id DESC",
		toPgUUID(owner))
	if err != nil {
		t.Fatalf("查询权威顺序: %v", err)
	}
	defer wantRows.Close()

	var want []string
	for wantRows.Next() {
		var code string
		if err := wantRows.Scan(&code); err != nil {
			t.Fatalf("扫描权威顺序: %v", err)
		}
		want = append(want, code)
	}
	if err := wantRows.Err(); err != nil {
		t.Fatalf("遍历权威顺序: %v", err)
	}
	if len(want) != total {
		t.Fatalf("样本准备失败：期望 %d 条，实际 %d", total, len(want))
	}

	var got []string
	var cursor domain.LinkCursor
	for {
		gotPage, next, err := links.ListByOwner(ctx, domain.LinkFilter{OwnerID: owner, Limit: page, Cursor: cursor})
		if err != nil {
			t.Fatalf("分页查询: %v", err)
		}
		if len(gotPage) > page {
			t.Fatalf("一页最多 %d 条，实际 %d", page, len(gotPage))
		}
		for i := range gotPage {
			got = append(got, gotPage[i].ShortCode)
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
		t.Errorf("keyset 分页结果与权威顺序不一致（漏行 / 重复 / 次序错都会落到这里）：\n got  = %v\n want = %v", got, want)
	}
}

// TestListByOwnerTagFilterUsesGinIndex 同时验证两件事：标签筛选的结果正确，
// 以及它走的是 GIN 索引 —— 后者是 M4-1 的验收标准，此前只有人工 EXPLAIN 守着。
func TestListByOwnerTagFilterUsesGinIndex(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()
	links := db.Links()

	owner := createUser(t, db).ID
	const (
		sample    = 200
		withOps   = 20 // 每 10 条里有 1 条带 ops
		wantOps   = sample / 10
		maxFilter = 100
	)
	// 用一条 SQL 造样本：200 次单条 INSERT 在 CI 上会白白多花几百毫秒，
	// 而这里要的只是「表里有一批数据，好让计划器认真考虑索引」。
	if _, err := db.pool.Exec(ctx, "INSERT INTO links (id, short_code, target_url, owner_id, status, tags) "+
		"SELECT gen_random_uuid(), $2 || g, 'https://example.com/it/gin', $1, 1, "+
		"CASE WHEN g % 10 = 0 THEN ARRAY['ops'] ELSE ARRAY['other-' || (g % 5)] END "+
		"FROM generate_series(1, $3) AS g",
		toPgUUID(owner), "it-gin-", sample); err != nil {
		t.Fatalf("造样本: %v", err)
	}
	if withOps != wantOps {
		t.Fatalf("样本设计有误：期望 %d 条带标签", wantOps)
	}
	// 不 ANALYZE 的话计划器只有默认统计，小表上会固执地选 seqscan
	if _, err := db.pool.Exec(ctx, "ANALYZE links"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	page, _, err := links.ListByOwner(ctx, domain.LinkFilter{OwnerID: owner, Tag: "ops", Limit: maxFilter})
	if err != nil {
		t.Fatalf("标签筛选: %v", err)
	}
	if len(page) != wantOps {
		t.Errorf("tags @> ARRAY['ops'] 应当命中 %d 条，实际 %d", wantOps, len(page))
	}
	for i := range page {
		if !slices.Contains(page[i].Tags, "ops") {
			t.Errorf("第 %d 条不含 ops 标签：%v", i, page[i].Tags)
		}
	}

	// 计划形状断言：SET LOCAL 必须在事务里 —— 会话级的 SET 会串到并发跑的其他用例上。
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("关闭 seqscan: %v", err)
	}

	// 与 ListByOwner 拼出来的 WHERE / ORDER BY 形状一致（少了 id DESC 这一维，
	// 但索引能否被选中取决于 tags @> 的谓词，这里要证的正是那一点）
	explainRows, err := tx.Query(ctx,
		"EXPLAIN SELECT "+linkColumns+" FROM links WHERE owner_id = $1 AND status <> 3 AND tags @> $2 "+
			"ORDER BY created_at DESC, id DESC LIMIT $3",
		toPgUUID(owner), []string{"ops"}, maxFilter+1)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer explainRows.Close()

	var lines []string
	for explainRows.Next() {
		var line string
		if err := explainRows.Scan(&line); err != nil {
			t.Fatalf("扫描计划: %v", err)
		}
		lines = append(lines, line)
	}
	if err := explainRows.Err(); err != nil {
		t.Fatalf("遍历计划: %v", err)
	}

	plan := strings.Join(lines, "\n")
	if !strings.Contains(plan, "links_tags_gin") {
		t.Errorf("tags @> ARRAY[...] 应当走 links_tags_gin，实际计划：\n%s", plan)
	}
}
