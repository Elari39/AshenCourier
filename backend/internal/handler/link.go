package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// linkHandler 处理 /api/links*。
type linkHandler struct {
	shortener   *service.Shortener
	trustProxy  bool
	pageSize    int
	maxPageSize int
	// deltas 读取列表页尚未回刷进 PG 的计数增量；nil 表示不叠加（只报 PG 基线）。
	deltas domain.ClickDeltaBatchReader
}

// create 处理 POST /api/links。登录与匿名均可调用。
func (h *linkHandler) create(w http.ResponseWriter, r *http.Request) {
	req, ok := httpx.DecodeJSON[createLinkRequest](w, r)
	if !ok {
		return
	}

	in := service.CreateInput{
		TargetURL:  req.TargetURL,
		CustomCode: req.CustomCode,
		Title:      req.Title,
		ExpiresAt:  req.ExpiresAt,
		ClientIP:   httpx.ClientIP(r, h.trustProxy),
	}
	if id, authed := userIDFrom(r.Context()); authed {
		in.OwnerID = &id
	}

	result, err := h.shortener.Create(r.Context(), in)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusCreated, createLinkResponse{
		Link:      toLinkDTO(result.Link, result.ShortURL),
		ManageKey: result.ManageKey,
	})
}

// list 处理 GET /api/links。
func (h *linkHandler) list(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFrom(r.Context())
	if !ok {
		httpx.WriteUnauthorized(w, r, "请先登录")
		return
	}

	query := r.URL.Query()
	limit := h.pageSize
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_limit", "limit 必须是正整数", "limit")
			return
		}
		limit = n
	}

	links, nextCursor, err := h.shortener.List(r.Context(), id, query.Get("q"), limit, query.Get("cursor"), h.maxPageSize)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}

	// 叠加尚未回刷进 PG 的增量：列表的 click_count 必须与详情页的 total_clicks 同口径，
	// 否则仪表盘汇总最多滞后一个回刷周期（2s），用户会看到「详情有数、列表没数」。
	deltas := h.pendingDeltas(r.Context(), links)

	items := make([]linkDTO, 0, len(links))
	for i := range links {
		link := &links[i]
		// map 里没有该短码 = 没有待同步增量，按 0 处理（不写 map 的零值也一样）
		link.ClickCount += deltas[link.ShortCode]
		items = append(items, toLinkDTO(link, h.shortener.ShortURL(link.ShortCode)))
	}
	httpx.WriteJSON(w, r, http.StatusOK, linkListResponse{Links: items, NextCursor: nextCursor})
}

// pendingDeltas 读取本页短码尚未回刷进 PG 的点击增量。
//
// 失败只记 warn 并返回 nil（调用方退回纯基线）：统计侧读不到不能让列表变成 5xx ——
// 数字稍滞后是可接受的降级，接口整体不可用不是。
func (h *linkHandler) pendingDeltas(ctx context.Context, links []domain.Link) map[string]int64 {
	if h.deltas == nil || len(links) == 0 {
		return nil
	}

	codes := make([]string, 0, len(links))
	for i := range links {
		codes = append(codes, links[i].ShortCode)
	}

	deltas, err := h.deltas.PendingDeltas(ctx, codes)
	if err != nil {
		slog.Warn("读取待同步点击增量失败，列表的 click_count 只反映 PG 基线",
			"err", err, "codes", len(codes))
		return nil
	}
	return deltas
}

// get 处理 GET /api/links/{code}。
// 无权限与不存在都返回 404 —— 避免通过状态码差异枚举出短码是否存在。
func (h *linkHandler) get(w http.ResponseWriter, r *http.Request) {
	link, ok := loadAuthorized(w, r, h.shortener, true)
	if !ok {
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toLinkDTO(link, h.shortener.ShortURL(link.ShortCode)))
}

// update 处理 PATCH /api/links/{code}。
func (h *linkHandler) update(w http.ResponseWriter, r *http.Request) {
	link, ok := loadAuthorized(w, r, h.shortener, false)
	if !ok {
		return
	}

	req, ok := httpx.DecodeJSON[updateLinkRequest](w, r)
	if !ok {
		return
	}

	patch, ok := h.buildPatch(w, r, link, req)
	if !ok {
		return
	}

	updated, err := h.shortener.Update(r.Context(), link.ShortCode, patch)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toLinkDTO(updated, h.shortener.ShortURL(updated.ShortCode)))
}

