package handler

import (
	"errors"
	"net/http"
	"time"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
	"ashen-courier/internal/pkg/shortcode"
	"ashen-courier/internal/service"
)

// redirectHandler 处理 GET /{code} 的 302 跳转。
//
// 这是全站唯一的超热路径，设计目标：
//   - 全程只读 Redis（PG 仅在缓存 miss 时回源），不写任何数据库
//   - 统计写入投进有界队列后立即返回，队列满就丢，绝不为统计牺牲跳转
type redirectHandler struct {
	shortener *service.Shortener
	// unlocker 签发/校验「已解锁」凭据。nil 表示未装配 —— 那时带口令的链接一律不放行，
	// 属于 fail-closed：装配缺失是配置错误，绝不能因此把口令当成没设。
	unlocker   *service.LinkUnlocker
	trustProxy bool
	// secureCookies 为 true 时解锁 cookie 带 Secure（仅 https 部署）。
	// 由 PUBLIC_BASE_URL 的 scheme 决定：写死 Secure 会让 http://localhost:8080
	// 与局域网 IP 永远解锁不了（浏览器直接拒收）。
	secureCookies bool
}

// unlockCookie 是「已解锁」凭据的 cookie 名。
const unlockCookie = "ac_unlock"

// maxUnlockBody 限制口令表单的请求体。表单只有一个字段，1 KiB 绰绰有余。
const maxUnlockBody = 1 << 10

// serve 处理 GET /{code}。
func (h *redirectHandler) serve(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// 双保险：nginx 的 location 正则已经挡了一层形态校验，
	// 这里再排一次保留字，避免 /api、/login 之类被当成短码解析。
	if !shortcode.IsValidShape(code) || shortcode.IsReserved(code) {
		h.notFound(w, r)
		return
	}

	// 带上 Host：同一个短码在不同域下是两条不同的短链，解析必须按域隔离
	link, err := h.shortener.Resolve(r.Context(), r.Host, code)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			h.notFound(w, r)
		case errors.Is(err, domain.ErrGone):
			h.gone(w, r)
		default:
			// 依赖不可用 / 脏数据：跳转路径也回 JSON 错误体，
			// 便于 curl 与冒烟工具断言
			httpx.WriteDomainError(w, r, err)
		}
		return
	}

	// 口令闸门放在 Redirectable 之后：死链（已删除 / 已停用 / 已过期）永远不该显示口令页，
	// 否则等于告诉探测者「这个短码存在，而且被保护」。
	if link.HasPassword() && !h.unlocked(r, code) {
		// 口令页不是跳转，因此**不计点击** —— 统计只认真正的跳转。
		// 状态码用 200：这是一张正常内容页，不是「请求失败」。
		writePasswordPage(w, r, code, "", http.StatusOK)
		return
	}

	// 302 而非 301：301 会被浏览器永久缓存，之后再也拿不到统计，目标地址也无法修改。
	w.Header().Set("Location", link.TargetURL)
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Referrer-Policy", "no-referrer-when-downgrade")
	w.WriteHeader(http.StatusFound)

	// 响应已发出，再做统计 —— 队列满就直接丢，不影响已经写出的 302
	h.shortener.RecordClick(domain.ClickRecord{
		Code:       link.ShortCode,
		LinkID:     link.ID,
		OccurredAt: time.Now().UTC(),
		IP:         httpx.ClientIP(r, h.trustProxy),
		UserAgent:  r.UserAgent(),
		Referer:    r.Referer(),
	})
}

// notFound 渲染 404 短码失效页。
func (h *redirectHandler) notFound(w http.ResponseWriter, r *http.Request) {
	writeErrorPage(w, r, errorPageData{
		Code:    http.StatusNotFound,
		Title:   "这条短链不存在",
		Message: "它可能从未被创建，也可能已经被删除。检查一下短码是否拼错了？",
	})
}

// gone 渲染 410 短码失效页。
func (h *redirectHandler) gone(w http.ResponseWriter, r *http.Request) {
	writeErrorPage(w, r, errorPageData{
		Code:    http.StatusGone,
		Title:   "这条短链已经失效",
		Message: "它已过期或被所有者停用，不再对外跳转。",
	})
}

// unlock 处理 POST /{code}：校验访问口令，成功则 303 回 GET /{code}。
//
// 为什么是 303 而不是 302：POST 返回 302 时浏览器可能用 POST 重放 Location；
// 303 See Other 明确「换个 GET 去取」。于是解锁本身不计点击 —— 记点击的是随后那个
// GET，一次解锁恰好 +1，而不是 +2。
func (h *redirectHandler) unlock(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// 与 GET 同源的形态校验：保留字与非法形态在这里同样不该被当成短码
	if !shortcode.IsValidShape(code) || shortcode.IsReserved(code) {
		h.notFound(w, r)
		return
	}
	if h.unlocker == nil {
		// 装配缺失是配置错误。宁可 500 也不静默放行 —— 这类 bug 必须吵。
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal", "解锁器未装配", "")
		return
	}

	// 没有它，一个超大表单会被完整读进内存
	r.Body = http.MaxBytesReader(w, r.Body, maxUnlockBody)
	if err := r.ParseForm(); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "请求体过大", "")
			return
		}
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_form", "口令表单无法解析", "password")
		return
	}

	// 与 Resolve 用同一个 Host：否则可以从 A 域提交口令去解锁一条属于 B 域的短链
	err := h.shortener.VerifyPassword(r.Context(), r.Host, code, r.PostFormValue("password"))
	switch {
	case err == nil:
		h.setUnlockCookie(w, code)
		// 303 + 相对 Location：让浏览器改用 GET 回到同一个地址
		// （那一次才是真正的跳转，也只记一次点击）
		w.Header().Set("Location", "/"+code)
		w.Header().Set("Cache-Control", "no-store, private")
		w.WriteHeader(http.StatusSeeOther)
	case errors.Is(err, domain.ErrUnauthorized):
		// 401 且重新渲染口令页：用户看到的是「口令不对」，而不是一页错误码
		writePasswordPage(w, r, code, "口令不对，请再试一次。", http.StatusUnauthorized)
	case errors.Is(err, domain.ErrNotFound):
		h.notFound(w, r)
	case errors.Is(err, domain.ErrGone):
		h.gone(w, r)
	default:
		httpx.WriteDomainError(w, r, err)
	}
}

// unlocked 判断本次请求是否带着有效的解锁凭据。
func (h *redirectHandler) unlocked(r *http.Request, code string) bool {
	if h.unlocker == nil {
		return false
	}
	cookie, err := r.Cookie(unlockCookie)
	if err != nil {
		return false
	}
	return h.unlocker.Verify(code, cookie.Value, time.Now()) == nil
}

// setUnlockCookie 下发「已解锁」cookie。
//
// 一个浏览器只记一条链接的解锁状态（code 在签名体里）：不为每条链接种一个 cookie，
// 否则 cookie 数量会随浏览过的受保护链接无界增长。
func (h *redirectHandler) setUnlockCookie(w http.ResponseWriter, code string) {
	http.SetCookie(w, &http.Cookie{
		Name:     unlockCookie,
		Value:    h.unlocker.Issue(code, time.Now()),
		Path:     "/",
		MaxAge:   int(h.unlocker.TTL().Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookies,
		// Lax：跨站子请求不会带上它，也不影响「从聊天工具点开短链」这种顶级导航
		SameSite: http.SameSiteLaxMode,
	})
}
