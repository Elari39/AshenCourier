// Package ipmask 把 IP 掩码成「网络前缀」形式，供对外的点击明细接口使用。
//
// 明细页是给人看的：知道这次访问来自哪个网段就够了，不需要精确到某一台机器。
// 原始 IP 仍然在入库时完整保留（click_events.ip），风控与排障可以直接查库；
// 不让它出现在 API 响应里，就不会被浏览器日志、截图和共享看板带出去。
package ipmask

import (
	"net/netip"
	"strings"
)

// 掩码后保留的前缀长度。
const (
	ipv4PrefixBits = 24
	ipv6PrefixBits = 64
)

// Mask 把 raw 掩码成网络前缀，例如 "203.0.113.0/24"、"2001:db8:1234:5678::/64"。
//
// 用 CIDR 写法而不是 "203.0.113.x"：前者明确表示「这是一个网段」，
// 不会让人误以为它是某个真实主机地址；v4 与 v6 的展示形状也统一。
//
// 解析失败（空串、非法地址）返回空串，调用方按「没有 IP 信息」处理 ——
// 这里刻意不返回 error：一条明细的 IP 坏了不该让整页明细 5xx。
func Mask(raw string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	// ::ffff:203.0.113.7 这类 v4-mapped 地址按 IPv4 处理：
	// 否则同一台机器会因为连接方式不同（直连 / 代理）显示成两种前缀长度。
	addr = addr.Unmap()

	bits := ipv4PrefixBits
	if addr.Is6() {
		bits = ipv6PrefixBits
	}

	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	// Masked 显式清零主机位。Prefix.String 目前内部也会掩码，
	// 但依赖那个实现细节不如自己写清楚。
	return prefix.Masked().String()
}
