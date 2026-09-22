package httpx

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAPIErrorContractRewritesMuxErrors 用的是**真实的 ServeMux**，不是手写的模仿。
//
// 这一点刻意的：被改写的正是 net/http 内建错误（`Error()` 先设 text/plain 再
// WriteHeader），整个实现都建立在这个写法上。用假 handler 复刻一份，
// 就等于把「Go 换了写法会发现」这条保证也一起假掉了。
func TestAPIErrorContractRewritesMuxErrors(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/known", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]string{"ok": "1"})
	})
	// 自己回的 404：状态码与内建 404 相同，只有 Content-Type 不同
	mux.HandleFunc("GET /api/own404", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, "not_found", "未找到该资源", "")
	})
	// 短码失效页那种 HTML 404（本包之外的真实形态）
	mux.HandleFunc("GET /api/ownhtml404", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<!DOCTYPE html><html>页面不存在</html>"))
	})
	mux.HandleFunc("GET /plain", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]string{"ok": "1"})
	})

	handler := APIErrorContract(mux)

	tests := []struct {
		name string
		// path 是请求路径
		method string
		path   string
		// wantStatus / wantCode 是改写后的期望；wantCode 为空表示「不该被改写」
		wantStatus int
		wantCode   string
		// wantContentType 为空表示只断言「不是 application/json」
		wantContentType string
		wantAllow       []string
	}{
		{
			name:   "未注册的 /api 路径 → 统一 JSON 404",
			method: http.MethodGet, path: "/api/does-not-exist",
			wantStatus: http.StatusNotFound, wantCode: "not_found",
			wantContentType: contentTypeJSON,
		},
		{
			name:   "路径注册了但方法不对 → 统一 JSON 405，且保留 Allow",
			method: http.MethodPut, path: "/api/known",
			wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed",
			wantContentType: contentTypeJSON, wantAllow: []string{"GET"},
		},
		{
			name:   "深层未注册路径同样改写",
			method: http.MethodDelete, path: "/api/nope/deep/path",
			wantStatus: http.StatusNotFound, wantCode: "not_found",
			wantContentType: contentTypeJSON,
		},
		{
			name:   "本包自己的 JSON 404 不能被误伤",
			method: http.MethodGet, path: "/api/own404",
			wantStatus: http.StatusNotFound, wantCode: "not_found",
			wantContentType: contentTypeJSON,
		},
		{
			name:   "HTML 404（短码失效页那种）不能被改成 JSON",
			method: http.MethodGet, path: "/api/ownhtml404",
			wantStatus:      http.StatusNotFound,
			wantContentType: "text/html; charset=utf-8",
		},
		{
			name:   "2xx 原样透传",
			method: http.MethodGet, path: "/api/known",
			wantStatus: http.StatusOK, wantContentType: contentTypeJSON,
		},
		{
			name:   "/apifoo 不在命名空间内（前缀相同但不是同一段）",
			method: http.MethodGet, path: "/apifoo",
			wantStatus:      http.StatusNotFound,
			wantContentType: "text/plain; charset=utf-8",
		},
		{
			name:   "/api 之外的内建错误不改写（作用域是刻意的）",
			method: http.MethodPost, path: "/plain",
			wantStatus:      http.StatusMethodNotAllowed,
			wantContentType: "text/plain; charset=utf-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil))

			if rr.Code != tt.wantStatus {
				t.Fatalf("%s %s → %d，期望 %d：%q", tt.method, tt.path, rr.Code, tt.wantStatus, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); got != tt.wantContentType {
				t.Errorf("Content-Type = %q，期望 %q", got, tt.wantContentType)
			}
			for _, method := range tt.wantAllow {
				if !strings.Contains(rr.Header().Get("Allow"), method) {
					t.Errorf("405 的 Allow = %q，期望含 %q —— 它是客户端唯一能知道「该用哪个方法」的地方",
						rr.Header().Get("Allow"), method)
				}
			}

			if tt.wantContentType != contentTypeJSON {
				return
			}
			// 关键：整段 body 必须是**一个**合法 JSON —— 若没吞掉那句纯文本，
			// 这里会以「JSON 后跟垃圾」的形式解析失败。
			var body ErrorBody
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON（纯文本没被吞掉？）：%v（%q）", err, rr.Body.String())
			}
			if tt.wantCode != "" && body.Error.Code != tt.wantCode {
				t.Errorf("error.code = %q，期望 %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}

// TestAPIErrorWriterSwallowsFollowUps 直接盯住改写后的收尾动作：
// net/http 的 Error() 在 WriteHeader 之后还会 Write 一次纯文本，
// 若那次没被吞掉，响应体就是「JSON + 纯文本」的拼接。
// 这里连重复 WriteHeader 一起覆盖（第二次不能真的写下去）。
func TestAPIErrorWriterSwallowsFollowUps(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/x", nil)
	w := &apiErrorWriter{ResponseWriter: rr, req: req}

	// 复刻 http.Error 的写法
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if _, err := w.Write([]byte("404 page not found\n")); err != nil {
		t.Fatalf("被吞掉的那次 Write 不该报错：%v", err)
	}
	w.WriteHeader(http.StatusInternalServerError) // 必须被忽略，否则记录器会看到 500

	if rr.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404（改写后重复的 WriteHeader 必须被丢弃）", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "page not found") {
		t.Fatalf("纯文本没被吞掉：%q", rr.Body.String())
	}

	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON：%v（%q）", err, rr.Body.String())
	}
	if body.Error.Code != "not_found" {
		t.Errorf("error.code = %q，期望 not_found", body.Error.Code)
	}
}

// TestAPIErrorWriterPassesThroughFlush 守住 Flush 透传：
// 不实现它的话，http.ResponseController 在本层会拿到 ErrNotSupported。
func TestAPIErrorWriterPassesThroughFlush(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	w := &apiErrorWriter{ResponseWriter: rr}
	w.Flush()

	if !rr.Flushed {
		t.Fatal("Flush 没有透传到底层 writer")
	}
	if got := w.Unwrap(); got != http.ResponseWriter(rr) {
		t.Fatal("Unwrap 没有返回底层 writer")
	}
}
