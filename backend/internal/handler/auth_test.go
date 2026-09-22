package handler

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"golang.org/x/crypto/bcrypt"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
)

// stubUsers 是内存版账号仓储。三个方法都必须真的实现 —— 登录用例会走到
// GetByEmail，若还像其它替身那样内嵌 nil 接口，用例只会 panic 而非断言失败。
type stubUsers struct {
	byEmail map[string]*domain.User
}

func newStubUsers(users ...*domain.User) *stubUsers {
	s := &stubUsers{byEmail: make(map[string]*domain.User, len(users))}
	for _, u := range users {
		s.byEmail[domain.NormalizeEmail(u.Email)] = u
	}
	return s
}

func (s *stubUsers) Create(_ context.Context, u *domain.User) error {
	key := domain.NormalizeEmail(u.Email)
	if _, ok := s.byEmail[key]; ok {
		return domain.Conflict("email", u.Email)
	}
	s.byEmail[key] = u
	return nil
}

func (s *stubUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	if u, ok := s.byEmail[email]; ok {
		return u, nil
	}
	return nil, domain.NotFound("user", email)
}

func (s *stubUsers) GetByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	for _, u := range s.byEmail {
		if u.ID.String() == id.String() {
			return u, nil
		}
	}
	return nil, domain.NotFound("user", id.String())
}

// mustTestUser 造一个账号，口令摘要用 MinCost：这些用例验证的是语义映射，
// 生产的 BcryptCost=12 会让每个用例白等 250ms。
func mustTestUser(t *testing.T, email, password string) *domain.User {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("造摘要失败：%v", err)
	}
	return &domain.User{ID: uuid.NewV7(), Email: email, PasswordHash: string(hash), DisplayName: "甲"}
}

// postJSON 发一个 JSON 请求给真实路由。
func postJSON(t *testing.T, router http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, r)
	return rr
}

// errMessage 解析统一错误体里的 message —— A8 的一半价值在文案上，必须断言。
func errMessage(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()

	var body httpx.ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（%q）", err, rr.Body.String())
	}
	return body.Error.Message
}

// TestRouterAPIErrorContract 钉住 A6：/api 命名空间里**任何**路径都要说 JSON，
// 包括两条由 net/http 内建产生、原本是纯文本的 404 / 405。
//
// 这个用例同时是对实现细节的守卫：改写靠的是「404/405 + Content-Type: text/plain」
// 这个组合（见 httpx.APIErrorContract 的注释）。Go 若改了内建错误的写法，
// 这里会红，而不是静默退回到纯文本。
func TestRouterAPIErrorContract(t *testing.T) {
	t.Parallel()

	router := newTestRouter(t, map[string]*domain.Link{})

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
		// allowContains 非空时断言 Allow 头（405 才需要）
		allowContains []string
	}{
		{
			name: "未注册的 /api 路径", method: http.MethodGet, path: "/api/does-not-exist",
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "深层未注册路径", method: http.MethodGet, path: "/api/nope/deep/path",
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "已注册路径 + 不支持的方法", method: http.MethodPut, path: "/api/links",
			status: http.StatusMethodNotAllowed, code: "method_not_allowed",
			// Allow 是 405 的全部价值：少了它客户端只能靠猜该用哪个方法
			allowContains: []string{"GET", "POST"},
		},
		{
			name: "子资源路径 + 不支持的方法", method: http.MethodPut, path: "/api/links/m0smoke",
			status: http.StatusMethodNotAllowed, code: "method_not_allowed",
			allowContains: []string{"GET", "PATCH", "DELETE"},
		},
		{
			name: "鉴权接口 + 不支持的方法", method: http.MethodDelete, path: "/api/auth/me",
			status: http.StatusMethodNotAllowed, code: "method_not_allowed",
			allowContains: []string{"GET"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil))

			if rr.Code != tt.status {
				t.Fatalf("%s %s → %d，期望 %d：%q", tt.method, tt.path, rr.Code, tt.status, rr.Body.String())
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q，期望 application/json —— 契约要求 /api 下永远是 JSON", ct)
			}
			if got := errCode(t, rr); got != tt.code {
				t.Errorf("error.code = %q，期望 %q", got, tt.code)
			}
			for _, want := range tt.allowContains {
				if !strings.Contains(rr.Header().Get("Allow"), want) {
					t.Errorf("Allow = %q，期望含 %q", rr.Header().Get("Allow"), want)
				}
			}
		})
	}
}

