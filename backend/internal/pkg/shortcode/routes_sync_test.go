package shortcode

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// 「新增前端顶级路由必须四处同步」这条规则原先只写在注释与 README 里，
// 于是它会漂移 —— 实测漂过一次：`terms` / `privacy` 在 nginx 与保留字表里都有，
// 只有 `frontend/vite.config.ts` 的 SPA_ROUTES 漏了，表现为本地 `pnpm dev` 下
// 刷新 `/terms` 被短码正则当成短码打到后端拿 404，而生产 nginx 返回 200 HTML。
//
// 这个测试把那条规则变成可执行的断言：同时读三个文件做交叉比对。
// 它跨出了 backend 模块（去读仓库根的 deploy/ 与 frontend/），因此路径按
// `backend/internal/pkg/shortcode` 相对仓库根推算；仓库没完整检出时透明 SKIP
// （与 store 集成测试同样的处理：跳过要说出来，不能静默绿）。

// minExpectedRoutes 是解析结果的下限，用来防止「两边都解析成空切片」导致的假绿 ——
// 那是最容易发生的失效模式：正则一改、清单没解析出来，空 == 空 照样通过。
//
// 2026-09-22 从 5 下调到 4：那轮审计把 10 条**前端并不存在**的路由从 nginx / vite 里删了
// （它们被 `try_files` 兜到 index.html，于是「不存在的页面」以 200 返回）。
// 现在真实的顶级路由就是 4 条：login / register / dashboard / links。
// 下限不能再降 —— 它同时是「解析逻辑失效」的探针。
const minExpectedRoutes = 4

// minExpectedFrontendRoutes 是前端路由表解析结果的下限，作用同上。
const minExpectedFrontendRoutes = 4

// nginxExactLocationRE 抓 `location = /xxx {`。带点的（favicon.ico / robots.txt）
// 因为后面紧跟的不是 `{` 而不会被抓成 `favicon` / `robots`。
var nginxExactLocationRE = regexp.MustCompile(`(?m)^\s*location\s*=\s*/([A-Za-z0-9_-]+)\s*\{`)

// viteSPARoutesRE 抓 `SPA_ROUTES = /^\/(a|b|c)(\/|$)/` 里的交替分支。
var viteSPARoutesRE = regexp.MustCompile(`SPA_ROUTES\s*=\s*/\^\\/\(([^)]+)\)`)

// routerPathRE 抓 `src/router/index.ts` 里的 `path: 'xxx'`。
var routerPathRE = regexp.MustCompile(`(?m)^\s*path:\s*'([^']*)'`)

// nginxNonSPA 是从 nginx 精确匹配里排除掉的基础设施端点。
//
// 它们不是 SPA 路由，所以不参与「与 vite 白名单一一对应」这条不变量；
// 但它们在保留字表里有自己的位置（见 reserved 的「基础设施 / 静态资源」段），
// 下面会一并断言，免得有人以为这里排除等于不用管。
var nginxNonSPA = []string{"api", "healthz", "metrics"}

