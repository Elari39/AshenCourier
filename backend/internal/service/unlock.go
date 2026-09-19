package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ashen-courier/internal/domain"
)

// unlockKeyInfo 是解锁凭据的域分离标签：子密钥由它派生，绝不直接用 JWT_SECRET。
//
// 同一条密钥签名两类凭据（JWT 与解锁 cookie）会互相放大泄露面 ——
// 一类凭据的伪造或泄露不该让另一类也跟着失守。
const unlockKeyInfo = "ashen-courier/link-unlock/v1"

// unlockSep 是 code 与 exp 之间的分隔符。
// 短码字符集是 [0-9A-Za-z_-]，两个值里都不可能出现换行，所以拼接不会有歧义。
const unlockSep = '\n'

// LinkUnlocker 签发并校验「已解锁」凭据：HMAC-SHA256、无状态、不落库。
//
// 凭据形如 base64url(<expUnix>|<sig>)，其中 sig = HMAC-SHA256(subkey, code + 分隔符 + expUnix)。
// code 装进签名体（而不是只放进 cookie 名）是为了让「A 链的凭据」在 B 链上一律失效。
type LinkUnlocker struct {
	key []byte
	ttl time.Duration
}

// NewLinkUnlocker 从主密钥派生出解锁专用子密钥。
func NewLinkUnlocker(secret string, ttl time.Duration) *LinkUnlocker {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unlockKeyInfo))
	return &LinkUnlocker{key: mac.Sum(nil), ttl: ttl}
}

// TTL 返回凭据有效期；handler 用它设置 cookie 的 MaxAge。
func (u *LinkUnlocker) TTL() time.Duration { return u.ttl }

// Issue 为某条短链签发解锁凭据。
func (u *LinkUnlocker) Issue(code string, now time.Time) string {
	exp := now.Add(u.ttl).Unix()
	payload := strconv.FormatInt(exp, 10) + "|" + u.sign(code, exp)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// Verify 校验凭据对这条短链是否有效。
//
// 一切失败（畸形、签名不符、过期、换了短码）都归为 domain.ErrUnauthorized：
// 对用户而言它们都是「需要重新输口令」，分开只会泄露「签名是对的、只是过期了」这类信息。
func (u *LinkUnlocker) Verify(code, token string, now time.Time) error {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return fmt.Errorf("service.unlock: 解码凭据: %w", domain.ErrUnauthorized)
	}

	expPart, sig, found := strings.Cut(string(raw), "|")
	if !found {
		return fmt.Errorf("service.unlock: 凭据缺少分隔符: %w", domain.ErrUnauthorized)
	}
	exp, err := strconv.ParseInt(expPart, 10, 64)
	if err != nil {
		return fmt.Errorf("service.unlock: 凭据里的过期时间非法: %w", domain.ErrUnauthorized)
	}

	// 先比签名再判过期：签名无效时不该顺带透露「它其实还没过期」
	if !hmac.Equal([]byte(sig), []byte(u.sign(code, exp))) {
		return fmt.Errorf("service.unlock: 凭据签名不匹配: %w", domain.ErrUnauthorized)
	}
	if !now.Before(time.Unix(exp, 0)) {
		return fmt.Errorf("service.unlock: 凭据已过期: %w", domain.ErrUnauthorized)
	}
	return nil
}

// sign 计算十六进制签名，便于放进文本凭据。
func (u *LinkUnlocker) sign(code string, exp int64) string {
	mac := hmac.New(sha256.New, u.key)
	mac.Write([]byte(code))
	mac.Write([]byte{unlockSep})
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
