package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/service"
)

// 受保护链接的夹具：真 Shortener + 真 LinkUnlocker，只把仓储换成内存替身
// （与其它 handler 测试同一套路 —— 鉴权与口令语义都走生产代码）。
const (
	lockedCode     = "lock123"
	lockedPlain    = "smoke-pass-9f3a"
	lockedSecret   = "test-secret-0123456789"
	lockedLifetime = 30 * time.Minute
)

// newRedirectFor 造一个只认识单条链接的 redirectHandler。
func newRedirectFor(t *testing.T, link *domain.Link, secureCookies bool) *redirectHandler {
	t.Helper()

	repo := &stubLinkRepo{getByCode: func(_ context.Context, code string) (*domain.Link, error) {
		if code == link.ShortCode {
			return link, nil
		}
		return nil, domain.NotFound("link", code)
	}}

	return &redirectHandler{
		shortener:     newTestShortener(repo),
		unlocker:      service.NewLinkUnlocker(lockedSecret, lockedLifetime),
		secureCookies: secureCookies,
	}
}

// lockedHash 只算一次 bcrypt 摘要。
//
// cost 12 在 -race 下要几秒一次：本文件十来个用例都要一条「带口令的链接」，
// 每个都重算一遍会让这个包的单测从秒级变成分钟级（而 bcrypt 的耗时本身
// 由 service 层的用例守着，这里不需要重复证明）。
var (
	lockedHashOnce sync.Once
	lockedHashVal  string
	lockedHashErr  error
)

func lockedHash(t *testing.T) string {
	t.Helper()

	lockedHashOnce.Do(func() { lockedHashVal, lockedHashErr = service.HashLinkPassword(lockedPlain) })
	if lockedHashErr != nil {
		t.Fatalf("准备摘要: %v", lockedHashErr)
	}
	return lockedHashVal
}

// lockedLink 造一条带口令的短链。
func lockedLink(t *testing.T) *domain.Link {
	t.Helper()

	return &domain.Link{
		ID:                uuid.NewV7(),
		ShortCode:         lockedCode,
		TargetURL:         "https://example.com/locked",
		Status:            domain.LinkStatusActive,
		PasswordHash:      lockedHash(t),
		PasswordProtected: true,
	}
}

// getRedirect 构造一次 GET /{code}。
func getRedirect(t *testing.T, code string, cookie *http.Cookie) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+code, nil)
	r.SetPathValue("code", code)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

// postPassword 构造一次表单 POST /{code}。
func postPassword(t *testing.T, code, password string) *http.Request {
	t.Helper()

	form := url.Values{"password": {password}}.Encode()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/"+code, strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("code", code)
	return r
}

