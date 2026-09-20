package handler

import (
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/service"
)

// stubClickRepo 返回预置明细，并记录收到的查询条件。
//
// 内嵌 nil 接口：明细接口只该走 ListByLink，走到聚合就是设计跑偏了。
type stubClickRepo struct {
	domain.ClickRepository
	got    []domain.ClickListQuery
	events []domain.ClickEvent
	next   domain.ClickCursor
}

func (r *stubClickRepo) ListByLink(_ context.Context, q domain.ClickListQuery) ([]domain.ClickEvent, domain.ClickCursor, error) {
	r.got = append(r.got, q)
	return r.events, r.next, nil
}

// newClicksRouter 装配一份与生产同构的路由，只把仓储换成内存替身。
func newClicksRouter(t *testing.T, link *domain.Link, clicks domain.ClickRepository) http.Handler {
	t.Helper()

	repo := &stubLinkRepo{getByCode: func(_ context.Context, code string) (*domain.Link, error) {
		if code == link.ShortCode {
			return link, nil
		}
		return nil, domain.NotFound("link", code)
	}}

	return Router(Options{
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:             service.NewAuth(&stubUsers{}, "test-secret-0123456789", time.Hour),
		Shortener:        newTestShortener(repo),
		Stats:            service.NewStats(clicks, stubDelta{}),
		Health:           okProbe{},
		Limiter:          nil, // 不限流：这些用例只关心契约
		PageSize:         20,
		MaxPageSize:      100,
		ClickPageSize:    20,
		MaxClickPageSize: 100,
	})
}

// clicksRequest 构造一个带管理密钥的明细请求（持钥的匿名链接即可通过鉴权）。
func clicksRequest(t *testing.T, code, query, manageKey string) *http.Request {
	t.Helper()

	path := "/api/links/" + code + "/clicks"
	if query != "" {
		path += "?" + query
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	if manageKey != "" {
		r.Header.Set(manageKeyHeader, manageKey)
	}
	return r
}

// TestClickListResponse 守住明细响应的对外契约，重点是 **IP 掩码**：
// 原始地址一旦出现在响应体里，就会顺着浏览器缓存、截图、共享看板流出去。
func TestClickListResponse(t *testing.T) {
	t.Parallel()

	const (
		code      = "det1234"
		manageKey = "detail-manage-key"
	)
	link := &domain.Link{
		ID:        uuid.NewV7(),
		ShortCode: code,
		Status:    domain.LinkStatusActive,
		KeyHash:   service.HashManageKey(manageKey),
	}

	occurred := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	repo := &stubClickRepo{
		events: []domain.ClickEvent{
			{
				ID:         9,
				EventUID:   "1700000000000-0",
				LinkID:     link.ID,
				ShortCode:  code,
				OccurredAt: occurred,
				Referer:    "https://news.example/post/1",
				UserAgent:  "Mozilla/5.0 (iPhone) Safari",
				IP:         "203.0.113.7",
				Device:     "mobile",
				Browser:    "safari",
				OS:         "ios",
			},
			{
				ID:         8,
				LinkID:     link.ID,
				ShortCode:  code,
				OccurredAt: occurred.Add(-time.Minute),
				IP:         "2001:db8:1234:5678:9abc:def0:1234:5678",
				Device:     "desktop",
			},
		},
		next: domain.ClickCursor{OccurredAt: occurred.Add(-time.Minute), ID: 8, Valid: true},
	}
	router := newClicksRouter(t, link, repo)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, clicksRequest(t, code, "limit=2", manageKey))

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200：%s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	// 最强的一条断言：原始地址不能以任何形式出现在响应体里
	for _, raw := range []string{"203.0.113.7", "2001:db8:1234:5678:9abc:def0:1234:5678"} {
		if strings.Contains(body, raw) {
			t.Fatalf("响应体里出现了未掩码的 IP %q：%s", raw, body)
		}
	}

	var got clickListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（%q）", err, body)
	}

	if len(got.Clicks) != 2 {
		t.Fatalf("明细条数 = %d，期望 2", len(got.Clicks))
	}
	if got.Clicks[0].IP != "203.0.113.0/24" {
		t.Errorf("IPv4 掩码 = %q，期望 203.0.113.0/24", got.Clicks[0].IP)
	}
	if got.Clicks[1].IP != "2001:db8:1234:5678::/64" {
		t.Errorf("IPv6 掩码 = %q，期望 2001:db8:1234:5678::/64", got.Clicks[1].IP)
	}
	if got.Clicks[0].Device != "mobile" || got.Clicks[0].Browser != "safari" || got.Clicks[0].OS != "ios" {
		t.Errorf("设备维度映射丢失：%+v", got.Clicks[0])
	}
	if got.Clicks[0].Referer != "https://news.example/post/1" {
		t.Errorf("referer = %q", got.Clicks[0].Referer)
	}
	if !got.Clicks[0].OccurredAt.Equal(occurred) {
		t.Errorf("occurred_at = %v，期望 %v", got.Clicks[0].OccurredAt, occurred)
	}
	if got.NextCursor == "" {
		t.Error("还有下一页时不该返回空游标")
	}
	if got.Days != service.DefaultStatsDays {
		t.Errorf("days = %d，期望默认 %d", got.Days, service.DefaultStatsDays)
	}
	// 窗口起点必须是 UTC 零点：前端要拿它显示「最近 N 天」的口径
	if h, m, s := got.Since.Clock(); got.Since.Location() != time.UTC || h != 0 || m != 0 || s != 0 {
		t.Errorf("since = %v，期望 UTC 零点整", got.Since)
	}

	// 查询参数原样传到仓储（分页与筛选的实际生效值）
	if len(repo.got) != 1 {
		t.Fatalf("仓储被查询 %d 次，期望 1 次", len(repo.got))
	}
	if q := repo.got[0]; q.Limit != 2 || q.Device != "" || q.Cursor.Valid {
		t.Errorf("仓储收到的查询 = %+v，期望 limit=2、无设备筛选、首页", q)
	}
}

