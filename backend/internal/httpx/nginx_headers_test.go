package httpx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件守的是 **nginx 的两条部署期契约**，都是「坏了也不报错」的类型：
//
//  1. 安全响应头必须出现在**每个**自己写了 add_header 的层级里。
//     nginx 的 add_header 是**替换而不是合并**：父层的只在「当前层级没有 add_header」
//     时才被继承。于是 `location ^~ /assets/` 那条 immutable 缓存头一写，
//     server 层那批安全头就在那里**静默消失** —— nginx -t 通过、页面正常、缓存正常。
//     配置里已经用 `include security-headers.conf` 把定义收成一处，本用例负责断言
//     「**没有哪个带 add_header 的块漏了 include**」。
//
//  2. SPA 外壳必须带 `Cache-Control: no-cache`。
//     没有它时只有 Last-Modified，浏览器按启发式规则缓存（约 `(now-LM)×10%`），
//     而 `/assets/` 是内容哈希 + `immutable` 且**没有 fallback**：
//     部署后还开着旧页面的用户会拿旧外壳去请求已失效的哈希资源，拿到硬 404 ⇒ **白屏**。
//     这条同时钉住「只需一个出口」这个推论（见 nginx.conf 里那段注释）：
//     所有 SPA 路径的 `try_files` 最后一个参数都是 `/index.html`，
//     而 try_files 的最后一个参数会触发**内部重定向**、重新做 location 匹配 ——
//     所以外壳只有一个出口，缓存头写在 `location = /index.html` 上即覆盖全部入口。
//
// 与 realip_test.go 同样的处理：跨出 backend 模块去读仓库根的 deploy/，
// 仓库没完整检出时透明 SKIP（跳过要说出来，不能静默绿）。
const securityHeadersInclude = "include /etc/nginx/security-headers.conf;"

// requiredSecurityHeaders 是安全响应头的**最低集合**。
// 键是头名，值是它必须包含的取值（空串表示只查头名在场）。
var requiredSecurityHeaders = map[string]string{
	"Strict-Transport-Security": "max-age=31536000",
	"X-Frame-Options":           "DENY",
	"X-Content-Type-Options":    "nosniff",
	"Referrer-Policy":           "strict-origin-when-cross-origin",
	"Content-Security-Policy":   "frame-ancestors 'none'",
}

// nginxBlock 是一个 nginx 配置块（含块头那一行）。
type nginxBlock struct {
	header string   // 块头，例如 `location = /index.html`（已去注释、已 TrimSpace）
	code   []string // 块内每行的「去注释」文本（含块头那一行）
}

// nginxConf 读回 nginx.conf 的原文；仓库没完整检出时 SKIP。
func nginxConf(t *testing.T) (string, bool) {
	t.Helper()

	// backend/internal/httpx → 上三级是仓库根
	path := filepath.Join("..", "..", "..", "deploy", "nginx", "nginx.conf")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
		return "", false
	}
	return string(body), true
}

// securityHeadersSnippet 读回 security-headers.conf 的原文；仓库没完整检出时 SKIP。
func securityHeadersSnippet(t *testing.T) (string, bool) {
	t.Helper()

	path := filepath.Join("..", "..", "..", "deploy", "nginx", "security-headers.conf")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
		return "", false
	}
	return string(body), true
}

