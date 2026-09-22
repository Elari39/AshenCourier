package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/service"
)

// newTestRouter 装配一份与生产同构的路由，只把仓储换成内存替身。
//
// 刻意走真实的 handler.Router：路由表是「契约」，用别的方式复刻一份就等于没测。
func newTestRouter(t *testing.T, links map[string]*domain.Link) http.Handler {
	t.Helper()

	return newTestRouterWith(t, links, newStubUsers())
}

// newTestRouterWith 是 newTestRouter 的可注入版本：需要登录成功（或需要
// 已有账号来验证「凭据错误」）的用例从它入手。
func newTestRouterWith(t *testing.T, links map[string]*domain.Link, users domain.UserRepository) http.Handler {
	t.Helper()

	repo := &stubLinkRepo{getByCode: func(_ context.Context, code string) (*domain.Link, error) {
		if l, ok := links[code]; ok {
			return l, nil
		}
		return nil, domain.NotFound("link", code)
	}}

	return Router(Options{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:        service.NewAuth(users, "test-secret-0123456789", time.Hour),
		Shortener:   newTestShortener(repo),
		Stats:       service.NewStats(&stubClicks{}, stubDelta{}),
		Health:      okProbe{},
		Unlock:      service.NewLinkUnlocker("test-secret-0123456789", time.Hour),
		Limiter:     nil, // 不限流：路由表用例不该被配额干扰
		TrustProxy:  false,
		PageSize:    20,
		MaxPageSize: 100,

		ClickPageSize:    20,
		MaxClickPageSize: 100,
	})
}

