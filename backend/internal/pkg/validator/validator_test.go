package validator

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"原样 https", "https://example.com/a/b?c=1", "https://example.com/a/b?c=1", nil},
		{"缺 scheme 补 https", "example.com/very/long/path?x=1", "https://example.com/very/long/path?x=1", nil},
		{"http 保留", "http://example.com", "http://example.com", nil},
		{"首尾空白被裁掉", "  https://example.com  ", "https://example.com", nil},
		{"大写 scheme 与小写化 host", "HTTPS://Example.COM/Path", "https://example.com/Path", nil},
		{"带端口", "https://example.com:8443/x", "https://example.com:8443/x", nil},
		// IPv6 字面量：方括号必须原样保留。
		// 曾经的实现用 Hostname() 重组 host，会剥掉方括号写出 http://::1:8080/x，
		// 这个坏地址能过 DB 的 scheme CHECK 与 IsAllowedTarget，最终 302 到打不开的地址。
		{"IPv6 字面量带端口", "http://[::1]:8080/path", "http://[::1]:8080/path", nil},
		{"IPv6 字面量无端口", "http://[::1]/path", "http://[::1]/path", nil},
		{"IPv6 大写折叠保留方括号", "http://[::FFFF:1.2.3.4]:80/x", "http://[::ffff:1.2.3.4]:80/x", nil},
		{"带 userinfo", "https://user:pass@example.com/x", "https://user:pass@example.com/x", nil},
		{"保留 fragment", "https://example.com/a#frag", "https://example.com/a#frag", nil},
		{"路径大小写不动", "https://example.com/AbC", "https://example.com/AbC", nil},
		{"空串", "", "", ErrEmpty},
		{"全空白", "   ", "", ErrEmpty},
		{"javascript 协议被拒", "javascript:alert(1)", "", ErrBadScheme},
		{"data 协议被拒", "data:text/html,<h1>x</h1>", "", ErrBadScheme},
		{"file 协议被拒", "file:///etc/passwd", "", ErrBadScheme},
		{"ftp 协议被拒", "ftp://example.com/x", "", ErrBadScheme},
		{"无主机名", "https://", "", ErrNoHost},
		{"超长", "https://example.com/" + strings.Repeat("a", MaxURLLength), "", ErrTooLong},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Normalize(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Normalize(%q) 错误 = %v, want %v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"https://example.com/a/b?c=1",
		"example.com/very/long/path?x=1",
		"HTTPS://Example.COM/Path?Q=1#F",
		"http://sub.example.co.uk:8080/%E4%B8%AD%E6%96%87",
		"http://[::1]:8080/ipv6",
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			once, err := Normalize(in)
			if err != nil {
				t.Fatalf("首次 Normalize(%q) 失败：%v", in, err)
			}
			twice, err := Normalize(once)
			if err != nil {
				t.Fatalf("二次 Normalize(%q) 失败：%v", once, err)
			}
			if once != twice {
				t.Fatalf("Normalize 不幂等：%q → %q", once, twice)
			}
		})
	}
}

func TestIsAllowedTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"https 正常", "https://example.com/a", true},
		{"http 正常", "http://example.com", true},
		{"无 scheme", "example.com", false},
		{"javascript", "javascript:alert(1)", false},
		{"data", "data:text/html,x", false},
		{"无 host", "https://", false},
		{"空串", "", false},
		{"协议相对", "//example.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsAllowedTarget(tc.in); got != tc.want {
				t.Fatalf("IsAllowedTarget(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRefererHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"完整 URL", "https://twitter.com/user/status/1", "twitter.com"},
		{"无路径", "https://twitter.com", "twitter.com"},
		{"带端口", "https://news.ycombinator.com:8443/item?id=1", "news.ycombinator.com"},
		{"大写折叠", "HTTPS://Weibo.COM/a", "weibo.com"},
		{"无 scheme", "twitter.com/x", "twitter.com"},
		{"空值", "", ""},
		{"只给了路径", "/foo/bar", ""},
		{"带 userinfo", "https://user:pass@example.com/x", "example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := RefererHost(tc.in); got != tc.want {
				t.Fatalf("RefererHost(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
