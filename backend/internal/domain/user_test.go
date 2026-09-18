package domain

import (
	"strings"
	"testing"
)

// TestIsValidEmail 守住注释与实现的一致：注释写的是「含单个 '@'」，
// 而 IndexByte 只找得到第一个 '@'，a@b@c.com 曾经会被判为合法。
func TestIsValidEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"普通邮箱", "a@b.com", true},
		{"子域 + 加号标签", "user.name+tag@mail.example.co.uk", true},
		{"大写域名也可接受（唯一性交给 lower(email) 索引）", "A@B.COM", true},
		{"两个 @ 必须拒掉", "a@b@c.com", false},
		{"缺少 @", "abc.com", false},
		{"以 @ 开头", "@b.com", false},
		{"以 @ 结尾", "a@", false},
		{"域名没有点", "a@localhost", false},
		{"点结尾", "a@b.", false},
		{"含空格", "a b@c.com", false},
		{"含换行（请求头走私常见手法）", "a@b.com\r\nX-Evil: 1", false},
		{"空串", "", false},
		{"过短", "a@", false},
		{"超过 254 字节", strings.Repeat("a", 250) + "@b.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsValidEmail(tc.in); got != tc.want {
				t.Fatalf("IsValidEmail(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeEmail 统一 trim + 小写，供查询与唯一性比较使用。
func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"  User@Example.COM  ", "user@example.com"},
		{"already@lower.com", "already@lower.com"},
		{"", ""},
	}

	for _, tc := range tests {
		if got := NormalizeEmail(tc.in); got != tc.want {
			t.Fatalf("NormalizeEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
