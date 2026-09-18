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
	shortener  *service.Shortener
	trustProxy bool
}

// serve 处理 GET /{code}。
func (h *redirectHandler) serve(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// 双保险：nginx 的 location 正则已经挡了一层形态校验，
	// 这里再排一次保留字，避免 /api、/login 之类被当成短码解析。
	if !shortcode.IsValidShape(code) || shortcode.IsReserved(code) {
		h.notFound(w, r)
		return
	}

	link, err := h.shortener.Resolve(r.Context(), code)
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
