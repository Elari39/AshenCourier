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
		{"mailto 被拒（不误补 https）", "mailto:foo@bar.com", "", ErrBadScheme},
		// 「域名:端口」没写协议：此前会被误读成 scheme=域名，报「仅支持 http/https」，
		// 对没写协议的用户是误导 —— 应按缺 scheme 补 https 处理。
		{"域名带端口缺 scheme", "example.com:8080/x", "https://example.com:8080/x", nil},
		{"localhost 带端口缺 scheme", "localhost:3000/dev", "https://localhost:3000/dev", nil},
		{"域名带端口无路径", "example.com:8443", "https://example.com:8443", nil},
		{"域名端口非数字仍是非法 scheme", "example.com:abc", "", ErrBadScheme},
		// 多字节会被百分号编码放大（一个中文 → 9 字节 ASCII），所以「原始输入没超」
		// 不代表「入库后没超」：这条原始输入 1820 字节能过输入检查，
		// 但 c.String() 之后是 5420 字节，必须被输出检查拦下。
		{"多字节编码后超限", "https://example.com/" + strings.Repeat("中", 600), "", ErrTooLong},
		{"多字节完全超限", "https://example.com/" + strings.Repeat("中", 3000), "", ErrTooLong},
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

// TestLooksLikeHostPort 单测护住「域名:端口」误判为 scheme 的判定逻辑本身。
func TestLooksLikeHostPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		scheme string
		opaque string
		want   bool
	}{
		{"example.com", "8080/x", true},   // 域名 + 数字端口
		{"localhost", "3000", true},       // localhost + 端口
		{"example.com", "8443", true},     // 无路径
		{"example.com", "abc/x", false},   // 端口非数字 → 真 scheme
		{"mailto", "foo@bar", false},      // 真 scheme 无点号
		{"javascript", "alert(1)", false}, // 同上
		{"", "8080", false},               // scheme 为空（走不到这里）
		{"example.com", "", false},        // opaque 为空（是 authority 形态）
	}

	for _, tc := range tests {
		if got := looksLikeHostPort(tc.scheme, tc.opaque); got != tc.want {
			t.Errorf("looksLikeHostPort(%q, %q) = %v, want %v", tc.scheme, tc.opaque, got, tc.want)
		}
	}
}

// TestIsPrivateHost 覆盖「目标不允许指向非公网」的判定。
//
// 重点是那些 netip 拒收、浏览器照收的宽松写法：只挡点分十进制等于没挡 ——
// 127.1 / 2130706433 / 0x7f.1 / 0177.0.0.1 指向的是同一个地址。
func TestIsPrivateHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want bool
		why  string
	}{
		// ---- 应当放行：真实公网站点 ----
		{"example.com", false, "普通域名"},
		{"sub.example.com", false, "多级域名"},
		{"example.com.", false, "末尾点（DNS 里与不带点等价）"},
		{"local.example.com", false, "含 local 但是正常公网域名"},
		{"8.8.8.8", false, "公网 IPv4"},
		{"1.1.1.1", false, "公网 IPv4"},
		{"172.32.0.1", false, "刚好在 172.16/12 之外"},
		{"9.255.255.255", false, "刚好在 10/8 之外"},
		{"100.128.0.1", false, "刚好在 100.64/10 之外"},
		{"2001:4860:4860::8888", false, "公网 IPv6"},
		{"::ffff:8.8.8.8", false, "v4-mapped 的公网地址"},
		{"", false, "空串不归本函数管（语法层报 ErrNoHost）"},

		// ---- 回环：各种写法指向同一个地址 ----
		{"127.0.0.1", true, "点分十进制"},
		{"127.1", true, "段数不足"},
		{"2130706433", true, "单个 32 位整数"},
		{"0x7f000001", true, "整体十六进制"},
		{"0x7f.1", true, "十六进制段 + 段数不足"},
		{"0177.0.0.1", true, "前导零视为八进制"},
		{"127.0.0.1.", true, "末尾点"},
		{"::1", true, "IPv6 回环"},
		{"::ffff:127.0.0.1", true, "v4-mapped 的回环（Unmap 之后才判得出来）"},

		// ---- 私网与特殊段 ----
		{"10.0.0.1", true, "RFC 1918"},
		{"192.168.1.1", true, "RFC 1918"},
		{"172.16.0.1", true, "RFC 1918 下沿"},
		{"172.31.255.255", true, "RFC 1918 上沿"},
		{"100.64.0.1", true, "运营商级 NAT"},
		{"0.0.0.0", true, "未指定"},
		{"169.254.169.254", true, "云元数据端点"},
		{"198.18.0.1", true, "基准测试段"},
		{"203.0.113.9", true, "文档段"},
		{"224.0.0.1", true, "组播"},
		{"255.255.255.255", true, "广播"},
		{"::", true, "IPv6 未指定"},
		{"fc00::1", true, "ULA"},
		{"fd12:3456::1", true, "ULA"},
		{"fe80::1", true, "IPv6 链路本地"},
		{"fe80::1%eth0", true, "带 zone 的链路本地"},
		{"ff02::1", true, "IPv6 组播"},
		{"2001:db8::1", true, "IPv6 文档段"},
		{"2002::1", true, "6to4（已弃用，内嵌 IPv4 可指向内网）"},
		{"64:ff9b::1", true, "NAT64 知名前缀"},

		// ---- 保留域名：名字形式，应用层不做 DNS 解析，只能按名字拒 ----
		{"localhost", true, "RFC 6761"},
		{"LOCALHOST", true, "大小写无关"},
		{"localhost.", true, "末尾点"},
		{"api.localhost", true, "localhost 子域"},
		{"nas.local", true, "mDNS"},
		{"git.internal", true, "企业内网保留 TLD"},
		{"printer.localdomain", true, "传统默认域"},
		{"router.home.arpa", true, "RFC 8375"},

		// ---- 看着像但其实不是 IP 的输入（不该误伤）----
		{"08.8.8.8", false, "08 不是合法八进制，整体不构成 IP"},
		{"010.0.0.1", false, "八进制 010 = 8，落在公网"},
		{"1.2.3.4.5", false, "超过 4 段，不是 IP 形态"},
	}

	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()

			if got := IsPrivateHost(tc.host); got != tc.want {
				t.Fatalf("IsPrivateHost(%q) = %v, want %v（%s）", tc.host, got, tc.want, tc.why)
			}
		})
	}
}

