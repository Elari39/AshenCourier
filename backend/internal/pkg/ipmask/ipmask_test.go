package ipmask

import "testing"

// TestMask 钉住掩码规则：IPv4 抹掉最后一段（/24），IPv6 只留前 4 组（/64）。
// 用例里刻意包含「完整地址绝不能原样出现」的形状，这是这条 API 契约的核心。
func TestMask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "IPv4 抹掉最后一段", in: "203.0.113.7", want: "203.0.113.0/24"},
		{name: "IPv4 私网", in: "192.168.1.42", want: "192.168.1.0/24"},
		{name: "IPv4 首段", in: "10.0.0.255", want: "10.0.0.0/24"},
		{name: "v4-mapped IPv6 归一成 IPv4", in: "::ffff:203.0.113.7", want: "203.0.113.0/24"},
		{name: "IPv6 只留前 4 组", in: "2001:db8:1234:5678:9abc:def0:1234:5678", want: "2001:db8:1234:5678::/64"},
		{name: "IPv6 省略写法", in: "2001:db8::1", want: "2001:db8::/64"},
		{name: "IPv6 环回：主机位全被抹掉", in: "::1", want: "::/64"},
		{name: "两端空白被忽略", in: "  203.0.113.7  ", want: "203.0.113.0/24"},
		{name: "空串", in: "", want: ""},
		{name: "非法字面量", in: "not-an-ip", want: ""},
		{name: "带端口的地址不是 IP", in: "203.0.113.7:8080", want: ""},
		{name: "IPv6 多写了一个冒号", in: "2001:db8:::1", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Mask(tt.in)
			if got != tt.want {
				t.Fatalf("Mask(%q) = %q，期望 %q", tt.in, got, tt.want)
			}
			// 掩码后不允许再出现输入里的完整地址（防「忘了掩码」的回归）
			if tt.in != "" && got == tt.in {
				t.Fatalf("Mask(%q) 原样返回了完整地址", tt.in)
			}
		})
	}
}
