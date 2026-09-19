package service

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// 短链访问口令的摘要与比对。
//
// 刻意复用账号口令那一套（ValidatePassword 的 8–72 字节 + BcryptCost = 12）：
// 两套强度规则只会让人记错其中一条。72 是 bcrypt 的硬上限（超出会静默截断），
// 必须显式拒绝，否则「长度 80 的口令」与「前 72 字节相同的口令」等价。

// HashLinkPassword 生成短链口令的 bcrypt 摘要。
func HashLinkPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("service.shortener: hash link password: %w", err)
	}
	return string(hash), nil
}

// CheckLinkPassword 比对口令与摘要。摘要为空时恒为 false（调用方应先问 HasPassword）。
func CheckLinkPassword(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// normalizeLinkPassword 把用户传的口令转成要落库的摘要：
// 空串 = 不设口令（原样返回空串），非空则先校验强度再摘要化。
//
// 明文只在本函数内存在 —— 不落日志、不写库、不进缓存。
func normalizeLinkPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if err := ValidatePassword(plain); err != nil {
		return "", err
	}
	return HashLinkPassword(plain)
}
