package shortcode

import (
	"maps"
	"slices"
)

// 保留字表：这些前缀/路径已经在 nginx 或前端路由层被占用，
// 一旦被注册成短码就会「吃掉」真实页面或 API。
//
// ⚠️ 硬约束：前端新增任何顶级路由，必须同步加到本表，并更新 shortcode_test.go
// 里 TestReservedSetContents 的 wantContains 断言（不是 reserved_test.go，
// 那个文件并不存在）。
// 详见 PLAN.md §9.1 与 README「新增前端顶级路由」一节。
var reserved = map[string]struct{}{
	// --- 基础设施 / 静态资源 ---
	"api":           {},
	"assets":        {},
	"healthz":       {},
	"metrics":       {}, // Prometheus 抓取端点：7 位、正好落在短码正则里，必须挡住
	"static":        {},
	"public":        {},
	"cdn":           {},
	"media":         {},
	"img":           {},
	"images":        {},
	"favicon":       {},
	"favicon.ico":   {},
	"robots.txt":    {},
	"sitemap.xml":   {},
	"manifest.json": {},
	"index.html":    {},
	"sw.js":         {},
	".well-known":   {},
	"_next":         {},

	// --- 前端 SPA 顶级路由 ---
	"login":     {},
	"register":  {},
	"logout":    {},
	"dashboard": {},
	"links":     {},
	"link":      {},
	"settings":  {},
	"account":   {},
	"profile":   {},

	// --- 易被误用 / 品牌与合规页 ---
	"admin":     {},
	"root":      {},
	"system":    {},
	"auth":      {},
	"oauth":     {},
	"signin":    {},
	"signup":    {},
	"stats":     {},
	"analytics": {},
	"about":     {},
	"help":      {},
	"docs":      {},
	"blog":      {},
	"terms":     {},
	"privacy":   {},
	"legal":     {},
	"contact":   {},
	"pricing":   {},
	"new":       {},
	"create":    {},
	"edit":      {},
	"delete":    {},
}

// IsReserved 判断短码是否为保留字。比较不区分大小写：
// nginx 的 location 匹配在 Linux 上区分大小写，但 /API 这类路径既打不开页面
// 也拿不到 API，直接一并拒绝最省心。
func IsReserved(code string) bool {
	if code == "" {
		return false
	}
	if _, ok := reserved[lowerASCII(code)]; ok {
		return true
	}
	return false
}

// ReservedCodes 返回保留字表的快照（已排序），供文档生成与单测断言使用。
func ReservedCodes() []string { return slices.Sorted(maps.Keys(reserved)) }

// lowerASCII 只折叠 ASCII 大写字母，避免 strings.ToLower 的 Unicode 开销与意外行为。
func lowerASCII(s string) string {
	hasUpper := false
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
