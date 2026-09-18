// Package ua 是一个「够用就好」的 User-Agent 解析器。
//
// 目标不是完整覆盖 UA 数据库（那是 uap-go 的活儿），而是在统计页面上
// 给出 desktop / mobile / tablet / bot 与主流浏览器 / 操作系统的粗略分布。
// 解析失败一律归入 unknown，绝不 panic。
package ua

import (
	"slices"
	"strings"
)

// 设备类型取值，同时也是 click_events.device 的枚举。
const (
	DeviceDesktop = "desktop"
	DeviceMobile  = "mobile"
	DeviceTablet  = "tablet"
	DeviceBot     = "bot"
	DeviceUnknown = "unknown"
)

// 解析不出来时的兜底取值。
const (
	ValueUnknown = "unknown"
	ValueOther   = "other"
)

// Info 是一次 UA 解析的结果。
type Info struct {
	Device  string
	Browser string
	OS      string
}

// Parse 解析 UA 字符串。空串直接返回全 unknown。
func Parse(userAgent string) Info {
	raw := strings.TrimSpace(userAgent)
	if raw == "" {
		return Info{Device: DeviceUnknown, Browser: ValueUnknown, OS: ValueUnknown}
	}
	// 只做一次小写化：后面所有匹配都在小写串上进行
	s := strings.ToLower(raw)

	return Info{
		Device:  detectDevice(s),
		Browser: detectBrowser(s),
		OS:      detectOS(s),
	}
}

// detectDevice 判定设备类型；bot 优先级最高（爬虫也常伪装成手机）。
func detectDevice(s string) string {
	if hasAny(s, botMarkers...) {
		return DeviceBot
	}
	if hasAny(s, "ipad", "tablet", "playbook", "silk/") {
		return DeviceTablet
	}
	// Android 平板不带 "mobile" 标记，Android 手机带
	if strings.Contains(s, "android") {
		if strings.Contains(s, "mobile") {
			return DeviceMobile
		}
		return DeviceTablet
	}
	if hasAny(s, "iphone", "ipod", "windows phone", "blackberry", "opera mini", "iemobile") {
		return DeviceMobile
	}
	if hasAny(s, "windows nt", "macintosh", "mac os x", "linux", "cros", "x11") {
		return DeviceDesktop
	}
	return DeviceUnknown
}

// detectBrowser 判定浏览器；顺序即优先级，必须先排除基于 Chrome 内核的换皮浏览器。
func detectBrowser(s string) string {
	switch {
	case hasAny(s, botMarkers...):
		return "Bot"
	case hasAny(s, "edg/", "edgios/", "edga/", "edge/"):
		return "Edge"
	case hasAny(s, "opr/", "opera mini", "opera mobi"):
		return "Opera"
	case hasAny(s, "samsungbrowser"):
		return "Samsung Internet"
	case hasAny(s, "ucbrowser"):
		return "UC Browser"
	case hasAny(s, "firefox/", "fxios/"):
		return "Firefox"
	case hasAny(s, "msie ", "trident/"):
		return "IE"
	case hasAny(s, "crios/", "chrome/", "chromium/"):
		return "Chrome"
	case strings.Contains(s, "safari/"):
		return "Safari"
	default:
		return ValueUnknown
	}
}

// detectOS 判定操作系统。
func detectOS(s string) string {
	switch {
	case hasAny(s, "iphone", "ipad", "ipod"):
		return "iOS"
	case strings.Contains(s, "android"):
		return "Android"
	case hasAny(s, "windows nt", "windows phone", "win32", "win64"):
		return "Windows"
	case hasAny(s, "mac os x", "macintosh", "darwin"):
		return "macOS"
	case hasAny(s, "cros"):
		return "ChromeOS"
	case hasAny(s, "freebsd", "openbsd"):
		return "BSD"
	case strings.Contains(s, "linux"):
		return "Linux"
	default:
		return ValueUnknown
	}
}

// botMarkers 是判定爬虫/无头客户端的关键词。
var botMarkers = []string{
	"bot", "crawler", "spider", "crawling", "slurp",
	"curl/", "wget", "python-requests", "python-urllib", "go-http-client",
	"java/", "okhttp", "axios/", "node-fetch", "httpclient",
	"headlesschrome", "phantomjs", "puppeteer", "playwright",
	"facebookexternalhit", "twitterbot", "slackbot", "telegrambot",
	"whatsapp", "skypeuripreview", "yandexbot", "bingpreview",
}

// hasAny 判断 s 是否包含 markers 中任意一个（大小写已在调用前统一为小写）。
func hasAny(s string, markers ...string) bool {
	return slices.ContainsFunc(markers, func(m string) bool {
		return strings.Contains(s, m)
	})
}
