package handler

import (
	"net/http"
	"strconv"

	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// clickHandler 处理 GET /api/links/{code}/clicks。
type clickHandler struct {
	shortener *service.Shortener
	stats     *service.Stats
	pageSize  int
	maxSize   int
}

// list 分页返回某条短链的点击明细。
//
// 鉴权与详情、统计一致：无权限返回 404 —— 明细带来源与设备指纹，
// 用 403 区分「存在但你没权限」等于把短码是否存在告诉了对方。
func (h *clickHandler) list(w http.ResponseWriter, r *http.Request) {
	link, ok := loadAuthorized(w, r, h.shortener, true)
	if !ok {
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

	days := service.DefaultStatsDays
	if raw := query.Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_days", "days 必须是正整数", "days")
			return
		}
		days = n
	}

	result, err := h.stats.ListClicks(r.Context(), link, service.ClickListInput{
		Limit:    limit,
		MaxLimit: h.maxSize,
		Cursor:   query.Get("cursor"),
		Days:     days,
		Device:   query.Get("device"),
	})
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}

	clicks := make([]clickDTO, 0, len(result.Events))
	for i := range result.Events {
		clicks = append(clicks, toClickDTO(&result.Events[i]))
	}
	httpx.WriteJSON(w, r, http.StatusOK, clickListResponse{
		Clicks:     clicks,
		NextCursor: result.NextCursor,
		Days:       result.Days,
		Since:      result.Since,
	})
}
