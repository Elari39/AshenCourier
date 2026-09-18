// Package shortcode 负责短码的形态校验、保留字判定与随机生成。
//
// 短码字符集 = [0-9A-Za-z_-]，长度 3–32。这三处必须始终一致：
//   - 本包 MinLength / MaxLength / isAllowed
//   - nginx 短码正则 "^/[A-Za-z0-9_-]{3,32}$"
//   - 数据库约束 links_code_shape
package shortcode

import (
	"errors"
	"fmt"

	"ashen-courier/internal/pkg/base62"
)

const (
	// DefaultLength 是自动生成的短码长度：62^7 ≈ 3.5 万亿，配合唯一约束重试足够安全。
	DefaultLength = 7
	// MinLength 是自定义别名的最短长度。
	MinLength = 3
	// MaxLength 是自定义别名的最长长度。
	MaxLength = 32
	// MaxAttempts 是短码唯一约束冲突后的最大重试次数。
	MaxAttempts = 5
)

// 短码校验哨兵错误。
var (
	// ErrTooShort 表示短码短于 MinLength。
	ErrTooShort = errors.New("short code too short")
	// ErrTooLong 表示短码长于 MaxLength。
	ErrTooLong = errors.New("short code too long")
	// ErrBadChar 表示短码含 [0-9A-Za-z_-] 之外的字符。
	ErrBadChar = errors.New("short code contains invalid character")
	// ErrReserved 表示短码命中保留字表。
	ErrReserved = errors.New("short code is reserved")
)

// Generate 生成一个随机短码；随机源为 crypto/rand，失败时向上传递错误。
func Generate() (string, error) {
	code, err := base62.Random(DefaultLength)
	if err != nil {
		return "", fmt.Errorf("shortcode: generate: %w", err)
	}
	return code, nil
}

// Validate 校验「用户自定义别名」：长度、字符集、保留字三重检查。
func Validate(code string) error {
	if len(code) < MinLength {
		return fmt.Errorf("shortcode: validate %q: %w", code, ErrTooShort)
	}
	if len(code) > MaxLength {
		return fmt.Errorf("shortcode: validate %q: %w", code, ErrTooLong)
	}
	for i := range len(code) {
		if !isAllowed(code[i]) {
			return fmt.Errorf("shortcode: validate %q: %w", code, ErrBadChar)
		}
	}
	if IsReserved(code) {
		return fmt.Errorf("shortcode: validate %q: %w", code, ErrReserved)
	}
	return nil
}

// IsValidShape 只做形态判定（长度 + 字符集），不含保留字检查。
// 跳转路径用它：历史短码可能是在保留字表扩充之前注册的，直接 404 会打断已有链接。
func IsValidShape(code string) bool {
	if len(code) < MinLength || len(code) > MaxLength {
		return false
	}
	for i := range len(code) {
		if !isAllowed(code[i]) {
			return false
		}
	}
	return true
}

// isAllowed 判定字节是否落在短码字符集 [0-9A-Za-z_-] 内。
func isAllowed(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c == '-' || c == '_':
		return true
	default:
		return false
	}
}
