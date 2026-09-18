// Package handler 是 HTTP 处理器层：解析入参、调用 service、把结果与错误映射成 HTTP。
//
// 路由装配（Router）也放在这里而不是 httpx —— handler 需要复用 httpx 的
// 响应助手，若路由再放进 httpx 就会形成 import 环。
package handler

import (
	"context"
	"net/http"
	"strings"
	"uuid"

	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// manageKeyHeader 是匿名管理密钥的请求头。
const manageKeyHeader = "X-Manage-Key"

// ctxKeyUserID 是 handler 包私有的 context 键类型。
type ctxKeyUserID struct{}

// withUserID 把已认证的用户 ID 塞进 context。
func withUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyUserID{}, id)
}

// userIDFrom 取已认证的用户 ID。
func userIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyUserID{}).(uuid.UUID)
	return id, ok
}

// actorOf 从请求构造操作者身份：登录态来自 context，匿名密钥来自请求头。
func actorOf(r *http.Request) service.Actor {
	actor := service.Actor{ManageKey: strings.TrimSpace(r.Header.Get(manageKeyHeader))}
	if id, ok := userIDFrom(r.Context()); ok {
		actor.UserID = &id
	}
	return actor
}

// bearerToken 从 Authorization 头取出 Bearer 令牌；格式不对返回空串。
func bearerToken(r *http.Request) string {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(strings.TrimSpace(scheme), "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// requireAuth 是「必须登录」中间件：令牌缺失或无效统一 401。
func requireAuth(auth *service.Auth) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				httpx.WriteUnauthorized(w, r, "请先登录")
				return
			}
			id, err := auth.ParseToken(token)
			if err != nil {
				httpx.WriteUnauthorized(w, r, "登录状态已失效，请重新登录")
				return
			}
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), id)))
		})
	}
}

// optionalAuth 是「尽力而为」中间件：带了有效令牌就注入身份，
// 没带或令牌无效都继续往下走，由 handler 自己决定要不要 401。
// 创建短链接口需要它 —— 登录与匿名都必须可用。
func optionalAuth(auth *service.Auth) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			id, err := auth.ParseToken(token)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), id)))
		})
	}
}
