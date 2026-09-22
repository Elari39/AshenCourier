package handler

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 本文件守的是**跨语言的两个常量**：Go 这边的取值与 `deploy/nginx/security-headers.conf`
// 那边的配置必须一致。两边单独看都对，错的是「只改了一边」。
//
//  1. 口令页 / 短链失效页那段内联 <style> 的 CSP 哈希。
//     CSP 用的是 `style-src 'self' 'sha256-…'` 而**不是** 'unsafe-inline'，
//     所以 pageCSS 一改、哈希没跟着改，浏览器就会**拒绝应用这段样式**。
//     后果是那两张页面退化成没有任何样式的裸 HTML：接口全对、状态码全对、
//     控制台之外没有任何报错（e2e 里那条「零 console 错误」能兜住，但那要跑到浏览器验收）。
//
//  2. 302 跳转的 Referrer-Policy。
//     重复的 Referrer-Policy 会被浏览器当成逗号列表、**以最后一条为准**，
//     而 nginx 的 add_header 追加在上游之后 ⇒ nginx 那份总是赢。
//     两处取值不一致时，`referrerPolicy` 是死代码，而「读代码以为的策略」
//     与「浏览器实际执行的策略」不是一回事。
//
// 与 httpx/nginx_headers_test.go、shortcode/routes_sync_test.go 一致：
// 跨出 backend 模块去读仓库根的 deploy/，没完整检出时透明 SKIP。

var (
	cspHeaderRE      = regexp.MustCompile(`(?m)^\s*add_header\s+Content-Security-Policy\s+"([^"]+)"\s+always\s*;`)
	referrerPolicyRE = regexp.MustCompile(`(?m)^\s*add_header\s+Referrer-Policy\s+"([^"]+)"\s+always\s*;`)
)

// securityHeadersConf 读回安全头配置；仓库没完整检出时 SKIP。
func securityHeadersConf(t *testing.T) (string, bool) {
	t.Helper()

	// backend/internal/handler → 上三级是仓库根
	path := filepath.Join("..", "..", "..", "deploy", "nginx", "security-headers.conf")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
		return "", false
	}
	return string(body), true
}

// TestPasswordPageInlineStyleHashMatchesCSP 断言内联样式的哈希与 CSP 里那一条一致。
func TestPasswordPageInlineStyleHashMatchesCSP(t *testing.T) {
	t.Parallel()

	conf, ok := securityHeadersConf(t)
	if !ok {
		return
	}

	m := cspHeaderRE.FindStringSubmatch(conf)
	if m == nil {
		t.Fatalf("安全头配置里找不到 Content-Security-Policy（或它没有带 always）：" +
			"口令页与失效页的内联样式全靠它放行")
	}
	csp := m[1]

	// 模板是 `pageHead(...)` 拼出来的：`<style>` 后面紧跟一个换行，再接 pageCSS。
	// 所以元素文本是 "\n" + pageCSS —— 这里的换行不能省，CSP 哈希算的是元素内容本身。
	sum := sha256.Sum256([]byte("\n" + pageCSS))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	if !strings.Contains(csp, want) {
		t.Errorf("CSP 里的内联样式哈希与 pageCSS 对不上：\n"+
			"    期望包含：%s\n"+
			"    说明：pageCSS 改过之后必须重算哈希并写回 deploy/nginx/security-headers.conf。\n"+
			"    不重算的后果是口令页与短链失效页**静默**退化成没有样式的裸 HTML ——\n"+
			"    接口、状态码、缓存全都正常，只有浏览器控制台里有一条被拒的提示。\n"+
			"    重算方式（从常量算，不要手抄）：\n"+
			"      sha256(\"\\n\" + pageCSS) 的 base64，前缀 'sha256-'，整个再套一对单引号", want)
	}
}

// TestRedirectReferrerPolicyMatchesNginx 断言 302 的 Referrer-Policy 与 nginx 全站策略一致。
func TestRedirectReferrerPolicyMatchesNginx(t *testing.T) {
	t.Parallel()

	conf, ok := securityHeadersConf(t)
	if !ok {
		return
	}

	m := referrerPolicyRE.FindStringSubmatch(conf)
	if m == nil {
		t.Fatalf("安全头配置里找不到 Referrer-Policy（或它没有带 always）")
	}
	if got := strings.TrimSpace(m[1]); got != referrerPolicy {
		t.Errorf("Referrer-Policy 两处不一致：\n"+
			"    nginx（security-headers.conf）：%q\n"+
			"    Go（handler/redirect.go 的 referrerPolicy）：%q\n"+
			"    重复的 Referrer-Policy 是逗号列表、以最后一条为准，而 nginx 的 add_header 追加在\n"+
			"    上游响应之后 —— 也就是 nginx 那份总是赢。不一致时 Go 这边那一行是死代码。", got, referrerPolicy)
	}
}