// remove 处理 DELETE /api/links/{code}（软删除）。
func (h *linkHandler) remove(w http.ResponseWriter, r *http.Request) {
	link, ok := loadAuthorized(w, r, h.shortener, false)
	if !ok {
		return
	}
	if err := h.shortener.Delete(r.Context(), link.ShortCode); err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// claim 处理 POST /api/links/{code}/claim：把匿名短链挂到当前账号下。
func (h *linkHandler) claim(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFrom(r.Context())
	if !ok {
		httpx.WriteUnauthorized(w, r, "请先登录")
		return
	}

	code := r.PathValue("code")
	link, err := h.shortener.Get(r.Context(), code)
	if err != nil || link.Status == domain.LinkStatusDeleted {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "未找到该短链", "")
		return
	}
	if !link.IsAnonymous() {
		httpx.WriteError(w, r, http.StatusConflict, "conflict", "该短链已归属某个账号", "")
		return
	}
	// 认领只认管理密钥：链接还是匿名状态，这里不可能靠 owner 匹配通过
	if err := h.shortener.Authorize(link, service.Actor{ManageKey: actorOf(r).ManageKey}); err != nil {
		httpx.WriteError(w, r, http.StatusForbidden, "forbidden",
			"缺少或错误的管理密钥，无法认领该短链", "")
		return
	}

	claimed, err := h.shortener.Claim(r.Context(), code, id)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toLinkDTO(claimed, h.shortener.ShortURL(claimed.ShortCode)))
}

// loadAuthorized 取短链并做鉴权，link / stats 两个 handler 共用。
// forDetail=true 时把「无权限」也报成 404（详情查询不泄露资源是否存在）。
func loadAuthorized(w http.ResponseWriter, r *http.Request, shortener *service.Shortener, forDetail bool) (*domain.Link, bool) {
	code := r.PathValue("code")

	link, err := shortener.Get(r.Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "未找到该短链", "")
			return nil, false
		}
		httpx.WriteDomainError(w, r, err)
		return nil, false
	}
	if link.Status == domain.LinkStatusDeleted {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "未找到该短链", "")
		return nil, false
	}

	if err := shortener.Authorize(link, actorOf(r)); err != nil {
		if forDetail {
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "未找到该短链", "")
			return nil, false
		}
		httpx.WriteDomainError(w, r, err)
		return nil, false
	}
	return link, true
}

// buildPatch 把请求体转成领域补丁，并做跨字段的一致性校验。
func (h *linkHandler) buildPatch(w http.ResponseWriter, r *http.Request, link *domain.Link, req updateLinkRequest) (domain.LinkPatch, bool) {
	var patch domain.LinkPatch
	patch.TargetURL = req.TargetURL
	patch.Title = req.Title

	if req.ExpiresAt != nil && req.ClearExpires {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_expires_at",
			"expires_at 与 clear_expires 不能同时指定", "expires_at")
		return patch, false
	}
	patch.ExpiresAt = req.ExpiresAt
	patch.ClearExpires = req.ClearExpires

	if req.Status != nil {
		status, err := domain.ParseLinkStatus(*req.Status)
		if err != nil {
			httpx.WriteDomainError(w, r, err)
			return patch, false
		}
		if status == domain.LinkStatusDeleted {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_status",
				"删除请使用 DELETE 接口", "status")
			return patch, false
		}
		patch.Status = &status
	}

	if patch.IsEmpty() {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "empty_patch", "没有需要更新的字段", "")
		return patch, false
	}

	// 重新启用一条已过期的链接是没意义的：这里直接拦掉，避免用户以为改成功了
	if patch.Status != nil && *patch.Status == domain.LinkStatusActive {
		effective := link.ExpiresAt
		if patch.ClearExpires {
			effective = nil
		} else if patch.ExpiresAt != nil {
			effective = patch.ExpiresAt
		}
		if effective != nil && !effective.After(time.Now()) {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "expired_link",
				"该短链的过期时间已过，请先更新过期时间再启用", "expires_at")
			return patch, false
		}
	}

	return patch, true
}