// TestRouterKeepsNonAPIErrorBehavior 守住 A6 的**作用域**：改写只发生在 /api 下。
//
// 非接口路径的 404/405 面向浏览器，保持 net/http 原样更合理；短码失效页
// 更必须是我们的 HTML 页面（它是产品界面，不是错误体）。
func TestRouterKeepsNonAPIErrorBehavior(t *testing.T) {
	t.Parallel()

	const code = "m0smoke"
	router := newTestRouter(t, map[string]*domain.Link{})

	t.Run("短码不存在仍回 HTML 失效页", func(t *testing.T) {
		t.Parallel()

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+code, nil))

		if rr.Code != http.StatusNotFound {
			t.Fatalf("状态码 %d，期望 404", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("Content-Type = %q，期望 HTML（这是产品界面，不是 JSON 错误体）", ct)
		}
	})

	t.Run("/healthz 的方法错误保持 net/http 原样", func(t *testing.T) {
		t.Parallel()

		// 用 PUT 而不是 POST：POST 会被 `POST /{code}` 兜底模式接走（那条模式
		// 匹配任意单段路径），根本走不到「方法不对」这一步。PUT 不在任何
		// 路由的方法集里，才是真正的内建 405 —— 与 TestRouterMethodAwareness 同一理由。
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/healthz", nil))

		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("状态码 %d，期望 405：%q", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("Content-Type = %q，期望 text/plain（非 /api 路径不参与改写）", ct)
		}
	})
}

// TestLoginHandlerErrors 钉住 A8 的三档语义。三者的下游动作不同，
// 所以错误码必须真的不同 —— 前端拿到 code 才知道该「改输入」「回登录页」。
func TestLoginHandlerErrors(t *testing.T) {
	t.Parallel()

	const (
		email    = "real@example.com"
		password = "correct-horse"
	)
	router := newTestRouterWith(t, map[string]*domain.Link{},
		newStubUsers(mustTestUser(t, email, password)))

	t.Run("邮箱格式不合法 → 422 字段级（与注册同口径）", func(t *testing.T) {
		t.Parallel()

		rr := postJSON(t, router, "/api/auth/login", `{"email":"bad","password":"whatever123"}`)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 %d，期望 422：%s", rr.Code, rr.Body.String())
		}
		if got := errCode(t, rr); got != "invalid_email" {
			t.Errorf("error.code = %q，期望 invalid_email", got)
		}
		if got := errField(t, rr); got != "email" {
			t.Errorf("error.field = %q，期望 email（前端靠它把提示挂到输入框）", got)
		}
	})

	t.Run("口令为空 → 422 field=password", func(t *testing.T) {
		t.Parallel()

		rr := postJSON(t, router, "/api/auth/login", `{"email":"real@example.com","password":""}`)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 %d，期望 422：%s", rr.Code, rr.Body.String())
		}
		if got := errField(t, rr); got != "password" {
			t.Errorf("error.field = %q，期望 password", got)
		}
	})

	t.Run("凭据错误 → 401 invalid_credentials，且两种失败无法区分", func(t *testing.T) {
		t.Parallel()

		wrongPassword := postJSON(t, router, "/api/auth/login", `{"email":"real@example.com","password":"wrong-horse"}`)
		unknownEmail := postJSON(t, router, "/api/auth/login", `{"email":"nobody@example.com","password":"correct-horse"}`)

		for name, rr := range map[string]*httptest.ResponseRecorder{
			"口令不对": wrongPassword, "邮箱不存在": unknownEmail,
		} {
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("%s：状态码 %d，期望 401：%s", name, rr.Code, rr.Body.String())
			}
			if got := errCode(t, rr); got != "invalid_credentials" {
				t.Errorf("%s：error.code = %q，期望 invalid_credentials", name, got)
			}
			if got := errMessage(t, rr); got != "邮箱或密码不正确" {
				t.Errorf("%s：message = %q，期望「邮箱或密码不正确」——"+
					"原先是「登录状态无效，请重新登录」，会把改密码的人引去清 cookie", name, got)
			}
		}

		// 防枚举：两种失败的对外表现必须一模一样
		if errMessage(t, wrongPassword) != errMessage(t, unknownEmail) {
			t.Error("两种凭据失败的文案不同 —— 登录页会变成账号枚举器")
		}
	})

	t.Run("未认证与凭据错误的 code 必须分开", func(t *testing.T) {
		t.Parallel()

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/auth/me", nil))

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 %d，期望 401", rr.Code)
		}
		if got := errCode(t, rr); got != "unauthorized" {
			t.Errorf("error.code = %q，期望 unauthorized（前端据此清空会话并送回登录页）", got)
		}
	})

	t.Run("正确凭据仍能登录", func(t *testing.T) {
		t.Parallel()

		rr := postJSON(t, router, "/api/auth/login", `{"email":"REAL@example.com","password":"correct-horse"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("状态码 %d，期望 200：%s", rr.Code, rr.Body.String())
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("响应不是合法 JSON：%v", err)
		}
		if body.Token == "" {
			t.Error("响应缺少 token")
		}
	})
}
