// Package hostname 把 HTTP Host 头归一成可比较的主机名。
//
// 为什么值得单独一个包：分域之后，「这个请求打到哪个域」是解析短码的第一步，
// 而 Host 头的形态比想象中杂 —— `Example.COM:8080`、`example.com.`（带尾点，
// 合法且等价于 `example.com`）、`[::1]:8080`。任何一处没归一化，
// 症状都是「域名明明登记了，短链却 404」，而且只在特定的 Host 写法下复现。
package hostname

import (
	"net"
	"strings"
)

// Normalize 把 Host 头归一成小写、不含端口与尾点的主机名。
//
// 归一化规则（每一步都对应一种真实的 Host 写法）：
//   - 去掉首尾空白：某些客户端会在头值两侧留空格
//   - 剥掉端口：`example.com:8080` 与 `example.com` 是同一个域
//   - 脱掉 IPv6 字面量的方括号：`[::1]` 与 `[::1]:8080` 都得到 `::1`
//     （两种写法必须落到同一个名字，否则域名表登记了其中一种就漏掉另一种）
//   - 去掉尾点：`example.com.` 是 DNS 的绝对形式，与 `example.com` 等价
//   - 转小写：DNS 大小写不敏感
//
// 解析不出主机名时（例如空串）返回空串 —— 调用方据此走「默认域名」分支。
func Normalize(host string) string {
	h := strings.TrimSpace(host)
	if h == "" {
		return ""
	}

	// 带端口时交给 net.SplitHostPort（它会正确处理 IPv6 的方括号）。
	// 不带端口时它会报错，此时 h 原样保留 —— 不能因为「没有端口」就丢掉主机名。
	if withoutPort, _, err := net.SplitHostPort(h); err == nil {
		h = withoutPort
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		// 裸的 IPv6 字面量：带端口那条路已经脱了括号，这里不脱就与它不一致 ——
		// `[::1]` 与 `[::1]:8080` 会变成两个不同的域名，登记一个漏一个。
		h = h[1 : len(h)-1]
	}

	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}
