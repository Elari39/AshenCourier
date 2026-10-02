package handler

import (
	"ashen-courier/internal/service"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInvalidOptionalSessionDoesNotCreateAnonymousLink(t *testing.T) {
	auth := service.NewAuth(nil, "test-secret", time.Hour)
	reached := false
	handler := optionalAuth(auth)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/links", nil)
	req.Header.Set("Authorization", "Bearer expired-or-invalid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if reached || response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid session reached create handler: %v, status=%d", reached, response.Code)
	}
}
