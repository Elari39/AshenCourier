package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCORS 守住两条容易被缓存放大的细节：
//   - Vary: Origin 必须在「收到 Origin」时就写，而不是只在白名单命中时写
//   - 预检只对 /api/ 生效，短码路径的 OPTIONS 要照常回 405，而不是假的 204
func TestCORS(t *testing.T) {
	t.Parallel()

	const allowedOrigin = "https://sho.rt"
	const weirdOrigin = "https://evil.example"

	handler := func() http.Handler {
		mux := http.NewServeMux()
		mux.Handle("GET /api/links", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		mux.Handle("GET /{code}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusFound)
		}))
		return CORS(allowedOrigin)(mux)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		wantStatus int
		wantOrigin string // Access-Control-Allow-Origin
		wantVary   string // Vary
	}{
		{
			name:       "白名单来源回显 ACAO 且带 Vary",
			method:     http.MethodGet,
			path:       "/api/links",
			origin:     allowedOrigin,
			wantStatus: http.StatusOK,
			wantOrigin: allowedOrigin,
			wantVary:   "Origin",
		},
		{
			name:       "非白名单来源不回显但仍要 Vary",
			method:     http.MethodGet,
			path:       "/api/links",
			origin:     weirdOrigin,
			wantStatus: http.StatusOK,
			wantOrigin: "",
			wantVary:   "Origin",
		},
		{
			name:       "没有 Origin 就不加 Vary",
			method:     http.MethodGet,
			path:       "/api/links",
			origin:     "",
			wantStatus: http.StatusOK,
			wantOrigin: "",
			wantVary:   "",
		},
		{
			name:       "/api 下的预检回 204 且给出允许的方法与头",
			method:     http.MethodOptions,
			path:       "/api/links",
			origin:     allowedOrigin,
			wantStatus: http.StatusNoContent,
			wantOrigin: allowedOrigin,
			wantVary:   "Origin",
		},
		{
			// 白名单来源仍会拿到 ACAO（中间件不区分 /api 与非 /api），
			// 但状态是 405 而不是 204 —— 浏览器只认 2xx 的预检，所以照样会拦住，
			// 而「这个路径不支持 OPTIONS」这个事实被如实告诉调用方。
			name:       "短码路径的 OPTIONS 交给 mux（405）",
			method:     http.MethodOptions,
			path:       "/abc1234",
			origin:     allowedOrigin,
			wantStatus: http.StatusMethodNotAllowed,
			wantOrigin: allowedOrigin,
			wantVary:   "Origin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()

			handler().ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tc.wantOrigin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantOrigin)
			}
			if got := rr.Header().Get("Vary"); got != tc.wantVary {
				t.Fatalf("Vary = %q, want %q", got, tc.wantVary)
			}
			if tc.wantOrigin != "" && rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatal("白名单命中时应带 Access-Control-Allow-Credentials: true")
			}
		})
	}
}

// TestCORSAllowsExtraHeaders 顺带确认预检里暴露的请求头没漏（前端要发 X-Manage-Key）。
func TestCORSPreflightHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodOptions, "/api/links", nil)
	req.Header.Set("Origin", "https://sho.rt")
	rr := httptest.NewRecorder()

	CORS("https://sho.rt")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("预检不该走到下游 handler")
	})).ServeHTTP(rr, req)

	for _, want := range []string{"X-Manage-Key", "Authorization", "X-Request-Id"} {
		got := rr.Header().Get("Access-Control-Allow-Headers")
		if !strings.Contains(got, want) {
			t.Fatalf("Access-Control-Allow-Headers 里缺少 %q：%q", want, got)
		}
	}
	if rr.Header().Get("Access-Control-Max-Age") == "" {
		t.Fatal("预检应给出 Access-Control-Max-Age")
	}
}
