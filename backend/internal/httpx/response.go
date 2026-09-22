// Package httpx 是 HTTP 传输层工具箱：JSON 编解码、统一错误体、
// 中间件、限流中间件与 http.Server 生命周期管理。
//
// 注意：路由装配放在 internal/handler/router.go，而不是本包。
// 原因是 handler 需要复用本包的响应助手（会 import httpx），
// 若路由再放在 httpx 里就会形成 import 环。
package httpx

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"uuid"

	"ashen-courier/internal/domain"
)

// 传输层常量。
const (
	// requestIDHeader 是贯穿全链路的请求 ID 头。
	requestIDHeader = "X-Request-Id"
	// maxBodyBytes 是请求体大小上限，超过直接 413。
	maxBodyBytes = 64 << 10
	// contentTypeJSON 是统一响应内容类型。
	contentTypeJSON = "application/json; charset=utf-8"
)

// ctxKey 是本包私有的 context 键类型，避免与其他包的键冲突。
type ctxKey int

const (
	// ctxKeyRequestID 承载请求 ID。
	ctxKeyRequestID ctxKey = iota
)

// ErrorBody 是统一错误响应体。
//
//	{ "error": { "code": "invalid_url", "message": "仅支持 http/https 链接", "request_id": "01J..." } }
type ErrorBody struct {
	// Error 是错误详情。
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 描述一个错误。
type ErrorDetail struct {
	// Code 是机器可读的错误码。
	Code string `json:"code"`
	// Message 是面向用户的中文说明。
	Message string `json:"message"`
	// Field 是出错的字段名（仅校验类错误有值）。
	Field string `json:"field,omitempty"`
	// RequestID 便于用户报障时定位日志。
	RequestID string `json:"request_id,omitempty"`
}

// HealthReport 是 /healthz 的响应体。
type HealthReport struct {
	// Status 是 ok / degraded。
	Status string `json:"status"`
	// Version 是构建版本。
	Version string `json:"version,omitempty"`
	// Postgres / Redis 是依赖探针结果：ok / error。
	Postgres string `json:"postgres"`
	Redis    string `json:"redis"`
	// WorkerEnabled 表示本进程是否内嵌了 worker。
	WorkerEnabled bool `json:"worker_enabled"`
	// 以下为诊断计数，都是「有问题才非零」。
	DroppedClicks int64 `json:"dropped_clicks,omitzero"`
	FailedClicks  int64 `json:"failed_clicks,omitzero"`
	QueueLen      int   `json:"queue_len,omitzero"`
	StreamLen     int64 `json:"stream_len,omitzero"`
	StreamPending int64 `json:"stream_pending,omitzero"`
	// PGFallbacks 是短码缓存未命中、真正回源 PG 的累计次数（缓存击穿的观测口径）。
	PGFallbacks       int64 `json:"pg_fallbacks,omitzero"`
	RateLimitDegraded int64 `json:"rate_limit_degraded,omitzero"`
	UptimeSeconds     int64 `json:"uptime_seconds,omitzero"`
	// RateLimitByNative 表示限流走的是 Redis 8.8+ 原生 INCREX 而非 Lua 回落实现。
	RateLimitByNative bool `json:"rate_limit_native_increx,omitzero"`
	// RateLimitDisabled 表示限流的应急开关被打开（RATE_LIMIT_DISABLED=true，全量放行）。
	RateLimitDisabled bool `json:"rate_limit_disabled,omitzero"`
	// ConsumedClicks / WorkerErrors 仅在内嵌 worker 时有值。
	ConsumedClicks int64 `json:"consumed_clicks,omitzero"`
	WorkerErrors   int64 `json:"worker_errors,omitzero"`
	// Errors 是简短的组件故障说明（不含连接串等敏感信息）。
	Errors []string `json:"errors,omitempty"`
}

// HealthProbe 由 cmd/api 实现，负责汇总各组件状态。
type HealthProbe interface {
	// Report 采集一次健康报告。
	Report(ctx context.Context) HealthReport
}

// RequestIDFrom 从 context 取请求 ID；取不到返回空串。
func RequestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// WriteJSON 写出 JSON 响应。
//
// 先 MarshaWrite 到内存缓冲、再一次性写出：这样序列化失败时还能回一个干净的 500，
// 不会出现「已经写了 200 头和半个 JSON」的撕裂响应。
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	var buf bytes.Buffer
	if err := json.MarshalWrite(&buf, payload); err != nil {
		slog.Error("序列化响应失败",
			"err", err, "request_id", RequestIDFrom(r.Context()), "path", r.URL.Path)
		http.Error(w, `{"error":{"code":"internal","message":"服务器内部错误"}}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// WriteError 写出统一错误体。
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message, field string) {
	WriteJSON(w, r, status, ErrorBody{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		Field:     field,
		RequestID: RequestIDFrom(r.Context()),
	}})
}

// WriteDomainError 把领域错误翻译成 HTTP 响应。
//
// 映射表：
//
//	*InvalidInputError → 422 invalid_<field>
//	ErrNotFound        → 404 not_found
//	ErrForbidden       → 403 forbidden
//	ErrConflict        → 409 conflict
//	ErrGone            → 410 gone
//	ErrInvalidCredentials → 401 invalid_credentials（凭据不对，让用户改输入）
//	ErrUnauthorized    → 401 unauthorized（未认证/令牌失效，送回登录页）
//	ErrUnavailable     → 503 unavailable（可重试，带 Retry-After）
//	ErrInternal / 其他  → 500 internal（并打 error 日志）
//
// 判定顺序即优先级。多个哨兵同时成立时，「可重试」压过「业务冲突」：
// service.Create 在自动短码连续冲突后返回 errors.Join(ErrUnavailable, *ConflictError)，
// 若让 ErrConflict 先命中，用户会看到「该短链已被占用，请换一个」——可他根本没提供短码。
func WriteDomainError(w http.ResponseWriter, r *http.Request, err error) {
	if invalid, ok := errors.AsType[*domain.InvalidInputError](err); ok {
		WriteError(w, r, http.StatusUnprocessableEntity, invalidCode(invalid.Field), invalid.Reason, invalid.Field)
		return
	}

	status, code, message := http.StatusInternalServerError, "internal", "服务器内部错误"
	switch {
	case errors.Is(err, domain.ErrUnavailable):
		status, code, message = http.StatusServiceUnavailable, "unavailable", "服务暂时不可用，请稍后重试"
		w.Header().Set("Retry-After", "2")
	case errors.Is(err, domain.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "未找到该资源"
	case errors.Is(err, domain.ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", "没有权限操作该资源"
	case errors.Is(err, domain.ErrConflict):
		status, code, message = http.StatusConflict, "conflict", conflictMessage(err)
	case errors.Is(err, domain.ErrGone):
		status, code, message = http.StatusGone, "gone", "该短链已失效"
	case errors.Is(err, domain.ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "invalid_credentials", "邮箱或密码不正确"
	case errors.Is(err, domain.ErrUnauthorized):
		status, code, message = http.StatusUnauthorized, "unauthorized", "登录状态无效，请重新登录"
	}

	if status >= http.StatusInternalServerError {
		slog.Error("请求处理失败",
			"err", err, "status", status, "path", r.URL.Path,
			"request_id", RequestIDFrom(r.Context()))
	} else {
		slog.Debug("请求被拒绝",
			"err", err, "status", status, "path", r.URL.Path,
			"request_id", RequestIDFrom(r.Context()))
	}
	WriteError(w, r, status, code, message, "")
}

// WriteUnauthorized 写 401 并带上 WWW-Authenticate。
func WriteUnauthorized(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="ashen-courier"`)
	WriteError(w, r, http.StatusUnauthorized, "unauthorized", message, "")
}

// DecodeJSON 读取并解析请求体。
//
// 用 encoding/json/v2 的 UnmarshalRead：v2 默认拒绝非法 UTF-8 与重复键，
// 这两点恰好是常见的参数走私手法。再显式打开 RejectUnknownMembers ——
// v2 的默认是**忽略**未知字段，字段名拼错（titel / targetUrl）时请求会
// 看起来「成功但没生效」（PATCH 场景下还会退化成「没有需要更新的字段」），
// 很难排查；直接 400 把问题指出来。
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var payload T
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.UnmarshalRead(r.Body, &payload, json.RejectUnknownMembers(true)); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large",
				"请求体过大", "")
			return payload, false
		}
		// 语义错误（未知字段、类型不匹配）与语法错误分开提示：
		// 前者多半是字段名写错，后者才是 JSON 本身坏了。
		if _, ok := errors.AsType[*json.SemanticError](err); ok {
			WriteError(w, r, http.StatusBadRequest, "invalid_json",
				"请求体字段不合法（含未知字段或类型不匹配）", "")
			return payload, false
		}
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON", "")
		return payload, false
	}
	return payload, true
}

// ClientIP 提取客户端 IP。
//
// 只信任 nginx 注入的 X-Real-IP（trustProxy=true 时）；
// 绝不解析 X-Forwarded-For —— 那是客户端可随意伪造的头。
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			if ip := net.ParseIP(real); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}

// OriginOf 取 URL 的 scheme://host 形式，用于 CORS 白名单比对。
func OriginOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// invalidCode 把字段名映射成错误码。
// target_url 用更短更常见的 invalid_url，与 API 文档示例保持一致。
func invalidCode(field string) string {
	if field == "target_url" {
		return "invalid_url"
	}
	return "invalid_" + field
}

// conflictMessage 给冲突错误一句面向用户的话。
func conflictMessage(err error) string {
	if conflict, ok := domain.AsConflict(err); ok && conflict.Field == "email" {
		return "该邮箱已注册"
	}
	if conflict, ok := domain.AsConflict(err); ok && conflict.Field == "short_code" {
		return "该短链已被占用，请换一个"
	}
	return "资源已存在"
}

// newRequestID 生成请求 ID。用 uuid v7：时间有序，日志里天然按时间排列。
func newRequestID() string { return uuid.NewV7().String() }