// TestRouterTable 用一张表钉住 README「API」一节的 16 条路由：
// 每条都要能被路由到（而不是 404），且鉴权层次符合文档 ——
// 这正好挡住「改了路由忘了改文档」和「requireUser 写成 optionalAuth」两类回归。
func TestRouterTable(t *testing.T) {
	t.Parallel()

	const code = "m0smoke"
	links := map[string]*domain.Link{
		code: {
			ID:        uuid.NewV7(),
			ShortCode: code,
			TargetURL: "https://example.com/self-check",
			Status:    domain.LinkStatusActive,
		},
	}
	router := newTestRouter(t, links)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		// why 写清「为什么是这个状态码」，否则将来只改数字不改理由
		why string
	}{
		{
			name: "1 GET /healthz", method: http.MethodGet, path: "/healthz",
			wantStatus: http.StatusOK,
			why:        "基础设施探针，无鉴权",
		},
		{
			name: "2 POST /api/auth/register", method: http.MethodPost, path: "/api/auth/register",
			wantStatus: http.StatusBadRequest,
			why:        "公开接口：空请求体走到解码层（400）就说明没被鉴权挡住",
		},
		{
			name: "3 POST /api/auth/login", method: http.MethodPost, path: "/api/auth/login",
			wantStatus: http.StatusBadRequest,
			why:        "同上",
		},
		{
			name: "4 GET /api/auth/me", method: http.MethodGet, path: "/api/auth/me",
			wantStatus: http.StatusUnauthorized,
			why:        "requireUser：无令牌一律 401",
		},
		{
			name: "5 POST /api/links", method: http.MethodPost, path: "/api/links",
			wantStatus: http.StatusBadRequest,
			why:        "optionalAuth：匿名也能到解码层（不能是 401）",
		},
		{
			name: "6 GET /api/links", method: http.MethodGet, path: "/api/links",
			wantStatus: http.StatusUnauthorized,
			why:        "requireUser：只有登录用户能列自己的链接",
		},
		{
			name: "7 GET /api/links/{code}", method: http.MethodGet, path: "/api/links/" + code,
			wantStatus: http.StatusNotFound,
			why:        "optionalAuth + forDetail：无权限/无凭据都回 404，不泄露存在性",
		},
		{
			name: "8 PATCH /api/links/{code}", method: http.MethodPatch, path: "/api/links/" + code,
			wantStatus: http.StatusForbidden,
			why:        "非 detail 路径：无权限报 403（与详情的 404 分工见 loadAuthorized 注释）",
		},
		{
			name: "9 DELETE /api/links/{code}", method: http.MethodDelete, path: "/api/links/" + code,
			wantStatus: http.StatusForbidden,
			why:        "同 PATCH：改/删走 403，查询类走 404",
		},
		{
			name: "10 GET /api/links/{code}/stats", method: http.MethodGet, path: "/api/links/" + code + "/stats",
			wantStatus: http.StatusNotFound,
			why:        "统计与详情同一套鉴权（无权限 404）",
		},
		{
			name: "11 POST /api/links/{code}/claim", method: http.MethodPost, path: "/api/links/" + code + "/claim",
			wantStatus: http.StatusUnauthorized,
			why:        "认领必须登录（requireUser）",
		},
		{
			name: "12 GET /{code}", method: http.MethodGet, path: "/" + code,
			wantStatus: http.StatusFound,
			why:        "跳转兜底模式：命中的短码回 302",
		},
		{
			name: "13 GET /api/links/{code}/clicks", method: http.MethodGet, path: "/api/links/" + code + "/clicks",
			wantStatus: http.StatusNotFound,
			why:        "点击明细与详情/统计同一套鉴权（无权限 404）",
		},
		{
			name: "12b GET /login 不被当成短码", method: http.MethodGet, path: "/login",
			wantStatus: http.StatusNotFound,
			why:        "保留字在 handler 内再排一次（nginx 之外的第二道保险）",
		},
		{
			name: "14 POST /{code}", method: http.MethodPost, path: "/" + code,
			wantStatus: http.StatusSeeOther,
			why:        "口令校验入口：没设口令的链接直接 303 回 GET（计点击的是随后那个 GET，不会重复计）",
		},
		{
			name: "15 GET /api/links/{code}/qr.svg", method: http.MethodGet, path: "/api/links/" + code + "/qr.svg",
			wantStatus: http.StatusOK,
			why:        "二维码图片**公开可读**（不挂 optionalUser/requireUser）：它的内容就是 short_url，要能被邮件与印刷品直接引用",
		},
		{
			name: "16 GET /metrics", method: http.MethodGet, path: "/metrics",
			wantStatus: http.StatusOK,
			why:        "Prometheus 文本端点：**不挂鉴权也不挂限流**（它只在内网可达，公网由 nginx 的 `= /metrics` 挡掉）；探针异常时仍回 200，故障由 ashen_*_up 0 表达",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
			router.ServeHTTP(rr, r)

			if rr.Code != tt.wantStatus {
				t.Fatalf("%s %s → %d，期望 %d（%s）：%s",
					tt.method, tt.path, rr.Code, tt.wantStatus, tt.why, rr.Body.String())
			}
		})
	}
}

// TestRouterMethodAwareness 用「不支持的方法必须回 405 而不是 404」来证明
// 每条路径都以 method-aware 模式注册 —— 405 与 404 的区别正是
// 「路径存在但方法不对」与「路径根本不存在」。
func TestRouterMethodAwareness(t *testing.T) {
	t.Parallel()

	router := newTestRouter(t, map[string]*domain.Link{})

	paths := []string{
		"/healthz",
		"/metrics",
		"/api/auth/register",
		"/api/auth/login",
		"/api/auth/me",
		"/api/links",
		"/api/links/m0smoke",
		"/api/links/m0smoke/stats",
		"/api/links/m0smoke/clicks",
		"/api/links/m0smoke/qr.svg",
		"/api/links/m0smoke/claim",
		"/m0smoke",
	}

	// PUT 不在任何路由的方法集里；OPTIONS 不用，那会被 CORS 中间件按预检接管
	for _, path := range paths {
		t.Run("PUT "+path, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, path, nil)
			router.ServeHTTP(rr, r)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("PUT %s → %d，期望 405（路径未注册时才会是 404）", path, rr.Code)
			}
		})
	}
}
