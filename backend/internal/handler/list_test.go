package handler

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"uuid"

	"ashen-courier/internal/domain"
)

// listLinks 用给定的替身跑一次 GET /api/links 并解出响应。
func listLinks(t *testing.T, links []domain.Link, deltas domain.ClickDeltaBatchReader) (*httptest.ResponseRecorder, linkListResponse) {
	t.Helper()

	repo := &stubLinkRepo{
		getByCode: func(context.Context, string) (*domain.Link, error) {
			return nil, domain.NotFound("link", "unused")
		},
		listByOwner: func(context.Context, domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error) {
			return links, domain.LinkCursor{}, nil
		},
	}

	return listLinksWithRepo(t, repo, deltas, "")
}

// listLinksWithRepo 允许调用方自己给仓储替身（用来断言下推的筛选条件）。
func listLinksWithRepo(t *testing.T, repo *stubLinkRepo, deltas domain.ClickDeltaBatchReader, rawQuery string) (*httptest.ResponseRecorder, linkListResponse) {
	t.Helper()

	h := &linkHandler{
		shortener:   newTestShortener(repo),
		pageSize:    20,
		maxPageSize: 100,
		deltas:      deltas,
	}

	target := "/api/links"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	userID := uuid.NewV7()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	r = r.WithContext(withUserID(r.Context(), userID))

	rr := httptest.NewRecorder()
	h.list(rr, r)

	var body linkListResponse
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("列表响应不是合法 JSON：%v（%q）", err, rr.Body.String())
		}
	}
	return rr, body
}

// TestListPassesTagFilter 守住 M4-1 的筛选下发：query 里的 `tag` 必须落到
// 仓储的 LinkFilter.Tag（并统一小写），否则筛选只在界面上「看起来」生效。
func TestListPassesTagFilter(t *testing.T) {
	t.Parallel()

	var got domain.LinkFilter
	repo := &stubLinkRepo{
		listByOwner: func(_ context.Context, filter domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error) {
			got = filter
			return nil, domain.LinkCursor{}, nil
		},
	}

	rr, _ := listLinksWithRepo(t, repo, nil, "tag=%20Ops%20&q=blog")

	if rr.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rr.Code, rr.Body.String())
	}
	if got.Tag != "ops" {
		t.Errorf("下推的 Tag = %q，期望 %q（统一小写）", got.Tag, "ops")
	}
	if got.Query != "blog" {
		t.Errorf("下推的 Query = %q，期望 %q", got.Query, "blog")
	}
}

// TestListOverlaysPendingDelta 守住 M2-2 的口径统一：
// 列表的 click_count 必须是「PG 基线 + Redis 待同步增量」，与详情页的
// total_clicks 同口径 —— 否则仪表盘汇总会比详情页慢一个回刷周期。
func TestListOverlaysPendingDelta(t *testing.T) {
	t.Parallel()

	links := []domain.Link{
		{ID: uuid.NewV7(), ShortCode: "aaa1234", Status: domain.LinkStatusActive, ClickCount: 10},
		{ID: uuid.NewV7(), ShortCode: "bbb1234", Status: domain.LinkStatusActive, ClickCount: 2},
		{ID: uuid.NewV7(), ShortCode: "ccc1234", Status: domain.LinkStatusActive, ClickCount: 5},
	}
	deltas := &stubDeltas{byCode: map[string]int64{
		"aaa1234": 3,
		"bbb1234": 0, // 有键但值为 0：叠加后不变
		// ccc1234 不在 map 里：没有待同步增量
	}}

	rr, body := listLinks(t, links, deltas)

	if rr.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rr.Code, rr.Body.String())
	}
	if len(body.Links) != 3 {
		t.Fatalf("返回 %d 条，期望 3 条", len(body.Links))
	}

	want := map[string]int64{"aaa1234": 13, "bbb1234": 2, "ccc1234": 5}
	for _, item := range body.Links {
		if got := item.ClickCount; got != want[item.ShortCode] {
			t.Errorf("%s 的 click_count = %d，期望 %d（基线 + 待同步增量）",
				item.ShortCode, got, want[item.ShortCode])
		}
	}

	// 必须一次问完本页所有短码（MGET 的意义就在这里：不能逐条查）
	if len(deltas.asked) != 1 || len(deltas.asked[0]) != 3 {
		t.Fatalf("期望一次性问 3 个短码，实际 %v", deltas.asked)
	}
}

// TestListFallsBackToBaselineWhenDeltaReadFails：统计侧读失败只能是降级
// （退回纯基线 + 200），绝不能把整个列表打成 5xx —— 列表是主功能，统计是附加信息。
func TestListFallsBackToBaselineWhenDeltaReadFails(t *testing.T) {
	t.Parallel()

	links := []domain.Link{
		{ID: uuid.NewV7(), ShortCode: "aaa1234", Status: domain.LinkStatusActive, ClickCount: 7},
	}
	deltas := &stubDeltas{err: errors.New("redis: connection refused")}

	rr, body := listLinks(t, links, deltas)

	if rr.Code != http.StatusOK {
		t.Fatalf("统计读失败时列表仍应 200，实际 %d：%s", rr.Code, rr.Body.String())
	}
	if len(body.Links) != 1 || body.Links[0].ClickCount != 7 {
		t.Fatalf("读失败时应退回纯基线 7，实际 %+v", body.Links)
	}
}

// TestListWithoutDeltaReader：未接线（deltas == nil）时列表照常可用，只报基线。
func TestListWithoutDeltaReader(t *testing.T) {
	t.Parallel()

	links := []domain.Link{
		{ID: uuid.NewV7(), ShortCode: "aaa1234", Status: domain.LinkStatusActive, ClickCount: 4},
	}

	rr, body := listLinks(t, links, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rr.Code, rr.Body.String())
	}
	if len(body.Links) != 1 || body.Links[0].ClickCount != 4 {
		t.Fatalf("未接线时应报基线 4，实际 %+v", body.Links)
	}
}

// TestListRejectsInvalidLimit 顺手守住 limit 的入参校验（列表的第一个分支）。
func TestListRejectsInvalidLimit(t *testing.T) {
	t.Parallel()

	repo := &stubLinkRepo{
		listByOwner: func(context.Context, domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error) {
			t.Error("limit 非法时不该查库")
			return nil, domain.LinkCursor{}, nil
		},
	}
	h := &linkHandler{shortener: newTestShortener(repo), pageSize: 20, maxPageSize: 100}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/links?limit=0", nil)
	r = r.WithContext(withUserID(r.Context(), uuid.NewV7()))

	rr := httptest.NewRecorder()
	h.list(rr, r)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("期望 422，实际 %d：%s", rr.Code, rr.Body.String())
	}
	if got := errCode(t, rr); got != "invalid_limit" {
		t.Errorf("错误码 %q，期望 invalid_limit", got)
	}
}
