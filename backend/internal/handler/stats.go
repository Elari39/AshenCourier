package handler

import (
	"net/http"
	"strconv"

	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// statsHandler 处理 GET /api/links/{code}/stats。
type statsHandler struct {
	shortener *service.Shortener
	stats     *service.Stats
}

// show 返回某条短链的统计聚合。
// 鉴权语义与详情一致：无权限返回 404，避免暴露短码是否存在。
func (h *statsHandler) show(w http.ResponseWriter, r *http.Request) {
	link, ok := loadAuthorized(w, r, h.shortener, true)
	if !ok {
		return
	}

	days := service.DefaultStatsDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_days",
				"days 必须是正整数", "days")
			return
		}
		days = n
	}

	result, err := h.stats.ForLink(r.Context(), link, days)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toStatsResponse(result))
}
