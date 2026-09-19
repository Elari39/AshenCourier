package hostname

import "testing"

// TestNormalize 覆盖每一种真实出现过的 Host 写法。
//
// 这些用例的价值在于：任何一条没被归一化，症状都是「域名已登记但短链 404」，
// 而那种 bug 只在特定的 Host 写法下复现 —— 是本机 curl 与浏览器都对、
// 只有某个反向代理的写法出错的那一类。
func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{"原样小写", "example.com", "example.com"},
		{"大写转小写", "Example.COM", "example.com"},
		{"带端口剥掉", "example.com:8080", "example.com"},
		{"尾点剥掉", "example.com.", "example.com"},
		{"大写 + 端口 + 尾点", "Example.COM.:443", "example.com"},
		{"首尾空白", "  example.com  ", "example.com"},
		{"多级域名", "a.b.example.co.uk", "a.b.example.co.uk"},
		{"单标签主机", "localhost", "localhost"},
		{"单标签带端口", "localhost:8080", "localhost"},
		{"IPv4", "203.0.113.7", "203.0.113.7"},
		{"IPv4 带端口", "203.0.113.7:8080", "203.0.113.7"},
		{"IPv6 字面量", "[2001:db8::1]", "2001:db8::1"},
		{"IPv6 字面量带端口", "[2001:db8::1]:8080", "2001:db8::1"},
		{"空串", "", ""},
		{"只有空白", "   ", ""},
		{"只有尾点", ".", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Normalize(tc.host); got != tc.want {
				t.Errorf("Normalize(%q) = %q，期望 %q", tc.host, got, tc.want)
			}
		})
	}
}
