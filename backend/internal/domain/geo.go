package domain

// GeoLocator 把客户端 IP 解析成 ISO 3166-1 alpha-2 国家代码（大写，如 "CN"、"US"）。
//
// 「不知道」一律表示为**空串**：库没配置、IP 非法、库里没有这个网段、库里没有那个字段，
// 调用方都不需要区分 —— 空串最终会落成 `click_events.country` 的 NULL。
//
// 端口定义在 domain（而不是让 worker 直接依赖 pkg/geoip）有两个好处：
//   - maxminddb 这个第三方依赖不会渗进 worker 的 import 列表，worker 依然只依赖抽象；
//   - 单测可以塞一个手写 fake，不需要任何 mmdb 文件（CI 里就没有）。
type GeoLocator interface {
	// CountryOf 返回 ip 的国家代码；解析不出来返回空串。
	CountryOf(ip string) string
}
