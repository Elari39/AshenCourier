// Package validator 负责「目标 URL」的规范化与方案白名单校验。
//
// 这是开放重定向防线的第一道闸门：
//   - DB 侧有 CHECK (target_url ~* '^https?://') 兜底（防止历史脏数据）
//   - 跳转时还会用 IsAllowedTarget 二次校验（防止绕过应用层的写入）
//
// 两层职责刻意分开，不要合并：
//   - Normalize / IsAllowedTarget 是**语法层**（「这是不是一个可用的 http(s) URL」），
//     没有开关、结果只取决于输入，Normalize 因此有幂等性契约；
//   - NormalizePublic 追加**策略层**（「这个目标允不允许」），它是可配置的。
//     策略会随运营需要变化，混进语法函数会让「规范化」变成一个有状态行为，
//     也会让幂等性测试的语义变得说不清。
package validator

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// MaxURLLength 是目标 URL 的最大长度。
//
// 两处长度检查都用字节数，与 DB 的 links_target_url_len 不冲突：
// 入库的是 c.String() 的输出，而它已把非 ASCII 百分号编码成纯 ASCII
// （一个中文字符 → %XX%XX%XX 共 9 字节），编码只会变长、不会变短；
// 因此「原始输入超过上限 ⇒ 编码后必然超限」，按字节卡原始输入不会
// 误拒任何 DB 能接受的值 —— 它只是把必然失败的请求提前挡掉。
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
	// ErrPrivateHost 表示 host 落在非公网范围（回环 / 私网 / 链路本地 / 保留段）。
	//
	// 注意它**不是语法错误**：这个字符串是一个完全合法的 http(s) URL，
	// 只是不允许当短链目标。所以它只会从 NormalizePublic 返回，
	// 纯语法层的 Normalize 永远不会给出它 —— 调用方据此判断「要不要给用户
	// 提 ALLOW_PRIVATE_TARGETS 这条出路」。
	ErrPrivateHost = errors.New("target host is not publicly routable")
)

