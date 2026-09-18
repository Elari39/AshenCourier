// Package domain 是领域层：实体、值对象、领域错误与仓储接口。
//
// 硬约束：本包（及其子文件）不 import 任何第三方库，也不 import database/sql、
// pgx、go-redis —— 仓储接口在这里定义，具体实现放在 internal/store。
package domain

import (
	"errors"
	"fmt"
)

// 领域哨兵错误。上层用 errors.Is 判定，HTTP 层统一映射成状态码。
var (
	// ErrNotFound 表示实体不存在。
	ErrNotFound = errors.New("not found")
	// ErrConflict 表示唯一约束冲突（短码被占、邮箱已注册等）。
	ErrConflict = errors.New("conflict")
	// ErrForbidden 表示没有权限操作该资源。
	ErrForbidden = errors.New("forbidden")
	// ErrGone 表示资源已失效（软删除 / 已过期 / 被禁用）。
	ErrGone = errors.New("gone")
	// ErrInvalidInput 表示入参不满足领域约束。
	ErrInvalidInput = errors.New("invalid input")
	// ErrUnauthorized 表示未认证或凭证无效。
	ErrUnauthorized = errors.New("unauthorized")
	// ErrUnavailable 表示依赖组件（PG / Redis）不可用，请求可重试。
	ErrUnavailable = errors.New("dependency unavailable")
	// ErrInternal 表示服务端内部错误（如脏数据），请求本身没有问题。
	ErrInternal = errors.New("internal error")
)

// ConflictError 携带「哪一个字段的唯一性被破坏」，让 service 能区分
// 「短码撞了 → 重试」与「邮箱撞了 → 直接 409 回给用户」。
type ConflictError struct {
	// Field 是冲突的字段名，如 "short_code" / "email"。
	Field string
	// Value 是冲突的具体取值（用于日志，不回显给客户端）。
	Value string
}

// Error 实现 error；文案面向日志，不直接暴露给终端用户。
func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict on %s=%q", e.Field, e.Value)
}

// Unwrap 让 errors.Is(err, domain.ErrConflict) 成立。
func (e *ConflictError) Unwrap() error { return ErrConflict }

// NotFoundError 描述「什么类型的什么键没找到」，便于日志归因。
type NotFoundError struct {
	// Entity 是实体名，如 "link" / "user"。
	Entity string
	// Key 是查询键。
	Key string
}

// Error 实现 error。
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found", e.Entity, e.Key)
}

// Unwrap 让 errors.Is(err, domain.ErrNotFound) 成立。
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// InvalidInputError 描述字段级校验失败，handler 会把它映射成 422 + 字段名。
type InvalidInputError struct {
	// Field 是出错的字段。
	Field string
	// Reason 是面向用户的中文说明。
	Reason string
}

// Error 实现 error。
func (e *InvalidInputError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
}

// Unwrap 让 errors.Is(err, domain.ErrInvalidInput) 成立。
func (e *InvalidInputError) Unwrap() error { return ErrInvalidInput }

// Invalid 构造一个字段级校验错误。
func Invalid(field, reason string) error {
	return &InvalidInputError{Field: field, Reason: reason}
}

// NotFound 构造一个不存在错误。
func NotFound(entity, key string) error {
	return &NotFoundError{Entity: entity, Key: key}
}

// Conflict 构造一个唯一约束冲突错误。
func Conflict(field, value string) error {
	return &ConflictError{Field: field, Value: value}
}

// AsConflict 是 errors.AsType[*ConflictError] 的语义化别名，读起来更贴近意图。
func AsConflict(err error) (*ConflictError, bool) {
	return errors.AsType[*ConflictError](err)
}