// TestNormalizePublic 覆盖「语法层 + 公网策略」的组合，并钉住两者的边界：
// 语法错误必须原样透传，不能一律改写成 ErrPrivateHost —— 调用方要靠它区分
// 「用户写错了」与「策略不允许」，否则给不出正确的提示，也说不出还有
// ALLOW_PRIVATE_TARGETS 这条出路。
func TestNormalizePublic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		allow   bool
		want    string
		wantErr error
	}{
		{"公网正常通过", "https://example.com/a?b=1", false, "https://example.com/a?b=1", nil},
		{"大写与端口不影响判定", "HTTPS://Example.COM:8443/Path", false, "https://example.com:8443/Path", nil},
		{"缺 scheme 补全后仍是公网", "example.com/x", false, "https://example.com/x", nil},
		{"回环被拒", "http://127.0.0.1:22/", false, "", ErrPrivateHost},
		{"宽松写法同样被拒", "http://2130706433/", false, "", ErrPrivateHost},
		{"私网被拒", "http://192.168.1.1/admin", false, "", ErrPrivateHost},
		{"云元数据端点被拒", "http://169.254.169.254/latest/meta-data/", false, "", ErrPrivateHost},
		{"IPv6 回环被拒", "http://[::1]:8080/x", false, "", ErrPrivateHost},
		{"localhost 被拒（补全 scheme 之后同样被拒）", "localhost:3000/dev", false, "", ErrPrivateHost},
		{"保留域名被拒", "https://git.internal/repo", false, "", ErrPrivateHost},
		{"开关打开后私网放行", "http://127.0.0.1:22/", true, "http://127.0.0.1:22/", nil},
		{"开关打开后保留域名也放行", "https://nas.local/x", true, "https://nas.local/x", nil},

		// 语法错误的种类不能被策略改写
		{"非法 scheme 仍报 ErrBadScheme", "javascript:alert(1)", false, "", ErrBadScheme},
		{"空串仍报 ErrEmpty", "", false, "", ErrEmpty},
		{"缺主机名报 ErrNoHost（不是 ErrPrivateHost）", "https://", false, "", ErrNoHost},
		{"超长仍报 ErrTooLong", "https://example.com/" + strings.Repeat("a", MaxURLLength), false, "", ErrTooLong},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizePublic(tc.in, tc.allow)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NormalizePublic(%q, allow=%v) 错误 = %v, want %v", tc.in, tc.allow, err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if got != tc.want {
				t.Fatalf("NormalizePublic(%q, allow=%v) = %q, want %q", tc.in, tc.allow, got, tc.want)
			}
		})
	}
}

// TestNormalizeStaysPure 钉住「语法层与策略层分离」这个设计决定：
// 私网目标在 Normalize 这里必须**成功**返回。
//
// 这不是顺带的行为，而是跳转路径的安全前提：策略收紧之前创建的历史链接
// （http://127.0.0.1/ …）必须仍然能跳。收紧创建只是「以后不许再建」，
// 若语法层也开始拒绝，IsAllowedTarget 会跟着拒掉它们 —— 那些链接会在
// 某次部署后成片失效，那不是安全，是摧毁数据。历史链接只能由运营去
// 列表里改或删。
func TestNormalizeStaysPure(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"http://127.0.0.1:22/",
		"http://192.168.1.1/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/x",
		"https://nas.local/share",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			out, err := Normalize(in)
			if err != nil {
				t.Fatalf("Normalize(%q) 必须成功（语法层不管策略），实际错误：%v", in, err)
			}
			if out != in {
				t.Fatalf("Normalize(%q) = %q，期望原样", in, out)
			}
			if !IsAllowedTarget(out) {
				t.Fatalf("IsAllowedTarget(%q) 必须为 true：跳转路径不该被策略收紧波及", out)
			}
		})
	}
}

// TestNonPublicPrefixesAreSane 是前缀表本身的下限守卫。
//
// 那张表是纯数据：删掉一行不会有任何行为测试变红，却会静默放开一整段
// 内网地址（最典型的是把 169.254.0.0/16 删掉 —— 云元数据端点就又能用了）。
// 所以这里既查重复也查「必守的那几条还在不在」。
func TestNonPublicPrefixesAreSane(t *testing.T) {
	t.Parallel()

	required := []string{
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
	}

	have := make(map[string]bool, len(nonPublicPrefixes))
	for _, p := range nonPublicPrefixes {
		if have[p.String()] {
			t.Errorf("前缀表里有重复项：%s", p)
		}
		have[p.String()] = true
	}
	for _, want := range required {
		if !have[want] {
			t.Errorf("前缀表缺少 %s —— 它挡的是一整类滥用，删掉会静默放开", want)
		}
	}
	if len(nonPublicPrefixes) < 20 {
		t.Errorf("前缀表只剩 %d 条（下限 20），像是被误删过", len(nonPublicPrefixes))
	}
}
