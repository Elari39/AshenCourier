// Package base62 实现短链服务所用的 Base62 编解码。
//
// 选 Base62（0-9A-Za-z）而非 Base64：短码要直接出现在 URL 路径里，
// 字符集不含 '-' / '_' / '=' 就无需转义，也与常用正则 ^[0-9A-Za-z_-]{3,32}$ 天然兼容。
package base62

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
)

const (
	// alphabet 的顺序即「数值 → 字符」的映射顺序：0-9 → A-Z → a-z。
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base     = uint64(len(alphabet))
	// unbiased 是 256 以内最大的 base 整数倍（256 - 256%62 = 248）。
	// 拒绝采样只接受 [0, unbiased) 的字节，避免取模引入分布偏置。
	unbiased = byte(256 - 256%len(alphabet))
	// maxLen 是 uint64 的 Base62 最长位数：ceil(64 / log2(62)) = 11。
	maxLen = 11
)

// 哨兵错误：调用方用 errors.Is 判定，避免字符串比较。
var (
	// ErrEmpty 表示解码时传入了空字符串。
	ErrEmpty = errors.New("empty input")
	// ErrInvalidChar 表示字符串包含非 Base62 字符。
	ErrInvalidChar = errors.New("invalid base62 character")
	// ErrOverflow 表示解码结果超出 uint64 表示范围。
	ErrOverflow = errors.New("base62 value overflows uint64")
	// ErrInvalidLength 表示请求的随机码长度非法。
	ErrInvalidLength = errors.New("invalid length")
)

// Encode 把非负整数编码为最短 Base62 字符串，0 编码为 "0"。
func Encode(n uint64) string {
	var buf [maxLen]byte
	i := len(buf)
	for {
		i--
		buf[i] = alphabet[n%base]
		n /= base
		if n == 0 {
			break
		}
	}
	return string(buf[i:])
}

// Decode 把 Base62 字符串解析回 uint64；空串、非法字符、溢出都会返回带哨兵的包装错误。
func Decode(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("base62: decode: %w", ErrEmpty)
	}
	var n uint64
	for i := range len(s) {
		v := index(s[i])
		if v < 0 {
			return 0, fmt.Errorf("base62: decode %q: %w", s, ErrInvalidChar)
		}
		if n > (math.MaxUint64-uint64(v))/base {
			return 0, fmt.Errorf("base62: decode %q: %w", s, ErrOverflow)
		}
		n = n*base + uint64(v)
	}
	return n, nil
}

// Random 用密码学安全随机数生成 n 位 Base62 字符串，分布均匀（拒绝采样，无取模偏置）。
func Random(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("base62: random: %w", ErrInvalidLength)
	}
	out := make([]byte, 0, n)
	// 每次多读 16 字节，抵消拒绝采样带来的浪费；统计上 1~2 轮即可填满。
	buf := make([]byte, n+16)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("base62: random: read entropy: %w", err)
		}
		for _, b := range buf {
			if b >= unbiased {
				continue
			}
			out = append(out, alphabet[int(b)%int(base)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// index 返回字节在字母表中的下标；非 Base62 字符返回 -1。
func index(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 36
	default:
		return -1
	}
}