// stripComment 去掉行内注释。
//
// 「找 # 就切」在本文件上足够安全：nginx.conf 里没有任何取值含 `#`。
// 这一步不能省 —— nginx.conf 的**注释里**就有 `^/[A-Za-z0-9_-]{3,32}$` 这类片段，
// 不先去掉注释，花括号计数会被注释里的 `{3,32}` 带偏，块结构就解析错了。
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// parseBlocks 扫出所有配置块。
//
// 用花括号栈而不是正则：`location = /metrics { return 404; }` 与
// `location = /favicon.ico { try_files $uri =404; }` 这两种「一行一整个块」都要算一个块
// （第一版解析漏了同一行里的收尾 `}`，于是后续所有块都被塞进了这个没闭合的块里 ——
//
//	表现是「守卫自己算错」，不是配置有问题）。嵌套关系（http → server → location）
//
// 正是本用例要用的信息。
func parseBlocks(src string) []nginxBlock {
	var (
		stack  []nginxBlock
		blocks []nginxBlock
	)

	for _, raw := range strings.Split(src, "\n") {
		code := stripComment(raw)

		// 本行第一个 `{` 之前的内容即该行新开块的块头
		head := ""
		if i := strings.IndexByte(code, '{'); i >= 0 {
			head = strings.TrimSpace(code[:i])
		}

		added := false // 本行是否已经塞进过栈顶块
		for _, ch := range code {
			switch ch {
			case '{':
				if len(stack) > 0 && !added {
					stack[len(stack)-1].code = append(stack[len(stack)-1].code, code)
					added = true
				}
				stack = append(stack, nginxBlock{header: head, code: []string{code}})
				head = "" // 同一行里若还有 `{`，不再复用这个块头
			case '}':
				if len(stack) > 0 {
					blocks = append(blocks, stack[len(stack)-1])
					stack = stack[:len(stack)-1]
					added = true
				}
			}
		}

		if !added && len(stack) > 0 {
			stack[len(stack)-1].code = append(stack[len(stack)-1].code, code)
		}
	}
	return blocks
}

// findBlocks 返回所有块头以 prefix 开头的块（用来挑 location）。
func findBlocks(blocks []nginxBlock, prefix string) []nginxBlock {
	var out []nginxBlock
	for _, b := range blocks {
		if strings.HasPrefix(b.header, prefix) {
			out = append(out, b)
		}
	}
	return out
}