func TestSPARoutesStayInSync(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	nginxPath := filepath.Join(root, "deploy", "nginx", "nginx.conf")
	vitePath := filepath.Join(root, "frontend", "vite.config.ts")
	routerPath := filepath.Join(root, "frontend", "src", "router", "index.ts")

	nginxRoutes := parseNginxRoutes(t, nginxPath)
	viteRoutes := parseViteRoutes(t, vitePath)
	frontendRoutes := parseFrontendRoutes(t, routerPath)

	if len(nginxRoutes) < minExpectedRoutes {
		t.Fatalf("从 nginx.conf 只解析出 %d 条 SPA 路由（下限 %d）：解析逻辑或配置结构变了，"+
			"先修解析再信任本用例。解析结果：%v", len(nginxRoutes), minExpectedRoutes, nginxRoutes)
	}
	if len(viteRoutes) < minExpectedRoutes {
		t.Fatalf("从 vite.config.ts 只解析出 %d 条 SPA 路由（下限 %d）：解析逻辑或写法变了，"+
			"先修解析再信任本用例。解析结果：%v", len(viteRoutes), minExpectedRoutes, viteRoutes)
	}
	if len(frontendRoutes) < minExpectedFrontendRoutes {
		t.Fatalf("从 router/index.ts 只解析出 %d 条顶级路由（下限 %d）：解析逻辑或写法变了，"+
			"先修解析再信任本用例。解析结果：%v",
			len(frontendRoutes), minExpectedFrontendRoutes, frontendRoutes)
	}

	// 不变量 1：两份清单必须完全相同 —— 漏一处就是 dev 与 prod 行为分叉。
	if !slices.Equal(nginxRoutes, viteRoutes) {
		t.Errorf("nginx 的精确 location 与 vite 的 SPA_ROUTES 不一致：\n"+
			"  只在 nginx.conf 里：%v\n  只在 vite.config.ts 里：%v\n"+
			"（生产用的是 nginx 那份，开发用的是 vite 那份 —— 不一致就意味着一处能刷新、一处 404）",
			missingFrom(viteRoutes, nginxRoutes), missingFrom(nginxRoutes, viteRoutes))
	}

	// 不变量 2：每一处占用的路径都必须在保留字表里，否则别人能把它注册成短码。
	reservedSet := make(map[string]struct{})
	for _, code := range ReservedCodes() {
		reservedSet[code] = struct{}{}
	}
	for _, route := range append(slices.Clone(nginxRoutes), nginxNonSPA...) {
		if _, ok := reservedSet[route]; !ok {
			t.Errorf("保留字表缺少 %q：它已经在 nginx / vite 里被占用，别人抢注成短码会把真实页面或端点吃掉", route)
		}
	}

	// 不变量 3：nginx / vite 的清单必须与前端**真实存在**的顶级路由一致（不多不少）。
	//
	// 「多」的害处不是抽象的：多出来的条目会被 `try_files` 兜到 index.html，
	// 于是「不存在的页面」以 **200** 返回 —— 爬虫与可用性监控会把不存在当正常，
	// 真实的 404 也被掩盖。2026-09-22 的审计就是在这里发现多出 10 条
	// （/logout /settings /account /profile /admin /about /help /docs /terms /privacy）。
	// 想预留将来要做的页面，正确做法是等页面做出来再加那一行。
	//
	// 「少」的害处是反过来的：前端有页面，而 nginx 没拦下来 → 被短码正则当成短码
	// 打到后端拿 404（开发环境则由 vite 白名单兜住，于是又变成「本地好、线上坏」）。
	//
	// 注意 `links/:code` 的顶级段是 `links`，所以两边的粒度都是「第一段路径」。
	for _, src := range []struct {
		name   string
		routes []string
	}{{"nginx.conf", nginxRoutes}, {"vite.config.ts", viteRoutes}} {
		if slices.Equal(src.routes, frontendRoutes) {
			continue
		}
		t.Errorf("%s 的路由清单与 router/index.ts 不一致（前端真实顶级路由：%v）：\n"+
			"  多余的（前端没有这个页面，却会被兜成 200）：%v\n"+
			"  缺失的（前端有这个页面，但线上刷新会 404）：%v",
			src.name, frontendRoutes,
			missingFrom(frontendRoutes, src.routes), missingFrom(src.routes, frontendRoutes))
	}
}

// parseNginxRoutes 解析 nginx.conf 里 `location = /xxx` 的清单（已排序、去掉基础设施端点）。
func parseNginxRoutes(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
	}

	matches := nginxExactLocationRE.FindAllStringSubmatch(string(body), -1)
	routes := make([]string, 0, len(matches))
	for _, match := range matches {
		if slices.Contains(nginxNonSPA, match[1]) {
			continue
		}
		routes = append(routes, match[1])
	}
	slices.Sort(routes)
	return slices.Compact(routes)
}

// parseViteRoutes 解析 vite.config.ts 里 SPA_ROUTES 正则的交替分支（已排序）。
func parseViteRoutes(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
	}

	match := viteSPARoutesRE.FindStringSubmatch(string(body))
	if match == nil {
		t.Fatalf("在 %s 里找不到 SPA_ROUTES 的正则形态：写法变了就同步改本用例的正则，"+
			"否则这条守卫会静默失效", path)
	}

	routes := make([]string, 0, 16)
	for _, part := range strings.Split(match[1], "|") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			routes = append(routes, trimmed)
		}
	}
	slices.Sort(routes)
	return slices.Compact(routes)
}

// parseFrontendRoutes 解析 `src/router/index.ts` 里顶级路由的第一段路径（已排序）。
//
// 只看第一段：`links/:code` 的顶级路径是 `links`，单段顶级路由才需要 nginx 用 `=` 精确匹配拦住。
// 父路由的 index 子路由（path 为空串）与参数段 catch-all（`:pathMatch(.*)*`）都不是「单段顶级路径」。
func parseFrontendRoutes(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
	}

	matches := routerPathRE.FindAllStringSubmatch(string(body), -1)
	routes := make([]string, 0, len(matches))
	for _, match := range matches {
		seg := strings.Trim(strings.TrimSpace(match[1]), "/")
		if seg == "" {
			continue
		}
		// 先切第一段再判参数段：`links/:code` 是真实路由，`/links` 需要被 nginx 拦住；
		// 而 `:pathMatch(.*)*` 切完仍是参数段，属于 catch-all，不是真实页面。
		if first, _, found := strings.Cut(seg, "/"); found {
			seg = first
		}
		if strings.HasPrefix(seg, ":") {
			continue
		}
		routes = append(routes, seg)
	}
	slices.Sort(routes)
	return slices.Compact(routes)
}

// missingFrom 返回 want 里有、have 里没有的元素（用于把差异打印成人能读的一行）。
func missingFrom(have, want []string) []string {
	out := make([]string, 0)
	for _, item := range want {
		if !slices.Contains(have, item) {
			out = append(out, item)
		}
	}
	return out
}
