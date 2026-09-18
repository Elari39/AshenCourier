package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// ManageKeyBytes 是匿名管理密钥的随机字节数。
// 32 字节 → base64url 无填充后 43 字符，熵 256 bit。
const ManageKeyBytes = 32

// NewManageKey 生成匿名链接的管理密钥。
//
// 返回 (明文, SHA-256 摘要, error)：
//   - 明文只在创建响应里出现一次，数据库只存摘要
//   - 密钥是 256 bit 真随机，不需要慢哈希（慢哈希是为了抵御低熵口令的爆破）
func NewManageKey() (string, []byte, error) {
	buf := make([]byte, ManageKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("service: generate manage key: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashManageKey(raw), nil
}

// HashManageKey 计算管理密钥的 SHA-256 摘要。摘要长度固定 32 字节。
func HashManageKey(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// ManageKeyMatches 用常量时间比对摘要，避免通过响应耗时逐字节猜密钥。
func ManageKeyMatches(hash []byte, raw string) bool {
	if len(hash) == 0 || raw == "" {
		return false
	}
	return subtle.ConstantTimeCompare(hash, HashManageKey(raw)) == 1
}