// hasDirective 判断块内是否存在包含给定片段的行。
func hasDirective(b nginxBlock, fragment string) bool {
	for _, line := range b.code {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// TestNginxIncludesSecurityHeadersWhereverItAddsHeaders 断言「任何写了 add_header 的块
// 都同时 include 了安全头」。
//
// 这是本文件里最值钱的一条：漏掉 include 的效果是**静默的安全退化** —— 页面、缓存、
// 状态码全都正常，只有那一批响应头不见了，靠人眼 review 几乎不可能发现。
func TestNginxIncludesSecurityHeadersWhereverItAddsHeaders(t *testing.T) {
	t.Parallel()

	conf, ok := nginxConf(t)
	if !ok {
		return
	}

	blocks := parseBlocks(conf)

	// 下限探针：真实配置里至少有 http / events / 两个 map / server / 十几个 location。
	// 解析出空集合是最容易发生的失效模式（空 == 空 照样通过）。
	const minBlocks = 8
	if len(blocks) < minBlocks {
		t.Fatalf("只解析出 %d 个块（下限 %d）：解析逻辑或配置结构变了，先修解析再信任本用例",
			len(blocks), minBlocks)
	}

	var withAddHeader int
	for _, b := range blocks {
		if !hasDirective(b, "add_header") {
			continue
		}
		withAddHeader++
		if !hasDirective(b, securityHeadersInclude) {
			t.Errorf("块 %q 里有 add_header 但没有 %q：\n"+
				"    nginx 的 add_header 是替换而不是合并 —— 这一层会把 server 层那批安全头**整批丢掉**，"+
				"而 nginx -t、页面渲染、缓存行为全都看不出异常。\n"+
				"    修法：在本块里加一行 `%s`（定义仍在 security-headers.conf，不要复制粘贴一份进来）。",
				strings.TrimSpace(b.header), securityHeadersInclude, securityHeadersInclude)
		}
	}

	if withAddHeader == 0 {
		t.Fatal("配置里一处 add_header 都没有：要么是解析错了，要么是安全头/缓存头被整体删掉了")
	}
}

// TestNginxSecurityHeadersComplete 断言那份 snippet 本身在场、被 include、五条头齐全且都带 always。
func TestNginxSecurityHeadersComplete(t *testing.T) {
	t.Parallel()

	conf, ok := nginxConf(t)
	if !ok {
		return
	}
	if !strings.Contains(conf, securityHeadersInclude) {
		t.Fatalf("nginx.conf 里没有 %q：安全头没有定义点，全站都会裸奔", securityHeadersInclude)
	}

	snippet, ok := securityHeadersSnippet(t)
	if !ok {
		return
	}

	for _, line := range strings.Split(snippet, "\n") {
		code := strings.TrimSpace(stripComment(line))
		if !strings.HasPrefix(code, "add_header ") {
			continue
		}
		// 每一条都必须带 always：否则 404（短链失效页）、401（令牌过期）、
		// 503（依赖降级）这些最需要它的响应上会**恰好没有**这些头。
		if !strings.HasSuffix(code, "always;") {
			t.Errorf("这一条缺 `always`：%s\n"+
				"    nginx 默认只给 2xx/3xx/204/301/302/304 加头，而 404/401/503 才是真正要用到它的地方", code)
		}
	}

	for name, want := range requiredSecurityHeaders {
		line := findHeaderLine(snippet, name)
		if line == "" {
			t.Errorf("安全头清单里缺少 %s —— 缺它的后果见 security-headers.conf 里的逐条说明", name)
			continue
		}
		if want != "" && !strings.Contains(line, want) {
			t.Errorf("%s 的取值不符合预期：\n    实际：%s\n    期望包含：%s", name, line, want)
		}
	}
}

// findHeaderLine 返回 snippet 里设置某个响应头的那一行（没有则空串）。
func findHeaderLine(snippet, name string) string {
	for _, line := range strings.Split(snippet, "\n") {
		code := strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(code, "add_header "+name+" ") {
			return code
		}
	}
	return ""
}

// TestNginxSPAShellAlwaysServedWithNoCache 断言 SPA 外壳的缓存头。
func TestNginxSPAShellAlwaysServedWithNoCache(t *testing.T) {
	t.Parallel()

	conf, ok := nginxConf(t)
	if !ok {
		return
	}
	blocks := parseBlocks(conf)

	shells := findBlocks(blocks, "location = /index.html")
	if len(shells) != 1 {
		t.Fatalf("`location = /index.html` 应当**恰好一个**，实际 %d 个：\n"+
			"    它是 SPA 外壳的唯一出口 —— 所有 SPA 路径的 try_files 最后一个参数都是 /index.html，"+
			"而 try_files 的最后一个参数会触发内部重定向、重新做一遍 location 匹配。\n"+
			"    没有它或有多份，缓存头要么不生效、要么两处说法不一致。", len(shells))
	}

	shell := shells[0]
	switch {
	case !hasDirective(shell, "Cache-Control"), !hasDirective(shell, "no-cache"):
		t.Errorf("`location = /index.html` 没有 `Cache-Control: no-cache`：\n"+
			"    没有它时只有 Last-Modified，浏览器按启发式规则缓存（约 (now-LM)×10%%），"+
			"而 /assets/ 是内容哈希 + immutable 且 try_files =404 没有 fallback ——\n"+
			"    部署后还开着旧页面的用户会拿旧外壳去请求已失效的哈希资源，拿到硬 404 ⇒ **白屏**。\n"+
			"    这类问题 CI 永远测不出来（每轮 e2e 都是干净浏览器），只有真实用户会话会踩到。\n"+
			"    当前块内容：%v", shell.code)
	case !hasDirective(shell, "always"):
		t.Errorf("`Cache-Control` 缺 `always`：外壳请求失败时（比如 index.html 缺失回 500）就不带了。当前块内容：%v", shell.code)
	}
	if !hasDirective(shell, securityHeadersInclude) {
		t.Errorf("`location = /index.html` 缺 %q：本块自己写了 add_header，"+
			"server 层那批安全头在这里**不再被继承**", securityHeadersInclude)
	}

	// 「唯一出口」这个推论依赖「所有回退目标都指向外壳」。每个 try_files 的回退要么是
	// 外壳（/index.html）、要么是明确 404（内容哈希资源刻意不给 fallback）——
	// 出现第三种目标时，它既拿不到 no-cache，也不再有本用例保护的语义。
	for _, b := range blocks {
		if !hasDirective(b, "try_files") {
			continue
		}
		if hasDirective(b, "/index.html") || hasDirective(b, "=404") {
			continue
		}
		t.Errorf("块 %q 的 try_files 既没回退到 /index.html、也没声明 =404：\n"+
			"    它绕过了外壳出口，拿不到 no-cache（而且这类回退目标通常是写错了）。\n"+
			"    当前块内容：%v", strings.TrimSpace(b.header), b.code)
	}
}
