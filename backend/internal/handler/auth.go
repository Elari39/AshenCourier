package handler

import (
	"net/http"

	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// authHandler 处理 /api/auth/*。
type authHandler struct {
	auth *service.Auth
}

// register 处理 POST /api/auth/register。
func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	req, ok := httpx.DecodeJSON[registerRequest](w, r)
	if !ok {
		return
	}

	session, err := h.auth.Register(r.Context(), service.RegisterInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusCreated, toSessionResponse(session))
}

// login 处理 POST /api/auth/login。
func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	req, ok := httpx.DecodeJSON[loginRequest](w, r)
	if !ok {
		return
	}

	session, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toSessionResponse(session))
}

// me 处理 GET /api/auth/me。用户 ID 由 requireAuth 中间件注入。
func (h *authHandler) me(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFrom(r.Context())
	if !ok {
		httpx.WriteUnauthorized(w, r, "请先登录")
		return
	}

	user, err := h.auth.User(r.Context(), id)
	if err != nil {
		httpx.WriteDomainError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toUserDTO(user))
}