// TestRedirectServesPasswordPage 守住「未解锁 ⇒ 200 口令页，而不是 302，也不计点击」。
func TestRedirectServesPasswordPage(t *testing.T) {
	t.Parallel()

	h := newRedirectFor(t, lockedLink(t), false)

	t.Run("没有 cookie：200 + HTML，不跳转也不种 cookie", func(t *testing.T) {
		rr := httptest.NewRecorder()
		h.serve(rr, getRedirect(t, lockedCode, nil))

		if rr.Code != http.StatusOK {
			t.Fatalf("状态码 %d，期望 200（这是一张正常内容页，不是「请求失败」）", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q，期望 text/html", ct)
		}
		if loc := rr.Header().Get("Location"); loc != "" {
			t.Errorf("不该有 Location，实际 %q", loc)
		}
		if len(rr.Result().Cookies()) != 0 {
			t.Error("还没解锁成功就不该种 cookie")
		}
		body := rr.Body.String()
		if !strings.Contains(body, "<form") || !strings.Contains(body, "解锁并跳转") {
			t.Errorf("页面里应当有口令表单：%s", body)
		}
		// no-store：页面状态取决于本次请求是否已解锁，不能被任何中间层缓存复用
		if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("Cache-Control = %q，期望含 no-store", cc)
		}
	})

	t.Run("带着本链接的有效 cookie：302 放行", func(t *testing.T) {
		cookie := &http.Cookie{Name: unlockCookie, Value: h.unlocker.Issue(lockedCode, time.Now())}

		rr := httptest.NewRecorder()
		h.serve(rr, getRedirect(t, lockedCode, cookie))

		if rr.Code != http.StatusFound {
			t.Fatalf("状态码 %d，期望 302", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "https://example.com/locked" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("cookie 是给别的短码签的：仍要口令", func(t *testing.T) {
		cookie := &http.Cookie{Name: unlockCookie, Value: h.unlocker.Issue("othercode", time.Now())}

		rr := httptest.NewRecorder()
		h.serve(rr, getRedirect(t, lockedCode, cookie))

		if rr.Code != http.StatusOK {
			t.Fatalf("状态码 %d，期望 200（口令凭据必须与短码绑定）", rr.Code)
		}
	})

	t.Run("cookie 被篡改：仍要口令", func(t *testing.T) {
		valid := h.unlocker.Issue(lockedCode, time.Now())
		cookie := &http.Cookie{Name: unlockCookie, Value: valid[:len(valid)-2] + "zz"}

		rr := httptest.NewRecorder()
		h.serve(rr, getRedirect(t, lockedCode, cookie))

		if rr.Code != http.StatusOK {
			t.Fatalf("状态码 %d，期望 200（签名不符一律视为未解锁，不报错页也不 500）", rr.Code)
		}
	})

	t.Run("没设口令的链接：照常 302", func(t *testing.T) {
		open := &domain.Link{
			ID: uuid.NewV7(), ShortCode: lockedCode,
			TargetURL: "https://example.com/open", Status: domain.LinkStatusActive,
		}
		h := newRedirectFor(t, open, false)

		rr := httptest.NewRecorder()
		h.serve(rr, getRedirect(t, lockedCode, nil))

		if rr.Code != http.StatusFound {
			t.Fatalf("状态码 %d，期望 302", rr.Code)
		}
	})
}

// TestRedirectUnlockPost 覆盖 POST /{code} 的成功与失败分支。
func TestRedirectUnlockPost(t *testing.T) {
	t.Parallel()

	t.Run("口令正确：303 + 种下合法 cookie", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, lockedCode, lockedPlain))

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("状态码 %d，期望 303（POST 之后必须换成 GET，否则刷新会重放提交）", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "/"+lockedCode {
			t.Errorf("Location = %q，期望 /%s", loc, lockedCode)
		}

		cookies := rr.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("应当种下且只种下 1 个 cookie，实际 %d", len(cookies))
		}
		c := cookies[0]
		if c.Name != unlockCookie {
			t.Errorf("cookie 名 = %q，期望 %q", c.Name, unlockCookie)
		}
		if c.Path != "/" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie 属性不全：path=%q httpOnly=%v sameSite=%v", c.Path, c.HttpOnly, c.SameSite)
		}
		if c.Secure {
			t.Error("http 部署下不能带 Secure：浏览器会直接拒收，表现为「口令输对了也跳不过去」")
		}
		if err := h.unlocker.Verify(lockedCode, c.Value, time.Now()); err != nil {
			t.Errorf("种下的 cookie 必须是合法凭据：%v", err)
		}
		if c.MaxAge != int(lockedLifetime.Seconds()) {
			t.Errorf("MaxAge = %d，期望 %d", c.MaxAge, int(lockedLifetime.Seconds()))
		}
	})

	t.Run("https 部署：cookie 带 Secure", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), true)

		// 直接调 setUnlockCookie：这条用例只关心 cookie 属性，
		// 没必要为了它再跑一次 bcrypt（口令校验的成败已由上面两条覆盖）
		rr := httptest.NewRecorder()
		h.setUnlockCookie(rr, lockedCode)

		cookies := rr.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("应当种下 1 个 cookie，实际 %d", len(cookies))
		}
		if !cookies[0].Secure {
			t.Error("https 部署下 cookie 必须带 Secure")
		}
	})

	t.Run("口令错误：401 + 重新渲染口令页 + 不种 cookie", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, lockedCode, "wrong-guess"))

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 %d，期望 401", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q：失败也要给用户一张口令页，而不是 JSON 错误码", ct)
		}
		if !strings.Contains(rr.Body.String(), "口令不对") {
			t.Error("页面里应当有错误提示")
		}
		if len(rr.Result().Cookies()) != 0 {
			t.Error("口令错误不该种 cookie")
		}
	})

	t.Run("没设口令的链接：303（照常回 GET，由那次 GET 计一次点击）", func(t *testing.T) {
		open := &domain.Link{
			ID: uuid.NewV7(), ShortCode: lockedCode,
			TargetURL: "https://example.com/open", Status: domain.LinkStatusActive,
		}
		h := newRedirectFor(t, open, false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, lockedCode, ""))

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("状态码 %d，期望 303", rr.Code)
		}
	})

	t.Run("不存在的短码：404", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, "missing1", lockedPlain))

		if rr.Code != http.StatusNotFound {
			t.Fatalf("状态码 %d，期望 404", rr.Code)
		}
	})

	t.Run("已删除的短码：404（不显示口令页）", func(t *testing.T) {
		deleted := lockedLink(t)
		deleted.Status = domain.LinkStatusDeleted
		h := newRedirectFor(t, deleted, false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, lockedCode, lockedPlain))

		if rr.Code != http.StatusNotFound {
			t.Fatalf("状态码 %d，期望 404（死链绝不该提示「需要口令」）", rr.Code)
		}
	})

	t.Run("保留字：404（与 GET 同源的形态校验）", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, "login", lockedPlain))

		if rr.Code != http.StatusNotFound {
			t.Fatalf("状态码 %d，期望 404", rr.Code)
		}
	})

	t.Run("请求体超过 1 KiB：413", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), false)

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, lockedCode, strings.Repeat("a", 2048)))

		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("状态码 %d，期望 413", rr.Code)
		}
	})

	t.Run("解锁器未装配：500（fail-closed，绝不静默放行）", func(t *testing.T) {
		h := newRedirectFor(t, lockedLink(t), false)
		h.unlocker = nil

		rr := httptest.NewRecorder()
		h.unlock(rr, postPassword(t, lockedCode, lockedPlain))

		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 %d，期望 500（装配缺失是配置错误，必须吵）", rr.Code)
		}
	})
}