// Normalize 规范化目标 URL：
//  1. 去掉首尾空白；空串、超长直接拒绝（长度按字符数计，与 DB 的 char_length 同口径）
//  2. 缺 scheme 时按 https 补齐（用户粘贴 example.com/xx 是常见输入）
//  3. scheme 只允许 http / https，host 必须存在
//  4. 小写化 scheme 与 host，其余部分原样保留（path 大小写有意义）
//
// 返回值为规范化后的字符串，可作为 links.target_url 直接入库。
//
// 它只做语法。写入路径要用的通常是 NormalizePublic（多一层「必须指向公网」的策略）。
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
	// 补 scheme 的判定不能只看 u.Scheme == ""：
	// "example.com:8080/x" 会被 url.Parse 拆成 scheme="example.com" + opaque="8080/x"
	// （scheme 的合法字符集里本来就有 '.'），随后落进 ErrBadScheme —— 用户并没有写
	// 协议，报「仅支持 http/https」就成了误导。形如「主机名:纯数字端口」的输入
	// 在这里一律按「缺 scheme」补齐再解析。
	if u.Scheme == "" || looksLikeHostPort(u.Scheme, u.Opaque) {
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

// looksLikeHostPort 判定「scheme + opaque」形态是否其实是 host:port 被误读。
//
// 条件（全部满足才认）：
//   - scheme 形如主机名：含点号（example.com / 127.0.0.1）或是 localhost
//   - opaque 的首段是纯数字端口（8080/x 的 "8080"）
//
// 这样 mailto:foo、javascript:alert(1) 这类真 scheme 不会被误补 https，
// 而用户忘写协议的「域名:端口」输入能落到正确的补全分支。
func looksLikeHostPort(scheme, opaque string) bool {
	if scheme == "" || opaque == "" {
		return false
	}
	if !strings.Contains(scheme, ".") && !strings.EqualFold(scheme, "localhost") {
		return false
	}
	port, _, _ := strings.Cut(opaque, "/")
	if port == "" {
		return false
	}
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

// NormalizePublic 在 Normalize 之上叠加一条策略：目标必须指向公网地址。
//
// allowPrivate 为 true 时它退化成 Normalize —— 内网部署（公司内部短链，
// 目标本来就在私网里）与本地开发需要这条出路，对应环境变量
// ALLOW_PRIVATE_TARGETS。默认是拒绝：公网短链服务被拿来当内网探测跳板
// （把访问者的浏览器指到 127.0.0.1 / 192.168.x / 169.254.169.254）是最廉价的一类滥用。
//
// ⚠️ 判定只针对**字面量地址与保留域名**，不发起任何 DNS 解析。这是刻意的：
//   - 创建路径不该有网络依赖（引入延迟、失败分支与另一条超时路径）
//   - 就算解析了也拦不住：域名可以先解析到公网、再改指内网（DNS rebinding），
//     或者干脆只在访问者所在的网络里解析成内网地址（split-horizon DNS）。
//     真正的出口控制在网络策略层，不在应用层。
//
// 所以这是「挡住最廉价的一类滥用」，**不是 SSRF 防护**。见 README「已知限制」。
func NormalizePublic(raw string, allowPrivate bool) (string, error) {
	out, err := Normalize(raw)
	if err != nil {
		return "", err
	}
	if allowPrivate {
		return out, nil
	}
	u, err := url.Parse(out)
	if err != nil {
		return "", fmt.Errorf("validator: normalize public %q: %w", out, ErrMalformed)
	}
	if host := u.Hostname(); IsPrivateHost(host) {
		return "", fmt.Errorf("validator: normalize public %q: %w (%s)", out, ErrPrivateHost, host)
	}
	return out, nil
}

// IsPrivateHost 判定主机名是否指向「公网访客不可能访问到」的地址。
//
// 传入的应当是 URL 的 host（不含 userinfo 与端口）。空串返回 false：
// 「没有主机名」是语法问题，归 Normalize 的 ErrNoHost 管，混进来会让本函数
// 的语义变成「空串也是私网」，误伤任何忘记先归一化的调用点。
func IsPrivateHost(host string) bool {
	// 末尾点（example.com.）在 DNS 里与不带点等价，先归一化掉再比后缀
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" {
		return false
	}
	// 裸名形式：没有任何点，后缀比对匹配不到
	if h == "localhost" || h == "local" || h == "internal" {
		return true
	}
	for _, suffix := range reservedHostSuffixes {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	// 名字不是保留名 → 只剩「字面量地址」一种可能
	addr, ok := parseLooseIP(h)
	if !ok {
		return false
	}
	return isNonPublicAddr(addr)
}

// nonPublicPrefixes 是不允许作为短链目标的地址段。
//
// 来源：RFC 6890 的特殊用途地址表，加上云元数据端点（169.254.169.254 落在
// 169.254.0.0/16 内）与几个已弃用但解析器仍认得的过渡段。
var nonPublicPrefixes = []netip.Prefix{
	// ---- IPv4 ----
	netip.MustParsePrefix("0.0.0.0/8"),       // 本网络
	netip.MustParsePrefix("10.0.0.0/8"),      // 私网（RFC 1918）
	netip.MustParsePrefix("100.64.0.0/10"),   // 运营商级 NAT（RFC 6598）
	netip.MustParsePrefix("127.0.0.0/8"),     // 回环
	netip.MustParsePrefix("169.254.0.0/16"),  // 链路本地，含云元数据端点
	netip.MustParsePrefix("172.16.0.0/12"),   // 私网（RFC 1918）
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF 协议分配
	netip.MustParsePrefix("192.0.2.0/24"),    // 文档 TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 中继任播（已弃用）
	netip.MustParsePrefix("192.168.0.0/16"),  // 私网（RFC 1918）
	netip.MustParsePrefix("198.18.0.0/15"),   // 基准测试
	netip.MustParsePrefix("198.51.100.0/24"), // 文档 TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // 文档 TEST-NET-3
	netip.MustParsePrefix("224.0.0.0/4"),     // 组播
	netip.MustParsePrefix("240.0.0.0/4"),     // 保留（含广播 255.255.255.255）

	// ---- IPv6 ----
	netip.MustParsePrefix("::/128"),         // 未指定
	netip.MustParsePrefix("::1/128"),        // 回环
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 知名前缀（内嵌 IPv4）
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 本地用途
	netip.MustParsePrefix("100::/64"),       // 丢弃前缀（RFC 6666）
	netip.MustParsePrefix("2001:2::/48"),    // 基准测试
	netip.MustParsePrefix("2001:db8::/32"),  // 文档
	netip.MustParsePrefix("2002::/16"),      // 6to4（已弃用，内嵌 IPv4 可指向内网）
	netip.MustParsePrefix("3fff::/20"),      // 文档（RFC 9637）
	netip.MustParsePrefix("fc00::/7"),       // 唯一本地地址（ULA）
	netip.MustParsePrefix("fe80::/10"),      // 链路本地
	netip.MustParsePrefix("ff00::/8"),       // 组播
}

// reservedHostSuffixes 是保留用途的私有域名后缀，一律不允许作为目标 host。
//
// 这些是**名字**而不是地址：解析器会把它们指到本机或局域网，
// 而应用层看不到解析结果（我们不做 DNS 查询），只能按名字拒。
var reservedHostSuffixes = [...]string{
	".localhost",   // RFC 6761：任何解析器都必须指向回环
	".local",       // RFC 6762：mDNS，局域网内自解析
	".localdomain", // 传统家庭/办公网的默认域
	".internal",    // ICANN 保留的私有用途 TLD（企业内网）
	".home.arpa",   // RFC 8375：家庭网络
}

// isNonPublicAddr 判定一个已解析的地址是否落在非公网段。
func isNonPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	// v4-mapped（::ffff:8.8.8.8）必须先 Unmap 再比前缀：
	// 否则它与 IPv4 前缀的地址族不一致，Contains 全部返回 false，
	// 于是 ::ffff:127.0.0.1 会被当成公网地址放行。
	addr = addr.Unmap()
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseLooseIP 解析主机名里的字面量地址，兼容 netip 拒收但浏览器照收的写法。
func parseLooseIP(host string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(host); err == nil {
		// 剥掉 zone（fe80::1%eth0 里的 %eth0）：它不影响地址属于哪个段，
		// 而带 zone 的地址与前缀比对的行为容易让人意外
		return addr.WithZone(""), true
	}
	return parseLooseIPv4(host)
}

// parseLooseIPv4 解析 inet_aton 风格的各种 IPv4 写法。
//
// 为什么不能只靠 netip.ParseAddr：浏览器与 libc 都接受下面这些形式，
// 它们**指向同一个地址** —— 只挡点分十进制等于没挡：
//
//	127.1            → 127.0.0.1（段数不足，最后一段吃掉剩余位宽）
//	2130706433       → 127.0.0.1（单个 32 位整数）
//	0x7f.1           → 127.0.0.1（十六进制段）
//	0177.0.0.1       → 127.0.0.1（前导零视为八进制）
//	0x7f000001       → 127.0.0.1（整体十六进制）
//	127.0.0.1.       → 127.0.0.1（末尾点）
//
// 误伤面很小：能被这里解析出来的输入，形如「纯数字」或「数字点分」，
// 都不是合法域名（没有 TLD），本来就不可能是一个真实站点。
func parseLooseIPv4(s string) (netip.Addr, bool) {
	s = strings.TrimSuffix(s, ".")
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	var vals [4]uint64
	for i, p := range parts {
		v, ok := parseLooseUint(p)
		if !ok {
			return netip.Addr{}, false
		}
		vals[i] = v
	}

	// 位宽分配遵循 inet_aton：前面每段 8 位，最后一段吃掉剩下的全部
	var n uint64
	switch len(parts) {
	case 1:
		if vals[0] > 0xFFFF_FFFF {
			return netip.Addr{}, false
		}
		n = vals[0]
	case 2:
		if vals[0] > 0xFF || vals[1] > 0xFF_FFFF {
			return netip.Addr{}, false
		}
		n = vals[0]<<24 | vals[1]
	case 3:
		if vals[0] > 0xFF || vals[1] > 0xFF || vals[2] > 0xFFFF {
			return netip.Addr{}, false
		}
		n = vals[0]<<24 | vals[1]<<16 | vals[2]
	default:
		for _, v := range vals {
			if v > 0xFF {
				return netip.Addr{}, false
			}
		}
		n = vals[0]<<24 | vals[1]<<16 | vals[2]<<8 | vals[3]
	}
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}), true
}

// parseLooseUint 按 inet_aton 的进制约定解析一段数字：
// 0x / 0X 开头是十六进制，单个前导 0 是八进制，其余十进制。
//
// 解析失败返回 false（而不是零值），避免把 "abc" 当成 0 —— 那会把
// example.com 这种正常域名误判成 address 0.0.0.0。
func parseLooseUint(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	base, body := 10, s
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		base, body = 16, s[2:]
	case len(s) > 1 && s[0] == '0':
		base, body = 8, s[1:]
	}
	v, err := strconv.ParseUint(body, base, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
