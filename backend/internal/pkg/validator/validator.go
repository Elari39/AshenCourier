// Package validator 负责「目标 URL」的规范化与方案白名单校验。
//
// 这是开放重定向防线的第一道闸门：
//   - DB 侧有 CHECK (target_url ~* '^https?://') 兜底（防止历史脏数据）
//   - 跳转时还会用 IsAllowedTarget 二次校验（防止绕过应用层的写入）
package validator

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// MaxURLLength 是目标 URL 的最大长度，与 DB 约束 links_target_url_len 保持一致。
const MaxURLLength = 2048

// 目标 URL 校验哨兵错误。
var (
	// ErrEmpty 表示没有提供 URL。
	ErrEmpty = errors.New("empty url")
	// ErrTooLong 表示 URL 超过 MaxURLLength。
	ErrTooLong = errors.New("url too long")
	// ErrBadScheme 表示 scheme 不是 http / https。
	ErrBadScheme = errors.New("only http and https are allowed")
	// ErrNoHost 表示 URL 缺少主机名。
	ErrNoHost = errors.New("url has no host")
	// ErrMalformed 表示 URL 无法被解析。
	ErrMalformed = errors.New("malformed url")
)

// Normalize 规范化目标 URL：
//  1. 去掉首尾空白；空串、超长直接拒绝
//  2. 缺 scheme 时按 https 补齐（用户粘贴 example.com/xx 是常见输入）
//  3. scheme 只允许 http / https，host 必须存在
//  4. 小写化 scheme 与 host，其余部分原样保留（path 大小写有意义）
//
// 返回值为规范化后的字符串，可作为 links.target_url 直接入库。
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("validator: normalize: %w", ErrEmpty)
	}
	if len(s) > MaxURLLength {
		return "", fmt.Errorf("validator: normalize: %w (%d > %d)", ErrTooLong, len(s), MaxURLLength)
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("validator: normalize %q: %w", s, ErrMalformed)
	}
	if u.Scheme == "" {
		// 无 scheme：按 https 补齐后重新解析
		u, err = url.Parse("https://" + s)
		if err != nil {
			return "", fmt.Errorf("validator: normalize %q: %w", s, ErrMalformed)
		}
	}

	// Clone 后再改，避免污染调用方可能持有的 *url.URL（url.Clone 是浅拷贝语义的安全做法）
	c := u.Clone()

	c.Scheme = strings.ToLower(c.Scheme)
	if c.Scheme != "http" && c.Scheme != "https" {
		return "", fmt.Errorf("validator: normalize %q: %w", s, ErrBadScheme)
	}
	if c.Host == "" || c.Hostname() == "" {
		return "", fmt.Errorf("validator: normalize %q: %w", s, ErrNoHost)
	}
	// 只小写化，绝不手工拆重组 host。
	// url.URL 已经把 host 与 port 分开，`Host` 里本来就带着 IPv6 需要的方括号；
	// 用 Hostname() 重新拼回去会剥掉方括号，把 http://[::1]:8080/x 写成
	// http://::1:8080/x —— 这种坏地址能过 DB 的 scheme CHECK，也过 IsAllowedTarget，
	// 最后作为 Location 头发给浏览器，用户拿到 302 却打不开。
	c.Host = strings.ToLower(c.Host)

	out := c.String()
	if len(out) > MaxURLLength {
		return "", fmt.Errorf("validator: normalize: %w (%d > %d)", ErrTooLong, len(out), MaxURLLength)
	}
	return out, nil
}

// IsAllowedTarget 在跳转路径上做二次校验：只有 scheme 合法且 host 非空才放行。
// 它接受的是「已入库的字符串」，因此不做补 scheme 处理 —— 不合法就该报警。
func IsAllowedTarget(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Hostname() != ""
}

// RefererHost 从 Referer 头里取出主机名，用于统计「来源分布」。
// 取不到（空值、纯路径、脏数据）时返回空串，调用方应归入「直接访问」。
func RefererHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	// 兜底：手工剥掉 scheme 与 path
	rest := raw
	if _, after, found := strings.Cut(raw, "//"); found {
		rest = after
	}
	host, _, _ := strings.Cut(rest, "/")
	// 去掉可能的 userinfo 与端口
	if _, after, found := strings.Cut(host, "@"); found {
		host = after
	}
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	return strings.ToLower(host)
}