// TestClickListValidation 钉住参数校验的错误码与字段名：
// 前端按 field 高亮输入框，按 code 决定提示文案。
func TestClickListValidation(t *testing.T) {
	t.Parallel()

	const (
		code      = "det1234"
		manageKey = "detail-manage-key"
	)
	link := &domain.Link{
		ID:        uuid.NewV7(),
		ShortCode: code,
		Status:    domain.LinkStatusActive,
		KeyHash:   service.HashManageKey(manageKey),
	}

	tests := []struct {
		name      string
		query     string
		wantCode  string
		wantField string
	}{
		{name: "limit 不是数字", query: "limit=abc", wantCode: "invalid_limit", wantField: "limit"},
		{name: "limit 为 0", query: "limit=0", wantCode: "invalid_limit", wantField: "limit"},
		{name: "limit 为负", query: "limit=-3", wantCode: "invalid_limit", wantField: "limit"},
		{name: "days 不是数字", query: "days=abc", wantCode: "invalid_days", wantField: "days"},
		{name: "days 为 0", query: "days=0", wantCode: "invalid_days", wantField: "days"},
		{name: "device 取值非法", query: "device=tv", wantCode: "invalid_device", wantField: "device"},
		{name: "cursor 非法", query: "cursor=%21%21%21", wantCode: "invalid_cursor", wantField: "cursor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &stubClickRepo{}
			router := newClicksRouter(t, link, repo)

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, clicksRequest(t, code, tt.query, manageKey))

			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d，期望 422：%s", rr.Code, rr.Body.String())
			}

			var body struct {
				Error struct {
					Code  string `json:"code"`
					Field string `json:"field"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON：%v（%q）", err, rr.Body.String())
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("错误码 = %q，期望 %q", body.Error.Code, tt.wantCode)
			}
			if body.Error.Field != tt.wantField {
				t.Errorf("错误字段 = %q，期望 %q", body.Error.Field, tt.wantField)
			}
			if len(repo.got) != 0 {
				t.Errorf("参数非法却查了仓储 %d 次", len(repo.got))
			}
		})
	}
}

// TestClickListRequiresPermission 明细带来源与设备指纹，属于「谁有权管理谁能看」的数据：
// 无凭据与无权限都必须 404（与详情、统计同一套语义），不能泄露短码是否存在。
func TestClickListRequiresPermission(t *testing.T) {
	t.Parallel()

	ownerID := uuid.NewV7()
	otherID := uuid.NewV7()
	link := &domain.Link{
		ID:        uuid.NewV7(),
		ShortCode: "det1234",
		Status:    domain.LinkStatusActive,
		OwnerID:   &ownerID,
	}

	tests := []struct {
		name      string
		manageKey string
	}{
		{name: "完全无凭据", manageKey: ""},
		{name: "错误的 manage key", manageKey: "wrong-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &stubClickRepo{}
			router := newClicksRouter(t, link, repo)

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, clicksRequest(t, link.ShortCode, "", tt.manageKey))

			if rr.Code != http.StatusNotFound {
				t.Fatalf("状态码 = %d，期望 404：%s", rr.Code, rr.Body.String())
			}
			if got := errCode(t, rr); got != "not_found" {
				t.Errorf("错误码 = %q，期望 not_found", got)
			}
			if len(repo.got) != 0 {
				t.Errorf("无权限却查了仓储 %d 次", len(repo.got))
			}
		})
	}

	// 有权限的对照用例：同一个路由、同一条链接，换成正确的密钥就该放行 ——
	// 否则上面的 404 可能只是「路由根本不通」的假象。
	t.Run("有权限放行", func(t *testing.T) {
		t.Parallel()

		owned := &domain.Link{
			ID:        uuid.NewV7(),
			ShortCode: "det1234",
			Status:    domain.LinkStatusActive,
			OwnerID:   &otherID,
			KeyHash:   service.HashManageKey("right-key"),
		}
		router := newClicksRouter(t, owned, &stubClickRepo{})

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, clicksRequest(t, owned.ShortCode, "", "right-key"))

		if rr.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200：%s", rr.Code, rr.Body.String())
		}
	})
}
